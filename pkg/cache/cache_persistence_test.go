package cache

import (
	"context"
	"sync"
	"testing"
	"time"

	"k8s-eni-tagger/pkg/aws"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
)

// MockAWSClient implements aws.Client for testing
type MockAWSClient struct {
	GetENIInfoByIPFunc func(ctx context.Context, ip string) (*aws.ENIInfo, error)
}

func (m *MockAWSClient) GetENIInfoByIP(ctx context.Context, ip string) (*aws.ENIInfo, error) {
	return m.GetENIInfoByIPFunc(ctx, ip)
}
func (m *MockAWSClient) TagENI(ctx context.Context, eniID string, tags map[string]string) error {
	return nil
}
func (m *MockAWSClient) UntagENI(ctx context.Context, eniID string, tagKeys []string) error {
	return nil
}
func (m *MockAWSClient) GetEC2Client() *ec2.Client { return nil } // simplified

// MockConfigMapPersister implements ConfigMapPersister for testing
type MockConfigMapPersister struct {
	mu           sync.Mutex
	store        map[string]*cacheEntry
	loadError    error
	flushError   error
	flushCalled  bool
	flushedCount int
}

func (m *MockConfigMapPersister) Load(ctx context.Context) (map[string]*cacheEntry, error) {
	if m.loadError != nil {
		return nil, m.loadError
	}
	// copy map
	res := make(map[string]*cacheEntry)
	for k, v := range m.store {
		res[k] = &cacheEntry{
			Info:       v.Info,
			LastAccess: v.LastAccess,
		}
	}
	return res, nil
}

func (m *MockConfigMapPersister) Flush(ctx context.Context, entries map[string]*cacheEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.flushCalled = true
	m.flushedCount = len(entries)
	if m.flushError != nil {
		return m.flushError
	}
	if m.store == nil {
		m.store = make(map[string]*cacheEntry)
	}
	for ip, entry := range entries {
		m.store[ip] = entry
	}
	return nil
}

func (m *MockConfigMapPersister) CleanupStaleShards(ctx context.Context) error {
	return nil
}

func (m *MockConfigMapPersister) SetShardConfig(shards int, maxBytesPerShard int64) {
}

func TestENICache_LoadFromConfigMap(t *testing.T) {
	mockAWS := &MockAWSClient{}
	c := NewENICache(mockAWS)

	mockPersister := &MockConfigMapPersister{
		store: map[string]*cacheEntry{
			"10.0.0.1": {
				Info:       &aws.ENIInfo{ID: "eni-1", SubnetID: "subnet-1"},
				LastAccess: time.Now(),
			},
		},
	}
	c.WithConfigMapPersister(mockPersister)

	if err := c.LoadFromConfigMap(context.Background()); err != nil {
		t.Fatalf("LoadFromConfigMap failed: %v", err)
	}

	info, err := c.GetENIInfoByIP(context.Background(), "10.0.0.1")
	if err != nil {
		t.Errorf("GetENIInfoByIP failed: %v", err)
	}
	if info.ID != "eni-1" {
		t.Errorf("Expected eni-1, got %s", info.ID)
	}
}

func TestENICache_Persistence(t *testing.T) {
	mockAWS := &MockAWSClient{
		GetENIInfoByIPFunc: func(ctx context.Context, ip string) (*aws.ENIInfo, error) {
			return &aws.ENIInfo{ID: "eni-2", SubnetID: "subnet-2"}, nil
		},
	}
	c := NewENICache(mockAWS)

	mockPersister := &MockConfigMapPersister{
		store: make(map[string]*cacheEntry),
	}
	c.WithConfigMapPersister(mockPersister)
	// speed up flush for tests
	c.SetFlushInterval(10 * time.Millisecond)

	// Test Flush (Async)
	_, err := c.GetENIInfoByIP(context.Background(), "10.0.0.2")
	if err != nil {
		t.Fatalf("GetENIInfoByIP failed: %v", err)
	}

	// Wait for async flush
	time.Sleep(50 * time.Millisecond)

	mockPersister.mu.Lock()
	if mockPersister.flushedCount != 1 {
		t.Errorf("Expected 1 entry flushed, got %d", mockPersister.flushedCount)
	}
	mockPersister.mu.Unlock()

	// Invalidate doesn't persist to ConfigMap anymore (happens at next flush)
	c.Invalidate(context.Background(), "10.0.0.2")

	// Next flush should have 0 entries
	time.Sleep(50 * time.Millisecond)

	mockPersister.mu.Lock()
	if mockPersister.flushedCount != 0 {
		t.Errorf("Expected 0 entries in second flush, got %d", mockPersister.flushedCount)
	}
	mockPersister.mu.Unlock()
}

func TestENICache_Size(t *testing.T) {
	c := NewENICache(&MockAWSClient{})

	// Initially 0
	if c.Size() != 0 {
		t.Errorf("Expected size 0, got %d", c.Size())
	}

	// Add mock entry
	c.setEntry("1.1.1.1", &aws.ENIInfo{})
	if c.Size() != 1 {
		t.Errorf("Expected size 1, got %d", c.Size())
	}
}

func TestENICache_LoadError(t *testing.T) {
	c := NewENICache(&MockAWSClient{})
	mockPersister := &MockConfigMapPersister{
		loadError: context.DeadlineExceeded,
	}
	c.WithConfigMapPersister(mockPersister)

	err := c.LoadFromConfigMap(context.Background())
	if err == nil {
		t.Error("Expected error from LoadFromConfigMap")
	}
}

func TestENICache_PersistenceErrors(t *testing.T) {
	mockAWS := &MockAWSClient{
		GetENIInfoByIPFunc: func(ctx context.Context, ip string) (*aws.ENIInfo, error) {
			return &aws.ENIInfo{ID: "eni-2"}, nil
		},
	}
	c := NewENICache(mockAWS)
	mockPersister := &MockConfigMapPersister{
		flushError: context.DeadlineExceeded,
	}
	c.WithConfigMapPersister(mockPersister)
	c.SetFlushInterval(10 * time.Millisecond)

	// GetENIInfoByIP should work despite flush errors (should just log)
	_, err := c.GetENIInfoByIP(context.Background(), "10.0.0.1")
	if err != nil {
		t.Errorf("GetENIInfoByIP failed despite persistence error: %v", err)
	}

	// Invalidate should work without errors
	c.Invalidate(context.Background(), "10.0.0.1")
	// Should not panic
}
