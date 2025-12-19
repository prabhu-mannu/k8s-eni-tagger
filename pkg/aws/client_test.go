package aws

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/smithy-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// mockEC2Client is a mock implementation of EC2API
type mockEC2Client struct {
	mock.Mock
}

func (m *mockEC2Client) DescribeNetworkInterfaces(ctx context.Context, params *ec2.DescribeNetworkInterfacesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeNetworkInterfacesOutput, error) {
	args := m.Called(ctx, params, optFns)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*ec2.DescribeNetworkInterfacesOutput), args.Error(1)
}

func (m *mockEC2Client) CreateTags(ctx context.Context, params *ec2.CreateTagsInput, optFns ...func(*ec2.Options)) (*ec2.CreateTagsOutput, error) {
	args := m.Called(ctx, params, optFns)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*ec2.CreateTagsOutput), args.Error(1)
}

func (m *mockEC2Client) DeleteTags(ctx context.Context, params *ec2.DeleteTagsInput, optFns ...func(*ec2.Options)) (*ec2.DeleteTagsOutput, error) {
	args := m.Called(ctx, params, optFns)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*ec2.DeleteTagsOutput), args.Error(1)
}

type throttlingAPIError struct{}

func (th throttlingAPIError) ErrorCode() string    { return "Throttling" }
func (th throttlingAPIError) ErrorMessage() string { return "rate limited" }
func (th throttlingAPIError) ErrorFault() smithy.ErrorFault {
	return smithy.FaultServer
}
func (th throttlingAPIError) String() string { return th.Error() }
func (th throttlingAPIError) Error() string  { return "Throttling: rate limited" }

func TestGetENIInfoByIP(t *testing.T) {
	ctx := context.TODO()

	tests := []struct {
		name          string
		ip            string
		mockSetup     func(m *mockEC2Client)
		expectedInfo  *ENIInfo
		expectedError string
	}{
		{
			name: "Success - Found ENI",
			ip:   "10.0.0.1",
			mockSetup: func(m *mockEC2Client) {
				m.On("DescribeNetworkInterfaces", ctx, mock.MatchedBy(func(input *ec2.DescribeNetworkInterfacesInput) bool {
					return len(input.Filters) > 0 && input.Filters[0].Values[0] == "10.0.0.1"
				}), mock.Anything).Return(&ec2.DescribeNetworkInterfacesOutput{
					NetworkInterfaces: []types.NetworkInterface{
						{
							NetworkInterfaceId: aws.String("eni-12345"),
							SubnetId:           aws.String("subnet-123"),
							InterfaceType:      types.NetworkInterfaceTypeInterface,
							Description:        aws.String("primary eni"),
							TagSet: []types.Tag{
								{Key: aws.String("Name"), Value: aws.String("test-eni")},
							},
							PrivateIpAddresses: []types.NetworkInterfacePrivateIpAddress{
								{PrivateIpAddress: aws.String("10.0.0.1")},
							},
						},
					},
				}, nil)
			},
			expectedInfo: &ENIInfo{
				ID:            "eni-12345",
				SubnetID:      "subnet-123",
				InterfaceType: "interface",
				Description:   "primary eni",
				IsShared:      false,
				Tags:          map[string]string{"Name": "test-eni"},
			},
		},
		{
			name: "Error - AWS Error",
			ip:   "10.0.0.2",
			mockSetup: func(m *mockEC2Client) {
				m.On("DescribeNetworkInterfaces", ctx, mock.Anything, mock.Anything).Return(nil, errors.New("aws error"))
			},
			expectedError: "failed to describe network interfaces: aws error",
		},
		{
			name: "Error - No ENI Found",
			ip:   "10.0.0.3",
			mockSetup: func(m *mockEC2Client) {
				m.On("DescribeNetworkInterfaces", ctx, mock.Anything, mock.Anything).Return(&ec2.DescribeNetworkInterfacesOutput{
					NetworkInterfaces: []types.NetworkInterface{},
				}, nil)
			},
			expectedError: "no ENI found for IP 10.0.0.3",
		},
		{
			name: "Success - Shared ENI (Multiple IPs)",
			ip:   "10.0.0.4",
			mockSetup: func(m *mockEC2Client) {
				m.On("DescribeNetworkInterfaces", ctx, mock.Anything, mock.Anything).Return(&ec2.DescribeNetworkInterfacesOutput{
					NetworkInterfaces: []types.NetworkInterface{
						{
							NetworkInterfaceId: aws.String("eni-shared"),
							SubnetId:           aws.String("subnet-1"),
							InterfaceType:      types.NetworkInterfaceTypeInterface,
							Description:        aws.String("shared eni"),
							PrivateIpAddresses: []types.NetworkInterfacePrivateIpAddress{
								{PrivateIpAddress: aws.String("10.0.0.4")},
								{PrivateIpAddress: aws.String("10.0.0.5")},
							},
						},
					},
				}, nil)
			},
			expectedInfo: &ENIInfo{ID: "eni-shared", SubnetID: "subnet-1", InterfaceType: "interface", Description: "shared eni", IsShared: true, Tags: map[string]string{}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := new(mockEC2Client)
			tt.mockSetup(mockClient)

			rl, err := newRateLimiter(10, 20)
			require.NoError(t, err)

			c := &defaultClient{
				ec2Client:   mockClient,
				rateLimiter: rl,
			}

			info, err := c.GetENIInfoByIP(ctx, tt.ip)

			if tt.expectedError != "" {
				assert.ErrorContains(t, err, tt.expectedError)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedInfo.ID, info.ID)
				assert.Equal(t, tt.expectedInfo.IsShared, info.IsShared)
				assert.Equal(t, tt.expectedInfo.Tags, info.Tags)
			}
			mockClient.AssertExpectations(t)
		})
	}
}

func TestTagENI(t *testing.T) {
	ctx := context.TODO()

	tests := []struct {
		name          string
		eniID         string
		tags          map[string]string
		mockSetup     func(m *mockEC2Client)
		expectedError string
	}{
		{
			name:  "Success",
			eniID: "eni-123",
			tags:  map[string]string{"k8s-pod": "nginx"},
			mockSetup: func(m *mockEC2Client) {
				m.On("CreateTags", ctx, mock.MatchedBy(func(input *ec2.CreateTagsInput) bool {
					return input.Resources[0] == "eni-123" && len(input.Tags) == 1 && *input.Tags[0].Key == "k8s-pod"
				}), mock.Anything).Return(&ec2.CreateTagsOutput{}, nil)
			},
		},
		{
			name:  "Empty Tags",
			eniID: "eni-123",
			tags:  map[string]string{},
			mockSetup: func(m *mockEC2Client) {
				// Should not call AWS
			},
		},
		{
			name:  "Error",
			eniID: "eni-fail",
			tags:  map[string]string{"foo": "bar"},
			mockSetup: func(m *mockEC2Client) {
				m.On("CreateTags", ctx, mock.Anything, mock.Anything).Return(nil, errors.New("tag error"))
			},
			expectedError: "failed to tag ENI eni-fail: tag error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := new(mockEC2Client)
			tt.mockSetup(mockClient)

			rl, err := newRateLimiter(10, 20)
			require.NoError(t, err)

			c := &defaultClient{
				ec2Client:   mockClient,
				rateLimiter: rl,
			}

			err = c.TagENI(ctx, tt.eniID, tt.tags)

			if tt.expectedError != "" {
				assert.ErrorContains(t, err, tt.expectedError)
			} else {
				assert.NoError(t, err)
			}
			mockClient.AssertExpectations(t)
		})
	}
}

func TestTagENI_RetryOnThrottling(t *testing.T) {
	ctx := context.Background()
	mockClient := new(mockEC2Client)
	mockClient.On("CreateTags", mock.Anything, mock.Anything, mock.Anything).Return(nil, throttlingAPIError{}).Once()
	mockClient.On("CreateTags", mock.Anything, mock.Anything, mock.Anything).Return(&ec2.CreateTagsOutput{}, nil).Once()

	rl, err := newRateLimiter(10, 20)
	require.NoError(t, err)

	c := &defaultClient{
		ec2Client:   mockClient,
		rateLimiter: rl,
	}

	err = c.TagENI(ctx, "eni-abc", map[string]string{"k": "v"})
	assert.NoError(t, err)
	mockClient.AssertNumberOfCalls(t, "CreateTags", 2)
	mockClient.AssertExpectations(t)
}

func TestTagENI_RetryStopsOnContextDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	mockClient := new(mockEC2Client)
	mockClient.On("CreateTags", mock.Anything, mock.Anything, mock.Anything).Return(nil, throttlingAPIError{}).Once()

	// Very low QPS so the second attempt blocks on the rate limiter and honors context deadline.
	rl, err := newRateLimiter(0.1, 1)
	require.NoError(t, err)

	c := &defaultClient{
		ec2Client:   mockClient,
		rateLimiter: rl,
	}

	err = c.TagENI(ctx, "eni-abc", map[string]string{"k": "v"})
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	mockClient.AssertNumberOfCalls(t, "CreateTags", 1)
	mockClient.AssertExpectations(t)
}

func TestUntagENI(t *testing.T) {
	ctx := context.TODO()

	tests := []struct {
		name          string
		eniID         string
		keys          []string
		mockSetup     func(m *mockEC2Client)
		expectedError string
	}{
		{
			name:  "Success",
			eniID: "eni-123",
			keys:  []string{"k8s-pod"},
			mockSetup: func(m *mockEC2Client) {
				m.On("DeleteTags", ctx, mock.MatchedBy(func(input *ec2.DeleteTagsInput) bool {
					return input.Resources[0] == "eni-123" && len(input.Tags) == 1 && *input.Tags[0].Key == "k8s-pod"
				}), mock.Anything).Return(&ec2.DeleteTagsOutput{}, nil)
			},
		},
		{
			name:  "Empty Keys",
			eniID: "eni-123",
			keys:  []string{},
			mockSetup: func(m *mockEC2Client) {
				// Should not call AWS
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := new(mockEC2Client)
			tt.mockSetup(mockClient)

			rl, err := newRateLimiter(10, 20)
			require.NoError(t, err)

			c := &defaultClient{
				ec2Client:   mockClient,
				rateLimiter: rl,
			}

			err = c.UntagENI(ctx, tt.eniID, tt.keys)

			if tt.expectedError != "" {
				assert.ErrorContains(t, err, tt.expectedError)
			} else {
				assert.NoError(t, err)
			}
			mockClient.AssertExpectations(t)
		})
	}
}

func TestRateLimitConfig(t *testing.T) {
	config := DefaultRateLimitConfig()
	assert.Equal(t, 10.0, config.QPS)
	assert.Equal(t, 20, config.Burst)
}

func TestRateLimiter(t *testing.T) {
	// Use a very low QPS to make waits deterministic in tests.
	// After consuming the initial burst token, the next token should take ~10s.
	rl, err := newRateLimiter(0.1, 1) // 0.1 QPS, burst 1
	require.NoError(t, err)

	// 1. First token should be available immediately (burst).
	err = rl.Wait(context.Background())
	assert.NoError(t, err)
	assert.Less(t, rl.Tokens(), 1.0)

	// 2. Next token should not arrive before a short timeout.
	ctxTimeout, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err = rl.Wait(ctxTimeout)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "exceed context deadline")

	// 3. Cancellation should be respected while waiting.
	ctxCancel, cancel2 := context.WithCancel(context.Background())
	cancel2()
	err = rl.Wait(ctxCancel)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestRateLimiterSafetyChecks(t *testing.T) {
	t.Run("negative refillRate rejected by constructor", func(t *testing.T) {
		_, err := newRateLimiter(-1, 10)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "qps must be positive")
	})

	t.Run("zero refillRate rejected by constructor", func(t *testing.T) {
		_, err := newRateLimiter(0, 10)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "qps must be positive")
	})

	t.Run("zero burst rejected by constructor", func(t *testing.T) {
		_, err := newRateLimiter(1, 0)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "burst must be at least 1")
	})
}

func TestConstructors(t *testing.T) {
	// Test GetEC2Client with mock (should return nil as it's not *ec2.Client)
	mockClient := new(mockEC2Client)
	rl, err := newRateLimiter(10, 20)
	require.NoError(t, err)
	c := &defaultClient{
		ec2Client:   mockClient,
		rateLimiter: rl,
	}
	assert.Nil(t, c.GetEC2Client())

	// Test real client wrapper
	// We won't call NewClient here to avoid AWS config loading issues in test environment
	// but we can test the structure if we manually assemble it or mock config loading
}
func TestNewClientWithEndpointOverride(t *testing.T) {
	// Test that AWS_ENDPOINT_URL environment variable is properly handled
	// This is a behavioral test - we verify the code path doesn't panic
	// Actual endpoint behavior is tested in E2E tests

	tests := []struct {
		name        string
		endpointEnv string
		shouldWork  bool
	}{
		{
			name:        "No endpoint override",
			endpointEnv: "",
			shouldWork:  true,
		},
		{
			name:        "With endpoint override",
			endpointEnv: "http://localhost:5000",
			shouldWork:  true,
		},
		{
			name:        "With https endpoint",
			endpointEnv: "https://mock-aws.example.com",
			shouldWork:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Save original env var
			originalEnv := os.Getenv("AWS_ENDPOINT_URL")
			defer func() {
				if originalEnv != "" {
					os.Setenv("AWS_ENDPOINT_URL", originalEnv)
				} else {
					os.Unsetenv("AWS_ENDPOINT_URL")
				}
			}()

			// Set test endpoint
			if tt.endpointEnv != "" {
				os.Setenv("AWS_ENDPOINT_URL", tt.endpointEnv)
			} else {
				os.Unsetenv("AWS_ENDPOINT_URL")
			}

			// This will fail in test environment due to missing AWS credentials,
			// but we're testing that the endpoint override logic doesn't panic
			// The actual functionality is validated in E2E tests
			ctx := context.Background()
			_, err := NewClient(ctx)

			// We expect an error (no AWS credentials in test env)
			// but NOT a panic or nil pointer error
			if err != nil {
				// Error is expected in test environment
				// Just ensure it's not a panic or programming error
				assert.NotContains(t, err.Error(), "panic")
				assert.NotContains(t, err.Error(), "nil pointer")
			}
		})
	}
}
