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
	store        map[string]*aws.ENIInfo
	loadError    error
	savedError   error
	deleteError  error
	saveCalled   bool
	deleteCalled bool
}

func (m *MockConfigMapPersister) Load(ctx context.Context) (map[string]*aws.ENIInfo, error) {
	if m.loadError != nil {
		return nil, m.loadError
	}
	// copy map
	res := make(map[string]*aws.ENIInfo)
	for k, v := range m.store {
		res[k] = v
	}
	return res, nil
}

func (m *MockConfigMapPersister) Save(ctx context.Context, ip string, info *aws.ENIInfo) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.saveCalled = true
	if m.savedError != nil {
		return m.savedError
	}
	if m.store == nil {
		m.store = make(map[string]*aws.ENIInfo)
	}
	m.store[ip] = info
	return nil
}

func (m *MockConfigMapPersister) Delete(ctx context.Context, ip string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deleteCalled = true
	if m.deleteError != nil {
		return m.deleteError
	}
	delete(m.store, ip)
	return nil
}

func TestENICache_LoadFromConfigMap(t *testing.T) {
	mockAWS := &MockAWSClient{}
	c := NewENICache(mockAWS)

	mockPersister := &MockConfigMapPersister{
		store: map[string]*aws.ENIInfo{
			"10.0.0.1": {ID: "eni-1", SubnetID: "subnet-1"},
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
		store: make(map[string]*aws.ENIInfo),
	}
	c.WithConfigMapPersister(mockPersister)

	// Test Save (Async)
	_, err := c.GetENIInfoByIP(context.Background(), "10.0.0.2")
	if err != nil {
		t.Fatalf("GetENIInfoByIP failed: %v", err)
	}

	// Wait for async save
	time.Sleep(100 * time.Millisecond) // Flaky but simple for now

	mockPersister.mu.Lock()
	if _, ok := mockPersister.store["10.0.0.2"]; !ok {
		t.Error("Expected entry to be persisted to ConfigMap")
	}
	mockPersister.mu.Unlock()

	// Test Delete (Async)
	c.Invalidate(context.Background(), "10.0.0.2")
	time.Sleep(100 * time.Millisecond)

	mockPersister.mu.Lock()
	if _, ok := mockPersister.store["10.0.0.2"]; ok {
		t.Error("Expected entry to be deleted from ConfigMap")
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
	c.set(context.Background(), "1.1.1.1", &aws.ENIInfo{})
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
		savedError:  context.DeadlineExceeded,
		deleteError: context.DeadlineExceeded,
	}
	c.WithConfigMapPersister(mockPersister)

	// Save error (should just log, not crash or return error to caller of GetENIInfoByIP)
	_, err := c.GetENIInfoByIP(context.Background(), "10.0.0.1")
	if err != nil {
		t.Errorf("GetENIInfoByIP failed despite persistence error: %v", err)
	}

	// Delete error
	c.Invalidate(context.Background(), "10.0.0.1")
	// Should not panic
}
