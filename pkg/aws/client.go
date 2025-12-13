package aws

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"k8s-eni-tagger/pkg/metrics"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/smithy-go"
)

// EC2API defines the interface for EC2 operations used by this package
// This allows for mocking in tests
type EC2API interface {
	DescribeNetworkInterfaces(ctx context.Context, params *ec2.DescribeNetworkInterfacesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeNetworkInterfacesOutput, error)
	CreateTags(ctx context.Context, params *ec2.CreateTagsInput, optFns ...func(*ec2.Options)) (*ec2.CreateTagsOutput, error)
	DeleteTags(ctx context.Context, params *ec2.DeleteTagsInput, optFns ...func(*ec2.Options)) (*ec2.DeleteTagsOutput, error)
}

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
	// GetEC2Client returns the underlying EC2 client for sharing with other components
	GetEC2Client() *ec2.Client
}

// RateLimitConfig configures rate limiting for AWS API calls
type RateLimitConfig struct {
	// QPS is the maximum queries per second
	QPS float64
	// Burst is the maximum burst size
	Burst int
}

// DefaultRateLimitConfig returns sensible defaults for AWS API rate limiting
// EC2 has different limits per API, but 10 QPS with burst 20 is conservative
func DefaultRateLimitConfig() RateLimitConfig {
	return RateLimitConfig{
		QPS:   10,
		Burst: 20,
	}
}

type defaultClient struct {
	ec2Client   EC2API
	rateLimiter *rateLimiter
}

// rateLimiter implements a token bucket rate limiter
type rateLimiter struct {
	tokens     float64
	maxTokens  float64
	refillRate float64 // tokens per second
	lastRefill time.Time
	mu         sync.Mutex
}

func newRateLimiter(qps float64, burst int) *rateLimiter {
	return &rateLimiter{
		tokens:     float64(burst),
		maxTokens:  float64(burst),
		refillRate: qps,
		lastRefill: time.Now(),
	}
}

func (r *rateLimiter) Wait(ctx context.Context) error {
	for {
		// Acquire lock and refill tokens
		r.mu.Lock()
		now := time.Now()
		elapsed := now.Sub(r.lastRefill).Seconds()
		r.tokens = min(r.maxTokens, r.tokens+elapsed*r.refillRate)
		r.lastRefill = now

		if r.tokens >= 1 {
			r.tokens--
			r.mu.Unlock()
			return nil
		}

		// Calculate wait time for next token and release lock while waiting
		// Ensure tokens is non-negative for accurate wait calculation
		tokensNeeded := 1.0 - max(0, r.tokens)
		waitTime := time.Duration(tokensNeeded/r.refillRate*1000) * time.Millisecond
		r.mu.Unlock()

		select {
		case <-ctx.Done():
			// Context canceled while waiting
			return ctx.Err()
		case <-time.After(waitTime):
			// Loop back to refill tokens based on elapsed time during wait
			// This ensures we account for all elapsed time, even if other goroutines
			// consumed tokens while we were waiting
			continue
		}
	}
}

// NewClient creates a new AWS client with default rate limiting
func NewClient(ctx context.Context) (Client, error) {
	return NewClientWithRateLimiter(ctx, DefaultRateLimitConfig())
}

// NewClientWithRateLimiter creates a new AWS client with custom rate limiting
func NewClientWithRateLimiter(ctx context.Context, rlConfig RateLimitConfig) (Client, error) {
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("unable to load SDK config: %w", err)
	}

	// Set custom User-Agent
	cfg.AppID = "k8s-eni-tagger"

	return &defaultClient{
		ec2Client:   ec2.NewFromConfig(cfg),
		rateLimiter: newRateLimiter(rlConfig.QPS, rlConfig.Burst),
	}, nil
}

// GetEC2Client returns the underlying EC2 client for sharing with other components
// Note: This now returns an interface, callers may need to type assert if they need the specific struct
// but for general usage the interface should suffice if extended.
// However, since we return *ec2.Client in the interface, we might have to cast it or change the interface return type.
// To avoid breaking changes, we'll keep the signature but cast if possible, or better yet,
// since we know in production it's *ec2.Client, we can type assert.
func (c *defaultClient) GetEC2Client() *ec2.Client {
	if client, ok := c.ec2Client.(*ec2.Client); ok {
		return client
	}
	return nil
}

// GetENIInfoByIP finds the ENI details associated with a private IP address
func (c *defaultClient) GetENIInfoByIP(ctx context.Context, ip string) (*ENIInfo, error) {
	// Rate limit AWS API calls
	if err := c.rateLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limiter wait: %w", err)
	}

	start := time.Now()
	status := "success"
	defer func() {
		duration := time.Since(start).Seconds()
		metrics.AWSAPILatency.WithLabelValues("DescribeNetworkInterfaces", status).Observe(duration)
	}()

	input := &ec2.DescribeNetworkInterfacesInput{
		Filters: []types.Filter{
			{
				Name:   aws.String("private-ip-address"),
				Values: []string{ip},
			},
		},
	}

	// Retry with exponential backoff
	// Retry is handled by the AWS SDK default retryer
	result, err := c.ec2Client.DescribeNetworkInterfaces(ctx, input)
	if err != nil {
		status = "error"
		return nil, fmt.Errorf("failed to describe network interfaces: %w", err)
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
		ID:            aws.ToString(eni.NetworkInterfaceId),
		SubnetID:      aws.ToString(eni.SubnetId),
		InterfaceType: string(eni.InterfaceType),
		Description:   aws.ToString(eni.Description),
		Tags:          tags,
	}

	// Determine if ENI is shared using improved heuristics
	// Check description for AWS VPC CNI patterns
	isVPCCNI := strings.Contains(aws.ToString(eni.Description), "aws-K8S-")

	switch {
	case string(eni.InterfaceType) == "branch":
		// EKS Fargate/trunk-based - branch ENIs are pod-exclusive
		info.IsShared = false
	case string(eni.InterfaceType) == "trunk":
		// Trunk ENIs host multiple branch ENIs
		info.IsShared = true
	case isVPCCNI && len(eni.PrivateIpAddresses) == 1:
		// VPC CNI secondary ENI with single IP - likely pod exclusive (prefix delegation)
		info.IsShared = false
	case len(eni.PrivateIpAddresses) > 1:
		// Multiple IPs on same ENI - definitely shared
		info.IsShared = true
	default:
		// Single IP, standard interface - could be either, assume not shared
		info.IsShared = false
	}

	return info, nil
}

// TagENI adds tags to an ENI
func (c *defaultClient) TagENI(ctx context.Context, eniID string, tags map[string]string) error {
	if len(tags) == 0 {
		return nil
	}

	// Rate limit AWS API calls
	if err := c.rateLimiter.Wait(ctx); err != nil {
		return fmt.Errorf("rate limiter wait: %w", err)
	}

	start := time.Now()
	status := "success"
	defer func() {
		duration := time.Since(start).Seconds()
		metrics.AWSAPILatency.WithLabelValues("CreateTags", status).Observe(duration)
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

	// Retry is handled by the AWS SDK default retryer
	_, err := c.ec2Client.CreateTags(ctx, input)
	if err != nil {
		status = "error"

		var apiErr smithy.APIError
		if errors.As(err, &apiErr) {
			switch apiErr.ErrorCode() {
			case "InvalidNetworkInterfaceID.NotFound":
				return fmt.Errorf("ENI %s not found (may have been deleted): %w", eniID, err)
			case "UnauthorizedOperation":
				return fmt.Errorf("insufficient permissions to tag ENI %s (check ec2:CreateTags): %w", eniID, err)
			}
		}
		return fmt.Errorf("failed to tag ENI %s: %w", eniID, err)
	}

	return nil
}

// UntagENI removes tags from an ENI
func (c *defaultClient) UntagENI(ctx context.Context, eniID string, tagKeys []string) error {
	if len(tagKeys) == 0 {
		return nil
	}

	// Rate limit AWS API calls
	if err := c.rateLimiter.Wait(ctx); err != nil {
		return fmt.Errorf("rate limiter wait: %w", err)
	}

	start := time.Now()
	status := "success"
	defer func() {
		duration := time.Since(start).Seconds()
		metrics.AWSAPILatency.WithLabelValues("DeleteTags", status).Observe(duration)
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

	// Retry is handled by the AWS SDK default retryer
	_, err := c.ec2Client.DeleteTags(ctx, input)
	if err != nil {
		status = "error"

		var apiErr smithy.APIError
		if errors.As(err, &apiErr) {
			switch apiErr.ErrorCode() {
			case "InvalidNetworkInterfaceID.NotFound":
				return fmt.Errorf("ENI %s not found (may have been deleted): %w", eniID, err)
			case "UnauthorizedOperation":
				return fmt.Errorf("insufficient permissions to untag ENI %s (check ec2:DeleteTags): %w", eniID, err)
			}
		}
		return fmt.Errorf("failed to untag ENI %s: %w", eniID, err)
	}

	return nil
}
