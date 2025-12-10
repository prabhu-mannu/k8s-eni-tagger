package health

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// AWSHealthAPI defines the minimal AWS API required for health checking.
// This interface allows mocking and testing of AWS connectivity logic for any service.
type AWSHealthAPI interface {
	HealthCheck(ctx context.Context) error
}
type EC2HealthAPI interface {
	DescribeAccountAttributes(ctx context.Context, params *ec2.DescribeAccountAttributesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeAccountAttributesOutput, error)
}

// AWSChecker checks connectivity to AWS services via a generic health API.
// It is designed to be used in health probes (e.g., readiness endpoints).
//
// Thread safety: AWSChecker is safe for concurrent use as it does not share mutable state between requests.
// Idempotency: Each Check invocation is independent and does not affect subsequent calls.
type AWSChecker struct {
	client         AWSHealthAPI
	timeoutSeconds int
	maxRetries     int
	metrics        AWSCheckerMetrics
}

// AWSCheckerMetrics defines hooks for metrics collection (e.g., Prometheus)
type AWSCheckerMetrics interface {
	IncSuccess()
	IncFailure()
	ObserveLatency(seconds float64)
}

// NewAWSChecker creates a new AWS health checker using a generic AWSHealthAPI client.
// Usage:
//
//	checker := NewAWSChecker(awsClient)
//	err := checker.Check(req)
func NewAWSChecker(client AWSHealthAPI) *AWSChecker {
	// Default: 5s timeout, 1 retry
	return &AWSChecker{client: client, timeoutSeconds: 5, maxRetries: 1, metrics: nil}
}

// NewAWSCheckerWithConfig creates a new AWSChecker with custom timeout and retry settings.
func NewAWSCheckerWithConfig(client AWSHealthAPI, timeoutSeconds, maxRetries int) *AWSChecker {
	return &AWSChecker{client: client, timeoutSeconds: timeoutSeconds, maxRetries: maxRetries, metrics: nil}
}

// Check performs a lightweight AWS API call to verify connectivity.
// Returns nil if AWS API is reachable and permissions are sufficient.
// Returns a wrapped error if connectivity or permissions are insufficient.
func (c *AWSChecker) Check(req *http.Request) error {
	if c == nil || c.client == nil {
		log.Printf("[AWSChecker] AWS client not configured")
		return fmt.Errorf("AWS client not configured")
	}
	log.Printf("[AWSChecker] Performing AWS health check via HealthCheck method")
	ctx, cancel := context.WithTimeout(req.Context(), time.Duration(c.timeoutSeconds)*time.Second)
	defer cancel()
	var err error
	start := time.Now()
	for attempt := 0; attempt < c.maxRetries; attempt++ {
		err = c.client.HealthCheck(ctx)
		if err == nil {
			log.Printf("[AWSChecker] AWS health check succeeded (attempt %d)", attempt+1)
			if c.metrics != nil {
				c.metrics.IncSuccess()
				c.metrics.ObserveLatency(time.Since(start).Seconds())
			}
			return nil
		}
		log.Printf("[AWSChecker] AWS health check failed (attempt %d): %v", attempt+1, err)
		// If context is done, break early
		if ctx.Err() != nil {
			break
		}
		// Optionally: add backoff here
	}
	if c.metrics != nil {
		c.metrics.IncFailure()
		c.metrics.ObserveLatency(time.Since(start).Seconds())
	}
	errMsg := ""
	if err != nil {
		errMsg = err.Error()
		if errMsg != "" && (containsPermissionError(errMsg)) {
			log.Printf("[AWSChecker] Permission error: %v", err)
			return fmt.Errorf("AWS permission error: %w", err)
		}
		if errMsg != "" && (containsConnectivityError(errMsg)) {
			log.Printf("[AWSChecker] Connectivity error: %v", err)
			return fmt.Errorf("AWS connectivity error: %w", err)
		}
		log.Printf("[AWSChecker] AWS API error: %v", err)
		return fmt.Errorf("AWS API error: %w", err)
	}
	return fmt.Errorf("AWS health check failed: unknown error")
}

// containsPermissionError checks if error message indicates a permission issue.
func containsPermissionError(msg string) bool {
	return containsAny(msg, []string{"UnauthorizedOperation", "AccessDenied", "not authorized", "permission denied"})
}

// containsConnectivityError checks if error message indicates a connectivity issue.
func containsConnectivityError(msg string) bool {
	return containsAny(msg, []string{"connection refused", "timeout", "no such host", "network unreachable", "dial tcp"})
}

// containsAny returns true if msg contains any of the substrings.
func containsAny(msg string, substrs []string) bool {
	for _, s := range substrs {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}

// EC2HealthClient implements AWSHealthAPI for EC2
type EC2HealthClient struct {
	EC2         EC2HealthAPI
	initialized bool
}

// HealthCheck implements AWSHealthAPI for EC2HealthClient
func (c *EC2HealthClient) HealthCheck(ctx context.Context) error {
	if !c.initialized {
		if err := c.Validate(); err != nil {
			return err
		}
	}
	_, err := c.EC2.DescribeAccountAttributes(ctx, &ec2.DescribeAccountAttributesInput{
		AttributeNames: []types.AccountAttributeName{types.AccountAttributeNameSupportedPlatforms},
	})
	return err
}

// Validate initializes EC2HealthClient and checks if EC2 client is non-nil
func (c *EC2HealthClient) Validate() error {
	if c.EC2 == nil {
		c.initialized = false
		return fmt.Errorf("EC2 client is nil")
	}
	c.initialized = true
	return nil
}
