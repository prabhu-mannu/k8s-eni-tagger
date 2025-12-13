package controller

import (
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
			name:        "valid tags with capital letters",
			annotation:  `{"CostCenter":"1234","Team":"Platform","Environment":"Production"}`,
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
