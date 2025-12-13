package config

import (
	"strconv"
	"strings"
)

// normalizeBindAddress ensures the controller-runtime bind addresses are valid:
// - "0" stays "0" (disabled)
// - bare ports like "8081" become ":8081"
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
	if _, err := strconv.Atoi(v); err == nil {
		return ":" + v
	}
	return v
}
