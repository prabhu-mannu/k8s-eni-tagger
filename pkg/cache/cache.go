package cache

import (
	"context"
	"sync"
	"time"

	"k8s-eni-tagger/pkg/aws"
	"k8s-eni-tagger/pkg/metrics"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// cacheEntry represents an in-memory cache entry with access tracking for LRU eviction.
type cacheEntry struct {
	Info       *aws.ENIInfo
	LastAccess time.Time
}

// Cache defines the interface for ENI caching
type Cache interface {
	GetENIInfoByIP(ctx context.Context, ip string) (*aws.ENIInfo, error)
	Invalidate(ctx context.Context, ip string)
	LoadFromConfigMap(ctx context.Context) error
	WithConfigMapPersister(persister ConfigMapPersister) *ENICache
}

// ENICache provides caching for ENI lookups based on pod lifecycle.
// Since an ENI-to-IP mapping doesn't change during a pod's lifetime,
// entries are cached until explicitly invalidated (on pod deletion).
// This reduces AWS API calls significantly.
type ENICache struct {
	mu        sync.RWMutex
	cache     map[string]*cacheEntry
	awsClient aws.Client

	// ConfigMap persistence (optional)
	cmPersister ConfigMapPersister

	// Flush configuration
	flushInterval time.Duration
	flushTicker   *time.Ticker
	flushDone     chan struct{}
	flushOnce     sync.Once

	// Shutdown context for graceful cleanup
	shutdownCtx    context.Context
	shutdownCancel context.CancelFunc
}

// ConfigMapPersister interface for optional ConfigMap persistence
type ConfigMapPersister interface {
	Load(ctx context.Context) (map[string]*cacheEntry, error)
	Flush(ctx context.Context, entries map[string]*cacheEntry) error
	CleanupStaleShards(ctx context.Context) error
	SetShardConfig(shards int, maxBytesPerShard int64)
}

// NewENICache creates a new ENI cache
func NewENICache(awsClient aws.Client) *ENICache {
	shutdownCtx, shutdownCancel := context.WithCancel(context.Background())
	c := &ENICache{
		cache:          make(map[string]*cacheEntry),
		awsClient:      awsClient,
		flushInterval:  1 * time.Minute, // default, configurable via SetFlushInterval
		flushDone:      make(chan struct{}),
		shutdownCtx:    shutdownCtx,
		shutdownCancel: shutdownCancel,
	}
	return c
}

// SetFlushInterval sets the flush interval for ConfigMap persistence.
func (c *ENICache) SetFlushInterval(interval time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if interval > 0 {
		c.flushInterval = interval
	}
}

// WithConfigMapPersister adds ConfigMap persistence to the cache
func (c *ENICache) WithConfigMapPersister(persister ConfigMapPersister) *ENICache {
	c.cmPersister = persister
	c.startFlushWorker()
	return c
}

// LoadFromConfigMap loads cached entries from ConfigMap on startup
func (c *ENICache) LoadFromConfigMap(ctx context.Context) error {
	if c.cmPersister == nil {
		return nil
	}

	logger := log.FromContext(ctx)

	// Clean up stale shards before loading
	if err := c.cmPersister.CleanupStaleShards(ctx); err != nil {
		logger.Error(err, "Failed to cleanup stale ConfigMap shards")
		// Don't fail startup on cleanup error
	}

	entries, err := c.cmPersister.Load(ctx)
	if err != nil {
		logger.Error(err, "Failed to load ENI cache from ConfigMap")
		return err
	}

	// Load valid entries to in-memory cache with LastAccess timestamp
	c.mu.Lock()
	for ip, entry := range entries {
		c.cache[ip] = entry
	}
	c.mu.Unlock()
	logger.Info("Loaded ENI cache from ConfigMap", "entries", len(entries))

	return nil
}

// GetENIInfoByIP returns ENI info for an IP, using cache if available
func (c *ENICache) GetENIInfoByIP(ctx context.Context, ip string) (*aws.ENIInfo, error) {
	// Try in-memory cache first
	if entry, ok := c.getEntry(ip); ok {
		metrics.CacheHitsTotal.Inc()
		return entry.Info, nil
	}
	metrics.CacheMissesTotal.Inc()

	// Cache miss - call AWS API
	info, err := c.awsClient.GetENIInfoByIP(ctx, ip)
	if err != nil {
		return nil, err
	}

	// Store in cache (persists until pod deletion)
	c.setEntry(ip, info)
	return info, nil
}

// getEntry retrieves from in-memory cache and updates LastAccess
func (c *ENICache) getEntry(ip string) (*cacheEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.cache[ip]
	if ok {
		// Update LastAccess on hit for LRU eviction tracking
		entry.LastAccess = time.Now()
	}
	return entry, ok
}

// setEntry stores in in-memory cache with current timestamp
func (c *ENICache) setEntry(ip string, info *aws.ENIInfo) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.cache[ip] = &cacheEntry{
		Info:       info,
		LastAccess: time.Now(),
	}
}

// Invalidate removes an entry from the cache (called on pod deletion)
func (c *ENICache) Invalidate(ctx context.Context, ip string) {
	// Remove from in-memory immediately
	c.mu.Lock()
	delete(c.cache, ip)
	c.mu.Unlock()
}

func (c *ENICache) startFlushWorker() {
	c.flushOnce.Do(func() {
		go c.flushWorker()
	})
}

// flushWorker periodically snapshots the cache and flushes to ConfigMap shards
func (c *ENICache) flushWorker() {
	logger := log.Log.WithName("eni-cache-flush-worker")

	c.mu.RLock()
	flushInterval := c.flushInterval
	c.mu.RUnlock()

	c.flushTicker = time.NewTicker(flushInterval)
	defer c.flushTicker.Stop()

	for {
		select {
		case <-c.shutdownCtx.Done():
			// Final flush on shutdown
			c.performFlush(logger)
			close(c.flushDone)
			return
		case <-c.flushTicker.C:
			c.performFlush(logger)
		}
	}
}

// performFlush snapshots the cache and flushes to ConfigMap shards with eviction
func (c *ENICache) performFlush(logger logr.Logger) {
	if c.cmPersister == nil {
		return
	}

	ctx, cancel := context.WithTimeout(c.shutdownCtx, 30*time.Second)
	defer cancel()

	// Take snapshot of cache
	c.mu.RLock()
	snapshot := make(map[string]*cacheEntry)
	for ip, entry := range c.cache {
		snapshot[ip] = entry
	}
	c.mu.RUnlock()

	// Flush snapshot to ConfigMap shards (persister handles packing and eviction)
	if err := c.cmPersister.Flush(ctx, snapshot); err != nil {
		logger.Error(err, "Failed to flush ENI cache to ConfigMap")
		metrics.CacheFlushErrorsTotal.Inc()
		return
	}

	metrics.CacheFlushesTotal.Inc()
	logger.V(1).Info("Cache flush completed", "entries", len(snapshot))
}

// Size returns the current cache size (for testing/metrics)
func (c *ENICache) Size() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.cache)
}

// Stop gracefully shuts down the cache worker and waits for in-flight
// ConfigMap flushes to finish or be canceled. It returns ctx.Err() if the
// wait times out or is canceled.
func (c *ENICache) Stop(ctx context.Context) error {
	// Signal shutdown to flush worker
	c.shutdownCancel()

	// Wait for flush worker to finish
	select {
	case <-c.flushDone:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
