package health

import (
	"context"
	"fmt"
	"net/http"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
)

// AWSChecker checks connectivity to AWS
type AWSChecker struct {
	ec2Client *ec2.Client
}

// NewAWSChecker creates a new AWS health checker
func NewAWSChecker(ctx context.Context) (*AWSChecker, error) {
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("unable to load SDK config: %w", err)
	}

	return &AWSChecker{
		ec2Client: ec2.NewFromConfig(cfg),
	}, nil
}

// Check performs a lightweight AWS API call to verify connectivity
func (c *AWSChecker) Check(req *http.Request) error {
	// DescribeRegions is a lightweight call that doesn't require specific resource permissions
	// and works in all regions.
	_, err := c.ec2Client.DescribeRegions(req.Context(), &ec2.DescribeRegionsInput{
		AllRegions: aws.Bool(false),
	})
	if err != nil {
		return fmt.Errorf("failed to connect to AWS: %w", err)
	}
	return nil
}
