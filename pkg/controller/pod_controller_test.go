package controller

import (
	"context"
	"fmt"
	"k8s-eni-tagger/pkg/aws"
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestParseTags(t *testing.T) {
	tests := []struct {
		name    string
		tagStr  string
		want    map[string]string
		wantErr bool
	}{
		{
			name:    "empty string",
			tagStr:  "",
			want:    map[string]string{},
			wantErr: false,
		},
		{
			name:    "whitespace only",
			tagStr:  "   ",
			want:    map[string]string{},
			wantErr: false,
		},
		{
			name:    "single tag",
			tagStr:  "key=value",
			want:    map[string]string{"key": "value"},
			wantErr: false,
		},
		{
			name:    "multiple tags",
			tagStr:  "key1=value1,key2=value2",
			want:    map[string]string{"key1": "value1", "key2": "value2"},
			wantErr: false,
		},
		{
			name:    "tags with whitespace",
			tagStr:  " key1 = value1 , key2 = value2 ",
			want:    map[string]string{"key1": "value1", "key2": "value2"},
			wantErr: false,
		},
		{
			name:    "empty value",
			tagStr:  "key=",
			want:    map[string]string{"key": ""},
			wantErr: false,
		},
		{
			name:    "multiple commas",
			tagStr:  "key1=value1,,key2=value2",
			want:    map[string]string{"key1": "value1", "key2": "value2"},
			wantErr: false,
		},
		{
			name:    "invalid format (no equals)",
			tagStr:  "key",
			want:    nil,
			wantErr: true,
		},
		{
			name:    "empty key",
			tagStr:  "=value",
			want:    nil,
			wantErr: true,
		},
		{
			name:    "reserved prefix aws:",
			tagStr:  "aws:key=value",
			want:    nil,
			wantErr: true,
		},
		{
			name:    "reserved prefix kubernetes.io/cluster/",
			tagStr:  "kubernetes.io/cluster/key=value",
			want:    nil,
			wantErr: true,
		},
		{
			name:    "invalid characters in key",
			tagStr:  "key!=value",
			want:    nil,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseTags(tt.tagStr)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseTags() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseTags() = %v, want %v", got, tt.want)
			}
		})
	}
}

// MockAWSClient is a mock implementation of aws.Client
type MockAWSClient struct {
	GetENIInfoByIPFunc func(ctx context.Context, ip string) (*aws.ENIInfo, error)
	TagENIFunc         func(ctx context.Context, eniID string, tags map[string]string) error
	UntagENIFunc       func(ctx context.Context, eniID string, tagKeys []string) error
}

func (m *MockAWSClient) GetENIInfoByIP(ctx context.Context, ip string) (*aws.ENIInfo, error) {
	return m.GetENIInfoByIPFunc(ctx, ip)
}

func (m *MockAWSClient) TagENI(ctx context.Context, eniID string, tags map[string]string) error {
	return m.TagENIFunc(ctx, eniID, tags)
}

func (m *MockAWSClient) UntagENI(ctx context.Context, eniID string, tagKeys []string) error {
	return m.UntagENIFunc(ctx, eniID, tagKeys)
}

func TestReconcile(t *testing.T) {
	// This is a basic test to verify the flow.
	// For a full test suite, we would use envtest to spin up a real K8s API server.
	// Here we just test the logic with a fake client.

	scheme := runtime.NewScheme()
	corev1.AddToScheme(scheme)

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
			Annotations: map[string]string{
				AnnotationKey: "env=prod",
			},
		},
		Status: corev1.PodStatus{
			PodIP: "10.0.0.1",
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(pod).Build()

	mockAWS := &MockAWSClient{
		GetENIInfoByIPFunc: func(ctx context.Context, ip string) (*aws.ENIInfo, error) {
			if ip == "10.0.0.1" {
				return &aws.ENIInfo{
					ID:       "eni-12345",
					SubnetID: "subnet-1",
					IsShared: false,
				}, nil
			}
			return nil, fmt.Errorf("not found")
		},
		TagENIFunc: func(ctx context.Context, eniID string, tags map[string]string) error {
			if eniID == "eni-12345" && tags["env"] == "prod" {
				return nil
			}
			return fmt.Errorf("unexpected tag call")
		},
		UntagENIFunc: func(ctx context.Context, eniID string, tagKeys []string) error {
			return nil
		},
	}

	r := &PodReconciler{
		Client:    fakeClient,
		Scheme:    scheme,
		AWSClient: mockAWS,
		Recorder:  record.NewFakeRecorder(10),
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      "test-pod",
			Namespace: "default",
		},
	}

	_, err := r.Reconcile(context.Background(), req)
	if err != nil {
		t.Errorf("Reconcile() error = %v", err)
	}

	// Verify pod annotation was updated
	updatedPod := &corev1.Pod{}
	err = fakeClient.Get(context.Background(), req.NamespacedName, updatedPod)
	if err != nil {
		t.Errorf("Get pod error = %v", err)
	}

	lastApplied := updatedPod.Annotations[LastAppliedAnnotationKey]
	if lastApplied == "" {
		t.Error("Last applied annotation not set")
	}
}

func TestReconcile_PodBootUp(t *testing.T) {
	// Simulate Pod Boot Up: Created without IP, then IP assigned
	scheme := runtime.NewScheme()
	corev1.AddToScheme(scheme)

	// 1. Pod created without IP
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "boot-pod",
			Namespace: "default",
			Annotations: map[string]string{
				AnnotationKey: "env=boot",
			},
		},
		Status: corev1.PodStatus{
			PodIP: "", // No IP yet
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(pod).Build()

	mockAWS := &MockAWSClient{
		GetENIInfoByIPFunc: func(ctx context.Context, ip string) (*aws.ENIInfo, error) {
			if ip == "10.0.0.2" {
				return &aws.ENIInfo{
					ID:       "eni-boot",
					SubnetID: "subnet-1",
					IsShared: false,
				}, nil
			}
			return nil, fmt.Errorf("not found")
		},
		TagENIFunc: func(ctx context.Context, eniID string, tags map[string]string) error {
			if eniID == "eni-boot" && tags["env"] == "boot" {
				return nil
			}
			return fmt.Errorf("unexpected tag call")
		},
		UntagENIFunc: func(ctx context.Context, eniID string, tagKeys []string) error {
			return nil
		},
	}

	r := &PodReconciler{
		Client:    fakeClient,
		Scheme:    scheme,
		AWSClient: mockAWS,
		Recorder:  record.NewFakeRecorder(10),
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      "boot-pod",
			Namespace: "default",
		},
	}

	// 2. First Reconcile (No IP) -> Should skip
	_, err := r.Reconcile(context.Background(), req)
	if err != nil {
		t.Errorf("Reconcile(No IP) error = %v", err)
	}

	// Verify NO changes made
	updatedPod := &corev1.Pod{}
	_ = fakeClient.Get(context.Background(), req.NamespacedName, updatedPod)
	if updatedPod.Annotations[LastAppliedAnnotationKey] != "" {
		t.Error("Reconcile should have skipped pod without IP")
	}

	// 3. Update Pod with IP
	updatedPod.Status.PodIP = "10.0.0.2"
	if err := fakeClient.Status().Update(context.Background(), updatedPod); err != nil {
		t.Fatalf("Failed to update pod status: %v", err)
	}

	// Verify IP is persisted
	checkPod := &corev1.Pod{}
	_ = fakeClient.Get(context.Background(), req.NamespacedName, checkPod)
	if checkPod.Status.PodIP != "10.0.0.2" {
		t.Fatalf("Pod IP not persisted in fake client. Got: %s", checkPod.Status.PodIP)
	}

	// 4. Second Reconcile (With IP) -> Should tag
	_, err = r.Reconcile(context.Background(), req)
	if err != nil {
		t.Errorf("Reconcile(With IP) error = %v", err)
	}

	// Verify tags applied
	_ = fakeClient.Get(context.Background(), req.NamespacedName, updatedPod)
	if updatedPod.Annotations[LastAppliedAnnotationKey] == "" {
		t.Error("Reconcile should have applied tags after IP assignment")
	}
}

func TestReconcile_SubnetFiltering(t *testing.T) {
	scheme := runtime.NewScheme()
	corev1.AddToScheme(scheme)

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "subnet-pod",
			Namespace: "default",
			Annotations: map[string]string{
				AnnotationKey: "env=subnet",
			},
		},
		Status: corev1.PodStatus{
			PodIP: "10.0.0.3",
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(pod).Build()

	mockAWS := &MockAWSClient{
		GetENIInfoByIPFunc: func(ctx context.Context, ip string) (*aws.ENIInfo, error) {
			return &aws.ENIInfo{
				ID:       "eni-subnet",
				SubnetID: "subnet-excluded",
				IsShared: false,
			}, nil
		},
		TagENIFunc: func(ctx context.Context, eniID string, tags map[string]string) error {
			return fmt.Errorf("should not tag excluded subnet")
		},
		UntagENIFunc: func(ctx context.Context, eniID string, tagKeys []string) error {
			return nil
		},
	}

	r := &PodReconciler{
		Client:    fakeClient,
		Scheme:    scheme,
		AWSClient: mockAWS,
		Recorder:  record.NewFakeRecorder(10),
		SubnetIDs: []string{"subnet-allowed"}, // Only allow this subnet
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      "subnet-pod",
			Namespace: "default",
		},
	}

	_, err := r.Reconcile(context.Background(), req)
	if err != nil {
		t.Errorf("Reconcile() error = %v", err)
	}

	// Verify NO tags applied (LastAppliedAnnotationKey should be empty)
	updatedPod := &corev1.Pod{}
	_ = fakeClient.Get(context.Background(), req.NamespacedName, updatedPod)
	if updatedPod.Annotations[LastAppliedAnnotationKey] != "" {
		t.Error("Reconcile should have skipped excluded subnet")
	}
}

func TestReconcile_SharedENISafety(t *testing.T) {
	scheme := runtime.NewScheme()
	corev1.AddToScheme(scheme)

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "shared-pod",
			Namespace: "default",
			Annotations: map[string]string{
				AnnotationKey: "env=shared",
			},
		},
		Status: corev1.PodStatus{
			PodIP: "10.0.0.4",
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(pod).Build()

	mockAWS := &MockAWSClient{
		GetENIInfoByIPFunc: func(ctx context.Context, ip string) (*aws.ENIInfo, error) {
			return &aws.ENIInfo{
				ID:       "eni-shared",
				SubnetID: "subnet-1",
				IsShared: true,                // Shared ENI
				Tags:     map[string]string{}, // Empty - no one has claimed it yet
			}, nil
		},
		TagENIFunc: func(ctx context.Context, eniID string, tags map[string]string) error {
			// With hash-based locking, shared ENIs CAN be tagged
			// Verify hash is included
			if _, ok := tags[HashTagKey]; !ok {
				return fmt.Errorf("missing hash tag")
			}
			return nil
		},
		UntagENIFunc: func(ctx context.Context, eniID string, tagKeys []string) error {
			return nil
		},
	}

	r := &PodReconciler{
		Client:                fakeClient,
		Scheme:                scheme,
		AWSClient:             mockAWS,
		Recorder:              record.NewFakeRecorder(10),
		AllowSharedENITagging: false, // Safety ON, but hash locking allows first writer
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      "shared-pod",
			Namespace: "default",
		},
	}

	_, err := r.Reconcile(context.Background(), req)
	if err != nil {
		t.Errorf("Reconcile() error = %v", err)
	}

	// Verify tags WERE applied (hash-based locking allows first writer)
	updatedPod := &corev1.Pod{}
	_ = fakeClient.Get(context.Background(), req.NamespacedName, updatedPod)
	if updatedPod.Annotations[LastAppliedHashKey] == "" {
		t.Error("Reconcile should have applied tags with hash-based locking")
	}
}

func TestReconcile_HashLocking(t *testing.T) {
	scheme := runtime.NewScheme()
	corev1.AddToScheme(scheme)

	// Scenario 1: ENI is free (no hash). Pod should claim it.
	pod1 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-1",
			Namespace: "default",
			Annotations: map[string]string{
				AnnotationKey: "env=prod",
			},
		},
		Status: corev1.PodStatus{
			PodIP: "10.0.0.5",
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(pod1).Build()

	mockAWS := &MockAWSClient{
		GetENIInfoByIPFunc: func(ctx context.Context, ip string) (*aws.ENIInfo, error) {
			return &aws.ENIInfo{
				ID:       "eni-shared-lock",
				SubnetID: "subnet-1",
				IsShared: true,
				Tags:     map[string]string{}, // Empty tags
			}, nil
		},
		TagENIFunc: func(ctx context.Context, eniID string, tags map[string]string) error {
			// Verify hash is included
			if _, ok := tags[HashTagKey]; !ok {
				return fmt.Errorf("missing hash tag")
			}
			return nil
		},
		UntagENIFunc: func(ctx context.Context, eniID string, tagKeys []string) error {
			return nil
		},
	}

	r := &PodReconciler{
		Client:                fakeClient,
		Scheme:                scheme,
		AWSClient:             mockAWS,
		Recorder:              record.NewFakeRecorder(10),
		AllowSharedENITagging: false, // Safety ON, but locking should allow it
	}

	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "pod-1", Namespace: "default"}}
	_, err := r.Reconcile(context.Background(), req)
	if err != nil {
		t.Errorf("Reconcile() error = %v", err)
	}

	// Verify Pod 1 claimed it
	updatedPod1 := &corev1.Pod{}
	if err := fakeClient.Get(context.Background(), req.NamespacedName, updatedPod1); err != nil {
		t.Fatalf("Failed to get updated pod: %v", err)
	}
	if updatedPod1.Annotations[LastAppliedHashKey] == "" {
		t.Errorf("Pod 1 should have claimed the ENI. Annotations: %v", updatedPod1.Annotations)
	}

	// Scenario 2: ENI is locked by Pod 1. Pod 2 should back off.
	pod2 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-2",
			Namespace: "default",
			Annotations: map[string]string{
				AnnotationKey: "env=dev", // Different tags
			},
		},
		Status: corev1.PodStatus{
			PodIP: "10.0.0.5", // Same IP/ENI
		},
	}

	fakeClient2 := fake.NewClientBuilder().WithScheme(scheme).WithObjects(pod2).Build()

	// Mock AWS returns ENI with Pod 1's hash
	pod1Hash := updatedPod1.Annotations[LastAppliedHashKey]
	mockAWS2 := &MockAWSClient{
		GetENIInfoByIPFunc: func(ctx context.Context, ip string) (*aws.ENIInfo, error) {
			return &aws.ENIInfo{
				ID:       "eni-shared-lock",
				SubnetID: "subnet-1",
				IsShared: true,
				Tags: map[string]string{
					HashTagKey: pod1Hash, // Locked by Pod 1
					"env":      "prod",
				},
			}, nil
		},
		TagENIFunc: func(ctx context.Context, eniID string, tags map[string]string) error {
			return fmt.Errorf("should not tag locked ENI")
		},
	}

	r2 := &PodReconciler{
		Client:                fakeClient2,
		Scheme:                scheme,
		AWSClient:             mockAWS2,
		Recorder:              record.NewFakeRecorder(10),
		AllowSharedENITagging: false,
	}

	req2 := ctrl.Request{NamespacedName: types.NamespacedName{Name: "pod-2", Namespace: "default"}}
	_, err = r2.Reconcile(context.Background(), req2)
	if err != nil {
		t.Errorf("Reconcile() error = %v", err)
	}

	// Verify Pod 2 did NOT claim it
	updatedPod2 := &corev1.Pod{}
	_ = fakeClient2.Get(context.Background(), req2.NamespacedName, updatedPod2)
	if updatedPod2.Annotations[LastAppliedHashKey] != "" {
		t.Error("Pod 2 should have backed off")
	}
}
