package utils

import (
	"errors"
	"testing"
)

func TestBuildKeyValue(t *testing.T) {
	tests := []struct {
		name      string
		key       string
		value     string
		separator string
		expected  string
	}{
		{
			name:      "basic key-value",
			key:       "Name",
			value:     "test",
			separator: "=",
			expected:  "Name=test",
		},
		{
			name:      "namespace key-value",
			key:       "CostCenter",
			value:     "1234",
			separator: ":",
			expected:  "CostCenter:1234",
		},
		{
			name:      "empty value",
			key:       "Key",
			value:     "",
			separator: "=",
			expected:  "Key=",
		},
		{
			name:      "special characters",
			key:       "Tag.Name",
			value:     "value@domain",
			separator: "=",
			expected:  "Tag.Name=value@domain",
		},
		{
			name:      "long strings",
			key:       "very-long-key-name-that-exceeds-normal-length",
			value:     "very-long-value-that-also-exceeds-normal-length-for-testing-purposes",
			separator: ":",
			expected:  "very-long-key-name-that-exceeds-normal-length:very-long-value-that-also-exceeds-normal-length-for-testing-purposes",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := BuildKeyValue(tt.key, tt.value, tt.separator)
			if result != tt.expected {
				t.Errorf("BuildKeyValue(%q, %q, %q) = %q; want %q", tt.key, tt.value, tt.separator, result, tt.expected)
			}
		})
	}
}

func TestBuildCommaSeparatedList(t *testing.T) {
	tests := []struct {
		name     string
		parts    []string
		expected string
	}{
		{
			name:     "empty list",
			parts:    []string{},
			expected: "",
		},
		{
			name:     "single item",
			parts:    []string{"item1"},
			expected: "item1",
		},
		{
			name:     "multiple items",
			parts:    []string{"item1", "item2", "item3"},
			expected: "item1,item2,item3",
		},
		{
			name:     "items with spaces",
			parts:    []string{"item 1", "item 2", "item 3"},
			expected: "item 1,item 2,item 3",
		},
		{
			name:     "special characters",
			parts:    []string{"tag-name", "tag.value", "tag:value"},
			expected: "tag-name,tag.value,tag:value",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := BuildCommaSeparatedList(tt.parts)
			if result != tt.expected {
				t.Errorf("BuildCommaSeparatedList(%v) = %q; want %q", tt.parts, result, tt.expected)
			}
		})
	}
}

// Benchmark tests to validate performance improvements
func BenchmarkBuildKeyValue(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = BuildKeyValue("key", "value", "=")
	}
}

func BenchmarkBuildCommaSeparatedList(b *testing.B) {
	parts := []string{"item1", "item2", "item3", "item4", "item5"}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = BuildCommaSeparatedList(parts)
	}
}

// TestIsRetryableError tests error classification for retry logic
func TestIsRetryableError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil error", nil, false},
		{"generic error - retryable", errors.New("connection timeout"), true},
		{"access denied - not retryable", errors.New("AccessDenied: not authorized"), false},
		{"unauthorized - not retryable", errors.New("Unauthorized request"), false},
		{"forbidden - not retryable", errors.New("Forbidden: access denied"), false},
		{"invalid parameter - not retryable", errors.New("InvalidParameterValue: bad input"), false},
		{"validation error - not retryable", errors.New("ValidationError: invalid format"), false},
		{"not found - not retryable", errors.New("NotFound: resource missing"), false},
		{"malformed - not retryable", errors.New("MalformedInput: bad request"), false},
		{"throttling - retryable", errors.New("Throttling: rate exceeded"), true},
		{"service unavailable - retryable", errors.New("ServiceUnavailable"), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsRetryableError(tt.err); got != tt.want {
				t.Errorf("IsRetryableError() = %v, want %v", got, tt.want)
			}
		})
	}
}
