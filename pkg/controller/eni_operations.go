package controller

import (
	"context"
	"fmt"

	"k8s-eni-tagger/pkg/aws"
	"k8s-eni-tagger/pkg/utils"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// retryUntagENI retries untag operations with exponential backoff and context cancellation support
func (r *PodReconciler) retryUntagENI(ctx context.Context, eniID string, tags []string) error {
	if r.AWSClient == nil {
		return fmt.Errorf("AWS client is not initialized")
	}
	return retryWithBackoff(ctx, maxUntagRetries, initialRetryBackoff, retryBackoffMultiplier, func() error {
		return r.AWSClient.UntagENI(ctx, eniID, tags)
	})
}

// getENIInfo retrieves ENI information for a given IP address.
// Uses cache if available, otherwise queries AWS API.
func (r *PodReconciler) getENIInfo(ctx context.Context, ip string) (*aws.ENIInfo, error) {
	if r.ENICache != nil {
		eniInfo, err := r.ENICache.GetENIInfoByIP(ctx, ip)
		if err != nil {
			return nil, fmt.Errorf("failed to get ENI info from cache for IP %s: %w", ip, err)
		}
		return eniInfo, nil
	}
	eniInfo, err := r.AWSClient.GetENIInfoByIP(ctx, ip)
	if err != nil {
		return nil, fmt.Errorf("failed to get ENI info from AWS for IP %s: %w", ip, err)
	}
	return eniInfo, nil
}

// validateENI performs validation checks on the ENI.
// It checks:
// - Subnet ID filtering (if configured)
// - Shared ENI detection (if AllowSharedENITagging is false)
func (r *PodReconciler) validateENI(ctx context.Context, eniInfo *aws.ENIInfo) error {
	if eniInfo == nil {
		return fmt.Errorf("ENI info is nil")
	}

	logger := log.FromContext(ctx)

	// Check subnet filtering
	if len(r.SubnetIDs) > 0 {
		allowed := false
		for _, subnet := range r.SubnetIDs {
			if eniInfo.SubnetID == subnet {
				allowed = true
				break
			}
		}
		if !allowed {
			subnetList := utils.BuildCommaSeparatedList(r.SubnetIDs)
			return fmt.Errorf("ENI %s subnet %s is not in allowed subnet list [%s]", eniInfo.ID, eniInfo.SubnetID, subnetList)
		}
	}

	// Check if ENI is shared
	if eniInfo.IsShared && !r.AllowSharedENITagging {
		logger.Info("Skipping shared ENI (use --allow-shared-eni-tagging to override)",
			"eniID", eniInfo.ID,
			"interfaceType", eniInfo.InterfaceType,
			"description", eniInfo.Description)
		return fmt.Errorf("ENI %s is shared (multiple IPs), tagging would affect other pods (use --allow-shared-eni-tagging to override)", eniInfo.ID)
	}

	return nil
}

// applyENITags applies tags to the ENI based on the pod annotation.
// It calculates the diff between current and desired state and applies only the necessary changes.
func (r *PodReconciler) applyENITags(ctx context.Context, pod *corev1.Pod, eniInfo *aws.ENIInfo, annotationValue string) error {
	logger := log.FromContext(ctx)

	// Get last applied tags
	lastAppliedValue := pod.Annotations[LastAppliedAnnotationKey]
	lastAppliedHash := pod.Annotations[LastAppliedHashKey]

	// Determine effective namespace for tag namespacing
	effectiveNamespace := ""
	if r.TagNamespace == "enable" {
		effectiveNamespace = pod.Namespace
	}

	// Parse and compare tags
	currentTags, _, diff, err := r.parseAndCompareTags(ctx, pod, annotationValue, lastAppliedValue)
	if err != nil {
		return fmt.Errorf("failed to parse and compare tags for pod %s: %w", pod.Name, err)
	}

	// Calculate desired hash
	desiredHash := computeHash(currentTags)

	// Check for hash conflicts
	if checkHashConflict(eniInfo, desiredHash, lastAppliedHash, r.AllowSharedENITagging) {
		eniHash := eniInfo.Tags[HashTagKey]
		return fmt.Errorf("hash conflict detected on ENI %s: current hash=%s, our last hash=%s (another controller may be managing this ENI)", eniInfo.ID, eniHash, lastAppliedHash)
	}

	eniHash := ""
	if eniInfo != nil && eniInfo.Tags != nil {
		eniHash = eniInfo.Tags[HashTagKey]
	}

	// If there are no desired tags, remove the hash tag too so the ENI can be claimed later.
	// Without this, a stale eni-tagger.io/hash can block future reconciles via checkHashConflict.
	if len(currentTags) == 0 && eniHash != "" {
		diff.toRemove = append(diff.toRemove, HashTagKey)
	}

	hashInSync := (eniHash == desiredHash)

	// If already synced, nothing to do.
	// When managing tags, also require the ENI hash to match to avoid leaving it stale/missing.
	if desiredHash == lastAppliedHash && len(diff.toAdd) == 0 && len(diff.toRemove) == 0 && (len(currentTags) == 0 || hashInSync) {
		logger.Info("Tags already in sync", "eniID", eniInfo.ID)
		if err := r.updateStatus(ctx, pod, corev1.ConditionTrue, "Synced", fmt.Sprintf("ENI %s tags are up to date", eniInfo.ID)); err != nil {
			return err
		}
		return nil
	}

	// Apply changes
	if r.DryRun {
		logger.Info("DRY RUN: Would apply tags", "eniID", eniInfo.ID, "toAdd", diff.toAdd, "toRemove", diff.toRemove)
	} else {
		// Only write the hash tag when we are actively managing tags.
		// If the hash is missing/outdated (even when there are no other diffs), ensure it is updated.
		tagsWithHash := make(map[string]string, len(diff.toAdd)+1)
		for k, v := range diff.toAdd {
			tagsWithHash[k] = v
		}
		if len(currentTags) > 0 && !hashInSync {
			tagsWithHash[HashTagKey] = desiredHash
		}

		// Apply tag changes
		if len(tagsWithHash) > 0 {
			if err := r.AWSClient.TagENI(ctx, eniInfo.ID, tagsWithHash); err != nil {
				return fmt.Errorf("failed to tag ENI %s with %d tags: %w", eniInfo.ID, len(tagsWithHash), err)
			}
		}

		if len(diff.toRemove) > 0 {
			if err := r.retryUntagENI(ctx, eniInfo.ID, diff.toRemove); err != nil {
				return fmt.Errorf("failed to untag ENI %s after %d attempts (removed %d tags): %w", eniInfo.ID, maxUntagRetries, len(diff.toRemove), err)
			}
		}

		logger.Info("Applied tags to ENI", "eniID", eniInfo.ID, "added", len(tagsWithHash), "removed", len(diff.toRemove))
		r.Recorder.Event(pod, corev1.EventTypeNormal, "TagsApplied", fmt.Sprintf("Applied %d tags to ENI %s", len(currentTags), eniInfo.ID))
	}

	// Update pod annotations including the effective namespace for orphaned tag cleanup
	if err := updatePodAnnotations(ctx, r, pod, currentTags, desiredHash, effectiveNamespace); err != nil {
		return fmt.Errorf("failed to update pod %s annotations after successful tagging: %w", pod.Name, err)
	}

	// Update status
	if err := r.updateStatus(ctx, pod, corev1.ConditionTrue, "Synced", fmt.Sprintf("Successfully tagged ENI %s", eniInfo.ID)); err != nil {
		return err
	}

	return nil
}
