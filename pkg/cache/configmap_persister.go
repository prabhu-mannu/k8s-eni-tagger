package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"k8s-eni-tagger/pkg/aws"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	configMapName = "eni-tagger-cache"
)

// configMapPersister implements ConfigMapPersister interface
type configMapPersister struct {
	client    client.Client
	namespace string
}

// NewConfigMapPersister creates a new ConfigMap-based persister
func NewConfigMapPersister(client client.Client, namespace string) ConfigMapPersister {
	return &configMapPersister{
		client:    client,
		namespace: namespace,
	}
}

// Load loads all cached ENI entries from the ConfigMap
func (p *configMapPersister) Load(ctx context.Context) (map[string]CachedEntry, error) {
	logger := log.FromContext(ctx)

	cm := &corev1.ConfigMap{}
	err := p.client.Get(ctx, client.ObjectKey{
		Namespace: p.namespace,
		Name:      configMapName,
	}, cm)

	if err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("ENI cache ConfigMap not found, starting fresh")
			return make(map[string]CachedEntry), nil
		}
		return nil, fmt.Errorf("failed to get ConfigMap: %w", err)
	}

	result := make(map[string]CachedEntry)
	migratedEntries := 0
	skippedEntries := []string{}
	for ip, data := range cm.Data {
		entry, migrated, ok := parseCacheEntry([]byte(data))
		if !ok {
			logger.Info("Cache entry corrupted, will clean up", "ip", ip)
			skippedEntries = append(skippedEntries, ip)
			continue
		}
		if migrated {
			migratedEntries++
		}
		result[ip] = entry
	}

	if migratedEntries > 0 {
		logger.Info("Loaded legacy-format ENI cache entries; they will be refreshed on next reconcile",
			"migratedEntries", migratedEntries)
	}

	// Clean up corrupted entries asynchronously with a detached context so
	// startup is not blocked and cleanup completes even if the caller's
	// context is cancelled. Cleanup is best-effort: corrupted entries are
	// never read back into the cache, so a failed delete only wastes a row.
	if len(skippedEntries) > 0 {
		logger.Info("ConfigMap corruption detected, scheduling cleanup",
			"invalidEntries", len(skippedEntries), "validEntries", len(result), "ips", skippedEntries)
		go p.cleanupEntries(skippedEntries)
	}

	return result, nil
}

// parseCacheEntry decodes a single ConfigMap value into a CachedEntry.
// It accepts both the current format ({"info":{...},"pod_uid":"..."}) and the
// legacy format from the pre-UID release (a top-level aws.ENIInfo JSON).
// The migrated bool is true when the data was decoded as legacy format; such
// entries have an empty PodUID so cache.get() will treat them as misses and
// rewrite them under the new format on next access.
func parseCacheEntry(data []byte) (entry CachedEntry, migrated bool, ok bool) {
	var newFormat CachedEntry
	if err := json.Unmarshal(data, &newFormat); err == nil && newFormat.Info != nil && newFormat.Info.ID != "" {
		return newFormat, false, true
	}

	var legacy aws.ENIInfo
	if err := json.Unmarshal(data, &legacy); err == nil && legacy.ID != "" {
		return CachedEntry{Info: &legacy}, true, true
	}

	return CachedEntry{}, false, false
}

func (p *configMapPersister) cleanupEntries(ips []string) {
	logger := log.Log.WithName("eni-cache-cleanup")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	for _, ip := range ips {
		if err := p.Delete(ctx, ip); err != nil {
			logger.Error(err, "Failed to clean up corrupted ConfigMap entry", "ip", ip)
			continue
		}
		logger.Info("Cleaned up corrupted ConfigMap entry", "ip", ip)
	}
}

// Save persists a single ENI entry to the ConfigMap
func (p *configMapPersister) Save(ctx context.Context, ip string, entry CachedEntry) error {
	logger := log.FromContext(ctx)

	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("failed to marshal cache entry: %w", err)
	}

	var lastErr error
	retryCount := 0

	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		retryCount++
		if retryCount > 1 {
			logger.V(1).Info("Retrying ConfigMap save", "ip", ip, "attempt", retryCount, "lastError", lastErr)
		}

		cm := &corev1.ConfigMap{}
		err := p.client.Get(ctx, client.ObjectKey{
			Namespace: p.namespace,
			Name:      configMapName,
		}, cm)

		if err != nil {
			if apierrors.IsNotFound(err) {
				// Create new ConfigMap
				cm = &corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      configMapName,
						Namespace: p.namespace,
					},
					Data: map[string]string{
						ip: string(data),
					},
				}
				if err := p.client.Create(ctx, cm); err != nil {
					lastErr = err
					return fmt.Errorf("failed to create ConfigMap: %w", err)
				}
				logger.Info("Created ENI cache ConfigMap", "ip", ip)
				return nil
			}
			lastErr = err
			return err
		}

		// Update with resource version check
		if cm.Data == nil {
			cm.Data = make(map[string]string)
		}
		cm.Data[ip] = string(data)

		if err := p.client.Update(ctx, cm); err != nil {
			lastErr = err
			return err
		}
		return nil
	})

	if retryCount > 1 {
		logger.Info("ConfigMap save completed after retries", "ip", ip, "attempts", retryCount)
	}

	return err
}

// Delete removes a single ENI entry from the ConfigMap
func (p *configMapPersister) Delete(ctx context.Context, ip string) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		cm := &corev1.ConfigMap{}
		err := p.client.Get(ctx, client.ObjectKey{
			Namespace: p.namespace,
			Name:      configMapName,
		}, cm)

		if err != nil {
			if apierrors.IsNotFound(err) {
				return nil // Already gone
			}
			return err
		}

		if cm.Data == nil {
			return nil
		}

		delete(cm.Data, ip)

		return p.client.Update(ctx, cm)
	})
}
