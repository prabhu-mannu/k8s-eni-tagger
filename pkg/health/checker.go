package health

import (
	"context"
	"fmt"
	"net/http"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// EC2HealthAPI defines the method needed for health checking
type EC2HealthAPI interface {
	DescribeAccountAttributes(ctx context.Context, params *ec2.DescribeAccountAttributesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeAccountAttributesOutput, error)
}

// AWSChecker checks connectivity to AWS
type AWSChecker struct {
	ec2Client EC2HealthAPI
}

// NewAWSChecker creates a new AWS health checker using a shared EC2 client.
// It accepts any implementation of EC2HealthAPI (including *ec2.Client).
func NewAWSChecker(ec2Client EC2HealthAPI) *AWSChecker {
	return &AWSChecker{ec2Client: ec2Client}
}

// Check performs a lightweight AWS API call to verify connectivity
func (c *AWSChecker) Check(req *http.Request) error {
	// DescribeAccountAttributes is a lightweight call to verify API access
	_, err := c.ec2Client.DescribeAccountAttributes(req.Context(), &ec2.DescribeAccountAttributesInput{
		AttributeNames: []types.AccountAttributeName{types.AccountAttributeNameSupportedPlatforms},
	})
	if err != nil {
		return fmt.Errorf("failed to connect to AWS: %w", err)
	}
	return nil
}
