package controller

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// updateStatus updates the pod's ENI tagging condition status.
// It creates or updates a pod condition of type ConditionTypeEniTagged with the given
// status, reason, and message. The condition's LastTransitionTime is set to the current time.
func (r *PodReconciler) updateStatus(ctx context.Context, pod *corev1.Pod, status corev1.ConditionStatus, reason, message string) error {
	// Create a patch for the status
	patch := client.MergeFrom(pod.DeepCopy())

	// Helper to find and update condition
	found := false
	for i, c := range pod.Status.Conditions {
		if c.Type == corev1.PodConditionType(ConditionTypeEniTagged) {
			pod.Status.Conditions[i].Status = status
			pod.Status.Conditions[i].Reason = reason
			pod.Status.Conditions[i].Message = message
			pod.Status.Conditions[i].LastTransitionTime = metav1.Now()
			found = true
			break
		}
	}

	if !found {
		pod.Status.Conditions = append(pod.Status.Conditions, corev1.PodCondition{
			Type:               corev1.PodConditionType(ConditionTypeEniTagged),
			Status:             status,
			Reason:             reason,
			Message:            message,
			LastTransitionTime: metav1.Now(),
		})
	}

	return r.Status().Patch(ctx, pod, patch)
}

// isConditionTrue checks if a pod condition of the given type exists and has status True.
// Returns false if the condition doesn't exist or has a different status.
func isConditionTrue(conditions []corev1.PodCondition, conditionType string) bool {
	for _, c := range conditions {
		if c.Type == corev1.PodConditionType(conditionType) {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}
