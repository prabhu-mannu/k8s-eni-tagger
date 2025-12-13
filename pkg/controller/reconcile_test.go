package controller

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

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
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// MockAWSClient for controller tests
type MockAWSClient struct {
	mock.Mock
}

func (m *MockAWSClient) GetENIInfoByIP(ctx context.Context, ip string) (*aws.ENIInfo, error) {
	args := m.Called(ctx, ip)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*aws.ENIInfo), args.Error(1)
}

func (m *MockAWSClient) TagENI(ctx context.Context, eniID string, tags map[string]string) error {
	args := m.Called(ctx, eniID, tags)
	return args.Error(0)
}

func (m *MockAWSClient) UntagENI(ctx context.Context, eniID string, tagKeys []string) error {
	args := m.Called(ctx, eniID, tagKeys)
	return args.Error(0)
}

func (m *MockAWSClient) GetEC2Client() *ec2.Client {
	return nil
}

func TestReconcile(t *testing.T) {
	scheme := runtime.NewScheme()
	err := corev1.AddToScheme(scheme)
	require.NoError(t, err)

	// Valid annotation string
	validTags := `{"cost-center":"123","team":"platform"}`

	tests := []struct {
		name            string
		pod             *corev1.Pod
		mockSetup       func(m *MockAWSClient)
		expectedRequeue bool
		expectedErr     bool
		verify          func(t *testing.T, client client.Client, m *MockAWSClient)
	}{
		{
			name: "No Annotation - Skip",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pod-no-annotation",
					Namespace: "default",
				},
			},
			mockSetup: func(m *MockAWSClient) {}, // No calls expected
		},
		{
			name: "Deletion - Untag transient errors with retries",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pod-delete-retry",
					Namespace: "default",
					Annotations: map[string]string{
						AnnotationKey:            validTags,
						LastAppliedAnnotationKey: `{"cost-center":"123","team":"platform"}`,
						LastAppliedHashKey:       "dummy-hash",
					},
					Finalizers:        []string{finalizerName},
					DeletionTimestamp: &metav1.Time{Time: time.Now()},
				},
				Status: corev1.PodStatus{
					PodIP: "10.0.0.6",
				},
			},
			mockSetup: func(m *MockAWSClient) {
				m.On("GetENIInfoByIP", mock.Anything, "10.0.0.6").Return(&aws.ENIInfo{
					ID: "eni-delete-retry",
					Tags: map[string]string{
						HashTagKey: "dummy-hash",
					},
				}, nil)

				// Simulate two transient failures then success
				call := m.On("UntagENI", mock.Anything, "eni-delete-retry", mock.MatchedBy(func(keys []string) bool {
					return true
				}))
				call.Return(errors.New("transient error")).Once()
				call.Return(errors.New("transient error")).Once()
				call.Return(nil).Once()
			},
		},
		{
			name: "No IP - Skip",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pod-no-ip",
					Namespace: "default",
					Annotations: map[string]string{
						AnnotationKey: validTags,
					},
				},
			},
			mockSetup: func(m *MockAWSClient) {},
		},
		{
			name: "Success - Tag ENI",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pod-success",
					Namespace: "default",
					Annotations: map[string]string{
						AnnotationKey: validTags,
					},
					Finalizers: []string{finalizerName},
				},
				Status: corev1.PodStatus{
					PodIP: "10.0.0.1",
				},
			},
			mockSetup: func(m *MockAWSClient) {
				m.On("GetENIInfoByIP", mock.Anything, "10.0.0.1").Return(&aws.ENIInfo{
					ID:            "eni-123",
					InterfaceType: "interface", // default
				}, nil)
				m.On("TagENI", mock.Anything, "eni-123", mock.MatchedBy(func(tags map[string]string) bool {
					return tags["cost-center"] == "123" && tags["team"] == "platform"
				})).Return(nil)
			},
			verify: func(t *testing.T, k8sClient client.Client, m *MockAWSClient) {
				// Check finalizer added
				pod := &corev1.Pod{}
				err := k8sClient.Get(context.Background(), client.ObjectKey{Name: "pod-success", Namespace: "default"}, pod)
				require.NoError(t, err)
				assert.Contains(t, pod.Finalizers, finalizerName)
			},
		},
		{
			name: "Validation Error - Invalid Tags",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pod-invalid-tags",
					Namespace: "default",
					Annotations: map[string]string{
						AnnotationKey: "invalid-json",
					},
					Finalizers: []string{finalizerName},
				},
				Status: corev1.PodStatus{PodIP: "10.0.0.1"},
			},
			mockSetup: func(m *MockAWSClient) {}, // Should prevent ENI lookup
		},
		{
			name: "ENI Validation Error - Shared ENI",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pod-shared-eni",
					Namespace: "default",
					Annotations: map[string]string{
						AnnotationKey: validTags,
					},
					Finalizers: []string{finalizerName},
				},
				Status: corev1.PodStatus{PodIP: "10.0.0.1"},
			},
			mockSetup: func(m *MockAWSClient) {
				m.On("GetENIInfoByIP", mock.Anything, "10.0.0.1").Return(&aws.ENIInfo{
					ID:       "eni-shared",
					IsShared: true,
				}, nil)
			},
		},
		{
			name: "Tagging Error",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pod-tag-error",
					Namespace: "default",
					Annotations: map[string]string{
						AnnotationKey: validTags,
					},
					Finalizers: []string{finalizerName},
				},
				Status: corev1.PodStatus{PodIP: "10.0.0.1"},
			},
			mockSetup: func(m *MockAWSClient) {
				m.On("GetENIInfoByIP", mock.Anything, "10.0.0.1").Return(&aws.ENIInfo{
					ID: "eni-tag-error",
				}, nil)
				m.On("TagENI", mock.Anything, "eni-tag-error", mock.Anything).Return(errors.New("tag failed"))
			},
			expectedErr: true,
		},
		{
			name: "AWS Error - Requeue",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pod-aws-error",
					Namespace: "default",
					Annotations: map[string]string{
						AnnotationKey: validTags,
					},
					Finalizers: []string{finalizerName}, // Add finalizer so it doesn't return early
				},
				Status: corev1.PodStatus{
					PodIP: "10.0.0.2",
				},
			},
			mockSetup: func(m *MockAWSClient) {
				m.On("GetENIInfoByIP", mock.Anything, "10.0.0.2").Return(nil, errors.New("aws error"))
			},
			expectedRequeue: true,
		},
		{
			name: "Deletion - Untag",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pod-delete",
					Namespace: "default",
					Annotations: map[string]string{
						AnnotationKey:            validTags,
						LastAppliedAnnotationKey: `{"cost-center":"123","team":"platform"}`,
						LastAppliedHashKey:       "dummy-hash",
					},
					Finalizers:        []string{finalizerName},
					DeletionTimestamp: &metav1.Time{Time: time.Now()},
				},
				Status: corev1.PodStatus{
					PodIP: "10.0.0.3",
				},
			},
			mockSetup: func(m *MockAWSClient) {
				m.On("GetENIInfoByIP", mock.Anything, "10.0.0.3").Return(&aws.ENIInfo{
					ID: "eni-delete",
					Tags: map[string]string{
						HashTagKey: "dummy-hash", // Match the hash
					},
				}, nil)
				// UntagENI should be called with keys from validTags + HashTagKey
				m.On("UntagENI", mock.Anything, "eni-delete", mock.MatchedBy(func(keys []string) bool {
					// Check for cost-center, team, and hash key
					hasCost := false
					hasTeam := false
					hasHash := false
					for _, k := range keys {
						if k == "cost-center" {
							hasCost = true
						}
						if k == "team" {
							hasTeam = true
						}
						if k == HashTagKey {
							hasHash = true
						}
					}
					return hasCost && hasTeam && hasHash && len(keys) == 3
				})).Return(nil)
			},
			verify: func(t *testing.T, k8sClient client.Client, m *MockAWSClient) {
				// Check finalizer removed
				pod := &corev1.Pod{}
				err := k8sClient.Get(context.Background(), client.ObjectKey{Name: "pod-delete", Namespace: "default"}, pod)

				// If pod is gone, that means finalizers were removed and it was GCe (success)
				// If pod exists, finalizers should be empty
				if err == nil {
					assert.NotContains(t, pod.Finalizers, finalizerName)
				} else {
					// We expect either no error (pod updated) or NotFound (pod deleted)
					// assert.NoError won't work if it's NotFound, so check manually
					if err.Error() != `pods "pod-delete" not found` {
						// For robustness, check if it's actually an api error
						// but message check is simple for now
						assert.Fail(t, "Unexpected error: "+err.Error())
					}
				}
			},
		},
		{
			name: "Transition from non-namespaced to namespaced tags",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pod-transition",
					Namespace: "production",
					Annotations: map[string]string{
						AnnotationKey:            validTags, // CostCenter=123, Team=platform
						LastAppliedAnnotationKey: validTags, // Old non-namespaced tags
						LastAppliedHashKey:       "dummy-hash",
					},
					Finalizers: []string{finalizerName},
				},
				Status: corev1.PodStatus{
					PodIP: "10.0.0.4",
				},
			},
			mockSetup: func(m *MockAWSClient) {
				// ENI has old non-namespaced tags
				m.On("GetENIInfoByIP", mock.Anything, "10.0.0.4").Return(&aws.ENIInfo{
					ID: "eni-transition",
					Tags: map[string]string{
						"CostCenter": "123",
						"Team":       "platform",
						HashTagKey:   "dummy-hash",
					},
				}, nil)
				// Should add namespaced tags and remove old ones
				m.On("TagENI", mock.Anything, "eni-transition", mock.MatchedBy(func(tags map[string]string) bool {
					// Should have production:cost-center and production:team
					hasCost := tags["production:cost-center"] == "123"
					hasTeam := tags["production:team"] == "platform"
					return hasCost && hasTeam
				})).Return(nil)
				// Should remove old non-namespaced tags
				m.On("UntagENI", mock.Anything, "eni-transition", mock.MatchedBy(func(keys []string) bool {
					// Should remove cost-center, team (hash is updated via TagENI)
					hasCost := false
					hasTeam := false
					for _, k := range keys {
						if k == "cost-center" {
							hasCost = true
						}
						if k == "team" {
							hasTeam = true
						}
					}
					return hasCost && hasTeam && len(keys) == 2
				})).Return(nil)
			},
			verify: func(t *testing.T, k8sClient client.Client, m *MockAWSClient) {
				// Check that last-applied was updated to namespaced tags
				pod := &corev1.Pod{}
				err := k8sClient.Get(context.Background(), client.ObjectKey{Name: "pod-transition", Namespace: "production"}, pod)
				assert.NoError(t, err)
				// The last-applied should now contain namespaced tags
				assert.Contains(t, pod.Annotations, LastAppliedAnnotationKey)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockAWS := new(MockAWSClient)
			tt.mockSetup(mockAWS)

			k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(tt.pod).Build()
			recorder := record.NewFakeRecorder(10)

			r := &PodReconciler{
				Client:            k8sClient,
				Scheme:            scheme,
				Recorder:          recorder,
				AWSClient:         mockAWS,
				AnnotationKey:     AnnotationKey,
				PodRateLimiters:   &sync.Map{},
				PodRateLimitQPS:   0.1,
				PodRateLimitBurst: 1,
			}
			// Enable namespacing for transition test
			if tt.name == "Transition from non-namespaced to namespaced tags" {
				r.TagNamespace = "enable"
			}

			req := reconcile.Request{
				NamespacedName: client.ObjectKey{
					Name:      tt.pod.Name,
					Namespace: tt.pod.Namespace,
				},
			}

			res, err := r.Reconcile(context.Background(), req)

			if tt.expectedErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			if tt.expectedRequeue {
				assert.NotZero(t, res.RequeueAfter)
			}

			if tt.verify != nil {
				tt.verify(t, k8sClient, mockAWS)
			}

			mockAWS.AssertExpectations(t)
		})
	}
}
