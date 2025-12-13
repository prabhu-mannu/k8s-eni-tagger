package controller

import (
	"context"
	"errors"
	"testing"
	"time"

	"k8s-eni-tagger/pkg/aws"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
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
	corev1.AddToScheme(scheme)

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
					return tags["default:cost-center"] == "123" && tags["default:team"] == "platform"
				})).Return(nil)
			},
			verify: func(t *testing.T, k8sClient client.Client, m *MockAWSClient) {
				// Check finalizer added
				pod := &corev1.Pod{}
				k8sClient.Get(context.Background(), client.ObjectKey{Name: "pod-success", Namespace: "default"}, pod)
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
						LastAppliedAnnotationKey: `{"default:cost-center":"123","default:team":"platform"}`,
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
					// Check for default:cost-center, default:team, and hash key
					hasCost := false
					hasTeam := false
					hasHash := false
					for _, k := range keys {
						if k == "default:cost-center" {
							hasCost = true
						}
						if k == "default:team" {
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockAWS := new(MockAWSClient)
			tt.mockSetup(mockAWS)

			k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(tt.pod).Build()
			recorder := record.NewFakeRecorder(10)

			r := &PodReconciler{
				Client:        k8sClient,
				Scheme:        scheme,
				Recorder:      recorder,
				AWSClient:     mockAWS,
				AnnotationKey: AnnotationKey,
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
