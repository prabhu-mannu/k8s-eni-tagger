package cache

import (
	"context"
	"sync"

	"k8s-eni-tagger/pkg/aws"
	"k8s-eni-tagger/pkg/metrics"

	"sigs.k8s.io/controller-runtime/pkg/log"
)

// ENICache provides caching for ENI lookups based on pod lifecycle.
// Since an ENI-to-IP mapping doesn't change during a pod's lifetime,
// entries are cached until explicitly invalidated (on pod deletion).
// This reduces AWS API calls significantly.
type ENICache struct {
	mu        sync.RWMutex
	cache     map[string]*aws.ENIInfo
	awsClient aws.Client

	// ConfigMap persistence (optional)
	cmPersister ConfigMapPersister
}

// ConfigMapPersister interface for optional ConfigMap persistence
type ConfigMapPersister interface {
	Load(ctx context.Context) (map[string]*aws.ENIInfo, error)
	Save(ctx context.Context, ip string, info *aws.ENIInfo) error
	Delete(ctx context.Context, ip string) error
}

// NewENICache creates a new ENI cache
func NewENICache(awsClient aws.Client) *ENICache {
	return &ENICache{
		cache:     make(map[string]*aws.ENIInfo),
		awsClient: awsClient,
	}
}

// WithConfigMapPersister adds ConfigMap persistence to the cache
func (c *ENICache) WithConfigMapPersister(persister ConfigMapPersister) *ENICache {
	c.cmPersister = persister
	return c
}

// LoadFromConfigMap loads cached entries from ConfigMap on startup
func (c *ENICache) LoadFromConfigMap(ctx context.Context) error {
	if c.cmPersister == nil {
		return nil
	}

	logger := log.FromContext(ctx)
	entries, err := c.cmPersister.Load(ctx)
	if err != nil {
		logger.Error(err, "Failed to load ENI cache from ConfigMap")
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	for ip, info := range entries {
		c.cache[ip] = info
	}
	logger.Info("Loaded ENI cache from ConfigMap", "entries", len(entries))
	return nil
}

// GetENIInfoByIP returns ENI info for an IP, using cache if available
func (c *ENICache) GetENIInfoByIP(ctx context.Context, ip string) (*aws.ENIInfo, error) {
	// Try in-memory cache first
	if info, ok := c.get(ip); ok {
		metrics.CacheHitsTotal.Inc()
		return info, nil
	}
	metrics.CacheMissesTotal.Inc()

	// Cache miss - call AWS API
	info, err := c.awsClient.GetENIInfoByIP(ctx, ip)
	if err != nil {
		return nil, err
	}

	// Store in cache (persists until pod deletion)
	c.set(ctx, ip, info)
	return info, nil
}

// get retrieves from in-memory cache
func (c *ENICache) get(ip string) (*aws.ENIInfo, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	info, ok := c.cache[ip]
	return info, ok
}

// set stores in in-memory cache and optionally persists to ConfigMap
func (c *ENICache) set(ctx context.Context, ip string, info *aws.ENIInfo) {
	c.mu.Lock()
	c.cache[ip] = info
	c.mu.Unlock()

	// Async persist to ConfigMap if enabled
	if c.cmPersister != nil {
		go func() {
			if err := c.cmPersister.Save(ctx, ip, info); err != nil {
				log.FromContext(ctx).Error(err, "Failed to persist ENI to ConfigMap", "ip", ip)
			}
		}()
	}
}

// Invalidate removes an entry from the cache (called on pod deletion)
func (c *ENICache) Invalidate(ctx context.Context, ip string) {
	c.mu.Lock()
	delete(c.cache, ip)
	c.mu.Unlock()

	if c.cmPersister != nil {
		go func() {
			if err := c.cmPersister.Delete(ctx, ip); err != nil {
				log.FromContext(ctx).Error(err, "Failed to delete ENI from ConfigMap", "ip", ip)
			}
		}()
	}
}

// Size returns the current cache size (for testing/metrics)
func (c *ENICache) Size() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.cache)
}
