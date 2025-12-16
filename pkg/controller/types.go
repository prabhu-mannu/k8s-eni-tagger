package controller

import (
	"fmt"
	"sync"
	"time"

	"k8s-eni-tagger/pkg/aws"
	enicache "k8s-eni-tagger/pkg/cache"

	"golang.org/x/time/rate"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// RateLimiterEntry holds a rate limiter with its last access timestamp.
// It is safe for concurrent use, with all mutable state protected by a mutex.
// The rate limiter cannot be nil or modified after creation.
//
// Example:
//
//	entry, err := NewRateLimiterEntry(10.0, 5) // 10 QPS, burst 5
//	if err != nil { /* handle error */ }
//	entry.UpdateLastAccess(time.Now())
//	if entry.Allow() {
//	    // proceed with operation
//	}
type RateLimiterEntry struct {
	limiter    *rate.Limiter // unexport to prevent nil assignment
	lastAccess time.Time     // unexport to enforce mutex usage
	mu         sync.Mutex
}

// NewRateLimiterEntry creates a new RateLimiterEntry with validation
func NewRateLimiterEntry(qps float64, burst int) (*RateLimiterEntry, error) {
	if qps <= 0 {
		return nil, fmt.Errorf("qps must be positive")
	}
	if burst < 1 {
		return nil, fmt.Errorf("burst must be at least 1")
	}
	return &RateLimiterEntry{
		limiter:    rate.NewLimiter(rate.Limit(qps), burst),
		lastAccess: time.Now(),
	}, nil
}

// UpdateLastAccess safely updates the last access timestamp
func (e *RateLimiterEntry) UpdateLastAccess(t time.Time) {
	e.mu.Lock()
	e.lastAccess = t
	e.mu.Unlock()
}

// GetLastAccess safely retrieves the last access timestamp
func (e *RateLimiterEntry) GetLastAccess() time.Time {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.lastAccess
}

// Allow checks if the rate limiter allows the request
func (e *RateLimiterEntry) Allow() bool {
	if e.limiter == nil {
		panic("rate limiter is nil - this indicates a programming error in initialization")
	}
	return e.limiter.Allow()
}

// AllowAndUpdate atomically checks if the request is allowed and updates last access time
// Returns true if the request is allowed, false if rate limited
func (e *RateLimiterEntry) AllowAndUpdate() bool {
	if e.limiter == nil {
		panic("rate limiter is nil - this indicates a programming error in initialization")
	}
	allowed := e.limiter.Allow()
	if allowed {
		e.UpdateLastAccess(time.Now())
	}
	return allowed
}

// IsStaleAfter checks if the entry has been stale for longer than the given threshold
func (e *RateLimiterEntry) IsStaleAfter(threshold time.Duration) bool {
	return time.Since(e.GetLastAccess()) > threshold
}

// RateLimiterPool provides a type-safe wrapper around sync.Map for RateLimiterEntry storage
type RateLimiterPool struct {
	mu    sync.RWMutex
	pool  *sync.Map // map[string]*RateLimiterEntry
	qps   float64
	burst int
}

// NewRateLimiterPool creates a new RateLimiterPool with the given configuration
func NewRateLimiterPool(qps float64, burst int) (*RateLimiterPool, error) {
	if qps <= 0 {
		return nil, fmt.Errorf("qps must be positive")
	}
	if burst < 1 {
		return nil, fmt.Errorf("burst must be at least 1")
	}

	return &RateLimiterPool{
		pool:  &sync.Map{},
		qps:   qps,
		burst: burst,
	}, nil
}

// GetOrCreate returns an existing rate limiter for the key, or creates a new one
// Returns the rate limiter entry and whether it was newly created
func (p *RateLimiterPool) GetOrCreate(key string) (*RateLimiterEntry, bool, error) {
	// Try to get existing entry
	if entryInterface, exists := p.pool.Load(key); exists {
		if entry, ok := entryInterface.(*RateLimiterEntry); ok && entry != nil {
			return entry, false, nil
		}
		// Invalid entry found, remove it
		p.pool.Delete(key)
	}

	// Create new entry
	entry, err := NewRateLimiterEntry(p.qps, p.burst)
	if err != nil {
		return nil, false, fmt.Errorf("failed to create rate limiter entry: %w", err)
	}

	// Store the new entry (may race with other goroutines, but that's ok)
	actual, loaded := p.pool.LoadOrStore(key, entry)
	if loaded {
		// Another goroutine created it first, return that one
		if actualEntry, ok := actual.(*RateLimiterEntry); ok && actualEntry != nil {
			return actualEntry, false, nil
		}
		// Invalid entry, replace it
		p.pool.Store(key, entry)
	}

	return entry, true, nil
}

// Remove removes a rate limiter from the pool
func (p *RateLimiterPool) Remove(key string) {
	p.pool.Delete(key)
}

// Range calls the given function for each key-value pair in the pool
func (p *RateLimiterPool) Range(f func(key string, entry *RateLimiterEntry) bool) {
	p.pool.Range(func(key, value interface{}) bool {
		if strKey, ok := key.(string); ok {
			if entry, ok := value.(*RateLimiterEntry); ok && entry != nil {
				return f(strKey, entry)
			}
		}
		return true
	})
}

// Size returns the current number of entries in the pool
func (p *RateLimiterPool) Size() int {
	count := 0
	p.pool.Range(func(key, value interface{}) bool {
		count++
		return true
	})
	return count
}

// Cleanup removes stale entries that haven't been accessed for longer than the threshold
func (p *RateLimiterPool) Cleanup(threshold time.Duration) int {
	removed := 0
	p.pool.Range(func(key, value interface{}) bool {
		if entry, ok := value.(*RateLimiterEntry); ok && entry != nil {
			if entry.IsStaleAfter(threshold) {
				p.pool.Delete(key)
				removed++
			}
		}
		return true
	})
	return removed
}

// PodReconciler reconciles Pod objects and manages ENI tags
type PodReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder

	// AWS client for ENI operations
	AWSClient aws.Client

	// ENI cache for reducing AWS API calls (optional)
	ENICache *enicache.ENICache

	// Configuration
	AnnotationKey         string
	DryRun                bool
	SubnetIDs             []string
	AllowSharedENITagging bool
	TagNamespace          string

	// Per-pod rate limiters for DoS protection
	PodRateLimiters   *sync.Map // map[string]*RateLimiterEntry
	PodRateLimitQPS   float64   // Requests per second per pod
	PodRateLimitBurst int       // Burst size per pod

	// Rate limiter cleanup configuration
	RateLimiterCleanupThreshold time.Duration // How long before considering a limiter stale
}
