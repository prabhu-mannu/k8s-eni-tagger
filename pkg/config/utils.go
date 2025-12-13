package config

import (
	"strconv"
	"strings"
)

// normalizeBindAddress ensures the controller-runtime bind addresses are valid:
// - "0" stays "0" (disabled)
// - bare ports like "8081" become "0.0.0.0:8081"
// - values containing ":" are returned unchanged (covers host:port, IPv6, etc.)
// - empty strings stay empty
func normalizeBindAddress(value string) string {
	v := strings.TrimSpace(value)
	if v == "" || v == "0" {
		return v
	}
	if strings.Contains(v, ":") {
		return v
	}
	port, err := strconv.Atoi(v)
	if err == nil && port >= 1 && port <= 65535 {
		return "0.0.0.0:" + v
	}
	return v
}
