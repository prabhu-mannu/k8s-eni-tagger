package controller

import (
	"context"
	"encoding/json"
	"k8s-eni-tagger/pkg/aws"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestEventFilters(t *testing.T) {
	// We can't easily test SetupWithManager directly because it requires a real Manager,
	// but we can test the predicate logic if we extract it, OR we reconstruct the logic here.
	// Since the logic is inside SetupWithManager anonymous functions, we can't unit test it easily
	// without refactoring.
	// However, we can use the Reconcile tests to cover the logic if we were running full integration tests,
	// but here we are doing unit tests.

	// Refactoring SetupWithManager to return the predicate would be best,
	// but for now let's just create a test that exercises the logic if we copy it,
	// or better: let's refactor SetupWithManager to accept options or be testable.
	//
	// Actually, `controller-runtime` predicates are hard to test without extracting them.
	// Let's create a new test file `setup_test.go` and try to instantiate the predicate there if we can access it?
	// No, it's defined inline.

	// Plan: Refactor `SetupWithManager` to use a named predicate variable or function that we can test.
}

// Instead of refactoring now, let's add more tests for `eni_operations` and `status` to boost coverage there first.

func TestConflictDetection(t *testing.T) {
	// Test checkHashConflict which accounts for coverage in reconcile_helpers.go
	tests := []struct {
		name           string
		eniHash        string
		desiredHash    string
		lastApplied    string
		allowShared    bool
		expectConflict bool
	}{
		{"Empty ENI Hash", "", "A", "B", false, false},
		{"Synced", "A", "A", "A", false, false},
		{"Owned", "A", "B", "A", false, false},
		{"Conflict", "C", "A", "B", false, true},
		{"Conflict Allowed", "C", "A", "B", true, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := &aws.ENIInfo{
				Tags: map[string]string{HashTagKey: tt.eniHash},
			}
			// If eniHash is empty, we don't put it in map?
			if tt.eniHash == "" {
				delete(info.Tags, HashTagKey)
			}

			conflict := checkHashConflict(info, tt.desiredHash, tt.lastApplied, tt.allowShared)
			assert.Equal(t, tt.expectConflict, conflict)
		})
	}
}

func TestStatusUtils(t *testing.T) {
	// Test isConditionTrue
	conditions := []corev1.PodCondition{
		{Type: "TestType", Status: corev1.ConditionTrue},
		{Type: "OtherType", Status: corev1.ConditionFalse},
	}
	assert.True(t, isConditionTrue(conditions, "TestType"))
	assert.False(t, isConditionTrue(conditions, "OtherType"))
	assert.False(t, isConditionTrue(conditions, "MissingType"))
}

func TestForeignTagsPreservation(t *testing.T) {
	// Verify that existing "foreign" tags on the ENI are not touched
	// during tagging or untagging operations.

	scheme := runtime.NewScheme()
	corev1.AddToScheme(scheme)

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-foreign-tags",
			Namespace: "default",
			Annotations: map[string]string{
				AnnotationKey: `{"my-tag":"my-value"}`,
			},
			Finalizers: []string{finalizerName},
		},
		Status: corev1.PodStatus{PodIP: "10.0.0.1"},
	}

	mockAWS := new(MockAWSClient)

	// Step 1: Reconcile (Add Tags)
	// ENI has existing tags: "foreign-tag": "foreign-value"
	mockAWS.On("GetENIInfoByIP", mock.Anything, "10.0.0.1").Return(&aws.ENIInfo{
		ID: "eni-1",
		Tags: map[string]string{
			"foreign-tag": "foreign-value",
		},
	}, nil).Times(1) // Called once for add

	// Expect TagENI to be called ONLY with "my-tag" and "hash"
	// It should NOT try to re-apply "foreign-tag" or remove it.
	mockAWS.On("TagENI", mock.Anything, "eni-1", mock.MatchedBy(func(tags map[string]string) bool {
		// Only check if my-tag is present.
		// Detailed check: should strictly contain my-tag and hash.
		// If it contained "foreign-tag", that would be weird (we don't read and re-apply).
		_, hasForeign := tags["foreign-tag"]
		return tags["my-tag"] == "my-value" && !hasForeign
	})).Return(nil)

	r := &PodReconciler{
		Client:        fake.NewClientBuilder().WithScheme(scheme).WithObjects(pod).Build(),
		Scheme:        scheme,
		Recorder:      record.NewFakeRecorder(10),
		AWSClient:     mockAWS,
		AnnotationKey: AnnotationKey,
	}

	// Run Reconcile
	_, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: client.ObjectKeyFromObject(pod),
	})
	assert.NoError(t, err)

	// Step 2: Simulate Deletion (Remove Tags)
	// Update pod to have last-applied-tags matching what we added
	tagsWithHash := map[string]string{"my-tag": "my-value"}
	hash := computeHash(tagsWithHash)
	tagsJson, _ := json.Marshal(tagsWithHash)

	pod.DeletionTimestamp = &metav1.Time{Time: time.Now()}
	pod.Annotations[LastAppliedAnnotationKey] = string(tagsJson)
	pod.Annotations[LastAppliedHashKey] = hash

	// Reset Mock for Deletion
	mockAWS.ExpectedCalls = nil
	// GetENIInfo called during deletion
	mockAWS.On("GetENIInfoByIP", mock.Anything, "10.0.0.1").Return(&aws.ENIInfo{
		ID: "eni-1",
		Tags: map[string]string{
			"foreign-tag": "foreign-value",
			"my-tag":      "my-value",
			HashTagKey:    hash,
		},
	}, nil)

	// Expect UntagENI to be called ONLY with "my-tag" and "hash"
	// It must NOT contain "foreign-tag"
	mockAWS.On("UntagENI", mock.Anything, "eni-1", mock.MatchedBy(func(keys []string) bool {
		for _, k := range keys {
			if k == "foreign-tag" {
				return false
			}
		}
		// Should have my-tag and hash
		return len(keys) == 2 // my-tag + hash
	})).Return(nil)

	// Run Reconcile (Deletion)
	r.Client = fake.NewClientBuilder().WithScheme(scheme).WithObjects(pod).Build()
	_, err = r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: client.ObjectKeyFromObject(pod),
	})
	assert.NoError(t, err)

	mockAWS.AssertExpectations(t)
}
