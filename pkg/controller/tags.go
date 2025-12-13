package controller

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// parseTags parses tag annotations into a map of key-value pairs.
// It supports two formats for better UX:
//  1. JSON format (recommended): {"CostCenter":"1234","Team":"Platform"}
//  2. Comma-separated format: CostCenter=1234,Team=Platform
//
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

	// Try JSON format first (most common for structured data)
	if err := json.Unmarshal([]byte(tagStr), &tags); err == nil {
		// JSON parse succeeded
		return validateParsedTags(tags)
	}

	// Fallback to comma-separated format for better UX
	tags = make(map[string]string)
	pairs := strings.Split(tagStr, ",")
	for _, pair := range pairs {
		kv := strings.SplitN(strings.TrimSpace(pair), "=", 2)
		if len(kv) != 2 {
			return nil, fmt.Errorf("invalid tag format: %q (expected JSON or key=value,key=value)", pair)
		}
		key := strings.TrimSpace(kv[0])
		value := strings.TrimSpace(kv[1])
		if key == "" {
			return nil, fmt.Errorf("empty tag key in: %q", pair)
		}
		tags[key] = value
		tags[key] = value
	}

	return validateParsedTags(tags)
}

// validateParsedTags validates a map of tags against AWS constraints.
// This is extracted from parseTags to allow reuse for both JSON and comma-separated formats.
func validateParsedTags(tags map[string]string) (map[string]string, error) {
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

// applyNamespace applies a namespace prefix to all tag keys if namespace is configured.
// For example, with namespace "acme-corp", the tag "CostCenter=1234" becomes "acme-corp:CostCenter=1234".
// This is useful for enterprise multi-tenant scenarios to prevent tag key conflicts.
// Returns the original tags if namespace is empty.
func applyNamespace(tags map[string]string, namespace string) map[string]string {
	if namespace == "" {
		return tags
	}

	namespaced := make(map[string]string, len(tags))
	for key, value := range tags {
		namespacedKey := namespace + ":" + key
		namespaced[namespacedKey] = value
	}
	return namespaced
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
