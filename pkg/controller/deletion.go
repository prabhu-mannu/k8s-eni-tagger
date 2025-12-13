package controller

import (
	"context"
	"encoding/json"

	"k8s-eni-tagger/pkg/aws"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// cleanupTagsForPod performs tag cleanup for a pod during deletion.
// It removes tags from the ENI if the hash matches or shared tagging is allowed.
func (r *PodReconciler) cleanupTagsForPod(ctx context.Context, logger logr.Logger, eniInfo *aws.ENIInfo, lastAppliedTags map[string]string, lastAppliedHash string) {
	// Safety check for deletion
	// Only delete if we own the hash (or if hash is missing/empty?)
	// If hash on ENI matches our last applied hash, we own it.
	eniHash := eniInfo.Tags[HashTagKey]
	shouldDelete := false

	if eniHash == lastAppliedHash {
		shouldDelete = true
	} else if r.AllowSharedENITagging {
		shouldDelete = true
	}

	if !shouldDelete {
		logger.Info("Skipping cleanup: ENI hash mismatch", "eniID", eniInfo.ID, "eniHash", eniHash, "myHash", lastAppliedHash)
		return
	}

	tagKeys := make([]string, 0, len(lastAppliedTags))
	for k := range lastAppliedTags {
		tagKeys = append(tagKeys, k)
	}
	// Also remove the hash tag
	tagKeys = append(tagKeys, HashTagKey)

	if err := r.retryUntagENI(ctx, eniInfo.ID, tagKeys); err != nil {
		logger.Error(err, "Failed to cleanup tags, continuing with finalizer removal")
	} else {
		logger.Info("Cleaned up tags on pod deletion", "eniID", eniInfo.ID, "tags", tagKeys)
	}
}

// handlePodDeletion handles cleanup when a pod is being deleted.
// It removes tags from the ENI if the pod has last-applied-tags and the hash matches,
// ensuring we only clean up tags that we own. The finalizer is then removed to allow
// pod deletion to proceed.
//
// Hash-based safety check:
//   - If ENI hash matches our last applied hash, we own the tags and can safely remove them
//   - If AllowSharedENITagging is true, we skip the hash check (dangerous mode)
//   - If hash doesn't match and AllowSharedENITagging is false, we skip cleanup
//
// The function continues with finalizer removal even if tag cleanup fails to prevent
// pods from being stuck in terminating state.
func (r *PodReconciler) handlePodDeletion(ctx context.Context, pod *corev1.Pod) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	if !controllerutil.ContainsFinalizer(pod, finalizerName) {
		return ctrl.Result{}, nil
	}

	// Clean up tags if we have last-applied-tags
	lastAppliedValue := pod.Annotations[LastAppliedAnnotationKey]
	lastAppliedHash := pod.Annotations[LastAppliedHashKey]

	if lastAppliedValue != "" && pod.Status.PodIP != "" {
		var lastAppliedTags map[string]string
		if err := json.Unmarshal([]byte(lastAppliedValue), &lastAppliedTags); err == nil {
			if len(lastAppliedTags) > 0 {
				eniInfo, err := r.AWSClient.GetENIInfoByIP(ctx, pod.Status.PodIP)
				if err != nil {
					logger.Error(err, "Failed to get ENI for cleanup, continuing with finalizer removal")
				} else {
					r.cleanupTagsForPod(ctx, logger, eniInfo, lastAppliedTags, lastAppliedHash)
				}
			}
		}
	}

	// Remove finalizer
	controllerutil.RemoveFinalizer(pod, finalizerName)
	if err := r.Update(ctx, pod); err != nil {
		return ctrl.Result{}, err
	}

	// Invalidate cache entry for this pod's IP
	if r.ENICache != nil && pod.Status.PodIP != "" {
		r.ENICache.Invalidate(ctx, pod.Status.PodIP)
		logger.Info("Invalidated ENI cache entry", "ip", pod.Status.PodIP)
	}

	return ctrl.Result{}, nil
}
