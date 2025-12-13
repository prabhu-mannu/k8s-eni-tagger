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
	return e.limiter.Allow()
}

// IsStaleAfter checks if the entry has been stale for longer than the given threshold
func (e *RateLimiterEntry) IsStaleAfter(threshold time.Duration) bool {
	return time.Since(e.GetLastAccess()) > threshold
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
