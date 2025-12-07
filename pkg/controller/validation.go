package controller

import (
	"encoding/json"
	"fmt"
	"strings"
)

// validateTags validates the tag annotation value.
// It checks:
// - JSON format is valid
// - Tag keys and values meet AWS requirements
// - No reserved prefixes are used
// - Tag count doesn't exceed AWS limits
func validateTags(annotationValue string) error {
	var tags map[string]string
	if err := json.Unmarshal([]byte(annotationValue), &tags); err != nil {
		return fmt.Errorf("invalid JSON format: %w", err)
	}

	if len(tags) == 0 {
		return fmt.Errorf("no tags specified")
	}

	if len(tags) > MaxTagsPerENI {
		return fmt.Errorf("too many tags: %d (max %d)", len(tags), MaxTagsPerENI)
	}

	for key, value := range tags {
		// Check key length
		if len(key) == 0 || len(key) > MaxTagKeyLength {
			return fmt.Errorf("tag key length must be 1-%d characters: %q", MaxTagKeyLength, key)
		}

		// Check value length
		if len(value) > MaxTagValueLength {
			return fmt.Errorf("tag value length must be 0-%d characters: %q", MaxTagValueLength, value)
		}

		// Check for reserved prefixes
		for _, prefix := range reservedPrefixes {
			if strings.HasPrefix(key, prefix) {
				return fmt.Errorf("tag key cannot start with reserved prefix %q: %q", prefix, key)
			}
		}

		// Validate key pattern
		if !tagKeyPattern.MatchString(key) {
			return fmt.Errorf("invalid tag key format: %q", key)
		}

		// Validate value pattern
		if !tagValuePattern.MatchString(value) {
			return fmt.Errorf("invalid tag value format: %q", value)
		}
	}

	return nil
}
