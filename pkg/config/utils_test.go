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
			want:  "0.0.0.0:8081",
		},
		{
			name:  "bare port with spaces",
			input: " 8081  ",
			want:  "0.0.0.0:8081",
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
		{
			name:  "port 1",
			input: "1",
			want:  "0.0.0.0:1",
		},
		{
			name:  "port 65535",
			input: "65535",
			want:  "0.0.0.0:65535",
		},
		{
			name:  "port 65536 invalid",
			input: "65536",
			want:  "65536",
		},
		{
			name:  "port 99999 invalid",
			input: "99999",
			want:  "99999",
		},
		{
			name:  "negative port",
			input: "-1",
			want:  "-1",
		},
		{
			name:  "localhost with port",
			input: "localhost:8080",
			want:  "localhost:8080",
		},
		{
			name:  "explicit 0.0.0.0 with port",
			input: "0.0.0.0:8080",
			want:  "0.0.0.0:8080",
		},
		{
			name:  "127.0.0.1 with port",
			input: "127.0.0.1:8080",
			want:  "127.0.0.1:8080",
		},
		{
			name:  "non-numeric string",
			input: "abc",
			want:  "abc",
		},
		{
			name:  "port too large",
			input: "999999",
			want:  "999999",
		},
		{
			name:  "port with leading zeros",
			input: "0001",
			want:  "0.0.0.0:0001",
		},
		{
			name:  "IPv4 with port",
			input: "1.2.3.4:8080",
			want:  "1.2.3.4:8080",
		},
		{
			name:  "IPv6 without port",
			input: "::1",
			want:  "::1",
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
