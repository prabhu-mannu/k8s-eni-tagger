package controller

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestRetryWithBackoff_CapsWaitAndBackoff(t *testing.T) {
	origJitter := jitterFn
	origMax := maxBackoffDuration
	defer func() {
		jitterFn = origJitter
		maxBackoffDuration = origMax
	}()

	// Make max backoff small so the test runs fast
	maxBackoffDuration = 200 * time.Millisecond

	// Make jitter large so wait would exceed the cap if not limited
	jitterFn = func(d time.Duration) time.Duration { return d }

	initial := 10 * time.Millisecond
	multiplier := 100 // would blow up quickly without caps
	attempts := 5

	var mu sync.Mutex
	var times []time.Time

	op := func() error {
		mu.Lock()
		times = append(times, time.Now())
		mu.Unlock()
		return fmt.Errorf("fail")
	}

	ctx := context.Background()
	start := time.Now()
	err := retryWithBackoff(ctx, attempts, initial, multiplier, op)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if got := len(times); got != attempts {
		t.Fatalf("expected %d attempts, got %d", attempts, got)
	}

	// ensure per-attempt waits are capped by maxBackoffDuration (with a small tolerance)
	for i := 0; i+1 < len(times); i++ {
		d := times[i+1].Sub(times[i])
		if d > maxBackoffDuration+50*time.Millisecond {
			t.Fatalf("wait between attempts exceeded cap: %v > %v", d, maxBackoffDuration)
		}
	}

	if time.Since(start) > time.Duration(attempts)*maxBackoffDuration+time.Second {
		t.Fatalf("total duration unexpectedly large")
	}
}
