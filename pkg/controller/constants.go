// Package controller implements the Kubernetes controller for ENI tagging.
// It watches Pod resources and applies tags to their associated ENIs based on annotations.
package controller

import (
	"context"
	"math/rand"
	"regexp"
	"time"

	"k8s-eni-tagger/pkg/utils"
)

const (
	// AnnotationKey is the default annotation key that the controller watches for tag specifications.
	// Pods with this annotation will have their ENIs tagged accordingly.
	AnnotationKey = "eni-tagger.io/tags"

	// LastAppliedAnnotationKey stores the last successfully applied tags as a JSON string.
	// This is used to calculate the diff between desired and current state.
	LastAppliedAnnotationKey = "eni-tagger.io/last-applied-tags"

	// LastAppliedNamespaceKey stores the namespace that was used when tags were last applied.
	// This is used to detect namespace changes and clean up orphaned namespaced tags.
	LastAppliedNamespaceKey = "eni-tagger.io/last-applied-namespace"

	// finalizerName is the finalizer added to pods to ensure cleanup of ENI tags on deletion.
	finalizerName = "eni-tagger.io/finalizer"

	// ConditionTypeEniTagged is the pod condition type that indicates ENI tagging status.
	// The condition status will be True when tags are successfully applied.
	ConditionTypeEniTagged = "eni-tagger.io/tagged"

	// HashTagKey is the tag key used for optimistic locking to prevent tag thrashing.
	// The hash value represents the state of all managed tags on the ENI.
	HashTagKey = "eni-tagger.io/hash"

	// LastAppliedHashKey stores the last hash value that was successfully applied.
	// This is used to detect conflicts when multiple controllers manage the same ENI.
	LastAppliedHashKey = "eni-tagger.io/last-applied-hash"

	// MaxTagKeyLength is the maximum length for AWS tag keys (127 characters).
	MaxTagKeyLength = 127

	// MaxTagValueLength is the maximum length for AWS tag values (255 characters).
	MaxTagValueLength = 255

	// MaxTagsPerENI is the maximum number of tags allowed per ENI by AWS (50 tags).
	MaxTagsPerENI = 50

	// MaxAnnotationValueLength is the maximum length for tag annotation values.
	// This limit prevents catastrophic backtracking in regex validation during tag parsing.
	// The value is generous enough for any reasonable use case (100+ tags with full-length values).
	MaxAnnotationValueLength = 10000

	// Retry configuration for untag operations
	// These constants define the exponential backoff retry strategy for AWS untag operations.

	// maxUntagRetries is the maximum number of retry attempts for untag operations.
	maxUntagRetries = 3

	// initialRetryBackoff is the initial backoff duration before the first retry.
	initialRetryBackoff = 100 * time.Millisecond

	// retryBackoffMultiplier is the factor by which the backoff duration increases after each retry.
	retryBackoffMultiplier = 2
)

// package-level test hook (can be replaced in tests)
var jitterFn = defaultJitter

// maxBackoffDuration is the maximum per-attempt wait; exposed as variable so tests can override it
var maxBackoffDuration = 30 * time.Second

func defaultJitter(d time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	// up to +50% jitter
	return time.Duration(rand.Float64() * float64(d) * 0.5)
}

// retryWithBackoff executes a function with exponential backoff retry logic.
// It retries up to maxRetries times with context-aware cancellation support.
// This implementation caps the per-attempt wait and the backoff growth to avoid unbounded (or effectively infinite) waits.
// Non-retryable errors (auth failures, validation errors) are returned immediately without retrying.
func retryWithBackoff(ctx context.Context, maxRetries int, initialBackoff time.Duration, backoffMultiplier int, operation func() error) error {
	// Input validation / sensible defaults
	if maxRetries <= 0 {
		maxRetries = 1
	}
	if initialBackoff <= 0 {
		initialBackoff = 100 * time.Millisecond
	}
	if backoffMultiplier < 1 {
		backoffMultiplier = 2
	}

	backoff := initialBackoff
	var lastErr error

	for attempt := 0; attempt < maxRetries; attempt++ {
		if err := operation(); err != nil {
			lastErr = err

			// Don't retry non-retryable errors (auth failures, validation errors, etc.)
			if !utils.IsRetryableError(err) {
				return err
			}

			if attempt == maxRetries-1 {
				break
			}

			// Respect cancellation before waiting
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}

			// Compute wait = backoff + jitter and cap it
			wait := backoff + jitterFn(backoff)
			if wait > maxBackoffDuration {
				wait = maxBackoffDuration
			}

			// Use a timer we can stop
			timer := time.NewTimer(wait)
			select {
			case <-timer.C:
				// proceed to next attempt
			case <-ctx.Done():
				if !timer.Stop() {
					<-timer.C // drain if fired
				}
				return ctx.Err()
			}

			// increase & cap backoff for next attempt (guard against overflow)
			mult := time.Duration(backoffMultiplier)
			if mult <= 0 {
				mult = 2
			}
			if backoff > maxBackoffDuration/mult {
				backoff = maxBackoffDuration
			} else {
				backoff *= mult
				if backoff > maxBackoffDuration {
					backoff = maxBackoffDuration
				}
			}
			continue
		}
		// success
		return nil
	}
	return lastErr
}

// Logging key constants for consistent structured logging
const (
	LogKeyPod           = "pod"
	LogKeyPodIP         = "podIP"
	LogKeyPodName       = "podName"
	LogKeyPodNamespace  = "podNamespace"
	LogKeyENIID         = "eniID"
	LogKeyENISubnet     = "eniSubnet"
	LogKeyTags          = "tags"
	LogKeyTagCount      = "tagCount"
	LogKeyAnnotation    = "annotation"
	LogKeyAnnotationKey = "annotationKey"
	LogKeyRequeueAfter  = "requeueAfter"
	LogKeyError         = "error"
	LogKeyDuration      = "duration"
	LogKeyOperation     = "operation"
)

var (
	// reservedPrefixes contains AWS reserved tag key prefixes that cannot be used.
	reservedPrefixes = []string{"aws:", "kubernetes.io/cluster/"}

	// tagKeyPattern is the regex pattern for valid AWS tag keys.
	// AWS allows alphanumeric characters, spaces, and the following special characters: . _ - : / = + @
	// Note: The hyphen (-) is placed at the end of the character class to be treated as a literal hyphen.
	tagKeyPattern = regexp.MustCompile(`^[a-zA-Z0-9 +\=._:/@\-]{1,127}$`)

	// tagValuePattern is the regex pattern for valid AWS tag values.
	// AWS allows alphanumeric characters, spaces, and the following special characters: . _ - : / = + @
	// Empty values are allowed (0-255 characters from the allowed character set).
	// Note: The hyphen (-) is placed at the end of the character class to be treated as a literal hyphen.
	tagValuePattern = regexp.MustCompile(`^[a-zA-Z0-9 +\=._:/@\-]{0,255}$`)
)
