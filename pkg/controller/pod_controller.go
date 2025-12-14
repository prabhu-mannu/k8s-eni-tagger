package controller

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// Reconcile handles the reconciliation of a Pod resource.
// It manages ENI tagging based on pod annotations and handles cleanup on deletion.
func (r *PodReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues(LogKeyPod, req.NamespacedName)

	// Check per-pod rate limit (if enabled)
	if r.PodRateLimitQPS > 0 {
		now := time.Now()
		limiterInterface, loaded := r.PodRateLimiters.LoadOrStore(
			req.String(),
			func() interface{} {
				entry, err := NewRateLimiterEntry(r.PodRateLimitQPS, r.PodRateLimitBurst)
				if err != nil {
					logger.Error(err, "Failed to create rate limiter entry, skipping rate limiting for this pod", "pod", req.String())
					return nil // Don't store anything for failed entries
				}
				return entry
			}(),
		)

		// If we just stored a nil (creation failed), skip rate limiting
		if !loaded && limiterInterface == nil {
			logger.V(1).Info("Skipping rate limiting due to creation failure", "pod", req.String())
		} else {
			entry, ok := limiterInterface.(*RateLimiterEntry)
			if !ok || entry == nil {
				logger.Error(nil, "Invalid rate limiter entry type, skipping rate limiting", "key", req.String(), "type", fmt.Sprintf("%T", limiterInterface))
			} else {
				entry.UpdateLastAccess(now)

				if !entry.Allow() {
					requeueAfter := time.Duration(1.0/r.PodRateLimitQPS) * time.Second
					logger.V(1).Info("Rate limited, skipping reconciliation", LogKeyRequeueAfter, requeueAfter)
					return ctrl.Result{RequeueAfter: requeueAfter}, nil
				}
			}
		}
	}

	// Fetch the Pod
	pod := &corev1.Pod{}
	if err := r.Get(ctx, req.NamespacedName, pod); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Handle deletion
	if pod.DeletionTimestamp != nil {
		return r.handlePodDeletion(ctx, pod)
	}

	// Get annotation key
	key := r.AnnotationKey
	if key == "" {
		key = AnnotationKey
	}

	// Check if pod has the annotation
	annotationValue, hasAnnotation := pod.Annotations[key]
	if !hasAnnotation {
		// No annotation, nothing to do
		return ctrl.Result{}, nil
	}

	// Validate pod has an IP
	if pod.Status.PodIP == "" {
		logger.Info("Pod does not have an IP yet, skipping")
		return ctrl.Result{}, nil
	}

	// Add finalizer if not present
	if updated, err := r.ensureFinalizer(ctx, pod); err != nil {
		return ctrl.Result{}, err
	} else if updated {
		// Requeue to continue processing
		return ctrl.Result{Requeue: true}, nil
	}

	// Validate tags
	if err := validateTags(annotationValue); err != nil {
		logger.Error(err, "Invalid tags in annotation", LogKeyPod, req.NamespacedName, LogKeyTags, annotationValue, LogKeyAnnotationKey, key)
		r.Recorder.Event(pod, corev1.EventTypeWarning, "InvalidTags", err.Error())
		if err := r.updateStatus(ctx, pod, corev1.ConditionFalse, "InvalidTags", err.Error()); err != nil {
			logger.Error(err, "Failed to update status", LogKeyPod, req.NamespacedName)
		}
		return ctrl.Result{}, nil
	}

	// Get ENI info
	eniInfo, err := r.getENIInfo(ctx, pod.Status.PodIP)
	if err != nil {
		logger.Error(err, "Failed to get ENI info", LogKeyPod, req.NamespacedName, LogKeyPodIP, pod.Status.PodIP)
		r.Recorder.Event(pod, corev1.EventTypeWarning, "ENILookupFailed", err.Error())
		if statusErr := r.updateStatus(ctx, pod, corev1.ConditionFalse, "ENILookupFailed", err.Error()); statusErr != nil {
			logger.Error(statusErr, "Failed to update status", "pod", req.NamespacedName)
		}
		// Backoff for transient failures instead of immediate retry
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	// Validate ENI
	if err := r.validateENI(ctx, eniInfo); err != nil {
		logger.Error(err, "ENI validation failed", LogKeyPod, req.NamespacedName, LogKeyENIID, eniInfo.ID, LogKeyENISubnet, eniInfo.SubnetID)
		r.Recorder.Event(pod, corev1.EventTypeWarning, "ENIValidationFailed", err.Error())
		if err := r.updateStatus(ctx, pod, corev1.ConditionFalse, "ENIValidationFailed", err.Error()); err != nil {
			logger.Error(err, "Failed to update status", "pod", req.NamespacedName)
		}
		return ctrl.Result{}, nil
	}

	// Apply tags
	if err := r.applyENITags(ctx, pod, eniInfo, annotationValue); err != nil {
		logger.Error(err, "Failed to apply ENI tags", LogKeyPod, req.NamespacedName, LogKeyENIID, eniInfo.ID)
		r.Recorder.Event(pod, corev1.EventTypeWarning, "TaggingFailed", err.Error())
		if err := r.updateStatus(ctx, pod, corev1.ConditionFalse, "TaggingFailed", err.Error()); err != nil {
			logger.Error(err, "Failed to update status", "pod", req.NamespacedName)
		}
		return ctrl.Result{}, err
	}

	logger.Info("Successfully reconciled pod", LogKeyENIID, eniInfo.ID)
	return ctrl.Result{}, nil
}

// ensureFinalizer adds the finalizer to the pod if it's missing.
// Returns true if the pod was updated, false otherwise.
func (r *PodReconciler) ensureFinalizer(ctx context.Context, pod *corev1.Pod) (bool, error) {
	if !controllerutil.ContainsFinalizer(pod, finalizerName) {
		controllerutil.AddFinalizer(pod, finalizerName)
		if err := r.Update(ctx, pod); err != nil {
			return false, err
		}
		return true, nil
	}
	return false, nil
}
