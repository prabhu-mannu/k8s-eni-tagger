package controller

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidateTags(t *testing.T) {
	tests := []struct {
		name        string
		annotation  string
		expectError bool
	}{
		{
			name:        "valid tags",
			annotation:  `{"env":"prod","team":"platform"}`,
			expectError: false,
		},
		{
			name:        "valid tags with spaces and special chars",
			annotation:  `{"Cost Center":"US East 1","Team/Env":"Platform.Dev","key_with=sign":"value@domain"}`,
			expectError: false,
		},
		{
			name:        "valid comma-separated format",
			annotation:  `CostCenter=1234,Team=Platform,Env=Dev`,
			expectError: false,
		},
		{
			name:        "valid comma-separated with spaces",
			annotation:  `CostCenter = 1234, Team = Platform, Env = Dev`,
			expectError: false,
		},
		{
			name:        "empty tags",
			annotation:  `{}`,
			expectError: true,
		},
		{
			name:        "invalid JSON",
			annotation:  `{invalid}`,
			expectError: true,
		},
		{
			name:        "comma-separated format with missing value",
			annotation:  `CostCenter=1234,Team=,Env=Dev`,
			expectError: false, // Empty values are allowed
		},
		{
			name:        "comma-separated format with invalid syntax",
			annotation:  `CostCenter:1234,Team=Platform`,
			expectError: true,
		},
		{
			name:        "reserved prefix aws:",
			annotation:  `{"aws:Name":"test"}`,
			expectError: true,
		},
		{
			name:        "reserved prefix kubernetes.io/cluster/",
			annotation:  `{"kubernetes.io/cluster/test":"owned"}`,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateTags(tt.annotation)
			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestApplyNamespace(t *testing.T) {
	tests := []struct {
		name      string
		tags      map[string]string
		namespace string
		expected  map[string]string
		expectErr bool
	}{
		{
			name:      "no namespace",
			tags:      map[string]string{"CostCenter": "1234", "Team": "Platform"},
			namespace: "",
			expected:  map[string]string{"CostCenter": "1234", "Team": "Platform"},
			expectErr: false,
		},
		{
			name:      "with namespace",
			tags:      map[string]string{"CostCenter": "1234", "Team": "Platform"},
			namespace: "acme-corp",
			expected:  map[string]string{"acme-corp:CostCenter": "1234", "acme-corp:Team": "Platform"},
			expectErr: false,
		},
		{
			name:      "empty tags with namespace",
			tags:      map[string]string{},
			namespace: "acme-corp",
			expected:  map[string]string{},
			expectErr: false,
		},
		{
			name:      "namespace with special characters",
			tags:      map[string]string{"Env": "prod"},
			namespace: "example-inc",
			expected:  map[string]string{"example-inc:Env": "prod"},
			expectErr: false,
		},
		{
			name:      "key too long after namespacing",
			tags:      map[string]string{strings.Repeat("a", 120): "value"},
			namespace: "long-namespace",
			expected:  nil,
			expectErr: true,
		},
		{
			name:      "namespace at max length (63 chars)",
			tags:      map[string]string{"Key": "value"},
			namespace: strings.Repeat("a", 63),
			expected:  map[string]string{strings.Repeat("a", 63) + ":Key": "value"},
			expectErr: false,
		},
		{
			name:      "key exactly at limit with namespace",
			tags:      map[string]string{strings.Repeat("a", 63): "value"},
			namespace: strings.Repeat("b", 63),
			expected:  map[string]string{strings.Repeat("b", 63) + ":" + strings.Repeat("a", 63): "value"},
			expectErr: false, // 63 + 1 + 63 = 127, exactly at limit
		},
		{
			name:      "key at limit without namespace",
			tags:      map[string]string{strings.Repeat("a", 127): "value"},
			namespace: "",
			expected:  map[string]string{strings.Repeat("a", 127): "value"},
			expectErr: false,
		},
		{
			name:      "namespace with valid special chars",
			tags:      map[string]string{"Test": "value"},
			namespace: "my-org.test_123",
			expected:  map[string]string{"my-org.test_123:Test": "value"},
			expectErr: false,
		},
		{
			name:      "key exceeds limit with namespace",
			tags:      map[string]string{strings.Repeat("a", 64): "value"},
			namespace: strings.Repeat("b", 63),
			expected:  nil,
			expectErr: true, // 63 + 1 + 64 = 128, exceeds 127
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := applyNamespace(tt.tags, tt.namespace)
			if tt.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}
