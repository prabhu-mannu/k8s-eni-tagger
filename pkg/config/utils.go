package config

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

// normalizeBindAddress ensures the controller-runtime bind addresses are valid:
// - "0" stays "0" (disabled)
// - bare ports like "8081" become "0.0.0.0:8081"
// - values containing ":" are returned unchanged (covers host:port, IPv6, etc.)
// - empty strings stay empty
func normalizeBindAddress(value string) (string, error) {
	v := strings.TrimSpace(value)
	if v == "" || v == "0" {
		return v, nil
	}
	if strings.Contains(v, ":") {
		return v, nil
	}
	port, err := strconv.Atoi(v)
	if err != nil {
		return "", fmt.Errorf("invalid port number '%s': %w", v, err)
	}
	if port < 1 || port > 65535 {
		return "", fmt.Errorf("port number %d out of valid range 1-65535", port)
	}
	// Use net.JoinHostPort for robust formatting (handles edge cases consistently)
	return net.JoinHostPort("0.0.0.0", v), nil
}
