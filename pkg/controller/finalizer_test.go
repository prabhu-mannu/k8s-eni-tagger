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

func TestEnsureFinalizer(t *testing.T) {
	scheme := runtime.NewScheme()
	err := corev1.AddToScheme(scheme)
	require.NoError(t, err)

	tests := []struct {
		name          string
		pod           *corev1.Pod
		expectUpdated bool
		expectError   bool
	}{
		{
			name: "Add finalizer when missing",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
				},
			},
			expectUpdated: true,
			expectError:   false,
		},
		{
			name: "Finalizer already present",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-pod",
					Namespace:  "default",
					Finalizers: []string{finalizerName},
				},
			},
			expectUpdated: false,
			expectError:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(tt.pod).Build()

			r := &PodReconciler{
				Client: k8sClient,
				Scheme: scheme,
			}

			ctx := context.Background()
			updated, err := r.ensureFinalizer(ctx, tt.pod)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectUpdated, updated)

				// Verify finalizer was added if expected
				if tt.expectUpdated {
					assert.Contains(t, tt.pod.Finalizers, finalizerName)
				}
			}
		})
	}
}
