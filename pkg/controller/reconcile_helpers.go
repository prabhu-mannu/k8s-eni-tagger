package controller

import (
	"context"
	"encoding/json"

	"k8s-eni-tagger/pkg/aws"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// tagDiff represents the difference between current and last applied tags.
// It contains the tags that need to be added and the tag keys that need to be removed.
type tagDiff struct {
	// toAdd contains tags that need to be added or updated on the ENI
	toAdd map[string]string

	// toRemove contains tag keys that need to be removed from the ENI
	toRemove []string
}

// parseAndCompareTags parses and compares the current desired tags with the last applied tags.
// It returns:
//   - currentTags: the parsed desired tags from the annotation
//   - lastAppliedTags: the parsed last applied tags from the pod annotation
//   - diff: a tagDiff containing tags to add/update and tag keys to remove
//   - error: any parsing error
//
// The function calculates the minimal set of changes needed to bring the ENI tags
// in sync with the desired state.
func parseAndCompareTags(ctx context.Context, pod *corev1.Pod, annotationValue, lastAppliedValue string) (map[string]string, map[string]string, *tagDiff, error) {
	logger := log.FromContext(ctx)

	// Parse current tags
	currentTags, err := parseTags(annotationValue)
	if err != nil {
		return nil, nil, nil, err
	}

	// Parse last applied tags
	lastAppliedTags := make(map[string]string)
	if lastAppliedValue != "" {
		if err := json.Unmarshal([]byte(lastAppliedValue), &lastAppliedTags); err != nil {
			logger.Error(err, "Failed to parse last applied tags, treating as empty", "value", lastAppliedValue)
			lastAppliedTags = make(map[string]string)
		}
	}

	// Calculate Diff
	diff := &tagDiff{
		toAdd:    make(map[string]string),
		toRemove: []string{},
	}

	for k, v := range currentTags {
		if lastVal, ok := lastAppliedTags[k]; !ok || lastVal != v {
			diff.toAdd[k] = v
		}
	}

	for k := range lastAppliedTags {
		if _, ok := currentTags[k]; !ok {
			diff.toRemove = append(diff.toRemove, k)
		}
	}

	return currentTags, lastAppliedTags, diff, nil
}

// checkHashConflict checks if there's a hash conflict indicating another controller modified the ENI.
// It implements the following decision matrix:
//  1. ENI Hash is Empty -> Safe to claim (no conflict)
//  2. ENI Hash == Desired Hash -> Already synced (no conflict)
//  3. ENI Hash == Last Applied Hash -> We own this hash, safe to update (no conflict)
//  4. ENI Hash != Last Applied Hash -> Conflict! Another entity modified the ENI
//
// If allowSharedENITagging is true, conflicts are ignored (dangerous mode).
// Returns true if there's a conflict, false otherwise.
func checkHashConflict(eniInfo *aws.ENIInfo, desiredHash, lastAppliedHash string, allowSharedENITagging bool) bool {
	eniHash := eniInfo.Tags[HashTagKey]

	// Decision Matrix:
	// 1. ENI Hash is Empty -> Safe to claim.
	// 2. ENI Hash == Desired Hash -> Already synced.
	// 3. ENI Hash == Last Applied Hash -> I own this hash. Safe to update.
	// 4. ENI Hash != Last Applied Hash -> Conflict!

	if eniHash != "" && eniHash != desiredHash {
		if eniHash != lastAppliedHash {
			// Conflict!
			// Exception: If allowSharedENITagging is true, we ignore conflict (Dangerous mode)
			if !allowSharedENITagging {
				return true
			}
		}
	}

	return false
}
