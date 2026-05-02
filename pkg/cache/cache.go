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

// CachedEntry represents a cached ENI lookup with validation metadata.
// An empty PodUID marks a legacy entry loaded during format migration; such
// entries are treated as misses on lookup so they are refreshed (and rewritten
// in the new format) on next access.
type CachedEntry struct {
	Info   *aws.ENIInfo `json:"info"`
	PodUID string       `json:"pod_uid"`
}

// cacheUpdate represents a pending update to the ConfigMap
type cacheUpdate struct {
	ip    string
	entry CachedEntry
}

// Cache defines the interface for ENI caching. The podUID parameter on
// GetENIInfoByIP and Invalidate is the requesting pod's UID; cache entries are
// only returned (or deleted) when the cached PodUID matches, which prevents
// stale results when an IP is reassigned to a different pod.
type Cache interface {
	GetENIInfoByIP(ctx context.Context, ip string, podUID string) (*aws.ENIInfo, error)
	Invalidate(ctx context.Context, ip string, podUID string)
	LoadFromConfigMap(ctx context.Context) error
	WithConfigMapPersister(persister ConfigMapPersister) *ENICache
}

// ENICache provides caching for ENI lookups based on pod lifecycle.
// Since an ENI-to-IP mapping doesn't change during a pod's lifetime,
// entries are cached until explicitly invalidated (on pod deletion).
// This reduces AWS API calls significantly.
type ENICache struct {
	mu        sync.RWMutex
	cache     map[string]CachedEntry
	awsClient aws.Client

	// ConfigMap persistence (optional)
	cmPersister ConfigMapPersister

	// Batching/rate limiting
	updateQueue   chan cacheUpdate
	stopWorker    chan struct{}
	batchInterval time.Duration
	batchSize     int
	workerOnce    sync.Once
}

// ConfigMapPersister interface for optional ConfigMap persistence
type ConfigMapPersister interface {
	Load(ctx context.Context) (map[string]CachedEntry, error)
	Save(ctx context.Context, ip string, entry CachedEntry) error
	Delete(ctx context.Context, ip string) error
}

// NewENICache creates a new ENI cache
func NewENICache(awsClient aws.Client) *ENICache {
	c := &ENICache{
		cache:         make(map[string]CachedEntry),
		awsClient:     awsClient,
		updateQueue:   make(chan cacheUpdate, 1000),
		stopWorker:    make(chan struct{}),
		batchInterval: 2 * time.Second, // configurable
		batchSize:     20,              // configurable
	}
	return c
}

// SetBatchConfig updates batching parameters. Call before enabling ConfigMap persistence.
func (c *ENICache) SetBatchConfig(interval time.Duration, size int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if interval > 0 {
		c.batchInterval = interval
	}
	if size > 0 {
		c.batchSize = size
	}
}

// WithConfigMapPersister adds ConfigMap persistence to the cache
func (c *ENICache) WithConfigMapPersister(persister ConfigMapPersister) *ENICache {
	c.cmPersister = persister
	c.ensureWorker()
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
	for ip, entry := range entries {
		c.cache[ip] = entry
	}
	logger.Info("Loaded ENI cache from ConfigMap", "entries", len(entries))
	return nil
}

// GetENIInfoByIP returns ENI info for an IP, using cache if available.
// It requires the expected PodUID to validate the cache entry.
func (c *ENICache) GetENIInfoByIP(ctx context.Context, ip string, podUID string) (*aws.ENIInfo, error) {
	// Try in-memory cache first
	if info, ok := c.get(ctx, ip, podUID); ok {
		metrics.CacheHitsTotal.Inc()
		return info, nil
	}
	metrics.CacheMissesTotal.Inc()

	// Cache miss, UID mismatch, or legacy migrated entry - call AWS API
	info, err := c.awsClient.GetENIInfoByIP(ctx, ip)
	if err != nil {
		return nil, err
	}

	// Store in cache (persists until pod deletion)
	c.set(ctx, ip, info, podUID)
	return info, nil
}

// get retrieves from in-memory cache with validation. An empty cached PodUID
// marks a legacy entry loaded during format migration and is always treated as
// a miss to force a refresh under the new format.
func (c *ENICache) get(ctx context.Context, ip string, podUID string) (*aws.ENIInfo, bool) {
	c.mu.RLock()
	entry, ok := c.cache[ip]
	c.mu.RUnlock()

	if !ok {
		return nil, false
	}

	if entry.PodUID == "" {
		log.FromContext(ctx).V(1).Info("Cache entry has no PodUID (migrated legacy entry), refreshing", "ip", ip)
		return nil, false
	}

	if entry.PodUID != podUID {
		log.FromContext(ctx).V(1).Info("Cache miss due to pod UID mismatch", "ip", ip, "cachedPodUID", entry.PodUID, "requestedPodUID", podUID)
		return nil, false
	}

	return entry.Info, true
}

// set stores in in-memory cache and optionally persists to ConfigMap
func (c *ENICache) set(ctx context.Context, ip string, info *aws.ENIInfo, podUID string) {
	c.mu.Lock()
	entry := CachedEntry{
		Info:   info,
		PodUID: podUID,
	}
	c.cache[ip] = entry
	c.mu.Unlock()

	// Enqueue update for batching/rate limiting
	if c.cmPersister != nil {
		c.ensureWorker()
		select {
		case c.updateQueue <- cacheUpdate{ip: ip, entry: entry}:
		default:
			// queue full, drop update (log warning)
			log.FromContext(ctx).Info("ConfigMap update queue full, dropping update", "ip", ip)
		}
	}
}

// Invalidate removes an entry from the cache when the pod UID matches.
func (c *ENICache) Invalidate(ctx context.Context, ip string, podUID string) {
	logger := log.FromContext(ctx)

	c.mu.Lock()
	entry, exists := c.cache[ip]
	if !exists {
		c.mu.Unlock()
		return
	}
	if entry.PodUID != podUID {
		c.mu.Unlock()
		logger.V(1).Info("Skipped cache invalidation due to pod UID mismatch", "ip", ip, "cachedPodUID", entry.PodUID, "requestedPodUID", podUID)
		return
	}
	delete(c.cache, ip)
	c.mu.Unlock()

	if c.cmPersister != nil {
		if err := c.cmPersister.Delete(ctx, ip); err != nil {
			logger.Error(err, "Failed to delete ENI from ConfigMap, cache may grow unbounded", "ip", ip)
		}
	}
}

func (c *ENICache) ensureWorker() {
	c.workerOnce.Do(func() {
		go c.configMapWorker()
	})
}

// configMapWorker batches and rate-limits ConfigMap updates
func (c *ENICache) configMapWorker() {
	logger := log.Log.WithName("eni-cache-worker")

	// Copy batching config under lock to avoid race conditions
	c.mu.RLock()
	batchSize := c.batchSize
	batchInterval := c.batchInterval
	c.mu.RUnlock()

	batch := make([]cacheUpdate, 0, batchSize)
	ticker := time.NewTicker(batchInterval)
	defer ticker.Stop()
	for {
		select {
		case <-c.stopWorker:
			return
		case upd := <-c.updateQueue:
			batch = append(batch, upd)
			if len(batch) >= c.batchSize {
				c.flushBatch(batch, logger)
				batch = batch[:0]
			}
		case <-ticker.C:
			if len(batch) > 0 {
				c.flushBatch(batch, logger)
				batch = batch[:0]
			}
		}
	}
}

// flushBatch applies a batch of updates to the ConfigMap
func (c *ENICache) flushBatch(batch []cacheUpdate, logger logr.Logger) {
	if c.cmPersister == nil || len(batch) == 0 {
		return
	}

	// Use timeout context to prevent hanging during shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Apply sets
	for _, upd := range batch {
		if err := c.cmPersister.Save(ctx, upd.ip, upd.entry); err != nil {
			logger.Error(err, "Batch persist ENI to ConfigMap failed", "ip", upd.ip)
		}
	}
}

// Size returns the current cache size (for testing/metrics)
func (c *ENICache) Size() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.cache)
}
