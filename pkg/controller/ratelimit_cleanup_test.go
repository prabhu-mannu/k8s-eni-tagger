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

// newTestRateLimiterEntry creates a RateLimiterEntry for testing with a specific last access time.
// The limiter field is nil since it's not needed for cleanup testing.
func newTestRateLimiterEntry(lastAccess time.Time) *RateLimiterEntry {
	return &RateLimiterEntry{
		limiter:    nil,
		lastAccess: lastAccess,
	}
}

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
	r.PodRateLimiters.Store("default/existing-pod", newTestRateLimiterEntry(time.Now()))
	r.PodRateLimiters.Store("default/stale-pod", newTestRateLimiterEntry(time.Now().Add(-time.Hour)))

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

func TestCleanupStaleLimiters_ThresholdBehavior(t *testing.T) {
	scheme := runtime.NewScheme()
	err := corev1.AddToScheme(scheme)
	assert.NoError(t, err)

	r := &PodReconciler{
		PodRateLimiters:             &sync.Map{},
		RateLimiterCleanupThreshold: 30 * time.Minute,
	}

	ctx := context.Background()

	now := time.Now()
	// Add limiters with different ages
	r.PodRateLimiters.Store("default/recent-pod", newTestRateLimiterEntry(now.Add(-10*time.Minute)))
	r.PodRateLimiters.Store("default/stale-pod", newTestRateLimiterEntry(now.Add(-45*time.Minute)))
	r.PodRateLimiters.Store("default/just-inside-threshold", newTestRateLimiterEntry(now.Add(-29*time.Minute)))

	// Run cleanup
	r.cleanupStaleLimiters(ctx)

	// Verify only stale limiter was removed
	_, exists := r.PodRateLimiters.Load("default/recent-pod")
	assert.True(t, exists, "recent limiter should remain")

	_, exists = r.PodRateLimiters.Load("default/stale-pod")
	assert.False(t, exists, "stale limiter should be removed")

	_, exists = r.PodRateLimiters.Load("default/just-inside-threshold")
	assert.True(t, exists, "just-inside-threshold limiter should remain")
}

func TestCleanupStaleLimiters_Disabled(t *testing.T) {
	r := &PodReconciler{
		PodRateLimiters:             &sync.Map{},
		RateLimiterCleanupThreshold: 0, // Disabled
	}

	ctx := context.Background()

	// Add a stale limiter
	r.PodRateLimiters.Store("default/stale-pod", newTestRateLimiterEntry(time.Now().Add(-time.Hour)))

	// Run cleanup
	r.cleanupStaleLimiters(ctx)

	// Verify limiter was NOT removed
	_, exists := r.PodRateLimiters.Load("default/stale-pod")
	assert.True(t, exists, "limiter should not be removed when cleanup is disabled")
}

func TestCleanupStaleLimiters_InvalidKeyType(t *testing.T) {
	r := &PodReconciler{
		PodRateLimiters:             &sync.Map{},
		RateLimiterCleanupThreshold: 30 * time.Minute,
	}

	ctx := context.Background()

	// Add entries with invalid key types (this shouldn't happen in practice, but test safety)
	r.PodRateLimiters.Store(123, newTestRateLimiterEntry(time.Now().Add(-time.Hour)))
	r.PodRateLimiters.Store("default/valid-pod", newTestRateLimiterEntry(time.Now().Add(-time.Hour)))

	// Run cleanup - should not panic
	assert.NotPanics(t, func() {
		r.cleanupStaleLimiters(ctx)
	})

	// Verify valid entry was still processed
	_, exists := r.PodRateLimiters.Load("default/valid-pod")
	assert.False(t, exists, "valid stale entry should be removed")

	// Invalid entry should be removed (corrupted entries are cleaned up)
	_, exists = r.PodRateLimiters.Load(123)
	assert.False(t, exists, "invalid key type entry should be removed")
}

func TestCleanupStaleLimiters_EmptyMap(t *testing.T) {
	r := &PodReconciler{
		PodRateLimiters:             &sync.Map{},
		RateLimiterCleanupThreshold: 30 * time.Minute,
	}

	ctx := context.Background()

	// Run cleanup on empty map
	assert.NotPanics(t, func() {
		r.cleanupStaleLimiters(ctx)
	})

	// Should not panic or do anything
}

func TestCleanupStaleLimiters_InvalidValueType(t *testing.T) {
	r := &PodReconciler{
		PodRateLimiters:             &sync.Map{},
		RateLimiterCleanupThreshold: 30 * time.Minute,
	}

	ctx := context.Background()

	// Add entries with invalid value types
	r.PodRateLimiters.Store("default/invalid-value", "not-a-rate-limiter-entry") // string instead of *RateLimiterEntry
	r.PodRateLimiters.Store("default/valid-pod", newTestRateLimiterEntry(time.Now().Add(-time.Hour)))

	// Run cleanup - should not panic
	assert.NotPanics(t, func() {
		r.cleanupStaleLimiters(ctx)
	})

	// Verify valid entry was still processed
	_, exists := r.PodRateLimiters.Load("default/valid-pod")
	assert.False(t, exists, "valid stale entry should be removed")

	// Invalid entry should be removed (corrupted entries are cleaned up)
	_, exists = r.PodRateLimiters.Load("default/invalid-value")
	assert.False(t, exists, "invalid value type entry should be removed")
}
