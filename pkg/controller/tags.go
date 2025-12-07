package controller

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

// parseTags parses a comma-separated tag string into a map of key-value pairs.
// The input format is "key1=value1,key2=value2,...".
// It validates each tag against AWS constraints:
//   - Key length must not exceed MaxTagKeyLength (127 characters)
//   - Value length must not exceed MaxTagValueLength (255 characters)
//   - Keys cannot use reserved prefixes (aws:, kubernetes.io/cluster/)
//   - Keys and values must match AWS allowed character patterns
//   - Total number of tags must not exceed MaxTagsPerENI (50 tags)
//
// Returns an error if any validation fails or if the format is invalid.
func parseTags(tagStr string) (map[string]string, error) {
	tagStr = strings.TrimSpace(tagStr)
	if tagStr == "" {
		return make(map[string]string), nil
	}

	tags := make(map[string]string)
	parts := strings.Split(tagStr, ",")

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue // Skip empty parts from multiple commas
		}

		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			return nil, fmt.Errorf("invalid tag format (missing '='): %s", part)
		}

		key := strings.TrimSpace(kv[0])
		value := strings.TrimSpace(kv[1])

		if key == "" {
			return nil, fmt.Errorf("empty tag key in: %s", part)
		}

		// Validate key length
		if len(key) > MaxTagKeyLength {
			return nil, fmt.Errorf("tag key exceeds %d characters: %s", MaxTagKeyLength, key)
		}

		// Validate value length
		if len(value) > MaxTagValueLength {
			return nil, fmt.Errorf("tag value exceeds %d characters for key %s", MaxTagValueLength, key)
		}

		// Check for reserved prefixes
		for _, prefix := range reservedPrefixes {
			if strings.HasPrefix(key, prefix) {
				return nil, fmt.Errorf("tag key uses reserved prefix '%s': %s", prefix, key)
			}
		}

		// Validate characters
		if !tagKeyPattern.MatchString(key) {
			return nil, fmt.Errorf("tag key contains invalid characters: %s", key)
		}
		if value != "" && !tagValuePattern.MatchString(value) {
			return nil, fmt.Errorf("tag value contains invalid characters for key %s", key)
		}

		tags[key] = value
	}

	// Check total tag count
	if len(tags) > MaxTagsPerENI {
		return nil, fmt.Errorf("too many tags (%d), AWS limit is %d", len(tags), MaxTagsPerENI)
	}

	return tags, nil
}

// computeHash calculates a SHA-256 hash of the tag map for optimistic locking.
// The hash is computed deterministically by sorting keys alphabetically before hashing.
// This ensures the same set of tags always produces the same hash value.
// The hash is used to detect conflicts when multiple controllers manage the same ENI.
// The hash is truncated to 16 characters (64 bits) which is sufficient for collision detection.
func computeHash(tags map[string]string) string {
	keys := make([]string, 0, len(tags))
	for k := range tags {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	h := sha256.New()
	for _, k := range keys {
		h.Write([]byte(k))
		h.Write([]byte("="))
		h.Write([]byte(tags[k]))
		h.Write([]byte(","))
	}
	fullHash := hex.EncodeToString(h.Sum(nil))
	return fullHash[:16] // 64-bit entropy is sufficient for conflict detection
}
