package cache

import (
	"context"
	"testing"
	"time"

	"k8s-eni-tagger/pkg/aws"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
)

// mockPersisterSharded implements ConfigMapPersister for testing
type mockPersisterSharded struct {
	lastFlush    map[string]*cacheEntry
	cleanupCount int
	flushCount   int
}

func (m *mockPersisterSharded) Load(ctx context.Context) (map[string]*cacheEntry, error) {
	return map[string]*cacheEntry{}, nil
}

func (m *mockPersisterSharded) Flush(ctx context.Context, entries map[string]*cacheEntry) error {
	// Make a copy of entries
	m.lastFlush = make(map[string]*cacheEntry)
	for ip, entry := range entries {
		m.lastFlush[ip] = entry
	}
	m.flushCount++
	return nil
}

func (m *mockPersisterSharded) CleanupStaleShards(ctx context.Context) error {
	m.cleanupCount++
	return nil
}

func (m *mockPersisterSharded) SetShardConfig(shards int, maxBytesPerShard int64) {
	// No-op for testing
}

// TestCacheEntryLastAccessUpdate verifies LastAccess is updated on cache hit
func TestCacheEntryLastAccessUpdate(t *testing.T) {
	c := NewENICache(nil)
	mockAws := &mockAWSClient{
		responses: map[string]*aws.ENIInfo{
			"10.0.0.1": {ID: "eni-123", SubnetID: "subnet-abc"},
		},
	}
	c.awsClient = mockAws

	ctx := context.Background()

	// First access via AWS API (cache miss)
	info, err := c.GetENIInfoByIP(ctx, "10.0.0.1")
	if err != nil {
		t.Fatalf("GetENIInfoByIP failed: %v", err)
	}
	if info.ID != "eni-123" {
		t.Errorf("Expected ID eni-123, got %s", info.ID)
	}

	// Verify entry exists and has LastAccess set
	c.mu.RLock()
	entry, ok := c.cache["10.0.0.1"]
	c.mu.RUnlock()
	if !ok {
		t.Fatal("Entry not in cache")
	}

	firstAccess := entry.LastAccess

	// Wait a bit and access again
	time.Sleep(10 * time.Millisecond)

	// Second access (cache hit)
	info2, err := c.GetENIInfoByIP(ctx, "10.0.0.1")
	if err != nil {
		t.Fatalf("GetENIInfoByIP failed: %v", err)
	}
	if info2.ID != "eni-123" {
		t.Errorf("Expected ID eni-123, got %s", info2.ID)
	}

	// Verify LastAccess was updated
	c.mu.RLock()
	entry2, _ := c.cache["10.0.0.1"]
	c.mu.RUnlock()

	if entry2.LastAccess.Before(firstAccess) || entry2.LastAccess.Equal(firstAccess) {
		t.Errorf("LastAccess was not updated on cache hit. Before: %v, After: %v", firstAccess, entry2.LastAccess)
	}
}

// TestFlushSnapshot verifies flush snapshots the cache and calls persister
func TestFlushSnapshot(t *testing.T) {
	c := NewENICache(nil)
	mockAws := &mockAWSClient{
		responses: map[string]*aws.ENIInfo{
			"10.0.0.1": {ID: "eni-123", SubnetID: "subnet-abc"},
			"10.0.0.2": {ID: "eni-456", SubnetID: "subnet-def"},
		},
	}
	c.awsClient = mockAws

	// Set flush interval very short for testing
	c.SetFlushInterval(10 * time.Millisecond)

	persister := &mockPersisterSharded{}
	c.WithConfigMapPersister(persister)

	ctx := context.Background()

	// Add entries to cache
	_, _ = c.GetENIInfoByIP(ctx, "10.0.0.1")
	_, _ = c.GetENIInfoByIP(ctx, "10.0.0.2")

	// Wait for flush to occur
	time.Sleep(50 * time.Millisecond)

	if persister.flushCount == 0 {
		t.Fatal("Flush was not called")
	}

	if len(persister.lastFlush) != 2 {
		t.Errorf("Expected 2 entries in flush, got %d", len(persister.lastFlush))
	}

	// Verify entries are cacheEntry type with LastAccess
	for ip, entry := range persister.lastFlush {
		if entry.Info == nil {
			t.Errorf("Entry for %s has nil Info", ip)
		}
		if entry.LastAccess.IsZero() {
			t.Errorf("Entry for %s has zero LastAccess", ip)
		}
	}

	// Stop cache
	stopCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.Stop(stopCtx); err != nil {
		t.Errorf("Stop failed: %v", err)
	}
}

// TestInvalidateRemovesEntry verifies invalidation removes entries
func TestInvalidateRemovesEntry(t *testing.T) {
	c := NewENICache(nil)
	mockAws := &mockAWSClient{
		responses: map[string]*aws.ENIInfo{
			"10.0.0.1": {ID: "eni-123", SubnetID: "subnet-abc"},
		},
	}
	c.awsClient = mockAws

	ctx := context.Background()

	// Add entry
	_, _ = c.GetENIInfoByIP(ctx, "10.0.0.1")

	c.mu.RLock()
	if _, ok := c.cache["10.0.0.1"]; !ok {
		t.Fatal("Entry not in cache after GetENIInfoByIP")
	}
	c.mu.RUnlock()

	// Invalidate
	c.Invalidate(ctx, "10.0.0.1")

	c.mu.RLock()
	if _, ok := c.cache["10.0.0.1"]; ok {
		t.Fatal("Entry still in cache after Invalidate")
	}
	c.mu.RUnlock()
}

// mockAWSClient is a test double for aws.Client
type mockAWSClient struct {
	responses map[string]*aws.ENIInfo
}

func (m *mockAWSClient) GetENIInfoByIP(ctx context.Context, ip string) (*aws.ENIInfo, error) {
	if info, ok := m.responses[ip]; ok {
		return info, nil
	}
	return nil, nil
}

func (m *mockAWSClient) TagENI(ctx context.Context, eniID string, tags map[string]string) error {
	return nil
}

func (m *mockAWSClient) UntagENI(ctx context.Context, eniID string, tagKeys []string) error {
	return nil
}

func (m *mockAWSClient) GetEC2Client() *ec2.Client {
	return nil // Return nil for testing purposes
}
