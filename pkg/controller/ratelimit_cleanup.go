package controller

import (
	"context"
	"time"

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

// cleanupStaleLimiters removes rate limiters that haven't been accessed for the cleanup threshold
func (r *PodReconciler) cleanupStaleLimiters(ctx context.Context) {
	logger := log.FromContext(ctx).WithName("rate-limiter-cleanup")

	if r.RateLimiterCleanupThreshold <= 0 {
		logger.V(1).Info("Rate limiter cleanup disabled (threshold not set)")
		return
	}

	now := time.Now()
	cutoff := now.Add(-r.RateLimiterCleanupThreshold)
	removed := 0

	r.PodRateLimiters.Range(func(key, value interface{}) bool {
		podKey := key.(string)
		entry := value.(*RateLimiterEntry)

		if entry.LastAccess.Before(cutoff) {
			r.PodRateLimiters.Delete(podKey)
			removed++
			logger.V(1).Info("Removed stale rate limiter", "pod", podKey, "lastAccess", entry.LastAccess)
		}
		return true
	})

	if removed > 0 {
		logger.Info("Cleaned up stale rate limiters", "removed", removed, "threshold", r.RateLimiterCleanupThreshold)
	}
}
