package health

import (
	"fmt"
	"net/http"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// AWSChecker checks connectivity to AWS
type AWSChecker struct {
	ec2Client *ec2.Client
}

// NewAWSChecker creates a new AWS health checker using a shared EC2 client.
// This avoids creating duplicate EC2 clients and ensures consistent configuration.
func NewAWSChecker(ec2Client *ec2.Client) *AWSChecker {
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
