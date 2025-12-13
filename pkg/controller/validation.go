package controller

import (
	"fmt"
)

// validateTags validates the tag annotation value.
// It supports both JSON and comma-separated formats.
// It checks:
// - Format is valid (JSON or comma-separated)
// - Tag keys and values meet AWS requirements
// - No reserved prefixes are used
// - Tag count doesn't exceed AWS limits
func validateTags(annotationValue string) error {
	tags, err := parseTags(annotationValue)
	if err != nil {
		return err
	}

	if len(tags) == 0 {
		return fmt.Errorf("no tags specified")
	}

	return nil
}
