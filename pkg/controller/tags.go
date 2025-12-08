package controller

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// parseTags parses a JSON tag string into a map of key-value pairs.
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

	var tags map[string]string
	if err := json.Unmarshal([]byte(tagStr), &tags); err != nil {
		return nil, fmt.Errorf("failed to parse tags: %w", err)
	}

	// Validate tags using same logic as validateTags
	// Note: We duplicate some logic here or we could export validateTags logic.
	// Since validateTags is in same package, we can just call it?
	// validateTags takes string input, not map.
	// Let's implement validation on the map here.

	if len(tags) > MaxTagsPerENI {
		return nil, fmt.Errorf("too many tags (%d), AWS limit is %d", len(tags), MaxTagsPerENI)
	}

	for key, value := range tags {
		// Key length
		if len(key) == 0 || len(key) > MaxTagKeyLength {
			return nil, fmt.Errorf("tag key length must be 1-%d characters: %q", MaxTagKeyLength, key)
		}

		// Value length
		if len(value) > MaxTagValueLength {
			return nil, fmt.Errorf("tag value length must be 0-%d characters: for key %q", MaxTagValueLength, key)
		}

		// Reserved prefixes
		for _, prefix := range reservedPrefixes {
			if strings.HasPrefix(key, prefix) {
				return nil, fmt.Errorf("tag key cannot start with reserved prefix %q: %q", prefix, key)
			}
		}

		// Key pattern
		if !tagKeyPattern.MatchString(key) {
			return nil, fmt.Errorf("invalid tag key format: %q", key)
		}

		// Value pattern
		if !tagValuePattern.MatchString(value) {
			return nil, fmt.Errorf("invalid tag value format: %q", value)
		}
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
