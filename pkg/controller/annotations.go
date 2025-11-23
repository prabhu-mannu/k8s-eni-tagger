package controller

import (
	"context"
	"encoding/json"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// updatePodAnnotations updates the pod's last-applied-tags and last-applied-hash annotations.
// These annotations track the state of tags that were successfully applied to the ENI,
// enabling the controller to calculate diffs on subsequent reconciliations.
// If currentTags is empty, the annotations are removed from the pod.
func updatePodAnnotations(ctx context.Context, r *PodReconciler, pod *corev1.Pod, currentTags map[string]string, desiredHash string) error {
	logger := log.FromContext(ctx)

	// Update State (Last Applied Annotation)
	newLastApplied, err := json.Marshal(currentTags)
	if err != nil {
		logger.Error(err, "Failed to marshal current tags")
		return err
	}

	// Patch Pod Annotation
	if pod.Annotations == nil {
		pod.Annotations = make(map[string]string)
	}
	if len(currentTags) == 0 {
		delete(pod.Annotations, LastAppliedAnnotationKey)
		delete(pod.Annotations, LastAppliedHashKey)
	} else {
		pod.Annotations[LastAppliedAnnotationKey] = string(newLastApplied)
		pod.Annotations[LastAppliedHashKey] = desiredHash
	}

	return r.Update(ctx, pod)
}
