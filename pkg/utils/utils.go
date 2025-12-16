package utils

import (
	"errors"
	"strings"
)

// RetryableError is an interface for errors that can indicate whether they should be retried.
// Implement this interface on custom error types to control retry behavior.
type RetryableError interface {
	error
	IsRetryable() bool
}

// IsRetryableError checks if an error should be retried.
// Returns true if:
// - The error implements RetryableError and IsRetryable() returns true
// - The error is a transient error (timeout, temporary network issues)
// Returns false for permanent errors (auth failures, validation errors, not found).
func IsRetryableError(err error) bool {
	if err == nil {
		return false
	}

	// Check if error implements RetryableError interface
	var retryable RetryableError
	if errors.As(err, &retryable) {
		return retryable.IsRetryable()
	}

	// Check for common non-retryable error patterns
	errMsg := strings.ToLower(err.Error())
	nonRetryablePatterns := []string{
		"accessdenied",
		"unauthorized",
		"forbidden",
		"invalidparameter",
		"validationerror",
		"notfound",
		"malformed",
	}
	for _, pattern := range nonRetryablePatterns {
		if strings.Contains(errMsg, pattern) {
			return false
		}
	}

	// Default to retryable for unknown errors (transient failures are more common)
	return true
}

// BuildKeyValue builds a key-value string with a separator efficiently.
// This utility function uses strings.Builder to minimize memory allocations.
func BuildKeyValue(key, value, separator string) string {
	var builder strings.Builder
	builder.Grow(len(key) + len(separator) + len(value))
	builder.WriteString(key)
	builder.WriteString(separator)
	builder.WriteString(value)
	return builder.String()
}

// BuildCommaSeparatedList builds a comma-separated string from a slice.
// More efficient than strings.Join for small to medium lists.
func BuildCommaSeparatedList(parts []string) string {
	if len(parts) == 0 {
		return ""
	}

	var builder strings.Builder
	totalLen := 0
	for _, part := range parts {
		totalLen += len(part)
	}
	// Add space for commas
	totalLen += len(parts) - 1
	builder.Grow(totalLen)

	for i, part := range parts {
		if i > 0 {
			builder.WriteString(",")
		}
		builder.WriteString(part)
	}
	return builder.String()
}
