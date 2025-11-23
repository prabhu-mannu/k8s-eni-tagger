package controller

import (
	"context"
	"fmt"

	"k8s-eni-tagger/pkg/metrics"

	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// Reconcile is the main reconciliation loop for the ENI tagger controller.
// It processes pod events and ensures that the pod's ENI has the correct tags applied.
//
// The reconciliation flow:
//  1. Validate the pod (skip hostNetwork pods, pods without IPs, handle deletions)
//  2. Parse and compare current desired tags with last applied tags
//  3. Check if already in sync (no changes needed)
//  4. Look up the ENI associated with the pod's IP
//  5. Apply subnet filtering if configured
//  6. Check for hash conflicts (optimistic locking)
//  7. Apply tag changes (removals first, then additions)
//  8. Update pod annotations with the new state
//  9. Update pod status condition
//
// The function uses optimistic locking via hash tags to prevent tag thrashing when
// multiple pods share the same ENI. It also supports dry-run mode and subnet filtering.
func (r *PodReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	metrics.ActiveWorkers.Inc()
	defer metrics.ActiveWorkers.Dec()

	logger := log.FromContext(ctx)

	var pod corev1.Pod
	if err := r.Get(ctx, req.NamespacedName, &pod); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Validate pod and handle early exits
	shouldContinue, result, err := r.validatePodForReconciliation(ctx, &pod, req)
	if !shouldContinue {
		return result, err
	}

	// Get annotation key
	key := r.AnnotationKey
	if key == "" {
		key = AnnotationKey
	}

	annotationValue := pod.Annotations[key]
	lastAppliedValue := pod.Annotations[LastAppliedAnnotationKey]
	lastAppliedHash := pod.Annotations[LastAppliedHashKey]

	// Parse and compare tags
	currentTags, _, diff, err := parseAndCompareTags(ctx, &pod, annotationValue, lastAppliedValue)
	if err != nil {
		logger.Error(err, "Failed to parse tags from annotation", "annotation", annotationValue)
		r.updateStatus(ctx, &pod, corev1.ConditionFalse, "TagParsingFailed", err.Error())
		r.Recorder.Event(&pod, corev1.EventTypeWarning, "TagParsingFailed", err.Error())
		return ctrl.Result{}, nil
	}

	// Calculate desired hash
	desiredHash := computeHash(currentTags)

	// Check if already in sync
	if len(diff.toAdd) == 0 && len(diff.toRemove) == 0 && lastAppliedHash == desiredHash {
		if !isConditionTrue(pod.Status.Conditions, ConditionTypeEniTagged) {
			r.updateStatus(ctx, &pod, corev1.ConditionTrue, "Synced", "ENI tags are up to date")
		}
		return ctrl.Result{}, nil
	}

	logger.Info("Reconciling tags", "pod", req.NamespacedName, "ip", pod.Status.PodIP, "adding", diff.toAdd, "removing", diff.toRemove, "dryRun", r.DryRun)

	// Find ENI
	eniInfo, err := r.AWSClient.GetENIInfoByIP(ctx, pod.Status.PodIP)
	if err != nil {
		logger.Error(err, "Failed to get ENI info for IP", "ip", pod.Status.PodIP)
		r.updateStatus(ctx, &pod, corev1.ConditionFalse, "ENILookupFailed", err.Error())
		r.Recorder.Event(&pod, corev1.EventTypeWarning, "ENILookupFailed", err.Error())
		return ctrl.Result{}, err
	}

	// Check subnet filter
	allowed, err := r.checkSubnetFilter(ctx, &pod, eniInfo)
	if err != nil || !allowed {
		return ctrl.Result{}, err
	}

	// Check for hash conflicts
	if checkHashConflict(eniInfo, desiredHash, lastAppliedHash, r.AllowSharedENITagging) {
		eniHash := eniInfo.Tags[HashTagKey]
		logger.Info("Skipping ENI due to hash mismatch (conflict)", "eniID", eniInfo.ID, "eniHash", eniHash, "myLastHash", lastAppliedHash)
		r.updateStatus(ctx, &pod, corev1.ConditionFalse, "Conflict", fmt.Sprintf("ENI modified by another entity (hash mismatch). ENI Hash: %s", eniHash))
		r.Recorder.Event(&pod, corev1.EventTypeWarning, "Conflict", "Skipping ENI due to hash mismatch (conflict)")
		return ctrl.Result{}, nil
	}

	// Add hash to tags
	diff.toAdd[HashTagKey] = desiredHash

	// Apply tag changes
	if result, err := r.applyTagChanges(ctx, &pod, eniInfo.ID, diff.toAdd, diff.toRemove); err != nil || result.Requeue {
		return result, err
	}

	// Update pod annotations
	if err := updatePodAnnotations(ctx, r, &pod, currentTags, desiredHash); err != nil {
		logger.Error(err, "Failed to update Pod annotation")
		return ctrl.Result{}, err
	}

	// Update status to success
	if err := r.updateStatus(ctx, &pod, corev1.ConditionTrue, "Synced", "Successfully applied tags to ENI "+eniInfo.ID); err != nil {
		logger.Error(err, "Failed to update Pod status")
	}

	if !r.DryRun {
		r.Recorder.Eventf(&pod, corev1.EventTypeNormal, "Synced", "Successfully applied tags to ENI %s", eniInfo.ID)
	}

	logger.Info("Successfully reconciled tags", "eniID", eniInfo.ID)
	return ctrl.Result{}, nil
}
