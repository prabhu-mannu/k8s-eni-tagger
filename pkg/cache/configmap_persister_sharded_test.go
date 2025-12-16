package cache

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"k8s-eni-tagger/pkg/aws"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// TestEncodeDecodeCompactEntry verifies compact schema encoding/decoding
func TestEncodeDecodeCompactEntry(t *testing.T) {
	persister := &configMapPersisterSharded{}

	entry := &cacheEntry{
		Info: &aws.ENIInfo{
			ID:       "eni-0123abcd",
			SubnetID: "subnet-abc123",
		},
		LastAccess: time.Now(),
	}

	compact := persister.encodeCompact(entry)

	// Verify compact format
	if compact.ID != "0123abcd" {
		t.Errorf("Expected ID suffix '0123abcd', got '%s'", compact.ID)
	}
	if compact.Subnet != "abc123" {
		t.Errorf("Expected Subnet suffix 'abc123', got '%s'", compact.Subnet)
	}
	if compact.LastAccess == 0 {
		t.Error("LastAccess should not be zero")
	}

	// Verify it can be marshaled
	data, err := json.Marshal(compact)
	if err != nil {
		t.Errorf("Failed to marshal compact entry: %v", err)
	}

	// Verify compact size is smaller than full ENIInfo
	fullData, _ := json.Marshal(entry.Info)
	if len(data) >= len(fullData) {
		t.Logf("Warning: compact format not smaller than full format (%d vs %d)", len(data), len(fullData))
	}
}

// TestShardPacking verifies entries are packed across shards
func TestShardPacking(t *testing.T) {
	scheme := runtime.NewScheme()
	corev1.AddToScheme(scheme)
	mockClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	persister := &configMapPersisterSharded{
		client:           mockClient,
		namespace:        "default",
		shards:           2,
		maxBytesPerShard: 100, // Small limit to force packing
	}

	entries := make(map[string]*cacheEntry)
	for i := 1; i <= 5; i++ {
		ip := "10.0.0." + string(rune(48+i))
		entries[ip] = &cacheEntry{
			Info: &aws.ENIInfo{
				ID:       "eni-000" + string(rune(48+i)),
				SubnetID: "subnet-00" + string(rune(48+i)),
			},
			LastAccess: time.Now().Add(time.Duration(-i) * time.Second), // Different times for LRU
		}
	}

	ctx := context.Background()
	err := persister.Flush(ctx, entries)
	if err != nil {
		t.Errorf("Flush failed: %v", err)
	}

	// Verify shards were created
	cmList := &corev1.ConfigMapList{}
	mockClient.List(ctx, cmList)
	if len(cmList.Items) == 0 {
		t.Error("No shards were created")
	}
}

// TestStaleShardCleanup verifies old shards are deleted when shard count changes
func TestStaleShardCleanup(t *testing.T) {
	scheme := runtime.NewScheme()
	corev1.AddToScheme(scheme)
	builder := fake.NewClientBuilder().WithScheme(scheme)

	// Add some old shards (index 3 and 4 when we only use 2)
	for i := 0; i < 5; i++ {
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "eni-tagger-cache-" + string(rune(48+i)),
				Namespace: "default",
				Labels: map[string]string{
					cacheLabelKey:           cacheLabelValue,
					cacheShardIndexLabelKey: string(rune(48 + i)),
					cacheShardCountLabelKey: "5",
				},
			},
			Data: map[string]string{},
		}
		builder.WithObjects(cm)
	}

	mockClient := builder.Build()

	persister := &configMapPersisterSharded{
		client:           mockClient,
		namespace:        "default",
		shards:           2, // Only want 2 shards now
		maxBytesPerShard: 900 * 1024,
	}

	ctx := context.Background()
	err := persister.CleanupStaleShards(ctx)
	if err != nil {
		t.Errorf("CleanupStaleShards failed: %v", err)
	}

	// Verify old shards are deleted (only shards 0 and 1 should remain)
	cmList := &corev1.ConfigMapList{}
	mockClient.List(ctx, cmList)
	if len(cmList.Items) > 2 {
		t.Errorf("Expected at most 2 shards after cleanup, got %d", len(cmList.Items))
	}
}

// TestLoadFromShards verifies cache loading from multiple shards
func TestLoadFromShards(t *testing.T) {
	scheme := runtime.NewScheme()
	corev1.AddToScheme(scheme)

	// Create two shards with entries
	shard0 := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "eni-tagger-cache-0",
			Namespace: "default",
			Labels: map[string]string{
				cacheLabelKey:           cacheLabelValue,
				cacheShardIndexLabelKey: "0",
				cacheShardCountLabelKey: "2",
			},
		},
		Data: map[string]string{
			"10.0.0.1": `{"i":"0123abcd","s":"abc123","a":1000000}`,
			"10.0.0.2": `{"i":"4567efgh","s":"def456","a":1000001}`,
		},
	}

	shard1 := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "eni-tagger-cache-1",
			Namespace: "default",
			Labels: map[string]string{
				cacheLabelKey:           cacheLabelValue,
				cacheShardIndexLabelKey: "1",
				cacheShardCountLabelKey: "2",
			},
		},
		Data: map[string]string{
			"10.0.0.3": `{"i":"89ijklmn","s":"ghi789","a":1000002}`,
		},
	}

	mockClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(shard0, shard1).Build()

	persister := &configMapPersisterSharded{
		client:           mockClient,
		namespace:        "default",
		shards:           2,
		maxBytesPerShard: 900 * 1024,
	}

	ctx := context.Background()
	entries, err := persister.Load(ctx)
	if err != nil {
		t.Errorf("Load failed: %v", err)
	}

	if len(entries) != 3 {
		t.Errorf("Expected 3 entries, got %d", len(entries))
	}

	// Verify entries
	if entry, ok := entries["10.0.0.1"]; ok {
		if entry.Info.ID != "eni-0123abcd" {
			t.Errorf("Entry 10.0.0.1: Expected ID eni-0123abcd, got %s", entry.Info.ID)
		}
		if entry.Info.SubnetID != "subnet-abc123" {
			t.Errorf("Entry 10.0.0.1: Expected Subnet subnet-abc123, got %s", entry.Info.SubnetID)
		}
	} else {
		t.Error("Entry 10.0.0.1 not found")
	}
}
