package controller

import (
	"context"
	"encoding/json"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// updatePodAnnotations updates the pod's last-applied-tags and last-applied-hash annotations.
// These annotations track the state of tags that were successfully applied to the ENI,
// enabling the controller to calculate diffs on subsequent reconciliations.
// If currentTags is empty, the annotations are removed from the pod.
// Uses retry on conflict to handle concurrent updates.
func updatePodAnnotations(ctx context.Context, r *PodReconciler, pod *corev1.Pod, currentTags map[string]string, desiredHash string) error {
	logger := log.FromContext(ctx)

	newLastApplied, err := json.Marshal(currentTags)
	if err != nil {
		logger.Error(err, "Failed to marshal current tags")
		return err
	}

	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		// Re-fetch pod to get latest version
		currentPod := &corev1.Pod{}
		if err := r.Get(ctx, client.ObjectKeyFromObject(pod), currentPod); err != nil {
			return err
		}

		// Apply annotation updates
		if currentPod.Annotations == nil {
			currentPod.Annotations = make(map[string]string)
		}
		if len(currentTags) == 0 {
			delete(currentPod.Annotations, LastAppliedAnnotationKey)
			delete(currentPod.Annotations, LastAppliedHashKey)
		} else {
			currentPod.Annotations[LastAppliedAnnotationKey] = string(newLastApplied)
			currentPod.Annotations[LastAppliedHashKey] = desiredHash
		}

		return r.Update(ctx, currentPod)
	})
}
