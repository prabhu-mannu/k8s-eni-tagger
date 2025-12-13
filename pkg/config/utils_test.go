package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeBindAddress(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "empty",
			input: "",
			want:  "",
		},
		{
			name:  "whitespace only",
			input: "   \t  ",
			want:  "",
		},
		{
			name:  "zero disables",
			input: "0",
			want:  "0",
		},
		{
			name:  "bare port",
			input: "8081",
			want:  ":8081",
		},
		{
			name:  "bare port with spaces",
			input: " 8081  ",
			want:  ":8081",
		},
		{
			name:  "host and port",
			input: "127.0.0.1:8081",
			want:  "127.0.0.1:8081",
		},
		{
			name:  "ipv6 with port",
			input: "[::1]:9090",
			want:  "[::1]:9090",
		},
		{
			name:  "ipv6 without port",
			input: "fe80::1",
			want:  "fe80::1",
		},
		{
			name:  "invalid numeric",
			input: "8081abc",
			want:  "8081abc",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := normalizeBindAddress(tt.input)
			require.Equal(t, tt.want, got)
		})
	}
}
