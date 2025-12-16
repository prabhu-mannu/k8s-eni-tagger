package controller

import (
	"context"
	"testing"

	"k8s-eni-tagger/pkg/aws"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type mockAWSClientForENIOps struct {
	mock.Mock
}

func (m *mockAWSClientForENIOps) GetENIInfoByIP(ctx context.Context, ip string) (*aws.ENIInfo, error) {
	args := m.Called(ctx, ip)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*aws.ENIInfo), args.Error(1)
}

func (m *mockAWSClientForENIOps) TagENI(ctx context.Context, eniID string, tags map[string]string) error {
	args := m.Called(ctx, eniID, tags)
	return args.Error(0)
}

func (m *mockAWSClientForENIOps) UntagENI(ctx context.Context, eniID string, tagKeys []string) error {
	args := m.Called(ctx, eniID, tagKeys)
	return args.Error(0)
}

func (m *mockAWSClientForENIOps) GetEC2Client() *ec2.Client {
	return nil
}

func TestApplyENITags_RemovesHashWhenNoDesiredTags(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-empty-tags",
			Namespace: "default",
			Annotations: map[string]string{
				LastAppliedAnnotationKey: `{"cost-center":"123"}`,
				LastAppliedHashKey:       "dummy-hash",
			},
		},
	}

	k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(pod).Build()

	awsMock := &mockAWSClientForENIOps{}
	awsMock.On("UntagENI", mock.Anything, "eni-1", mock.MatchedBy(func(keys []string) bool {
		want := map[string]bool{"cost-center": true, HashTagKey: true}
		for _, k := range keys {
			delete(want, k)
		}
		return len(want) == 0
	})).Return(nil).Once()

	r := &PodReconciler{
		Client:    k8sClient,
		Scheme:    scheme,
		Recorder:  record.NewFakeRecorder(10),
		AWSClient: awsMock,
	}

	ctx := context.Background()
	annotationValue := "" // empty desired tags

	eniInfo := &aws.ENIInfo{
		ID: "eni-1",
		Tags: map[string]string{
			HashTagKey: "dummy-hash",
		},
	}

	err := r.applyENITags(ctx, pod, eniInfo, annotationValue)
	require.NoError(t, err)

	awsMock.AssertNotCalled(t, "TagENI", mock.Anything, mock.Anything, mock.Anything)
	awsMock.AssertExpectations(t)

	updated := &corev1.Pod{}
	require.NoError(t, k8sClient.Get(ctx, client.ObjectKeyFromObject(pod), updated))
	_, hasLastApplied := updated.Annotations[LastAppliedAnnotationKey]
	_, hasLastHash := updated.Annotations[LastAppliedHashKey]
	assert.False(t, hasLastApplied)
	assert.False(t, hasLastHash)
}

func TestApplyENITags_SetsHashWhenManagingTagsEvenIfNoDiff(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))

	annotationValue := `{"a":"b"}`
	parsed, err := parseTags(annotationValue)
	require.NoError(t, err)
	desiredHash := computeHash(parsed)

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-hash-only",
			Namespace: "default",
			Annotations: map[string]string{
				LastAppliedAnnotationKey: annotationValue,
				LastAppliedHashKey:       desiredHash,
			},
		},
	}

	k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(pod).Build()

	awsMock := &mockAWSClientForENIOps{}
	awsMock.On("TagENI", mock.Anything, "eni-2", mock.MatchedBy(func(tags map[string]string) bool {
		return len(tags) == 1 && tags[HashTagKey] == desiredHash
	})).Return(nil).Once()

	r := &PodReconciler{
		Client:    k8sClient,
		Scheme:    scheme,
		Recorder:  record.NewFakeRecorder(10),
		AWSClient: awsMock,
	}

	ctx := context.Background()
	eniInfo := &aws.ENIInfo{ID: "eni-2", Tags: map[string]string{}}

	err = r.applyENITags(ctx, pod, eniInfo, annotationValue)
	require.NoError(t, err)

	awsMock.AssertNotCalled(t, "UntagENI", mock.Anything, mock.Anything, mock.Anything)
	awsMock.AssertExpectations(t)
}
