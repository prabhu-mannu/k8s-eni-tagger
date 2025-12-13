package controller

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/stretchr/testify/assert"
)

func TestParseAndCompareTags_Namespacing(t *testing.T) {
	tests := []struct {
		name             string
		pod              *corev1.Pod
		annotationValue  string
		lastAppliedValue string
		tagNamespace     string
		expectedCurrent  map[string]string
		expectedLast     map[string]string
		expectedToAdd    map[string]string
		expectedToRemove []string
		expectError      bool
	}{
		{
			name: "No namespacing (default)",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "production",
				},
			},
			annotationValue:  `{"cost-center":"123","team":"platform"}`,
			lastAppliedValue: "",
			tagNamespace:     "",
			expectedCurrent: map[string]string{
				"cost-center": "123",
				"team":        "platform",
			},
			expectedLast:     map[string]string{},
			expectedToAdd:    map[string]string{"cost-center": "123", "team": "platform"},
			expectedToRemove: []string{},
			expectError:      false,
		},
		{
			name: "With namespacing enabled",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "production",
				},
			},
			annotationValue:  `{"cost-center":"123","team":"platform"}`,
			lastAppliedValue: "",
			tagNamespace:     "enable",
			expectedCurrent: map[string]string{
				"production:cost-center": "123",
				"production:team":        "platform",
			},
			expectedLast:     map[string]string{},
			expectedToAdd:    map[string]string{"production:cost-center": "123", "production:team": "platform"},
			expectedToRemove: []string{},
			expectError:      false,
		},
		{
			name: "Invalid TagNamespace value treated as disabled",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "production",
				},
			},
			annotationValue:  `{"cost-center":"123","team":"platform"}`,
			lastAppliedValue: "",
			tagNamespace:     "invalid",
			expectedCurrent: map[string]string{
				"cost-center": "123",
				"team":        "platform",
			},
			expectedLast:     map[string]string{},
			expectedToAdd:    map[string]string{"cost-center": "123", "team": "platform"},
			expectedToRemove: []string{},
			expectError:      false,
		},
		{
			name: "Transition from non-namespaced to namespaced",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "production",
				},
			},
			annotationValue:  `{"cost-center":"123","team":"platform"}`,
			lastAppliedValue: `{"cost-center":"123","team":"platform"}`,
			tagNamespace:     "enable",
			expectedCurrent: map[string]string{
				"production:cost-center": "123",
				"production:team":        "platform",
			},
			expectedLast: map[string]string{
				"cost-center": "123",
				"team":        "platform",
			},
			expectedToAdd: map[string]string{
				"production:cost-center": "123",
				"production:team":        "platform",
			},
			expectedToRemove: []string{"cost-center", "team"},
			expectError:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &PodReconciler{
				TagNamespace: tt.tagNamespace,
			}

			currentTags, lastAppliedTags, diff, err := r.parseAndCompareTags(
				context.Background(), tt.pod, tt.annotationValue, tt.lastAppliedValue)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedCurrent, currentTags)
				assert.Equal(t, tt.expectedLast, lastAppliedTags)
				if diff != nil {
					assert.Equal(t, tt.expectedToAdd, diff.toAdd)
					assert.Equal(t, tt.expectedToRemove, diff.toRemove)
				}
			}
		})
	}
}
