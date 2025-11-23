package controller

import (
	"context"
	"fmt"
	"strings"

	"k8s-eni-tagger/pkg/aws"
	"k8s-eni-tagger/pkg/metrics"

	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// applyTagChanges applies tag additions and removals to the specified ENI.
// It handles both dry-run mode (logging only) and actual AWS API calls.
// The function:
//   - Removes tags first (if any), then adds/updates tags
//   - Emits Kubernetes events for each operation
//   - Updates metrics for success/failure
//   - Returns early on fatal errors like UnauthorizedOperation
//
// In dry-run mode, it only logs what would be done without making AWS API calls.
func (r *PodReconciler) applyTagChanges(ctx context.Context, pod *corev1.Pod, eniID string, tagsToAdd map[string]string, tagsToRemove []string) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Apply Removals
	if len(tagsToRemove) > 0 {
		if r.DryRun {
			logger.Info("Dry-run: skipping untagging", "eniID", eniID, "tags", tagsToRemove)
			r.Recorder.Eventf(pod, corev1.EventTypeNormal, "DryRunUntag", "Would remove tags from ENI %s: %v", eniID, tagsToRemove)
		} else {
			if err := r.AWSClient.UntagENI(ctx, eniID, tagsToRemove); err != nil {
				logger.Error(err, "Failed to untag ENI", "eniID", eniID)
				r.updateStatus(ctx, pod, corev1.ConditionFalse, "UntaggingFailed", err.Error())
				r.Recorder.Event(pod, corev1.EventTypeWarning, "UntaggingFailed", err.Error())
				metrics.TagOperationsTotal.WithLabelValues("untag", "error").Inc()

				// Check for fatal errors
				if strings.Contains(err.Error(), "UnauthorizedOperation") {
					return ctrl.Result{}, nil // Stop retrying
				}
				return ctrl.Result{}, err
			}
			metrics.TagOperationsTotal.WithLabelValues("untag", "success").Inc()
		}
	}

	// Apply Additions
	if len(tagsToAdd) > 0 {
		if r.DryRun {
			logger.Info("Dry-run: skipping tagging", "eniID", eniID, "tags", tagsToAdd)
			r.Recorder.Eventf(pod, corev1.EventTypeNormal, "DryRunTag", "Would add tags to ENI %s: %v", eniID, tagsToAdd)
		} else {
			if err := r.AWSClient.TagENI(ctx, eniID, tagsToAdd); err != nil {
				logger.Error(err, "Failed to tag ENI", "eniID", eniID)
				r.updateStatus(ctx, pod, corev1.ConditionFalse, "TaggingFailed", err.Error())
				r.Recorder.Event(pod, corev1.EventTypeWarning, "TaggingFailed", err.Error())
				metrics.TagOperationsTotal.WithLabelValues("tag", "error").Inc()

				// Check for fatal errors
				if strings.Contains(err.Error(), "UnauthorizedOperation") {
					return ctrl.Result{}, nil // Stop retrying
				}
				return ctrl.Result{}, err
			}
			metrics.TagOperationsTotal.WithLabelValues("tag", "success").Inc()
		}
	}

	return ctrl.Result{}, nil
}

// checkSubnetFilter checks if the ENI's subnet is in the allowed list.
// If SubnetIDs is configured and the ENI's subnet is not in the list,
// the function updates the pod status and returns false.
// If SubnetIDs is empty or the subnet is allowed, it returns true.
func (r *PodReconciler) checkSubnetFilter(ctx context.Context, pod *corev1.Pod, eniInfo *aws.ENIInfo) (bool, error) {
	logger := log.FromContext(ctx)

	if len(r.SubnetIDs) > 0 {
		allowed := false
		for _, id := range r.SubnetIDs {
			if id == eniInfo.SubnetID {
				allowed = true
				break
			}
		}
		if !allowed {
			logger.Info("Skipping ENI in excluded subnet", "eniID", eniInfo.ID, "subnetID", eniInfo.SubnetID)
			r.updateStatus(ctx, pod, corev1.ConditionFalse, "SubnetExcluded", fmt.Sprintf("ENI subnet %s is not in allowed list", eniInfo.SubnetID))
			return false, nil
		}
	}

	return true, nil
}
