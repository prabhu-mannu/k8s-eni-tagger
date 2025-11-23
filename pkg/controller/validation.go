package controller

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// validatePodForReconciliation performs initial validation checks on a pod before reconciliation.
// It returns (shouldContinue, result, error) where:
//   - shouldContinue: true if reconciliation should proceed, false if it should exit early
//   - result: the ctrl.Result to return if shouldContinue is false
//   - error: any error that occurred during validation
//
// The function handles:
//   - Skipping pods with hostNetwork=true (they don't use ENIs)
//   - Skipping pods without an IP address (not ready for tagging)
//   - Delegating to handlePodDeletion for pods being deleted
//   - Adding the finalizer if not already present
func (r *PodReconciler) validatePodForReconciliation(ctx context.Context, pod *corev1.Pod, req ctrl.Request) (bool, ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Skip pods using host network
	if pod.Spec.HostNetwork {
		logger.V(1).Info("Skipping pod with hostNetwork=true", "pod", req.NamespacedName)
		return false, ctrl.Result{}, nil
	}

	// Skip if pod is in pending state without IP
	if pod.Status.PodIP == "" {
		logger.V(1).Info("Pod has no IP yet, skipping", "pod", req.NamespacedName, "phase", pod.Status.Phase)
		return false, ctrl.Result{}, nil
	}

	// Handle pod deletion
	if !pod.DeletionTimestamp.IsZero() {
		result, err := r.handlePodDeletion(ctx, pod)
		return false, result, err
	}

	// Add finalizer if not present
	if !controllerutil.ContainsFinalizer(pod, finalizerName) {
		controllerutil.AddFinalizer(pod, finalizerName)
		if err := r.Update(ctx, pod); err != nil {
			return false, ctrl.Result{}, err
		}
	}

	return true, ctrl.Result{}, nil
}
