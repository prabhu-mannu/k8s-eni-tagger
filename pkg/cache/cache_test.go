package cache

import (
	"context"
	"testing"
	"time"

	"k8s-eni-tagger/pkg/aws"
)

// blockingMockPersister blocks on Flush to simulate long-running writes
type blockingMockPersister struct{}

func (m *blockingMockPersister) Load(ctx context.Context) (map[string]*cacheEntry, error) {
	return map[string]*cacheEntry{}, nil
}

func (m *blockingMockPersister) Flush(ctx context.Context, entries map[string]*cacheEntry) error {
	<-ctx.Done()
	return ctx.Err()
}

func (m *blockingMockPersister) CleanupStaleShards(ctx context.Context) error {
	return nil
}

func (m *blockingMockPersister) SetShardConfig(shards int, maxBytesPerShard int64) {
}

// TestENICacheStopWaitsForWorker verifies Stop waits for flush to complete
func TestENICacheStopWaitsForWorker(t *testing.T) {
	c := NewENICache(nil)
	c.SetFlushInterval(10 * time.Millisecond)
	c.WithConfigMapPersister(&blockingMockPersister{})

	// Add an entry so there's something to flush
	c.setEntry("1.2.3.4", &aws.ENIInfo{ID: "eni-1"})

	// Wait for flush to start (blocking)
	time.Sleep(50 * time.Millisecond)

	// Stop should wait for the flush to be canceled and return
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := c.Stop(ctx); err != nil {
		t.Fatalf("Stop() returned error: %v", err)
	}
}
