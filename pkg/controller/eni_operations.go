package controller

import (
	"context"
	"fmt"

	"k8s-eni-tagger/pkg/aws"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// getENIInfo retrieves ENI information for a given IP address.
// Uses cache if available, otherwise queries AWS API.
func (r *PodReconciler) getENIInfo(ctx context.Context, ip string) (*aws.ENIInfo, error) {
	if r.ENICache != nil {
		return r.ENICache.GetENIInfoByIP(ctx, ip)
	}
	return r.AWSClient.GetENIInfoByIP(ctx, ip)
}

// validateENI performs validation checks on the ENI.
// It checks:
// - Subnet ID filtering (if configured)
// - Shared ENI detection (if AllowSharedENITagging is false)
func (r *PodReconciler) validateENI(ctx context.Context, eniInfo *aws.ENIInfo) error {
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
			return fmt.Errorf("ENI subnet %s is not in allowed list", eniInfo.SubnetID)
		}
	}

	// Check if ENI is shared
	if eniInfo.IsShared && !r.AllowSharedENITagging {
		logger.Info("Skipping shared ENI (use --allow-shared-eni-tagging to override)",
			"eniID", eniInfo.ID,
			"interfaceType", eniInfo.InterfaceType,
			"description", eniInfo.Description)
		return fmt.Errorf("ENI %s is shared (multiple IPs), tagging would affect other pods", eniInfo.ID)
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

	// Parse and compare tags
	currentTags, _, diff, err := r.parseAndCompareTags(ctx, pod, annotationValue, lastAppliedValue)
	if err != nil {
		return fmt.Errorf("failed to parse tags: %w", err)
	}

	// Calculate desired hash
	desiredHash := computeHash(currentTags)

	// Check for hash conflicts
	if checkHashConflict(eniInfo, desiredHash, lastAppliedHash, r.AllowSharedENITagging) {
		eniHash := eniInfo.Tags[HashTagKey]
		return fmt.Errorf("hash conflict detected: ENI hash=%s, our last hash=%s (another controller may be managing this ENI)", eniHash, lastAppliedHash)
	}

	// If already synced, nothing to do
	if desiredHash == lastAppliedHash && len(diff.toAdd) == 0 && len(diff.toRemove) == 0 {
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
		// Add hash to tags
		tagsWithHash := make(map[string]string)
		for k, v := range diff.toAdd {
			tagsWithHash[k] = v
		}
		tagsWithHash[HashTagKey] = desiredHash

		// Apply tag changes
		if len(tagsWithHash) > 0 {
			if err := r.AWSClient.TagENI(ctx, eniInfo.ID, tagsWithHash); err != nil {
				return fmt.Errorf("failed to tag ENI: %w", err)
			}
		}

		if len(diff.toRemove) > 0 {
			if err := r.AWSClient.UntagENI(ctx, eniInfo.ID, diff.toRemove); err != nil {
				return fmt.Errorf("failed to untag ENI: %w", err)
			}
		}

		logger.Info("Applied tags to ENI", "eniID", eniInfo.ID, "added", len(tagsWithHash), "removed", len(diff.toRemove))
		r.Recorder.Event(pod, corev1.EventTypeNormal, "TagsApplied", fmt.Sprintf("Applied %d tags to ENI %s", len(currentTags), eniInfo.ID))
	}

	// Update pod annotations
	if err := updatePodAnnotations(ctx, r, pod, currentTags, desiredHash); err != nil {
		return fmt.Errorf("failed to update pod annotations: %w", err)
	}

	// Update status
	if err := r.updateStatus(ctx, pod, corev1.ConditionTrue, "Synced", fmt.Sprintf("Successfully tagged ENI %s", eniInfo.ID)); err != nil {
		return err
	}

	return nil
}
