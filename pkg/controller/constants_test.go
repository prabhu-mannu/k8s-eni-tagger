package controller

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRetryWithBackoff_Success(t *testing.T) {
	ctx := context.Background()
	attempts := 0

	err := retryWithBackoff(ctx, 3, 10*time.Millisecond, 2, func() error {
		attempts++
		return nil
	})

	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	if attempts != 1 {
		t.Errorf("Expected 1 attempt, got %d", attempts)
	}
}

func TestRetryWithBackoff_SuccessAfterRetries(t *testing.T) {
	ctx := context.Background()
	attempts := 0

	err := retryWithBackoff(ctx, 3, 10*time.Millisecond, 2, func() error {
		attempts++
		if attempts < 3 {
			return errors.New("temporary error")
		}
		return nil
	})

	if err != nil {
		t.Errorf("Expected no error after retries, got %v", err)
	}

	if attempts != 3 {
		t.Errorf("Expected 3 attempts, got %d", attempts)
	}
}

func TestRetryWithBackoff_ExhaustedRetries(t *testing.T) {
	ctx := context.Background()
	attempts := 0
	expectedErr := errors.New("persistent error")

	err := retryWithBackoff(ctx, 3, 10*time.Millisecond, 2, func() error {
		attempts++
		return expectedErr
	})

	if err == nil {
		t.Error("Expected error after exhausting retries, got nil")
	}

	if attempts != 3 {
		t.Errorf("Expected 3 attempts, got %d", attempts)
	}
}

func TestRetryWithBackoff_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	attempts := 0

	// Cancel context after first attempt
	go func() {
		time.Sleep(15 * time.Millisecond)
		cancel()
	}()

	err := retryWithBackoff(ctx, 10, 10*time.Millisecond, 2, func() error {
		attempts++
		return errors.New("error")
	})

	if err == nil {
		t.Error("Expected context cancellation error, got nil")
	}

	if err != context.Canceled {
		t.Errorf("Expected context.Canceled error, got %v", err)
	}

	// Should have attempted at least once, but not all 10 times due to context cancellation
	if attempts == 0 || attempts >= 10 {
		t.Errorf("Expected 1-3 attempts due to context cancellation, got %d", attempts)
	}
}

func TestRetryWithBackoff_BackoffMultiplier(t *testing.T) {
	ctx := context.Background()
	attempts := 0
	startTime := time.Now()
	minBackoff := 10 * time.Millisecond

	err := retryWithBackoff(ctx, 3, minBackoff, 2, func() error {
		attempts++
		return errors.New("error")
	})

	elapsed := time.Since(startTime)

	if err == nil {
		t.Error("Expected error, got nil")
	}

	// With 3 retries and 2x multiplier, backoff should be: 10ms + 20ms = 30ms minimum
	// (first attempt immediately, then wait 10ms, second attempt, wait 20ms, third attempt)
	if elapsed < 25*time.Millisecond {
		t.Errorf("Expected at least 25ms due to backoff, got %v", elapsed)
	}

	if attempts != 3 {
		t.Errorf("Expected 3 attempts, got %d", attempts)
	}
}

func TestRetryWithBackoff_SingleRetry(t *testing.T) {
	ctx := context.Background()
	attempts := 0

	err := retryWithBackoff(ctx, 1, 10*time.Millisecond, 2, func() error {
		attempts++
		return errors.New("error")
	})

	if err == nil {
		t.Error("Expected error, got nil")
	}

	if attempts != 1 {
		t.Errorf("Expected 1 attempt, got %d", attempts)
	}
}

func TestRetryWithBackoff_ContextDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()

	attempts := 0

	err := retryWithBackoff(ctx, 10, 10*time.Millisecond, 2, func() error {
		attempts++
		return errors.New("error")
	})

	if err == nil {
		t.Error("Expected context deadline exceeded error, got nil")
	}

	// Should have attempted multiple times but not all 10 due to deadline
	if attempts == 0 || attempts >= 10 {
		t.Errorf("Expected 1-3 attempts due to context deadline, got %d", attempts)
	}
}

func TestRetryWithBackoff_ZeroBackoff(t *testing.T) {
	ctx := context.Background()
	attempts := 0
	startTime := time.Now()

	err := retryWithBackoff(ctx, 3, 0*time.Millisecond, 2, func() error {
		attempts++
		return errors.New("error")
	})

	elapsed := time.Since(startTime)

	if err == nil {
		t.Error("Expected error, got nil")
	}

	if attempts != 3 {
		t.Errorf("Expected 3 attempts, got %d", attempts)
	}

	// With zero backoff, should complete very quickly (< 50ms)
	if elapsed > 50*time.Millisecond {
		t.Errorf("Expected quick completion with zero backoff, got %v", elapsed)
	}
}

func TestRetryWithBackoff_LargeBackoffMultiplier(t *testing.T) {
	ctx := context.Background()
	attempts := 0
	startTime := time.Now()

	err := retryWithBackoff(ctx, 3, 1*time.Millisecond, 10, func() error {
		attempts++
		return errors.New("error")
	})

	elapsed := time.Since(startTime)

	if err == nil {
		t.Error("Expected error, got nil")
	}

	if attempts != 3 {
		t.Errorf("Expected 3 attempts, got %d", attempts)
	}

	// With 1ms initial backoff and 10x multiplier: 1ms + 10ms = 11ms minimum
	if elapsed < 8*time.Millisecond {
		t.Errorf("Expected at least 8ms due to backoff, got %v", elapsed)
	}
}
