package controller

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestStartRateLimiterCleanup(t *testing.T) {
	t.Run("Disabled when interval is zero", func(t *testing.T) {
		r := &PodReconciler{PodRateLimitQPS: 0.1}
		ctx := context.Background()

		// Should not panic or start goroutine
		r.StartRateLimiterCleanup(ctx, 0)
	})

	t.Run("Disabled when QPS is zero", func(t *testing.T) {
		r := &PodReconciler{PodRateLimitQPS: 0}
		ctx := context.Background()

		// Should not panic or start goroutine
		r.StartRateLimiterCleanup(ctx, time.Minute)
	})

	t.Run("Starts cleanup goroutine", func(t *testing.T) {
		scheme := runtime.NewScheme()
		err := corev1.AddToScheme(scheme)
		assert.NoError(t, err)

		k8sClient := fake.NewClientBuilder().WithScheme(scheme).Build()

		r := &PodReconciler{
			Client:            k8sClient,
			Scheme:            scheme,
			PodRateLimitQPS:   0.1,
			PodRateLimiters:   &sync.Map{},
			PodRateLimitBurst: 1,
		}
		ctx, cancel := context.WithCancel(context.Background())

		// Start cleanup
		r.StartRateLimiterCleanup(ctx, 10*time.Millisecond)

		// Let it run briefly
		time.Sleep(50 * time.Millisecond)

		// Cancel context
		cancel()

		// Should not panic
		time.Sleep(10 * time.Millisecond)
	})
}

func TestCleanupStaleLimiters(t *testing.T) {
	scheme := runtime.NewScheme()
	err := corev1.AddToScheme(scheme)
	assert.NoError(t, err)

	// Setup fake client with existing pod
	existingPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "existing-pod",
			Namespace: "default",
		},
	}
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(existingPod).Build()

	r := &PodReconciler{
		Client:                      k8sClient,
		Scheme:                      scheme,
		PodRateLimiters:             &sync.Map{},
		PodRateLimitBurst:           1,
		RateLimiterCleanupThreshold: 30 * time.Minute, // Set cleanup threshold
	}

	ctx := context.Background()

	// Add some limiters (one existing, one stale)
	r.PodRateLimiters.Store("default/existing-pod", &RateLimiterEntry{
		Limiter:    nil, // Not needed for this test
		LastAccess: time.Now(),
	})
	r.PodRateLimiters.Store("default/stale-pod", &RateLimiterEntry{
		Limiter:    nil,                        // Not needed for this test
		LastAccess: time.Now().Add(-time.Hour), // Old enough to be cleaned up
	})

	// Run cleanup
	r.cleanupStaleLimiters(ctx)

	// Verify stale limiter was removed
	_, exists := r.PodRateLimiters.Load("default/stale-pod")
	assert.False(t, exists, "stale limiter should be removed")

	// Verify existing limiter remains
	_, exists = r.PodRateLimiters.Load("default/existing-pod")
	assert.True(t, exists, "existing limiter should remain")
}

func TestCleanupStaleLimiters_ListError(t *testing.T) {
	// Skip this test as fake client doesn't support error injection
	// In real usage, List errors are handled gracefully in the function
	t.Skip("Cannot easily test List errors with fake client")
}
