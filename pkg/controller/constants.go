// Package controller implements the Kubernetes controller for ENI tagging.
// It watches Pod resources and applies tags to their associated ENIs based on annotations.
package controller

import (
	"context"
	"regexp"
	"time"
)

const (
	// AnnotationKey is the default annotation key that the controller watches for tag specifications.
	// Pods with this annotation will have their ENIs tagged accordingly.
	AnnotationKey = "eni-tagger.io/tags"

	// LastAppliedAnnotationKey stores the last successfully applied tags as a JSON string.
	// This is used to calculate the diff between desired and current state.
	LastAppliedAnnotationKey = "eni-tagger.io/last-applied-tags"

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

	// Retry configuration for untag operations
	// These constants define the exponential backoff retry strategy for AWS untag operations.

	// maxUntagRetries is the maximum number of retry attempts for untag operations.
	maxUntagRetries = 3

	// initialRetryBackoff is the initial backoff duration before the first retry.
	initialRetryBackoff = 100 * time.Millisecond

	// retryBackoffMultiplier is the factor by which the backoff duration increases after each retry.
	retryBackoffMultiplier = 2
)

// retryWithBackoff executes a function with exponential backoff retry logic.
// It retries up to maxRetries times with context-aware cancellation support.
func retryWithBackoff(ctx context.Context, maxRetries int, initialBackoff time.Duration, backoffMultiplier int, operation func() error) error {
	var lastErr error
	backoff := initialBackoff
retryLoop:
	for i := 0; i < maxRetries; i++ {
		if err := operation(); err != nil {
			lastErr = err
			if i == maxRetries-1 {
				break
			}
			select {
			case <-time.After(backoff):
				// continue to next retry
			case <-ctx.Done():
				lastErr = ctx.Err()
				break retryLoop
			}
			backoff *= time.Duration(backoffMultiplier)
			continue
		}
		lastErr = nil
		break
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
	// AWS allows alphanumeric characters, spaces, and the following: ._-:/=+@
	tagKeyPattern = regexp.MustCompile(`^[a-zA-Z0-9+\\=._:\\/@\\s-]{1,127}$`)

	// tagValuePattern is the regex pattern for valid AWS tag values.
	// AWS allows alphanumeric characters, spaces, and the following: ._-:/=+@
	// Empty values are also allowed
	tagValuePattern = regexp.MustCompile(`^[a-zA-Z0-9+\\=._:\\/@\\s-]{0,255}$`)
)
