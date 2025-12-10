package health

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

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
