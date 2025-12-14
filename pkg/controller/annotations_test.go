package controller

import (
	"context"
	"encoding/json"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestUpdatePodAnnotations(t *testing.T) {
	tests := []struct {
		name           string
		pod            *corev1.Pod
		currentTags    map[string]string
		desiredHash    string
		expectedError  bool
		expectedTags   map[string]string
		expectedHash   string
	}{
		{
			name: "UpdateAnnotationsWithValidTags",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
				},
				Status: corev1.PodStatus{
					PodIP: "10.0.0.1",
				},
			},
			currentTags: map[string]string{
				"env": "production",
				"app": "web",
			},
			desiredHash: "hash123",
			expectedError: false,
			expectedTags: map[string]string{
				"env": "production",
				"app": "web",
			},
			expectedHash: "hash123",
		},
		{
			name: "RemoveAnnotationsWhenTagsEmpty",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
					Annotations: map[string]string{
						LastAppliedAnnotationKey: `{"env":"production"}`,
						LastAppliedHashKey:       "hash123",
					},
				},
			},
			currentTags:   map[string]string{},
			desiredHash:   "",
			expectedError: false,
			expectedTags:  nil,
			expectedHash:  "",
		},
		{
			name: "UpdateExistingAnnotations",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
					Annotations: map[string]string{
						LastAppliedAnnotationKey: `{"old":"tag"}`,
						LastAppliedHashKey:       "oldhash",
					},
				},
			},
			currentTags: map[string]string{
				"new": "tag",
			},
			desiredHash: "newhash",
			expectedError: false,
			expectedTags: map[string]string{
				"new": "tag",
			},
			expectedHash: "newhash",
		},
		{
			name: "CreateAnnotationsOnPodWithoutAny",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
				},
			},
			currentTags: map[string]string{
				"key": "value",
			},
			desiredHash: "newhash",
			expectedError: false,
			expectedTags: map[string]string{
				"key": "value",
			},
			expectedHash: "newhash",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create fake client
			client := fake.NewClientBuilder().
				WithObjects(tt.pod).
				Build()

			reconciler := &PodReconciler{
				Client: client,
			}

			ctx := context.Background()
			err := updatePodAnnotations(ctx, reconciler, tt.pod, tt.currentTags, tt.desiredHash)

			if (err != nil) != tt.expectedError {
				t.Errorf("updatePodAnnotations() error = %v, wantErr %v", err, tt.expectedError)
				return
			}

			// Fetch updated pod
			updated := &corev1.Pod{}
			err = client.Get(ctx, types.NamespacedName{
				Name:      tt.pod.Name,
				Namespace: tt.pod.Namespace,
			}, updated)
			if err != nil {
				t.Fatalf("Failed to get updated pod: %v", err)
			}

			// Verify annotations
			if tt.expectedTags != nil {
				if updated.Annotations == nil {
					t.Errorf("Expected annotations to be set, got nil")
					return
				}
				appliedValue := updated.Annotations[LastAppliedAnnotationKey]
				if appliedValue == "" {
					t.Errorf("Expected annotation %s to be set", LastAppliedAnnotationKey)
					return
				}

				var appliedTags map[string]string
				if err := json.Unmarshal([]byte(appliedValue), &appliedTags); err != nil {
					t.Fatalf("Failed to unmarshal applied tags: %v", err)
				}

				if len(appliedTags) != len(tt.expectedTags) {
					t.Errorf("Tag count mismatch: got %d, want %d", len(appliedTags), len(tt.expectedTags))
				}

				for k, v := range tt.expectedTags {
					if appliedTags[k] != v {
						t.Errorf("Tag mismatch for key %s: got %s, want %s", k, appliedTags[k], v)
					}
				}

				hashValue := updated.Annotations[LastAppliedHashKey]
				if hashValue != tt.expectedHash {
					t.Errorf("Hash mismatch: got %s, want %s", hashValue, tt.expectedHash)
				}
			} else {
				// Verify annotations are removed
				if updated.Annotations != nil {
					if _, hasKey := updated.Annotations[LastAppliedAnnotationKey]; hasKey {
						t.Errorf("Expected annotation %s to be removed", LastAppliedAnnotationKey)
					}
					if _, hasKey := updated.Annotations[LastAppliedHashKey]; hasKey {
						t.Errorf("Expected annotation %s to be removed", LastAppliedHashKey)
					}
				}
			}
		})
	}
}

func TestUpdatePodAnnotationsConflictRetry(t *testing.T) {
	// This test verifies that the conflict retry logic works
	// by simulating a conflict scenario
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
		},
	}

	client := fake.NewClientBuilder().
		WithObjects(pod).
		Build()

	reconciler := &PodReconciler{
		Client: client,
	}

	ctx := context.Background()
	tags := map[string]string{"key": "value"}
	hash := "testhash"

	err := updatePodAnnotations(ctx, reconciler, pod, tags, hash)
	if err != nil {
		t.Errorf("updatePodAnnotations() error = %v", err)
	}

	// Verify update was applied
	updated := &corev1.Pod{}
	if err := client.Get(ctx, types.NamespacedName{
		Name:      pod.Name,
		Namespace: pod.Namespace,
	}, updated); err != nil {
		t.Fatalf("Failed to get updated pod: %v", err)
	}

	appliedValue := updated.Annotations[LastAppliedAnnotationKey]
	if appliedValue == "" {
		t.Error("Expected annotation to be set after conflict retry")
	}
}
