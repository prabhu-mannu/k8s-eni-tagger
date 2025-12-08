package health

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
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

			checker := NewAWSChecker(m)
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
}
