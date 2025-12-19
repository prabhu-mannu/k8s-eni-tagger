package health

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

type mockEC2Health struct {
	mock.Mock
}

func (m *mockEC2Health) DescribeAccountAttributes(ctx context.Context, params *ec2.DescribeAccountAttributesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeAccountAttributesOutput, error) {
	args := m.Called(ctx, params, optFns)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*ec2.DescribeAccountAttributesOutput), args.Error(1)
}

// Implement AWSHealthAPI for mockEC2Health via EC2HealthClient
func (m *mockEC2Health) HealthCheck(ctx context.Context) error {
	_, err := m.DescribeAccountAttributes(ctx, &ec2.DescribeAccountAttributesInput{
		AttributeNames: []ec2types.AccountAttributeName{ec2types.AccountAttributeNameSupportedPlatforms},
	})
	return err
}

func TestCheck(t *testing.T) {
	tests := []struct {
		name      string
		mockSetup func(m *mockEC2Health)
		expectErr bool
	}{
		{
			name: "Success",
			mockSetup: func(m *mockEC2Health) {
				m.On("DescribeAccountAttributes", mock.Anything, mock.Anything, mock.Anything).Return(&ec2.DescribeAccountAttributesOutput{}, nil)
			},
			expectErr: false,
		},
		{
			name: "Failure",
			mockSetup: func(m *mockEC2Health) {
				m.On("DescribeAccountAttributes", mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("aws error"))
			},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := new(mockEC2Health)
			tt.mockSetup(m)

			client := &EC2HealthClient{EC2: m}
			checker := NewAWSChecker(client)
			req := httptest.NewRequest("GET", "/healthz", nil)

			err := checker.Check(req)
			if tt.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			m.AssertExpectations(t)
		})
	}

	// Ensure nil client doesn't panic and returns an error
	t.Run("NilClient", func(t *testing.T) {
		checker := NewAWSChecker(nil)
		req := httptest.NewRequest("GET", "/healthz", nil)
		err := checker.Check(req)
		assert.Error(t, err)
	})

	// Simulate permission error from AWS
	t.Run("PermissionError", func(t *testing.T) {
		m := new(mockEC2Health)
		permErr := errors.New("UnauthorizedOperation: You are not authorized to perform this operation.")
		m.On("DescribeAccountAttributes", mock.Anything, mock.Anything, mock.Anything).Return(nil, permErr)
		checker := NewAWSChecker(m)
		req := httptest.NewRequest("GET", "/healthz", nil)
		err := checker.Check(req)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "UnauthorizedOperation")
		m.AssertExpectations(t)
	})

	// Simulate context cancellation
	t.Run("ContextCancelled", func(t *testing.T) {
		m := new(mockEC2Health)
		cancelledCtx, cancel := context.WithCancel(context.Background())
		cancel()
		m.On("DescribeAccountAttributes", mock.Anything, mock.Anything, mock.Anything).Return(nil, context.Canceled)
		checker := NewAWSChecker(m)
		req := httptest.NewRequest("GET", "/healthz", nil).WithContext(cancelledCtx)
		err := checker.Check(req)
		assert.Error(t, err)
		assert.Equal(t, context.Canceled, errors.Unwrap(err))
		m.AssertExpectations(t)
	})

	// Placeholder for integration test (requires real AWS credentials)
	// t.Run("IntegrationAWS", func(t *testing.T) {
	//     // TODO: Implement integration test with real EC2 client
	// })
}

func TestCheck_LatchesAfterSuccesses(t *testing.T) {
	m := new(mockEC2Health)
	// Always return success from AWS call
	m.On("DescribeAccountAttributes", mock.Anything, mock.Anything, mock.Anything).Return(&ec2.DescribeAccountAttributesOutput{}, nil)

	client := &EC2HealthClient{EC2: m}
	checker := NewAWSChecker(client)
	// Reduce the threshold for the test
	checker.maxSuccesses = 2

	req := httptest.NewRequest("GET", "/healthz", nil)

	// Perform multiple checks; only the first two should hit AWS
	for i := 0; i < 5; i++ {
		err := checker.Check(req)
		assert.NoError(t, err)
	}

	m.AssertNumberOfCalls(t, "DescribeAccountAttributes", 2)
	m.AssertExpectations(t)
}

func TestCheck_LatchesAfterSuccesses_Concurrent(t *testing.T) {
	m := new(mockEC2Health)
	// Always return success from AWS call
	m.On("DescribeAccountAttributes", mock.Anything, mock.Anything, mock.Anything).Return(&ec2.DescribeAccountAttributesOutput{}, nil)

	client := &EC2HealthClient{EC2: m}
	checker := NewAWSChecker(client)
	// Set threshold to 3 to verify race condition fix
	checker.maxSuccesses = 3

	// Run concurrent checks to simulate realistic Kubernetes probe behavior
	// Multiple probes run concurrently and should not all make AWS calls
	concurrentChecks := 10
	for i := 0; i < 5; i++ { // Run the concurrent batch 5 times
		// Create a channel to coordinate goroutines
		done := make(chan error, concurrentChecks)

		// Launch concurrent checks
		for j := 0; j < concurrentChecks; j++ {
			go func() {
				req := httptest.NewRequest("GET", "/healthz", nil)
				done <- checker.Check(req)
			}()
		}

		// Wait for all goroutines to complete
		for j := 0; j < concurrentChecks; j++ {
			err := <-done
			assert.NoError(t, err)
		}
	}

	// With proper latch implementation, only ~3 calls should hit AWS per batch
	// Even with 5 batches and 10 concurrent checks per batch, total should be significantly less
	// Expected: ~3 calls (the threshold), not 50 calls (if no latch) or 15 (if latch fails under concurrency)
	calls := m.Calls
	totalCalls := len(calls)
	t.Logf("Total AWS calls made: %d (expected <= 3 due to latch)", totalCalls)
	assert.LessOrEqual(t, totalCalls, 3, "Race condition detected: too many AWS calls made despite latch mechanism")
}

func TestAWSChecker_computeBackoff(t *testing.T) {
	checker := NewAWSChecker(nil)

	tests := []struct {
		attempt       int
		min           time.Duration
		max           time.Duration
		expectedFloor time.Duration
	}{
		{attempt: 0, min: 50 * time.Millisecond, max: 100 * time.Millisecond, expectedFloor: 50 * time.Millisecond},
		{attempt: 1, min: 100 * time.Millisecond, max: 200 * time.Millisecond, expectedFloor: 100 * time.Millisecond},
		{attempt: 3, min: 400 * time.Millisecond, max: 800 * time.Millisecond, expectedFloor: 400 * time.Millisecond},
		// Capped at 2s for higher attempts
		{attempt: 5, min: 1 * time.Second, max: 2 * time.Second, expectedFloor: 1 * time.Second},
	}

	var last time.Duration
	for _, tt := range tests {
		got := checker.computeBackoff(tt.attempt)
		assert.GreaterOrEqual(t, got, tt.min, "attempt %d below min", tt.attempt)
		assert.LessOrEqual(t, got, tt.max, "attempt %d above max", tt.attempt)
		if last > 0 {
			assert.GreaterOrEqual(t, got, tt.expectedFloor, "attempt %d should not shrink below floor", tt.attempt)
		}
		last = got
	}
}
