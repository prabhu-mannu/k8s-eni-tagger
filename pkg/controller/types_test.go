package controller

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRateLimiterEntryConcurrentAccess(t *testing.T) {
	t.Parallel()

	entry, err := NewRateLimiterEntry(10.0, 5)
	require.NoError(t, err)
	require.NotNil(t, entry)

	// Test concurrent access to UpdateLastAccess and GetLastAccess
	var wg sync.WaitGroup
	const numGoroutines = 10
	const numIterations = 100

	// Start multiple goroutines updating last access time
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < numIterations; j++ {
				now := time.Now()
				entry.UpdateLastAccess(now)
				// Small delay to increase chance of interleaving
				time.Sleep(time.Microsecond)
			}
		}(i)
	}

	// Start multiple goroutines reading last access time
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < numIterations; j++ {
				_ = entry.GetLastAccess()
				time.Sleep(time.Microsecond)
			}
		}(i)
	}

	// Wait for all goroutines to complete
	wg.Wait()

	// Verify the entry is still functional
	assert.True(t, entry.Allow(), "Rate limiter should allow requests after concurrent access")
	assert.False(t, entry.IsStaleAfter(time.Hour), "Entry should not be stale immediately after access")
}

func TestRateLimiterEntryAllowConcurrent(t *testing.T) {
	t.Parallel()

	entry, err := NewRateLimiterEntry(1000.0, 10) // High rate limit for testing
	require.NoError(t, err)
	require.NotNil(t, entry)

	var wg sync.WaitGroup
	const numGoroutines = 5
	const numIterations = 50

	allowedCount := 0
	var mu sync.Mutex

	// Start multiple goroutines calling Allow()
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			localAllowed := 0
			for j := 0; j < numIterations; j++ {
				if entry.Allow() {
					localAllowed++
				}
				time.Sleep(time.Microsecond)
			}
			mu.Lock()
			allowedCount += localAllowed
			mu.Unlock()
		}()
	}

	wg.Wait()

	// With high rate limit, most requests should be allowed
	assert.True(t, allowedCount > 0, "Some requests should be allowed")
	assert.True(t, allowedCount <= numGoroutines*numIterations, "Allowed count should not exceed total requests")
}

func TestRateLimiterEntryIsStaleAfterConcurrent(t *testing.T) {
	t.Parallel()

	entry, err := NewRateLimiterEntry(1.0, 1)
	require.NoError(t, err)
	require.NotNil(t, entry)

	// Update last access to now
	now := time.Now()
	entry.UpdateLastAccess(now)

	var wg sync.WaitGroup
	const numGoroutines = 5
	const numIterations = 20

	// Start multiple goroutines calling IsStaleAfter
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < numIterations; j++ {
				_ = entry.IsStaleAfter(time.Hour)
				time.Sleep(time.Microsecond)
			}
		}()
	}

	wg.Wait()

	// Verify the entry is still functional and not stale
	assert.False(t, entry.IsStaleAfter(time.Hour), "Entry should not be stale immediately after setting last access")
}
