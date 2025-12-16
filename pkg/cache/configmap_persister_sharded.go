package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"k8s-eni-tagger/pkg/aws"
	"k8s-eni-tagger/pkg/metrics"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	cacheShardBaseName      = "eni-tagger-cache"
	cacheLabelKey           = "eni-tagger.io/cache"
	cacheLabelValue         = "eni-cache"
	cacheShardIndexLabelKey = "eni-tagger.io/cache-shard-index"
	cacheShardCountLabelKey = "eni-tagger.io/cache-shards"
)

// compactEntry represents the on-disk JSON format for cached ENI entries (compact schema).
type compactEntry struct {
	ID         string `json:"i"`           // ENI ID suffix (e.g., "0123abcd" for "eni-0123abcd")
	Subnet     string `json:"s,omitempty"` // Subnet ID suffix (e.g., "abc123" for "subnet-abc123")
	LastAccess int64  `json:"a"`           // Unix milliseconds for LastAccess
}

// configMapPersisterSharded implements ConfigMapPersister interface with sharded snapshot persistence.
type configMapPersisterSharded struct {
	client           client.Client
	namespace        string
	shards           int
	maxBytesPerShard int64
}

// NewConfigMapPersister creates a new ConfigMap-based sharded persister
func NewConfigMapPersister(client client.Client, namespace string) ConfigMapPersister {
	return &configMapPersisterSharded{
		client:           client,
		namespace:        namespace,
		shards:           3,
		maxBytesPerShard: 900 * 1024, // 900 KiB default
	}
}

// SetShardConfig sets the shard configuration
func (p *configMapPersisterSharded) SetShardConfig(shards int, maxBytesPerShard int64) {
	if shards > 0 {
		p.shards = shards
	}
	if maxBytesPerShard > 0 {
		p.maxBytesPerShard = maxBytesPerShard
	}
}

// Load loads all cached ENI entries from sharded ConfigMaps on startup
func (p *configMapPersisterSharded) Load(ctx context.Context) (map[string]*cacheEntry, error) {
	logger := log.FromContext(ctx)

	result := make(map[string]*cacheEntry)

	// Load all shards (0..N-1)
	for i := 0; i < p.shards; i++ {
		shardName := fmt.Sprintf("%s-%d", cacheShardBaseName, i)
		cm := &corev1.ConfigMap{}

		err := p.client.Get(ctx, client.ObjectKey{
			Namespace: p.namespace,
			Name:      shardName,
		}, cm)

		if err != nil {
			if apierrors.IsNotFound(err) {
				// Missing shard is fine, treat as empty
				logger.V(1).Info("Cache shard not found, treating as empty", "shard", shardName)
				continue
			}
			logger.Error(err, "Failed to get cache shard", "shard", shardName)
			// Continue loading other shards on error
			continue
		}

		// Decode entries from this shard
		for ip, data := range cm.Data {
			var compact compactEntry
			if err := json.Unmarshal([]byte(data), &compact); err != nil {
				logger.Error(err, "Failed to decode compact entry, skipping", "ip", ip, "shard", shardName)
				continue
			}

			// Reconstruct full ENI and Subnet IDs from compact format
			eniID := "eni-" + compact.ID
			subnetID := ""
			if compact.Subnet != "" {
				subnetID = "subnet-" + compact.Subnet
			}

			// Parse LastAccess timestamp
			lastAccess := time.UnixMilli(compact.LastAccess)

			// Reconstruct full ENIInfo (note: other fields lost, but this is acceptable for cache)
			info := &aws.ENIInfo{
				ID:       eniID,
				SubnetID: subnetID,
			}

			result[ip] = &cacheEntry{
				Info:       info,
				LastAccess: lastAccess,
			}
		}

		logger.V(1).Info("Loaded cache shard", "shard", shardName, "entries", len(cm.Data))
	}

	logger.Info("Loaded ENI cache from ConfigMap shards", "totalEntries", len(result))
	return result, nil
}

// Flush performs a snapshot flush with packing, eviction, and stale shard cleanup
func (p *configMapPersisterSharded) Flush(ctx context.Context, entries map[string]*cacheEntry) error {
	logger := log.FromContext(ctx)
	startTime := time.Now()

	// First, cleanup stale shards
	if err := p.CleanupStaleShards(ctx); err != nil {
		logger.Error(err, "Cleanup stale shards failed, continuing with flush")
	}

	// Sort entries by LastAccess descending (newest first) for eviction purposes
	sortedIPs := make([]string, 0, len(entries))
	for ip := range entries {
		sortedIPs = append(sortedIPs, ip)
	}
	sort.Slice(sortedIPs, func(i, j int) bool {
		return entries[sortedIPs[i]].LastAccess.After(entries[sortedIPs[j]].LastAccess)
	})

	// Encode entries to compact format and pack into shards
	shardData := make([]map[string]string, p.shards)
	for i := range shardData {
		shardData[i] = make(map[string]string)
	}

	shardBytes := make([]int64, p.shards)
	persistedCount := 0
	evictedCount := 0

	for _, ip := range sortedIPs {
		entry := entries[ip]
		compact := p.encodeCompact(entry)
		data, err := json.Marshal(compact)
		if err != nil {
			logger.Error(err, "Failed to encode entry, skipping", "ip", ip)
			evictedCount++
			continue
		}

		entrySize := int64(len(ip) + len(data) + 10) // Rough approximation including key

		// Find best-fit shard (first shard with enough space)
		placed := false
		for i := 0; i < p.shards; i++ {
			if shardBytes[i]+entrySize <= p.maxBytesPerShard {
				shardData[i][ip] = string(data)
				shardBytes[i] += entrySize
				persistedCount++
				placed = true
				break
			}
		}

		if !placed {
			logger.V(1).Info("Entry evicted, no shard has space", "ip", ip)
			evictedCount++
		}
	}

	// Write all shards
	for i := 0; i < p.shards; i++ {
		shardName := fmt.Sprintf("%s-%d", cacheShardBaseName, i)
		if err := p.writeShard(ctx, shardName, shardData[i]); err != nil {
			logger.Error(err, "Failed to write shard", "shard", shardName)
			metrics.CacheFlushErrorsTotal.Inc()
			return err
		}
	}

	duration := time.Since(startTime)
	logger.Info("Cache flush completed",
		"totalEntries", len(entries),
		"persistedEntries", persistedCount,
		"evictedEntries", evictedCount,
		"shards", p.shards,
		"duration", duration)

	metrics.CacheEntriesEvictedTotal.Add(float64(evictedCount))
	return nil
}

// CleanupStaleShards deletes ConfigMap shards that don't match current shard configuration
func (p *configMapPersisterSharded) CleanupStaleShards(ctx context.Context) error {
	logger := log.FromContext(ctx)

	// List all ConfigMaps with cache label in the configured namespace
	cmList := &corev1.ConfigMapList{}
	if err := p.client.List(ctx, cmList, client.InNamespace(p.namespace), client.MatchingLabels{cacheLabelKey: cacheLabelValue}); err != nil {
		return fmt.Errorf("failed to list cache ConfigMaps: %w", err)
	}

	// Check each ConfigMap and delete if it doesn't match current config
	for i := range cmList.Items {
		cm := &cmList.Items[i]

		// Skip non-matching labels or indices outside valid range
		shardIndexStr := cm.Labels[cacheShardIndexLabelKey]
		shardCountStr := cm.Labels[cacheShardCountLabelKey]

		// Parse shard index and count
		var shardIndex int
		var shardCount int
		_, _ = fmt.Sscanf(shardIndexStr, "%d", &shardIndex)
		_, _ = fmt.Sscanf(shardCountStr, "%d", &shardCount)

		// Delete if index is outside valid range or shard count doesn't match
		if shardIndex < 0 || shardIndex >= p.shards || shardCount != p.shards {
			logger.Info("Deleting stale cache shard", "name", cm.Name, "shardIndex", shardIndex, "shardCount", shardCount)
			if err := p.client.Delete(ctx, cm); err != nil && !apierrors.IsNotFound(err) {
				logger.Error(err, "Failed to delete stale shard", "name", cm.Name)
			}
		}
	}

	return nil
}

// writeShard writes or overwrites a single shard ConfigMap
func (p *configMapPersisterSharded) writeShard(ctx context.Context, shardName string, data map[string]string) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		cm := &corev1.ConfigMap{}
		err := p.client.Get(ctx, client.ObjectKey{
			Namespace: p.namespace,
			Name:      shardName,
		}, cm)

		if err != nil {
			if apierrors.IsNotFound(err) {
				// Create new shard ConfigMap
				shardIndex := 0
				_, _ = fmt.Sscanf(shardName, cacheShardBaseName+"-%d", &shardIndex)

				cm = &corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      shardName,
						Namespace: p.namespace,
						Labels: map[string]string{
							cacheLabelKey:           cacheLabelValue,
							cacheShardIndexLabelKey: fmt.Sprintf("%d", shardIndex),
							cacheShardCountLabelKey: fmt.Sprintf("%d", p.shards),
						},
					},
					Data: data,
				}
				return p.client.Create(ctx, cm)
			}
			return err
		}

		// Update existing shard ConfigMap (full overwrite)
		cm.Data = data
		return p.client.Update(ctx, cm)
	})
}

// encodeCompact converts a cacheEntry to compact JSON format
func (p *configMapPersisterSharded) encodeCompact(entry *cacheEntry) compactEntry {
	// Extract suffix from ENI ID (remove "eni-" prefix)
	eniSuffix := entry.Info.ID
	if len(eniSuffix) > 4 && eniSuffix[:4] == "eni-" {
		eniSuffix = eniSuffix[4:]
	}

	// Extract suffix from Subnet ID (remove "subnet-" prefix)
	subnetSuffix := ""
	if entry.Info.SubnetID != "" {
		subnetSuffix = entry.Info.SubnetID
		if len(subnetSuffix) > 7 && subnetSuffix[:7] == "subnet-" {
			subnetSuffix = subnetSuffix[7:]
		}
	}

	return compactEntry{
		ID:         eniSuffix,
		Subnet:     subnetSuffix,
		LastAccess: entry.LastAccess.UnixMilli(),
	}
}
