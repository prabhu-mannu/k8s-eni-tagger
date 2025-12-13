package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestUpdateStatus(t *testing.T) {
	scheme := runtime.NewScheme()
	err := corev1.AddToScheme(scheme)
	require.NoError(t, err)

	tests := []struct {
		name         string
		initialPod   *corev1.Pod
		status       corev1.ConditionStatus
		reason       string
		message      string
		expectStatus corev1.ConditionStatus
	}{
		{
			name: "Add new condition",
			initialPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
				},
				Status: corev1.PodStatus{},
			},
			status:       corev1.ConditionTrue,
			reason:       "ENITagged",
			message:      "Successfully tagged ENI",
			expectStatus: corev1.ConditionTrue,
		},
		{
			name: "Update existing condition",
			initialPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
				},
				Status: corev1.PodStatus{
					Conditions: []corev1.PodCondition{
						{
							Type:    corev1.PodConditionType(ConditionTypeEniTagged),
							Status:  corev1.ConditionFalse,
							Reason:  "ENITaggingFailed",
							Message: "Failed to tag ENI",
						},
					},
				},
			},
			status:       corev1.ConditionTrue,
			reason:       "ENITagged",
			message:      "Successfully tagged ENI",
			expectStatus: corev1.ConditionTrue,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(tt.initialPod).Build()

			r := &PodReconciler{
				Client: k8sClient,
				Scheme: scheme,
			}

			ctx := context.Background()
			err := r.updateStatus(ctx, tt.initialPod, tt.status, tt.reason, tt.message)

			assert.NoError(t, err)

			// Verify the condition was set correctly
			found := false
			for _, condition := range tt.initialPod.Status.Conditions {
				if condition.Type == corev1.PodConditionType(ConditionTypeEniTagged) {
					assert.Equal(t, tt.expectStatus, condition.Status)
					assert.Equal(t, tt.reason, condition.Reason)
					assert.Equal(t, tt.message, condition.Message)
					assert.NotZero(t, condition.LastTransitionTime)
					found = true
					break
				}
			}
			assert.True(t, found, "ENI tagged condition should be present")
		})
	}
}

func TestIsConditionTrue(t *testing.T) {
	tests := []struct {
		name           string
		conditions     []corev1.PodCondition
		conditionType  string
		expectedResult bool
	}{
		{
			name: "Condition true",
			conditions: []corev1.PodCondition{
				{
					Type:   corev1.PodConditionType(ConditionTypeEniTagged),
					Status: corev1.ConditionTrue,
				},
			},
			conditionType:  ConditionTypeEniTagged,
			expectedResult: true,
		},
		{
			name: "Condition false",
			conditions: []corev1.PodCondition{
				{
					Type:   corev1.PodConditionType(ConditionTypeEniTagged),
					Status: corev1.ConditionFalse,
				},
			},
			conditionType:  ConditionTypeEniTagged,
			expectedResult: false,
		},
		{
			name: "Condition missing",
			conditions: []corev1.PodCondition{
				{
					Type:   corev1.PodConditionType("OtherCondition"),
					Status: corev1.ConditionTrue,
				},
			},
			conditionType:  ConditionTypeEniTagged,
			expectedResult: false,
		},
		{
			name:           "No conditions",
			conditions:     []corev1.PodCondition{},
			conditionType:  ConditionTypeEniTagged,
			expectedResult: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isConditionTrue(tt.conditions, tt.conditionType)
			assert.Equal(t, tt.expectedResult, result)
		})
	}
}
