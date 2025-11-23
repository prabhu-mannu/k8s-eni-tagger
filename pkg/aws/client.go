package aws

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"k8s-eni-tagger/pkg/metrics"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/smithy-go"
)

// ENIInfo contains details about an Elastic Network Interface
type ENIInfo struct {
	ID            string
	SubnetID      string
	InterfaceType string
	IsShared      bool
	Description   string
	Tags          map[string]string
}

// Client defines the interface for AWS operations
type Client interface {
	GetENIInfoByIP(ctx context.Context, ip string) (*ENIInfo, error)
	TagENI(ctx context.Context, eniID string, tags map[string]string) error
	UntagENI(ctx context.Context, eniID string, tagKeys []string) error
}

type defaultClient struct {
	ec2Client *ec2.Client
}

// NewClient creates a new AWS client
func NewClient(ctx context.Context) (Client, error) {
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("unable to load SDK config: %w", err)
	}

	return &defaultClient{
		ec2Client: ec2.NewFromConfig(cfg),
	}, nil
}

// GetENIInfoByIP finds the ENI details associated with a private IP address
func (c *defaultClient) GetENIInfoByIP(ctx context.Context, ip string) (*ENIInfo, error) {
	start := time.Now()
	defer func() {
		duration := time.Since(start).Seconds()
		metrics.AWSAPILatency.WithLabelValues("DescribeNetworkInterfaces", "success").Observe(duration)
	}()

	input := &ec2.DescribeNetworkInterfacesInput{
		Filters: []types.Filter{
			{
				Name:   aws.String("private-ip-address"),
				Values: []string{ip},
			},
		},
	}

	var result *ec2.DescribeNetworkInterfacesOutput
	var err error

	// Retry with exponential backoff
	for attempt := 0; attempt < 3; attempt++ {
		result, err = c.ec2Client.DescribeNetworkInterfaces(ctx, input)
		if err == nil {
			break
		}

		// Check if it's a throttling error
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) {
			if apiErr.ErrorCode() == "RequestLimitExceeded" ||
				apiErr.ErrorCode() == "Throttling" {
				backoff := time.Duration(math.Pow(2, float64(attempt))) * time.Second
				time.Sleep(backoff)
				continue
			}
		}

		metrics.AWSAPILatency.WithLabelValues("DescribeNetworkInterfaces", "error").Observe(time.Since(start).Seconds())
		return nil, fmt.Errorf("failed to describe network interfaces: %w", err)
	}

	if err != nil {
		metrics.AWSAPILatency.WithLabelValues("DescribeNetworkInterfaces", "error").Observe(time.Since(start).Seconds())
		return nil, fmt.Errorf("failed to describe network interfaces after retries: %w", err)
	}

	if len(result.NetworkInterfaces) == 0 {
		return nil, fmt.Errorf("no ENI found for IP %s (pod may be using host network or Fargate)", ip)
	}

	// In case of multiple matches (unlikely for private IP in same VPC), return the first one
	eni := result.NetworkInterfaces[0]

	tags := make(map[string]string)
	for _, t := range eni.TagSet {
		if t.Key != nil && t.Value != nil {
			tags[*t.Key] = *t.Value
		}
	}

	info := &ENIInfo{
		ID:            *eni.NetworkInterfaceId,
		SubnetID:      *eni.SubnetId,
		InterfaceType: string(eni.InterfaceType),
		Description:   aws.ToString(eni.Description),
		Tags:          tags,
	}

	// Determine if ENI is shared
	// 1. If it has multiple private IPs, it's likely shared (e.g. Node primary ENI)
	// 2. If it's a "interface" type (standard ENI) and has multiple IPs, it's definitely shared on EKS
	// 3. Fargate ENIs usually have 1 IP. Branch ENIs (trunk) might be different.
	if len(eni.PrivateIpAddresses) > 1 {
		info.IsShared = true
	}

	// Additional heuristic: Check description for "aws-K8S" which often indicates a secondary ENI managed by VPC CNI
	// But the most reliable check for "Is this EXCLUSIVE to this pod?" is hard without more context.
	// For now, >1 IP is a strong signal of "Shared Node ENI".

	return info, nil
}

// TagENI adds tags to an ENI
func (c *defaultClient) TagENI(ctx context.Context, eniID string, tags map[string]string) error {
	if len(tags) == 0 {
		return nil
	}

	start := time.Now()
	defer func() {
		duration := time.Since(start).Seconds()
		metrics.AWSAPILatency.WithLabelValues("CreateTags", "success").Observe(duration)
	}()

	var ec2Tags []types.Tag
	for k, v := range tags {
		ec2Tags = append(ec2Tags, types.Tag{
			Key:   aws.String(k),
			Value: aws.String(v),
		})
	}

	input := &ec2.CreateTagsInput{
		Resources: []string{eniID},
		Tags:      ec2Tags,
	}

	var err error
	for attempt := 0; attempt < 3; attempt++ {
		_, err = c.ec2Client.CreateTags(ctx, input)
		if err == nil {
			return nil
		}

		var apiErr smithy.APIError
		if errors.As(err, &apiErr) {
			// Handle specific errors
			switch apiErr.ErrorCode() {
			case "RequestLimitExceeded", "Throttling":
				backoff := time.Duration(math.Pow(2, float64(attempt))) * time.Second
				time.Sleep(backoff)
				continue
			case "InvalidNetworkInterfaceID.NotFound":
				metrics.AWSAPILatency.WithLabelValues("CreateTags", "error").Observe(time.Since(start).Seconds())
				return fmt.Errorf("ENI %s not found (may have been deleted): %w", eniID, err)
			case "UnauthorizedOperation":
				metrics.AWSAPILatency.WithLabelValues("CreateTags", "error").Observe(time.Since(start).Seconds())
				return fmt.Errorf("insufficient permissions to tag ENI %s: %w", eniID, err)
			}
		}

		metrics.AWSAPILatency.WithLabelValues("CreateTags", "error").Observe(time.Since(start).Seconds())
		return fmt.Errorf("failed to tag ENI %s: %w", eniID, err)
	}

	metrics.AWSAPILatency.WithLabelValues("CreateTags", "error").Observe(time.Since(start).Seconds())
	return fmt.Errorf("failed to tag ENI %s after retries: %w", eniID, err)
}

// UntagENI removes tags from an ENI
func (c *defaultClient) UntagENI(ctx context.Context, eniID string, tagKeys []string) error {
	if len(tagKeys) == 0 {
		return nil
	}

	start := time.Now()
	defer func() {
		duration := time.Since(start).Seconds()
		metrics.AWSAPILatency.WithLabelValues("DeleteTags", "success").Observe(duration)
	}()

	var ec2Tags []types.Tag
	for _, k := range tagKeys {
		ec2Tags = append(ec2Tags, types.Tag{
			Key: aws.String(k),
		})
	}

	input := &ec2.DeleteTagsInput{
		Resources: []string{eniID},
		Tags:      ec2Tags,
	}

	var err error
	for attempt := 0; attempt < 3; attempt++ {
		_, err = c.ec2Client.DeleteTags(ctx, input)
		if err == nil {
			return nil
		}

		var apiErr smithy.APIError
		if errors.As(err, &apiErr) {
			switch apiErr.ErrorCode() {
			case "RequestLimitExceeded", "Throttling":
				backoff := time.Duration(math.Pow(2, float64(attempt))) * time.Second
				time.Sleep(backoff)
				continue
			case "InvalidNetworkInterfaceID.NotFound":
				metrics.AWSAPILatency.WithLabelValues("DeleteTags", "error").Observe(time.Since(start).Seconds())
				return fmt.Errorf("ENI %s not found (may have been deleted): %w", eniID, err)
			case "UnauthorizedOperation":
				metrics.AWSAPILatency.WithLabelValues("DeleteTags", "error").Observe(time.Since(start).Seconds())
				return fmt.Errorf("insufficient permissions to untag ENI %s: %w", eniID, err)
			}
		}

		metrics.AWSAPILatency.WithLabelValues("DeleteTags", "error").Observe(time.Since(start).Seconds())
		return fmt.Errorf("failed to untag ENI %s: %w", eniID, err)
	}

	metrics.AWSAPILatency.WithLabelValues("DeleteTags", "error").Observe(time.Since(start).Seconds())
	return fmt.Errorf("failed to untag ENI %s after retries: %w", eniID, err)
}
