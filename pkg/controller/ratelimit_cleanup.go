package controller

import (
	"context"
	"time"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// StartRateLimiterCleanup starts a background goroutine that periodically cleans up
// stale pod rate limiters from pods that no longer exist.
func (r *PodReconciler) StartRateLimiterCleanup(ctx context.Context, interval time.Duration) {
	if interval <= 0 || r.PodRateLimitQPS <= 0 {
		return // Cleanup disabled
	}

	logger := log.FromContext(ctx).WithName("rate-limiter-cleanup")
	ticker := time.NewTicker(interval)

	go func() {
		defer ticker.Stop()
		logger.Info("Starting rate limiter cleanup", "interval", interval)

		for {
			select {
			case <-ctx.Done():
				logger.Info("Stopping rate limiter cleanup")
				return
			case <-ticker.C:
				r.cleanupStaleLimiters(ctx)
			}
		}
	}()
}

// cleanupStaleLimiters removes rate limiters for pods that no longer exist
func (r *PodReconciler) cleanupStaleLimiters(ctx context.Context) {
	logger := log.FromContext(ctx).WithName("rate-limiter-cleanup")

	// Collect all current pod keys
	existingPods := make(map[string]bool)
	podList := &corev1.PodList{}
	if err := r.List(ctx, podList); err != nil {
		logger.Error(err, "Failed to list pods for cleanup")
		return
	}

	for _, pod := range podList.Items {
		key := client.ObjectKeyFromObject(&pod).String()
		existingPods[key] = true
	}

	// Remove limiters for non-existent pods
	removed := 0
	r.PodRateLimiters.Range(func(key, value interface{}) bool {
		podKey := key.(string)
		if !existingPods[podKey] {
			r.PodRateLimiters.Delete(podKey)
			removed++
		}
		return true
	})

	if removed > 0 {
		logger.Info("Cleaned up stale rate limiters", "removed", removed, "remaining", len(existingPods))
	}
}
