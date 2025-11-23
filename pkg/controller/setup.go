package controller

import (
	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

//+kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups=core,resources=pods/status,verbs=get;update;patch
//+kubebuilder:rbac:groups="",resources=events,verbs=create;patch

// SetupWithManager configures the controller with the manager and sets up event filters.
// It configures the controller to:
//   - Watch Pod resources with the configured annotation key
//   - Set the maximum number of concurrent reconciliations
//   - Filter events to only reconcile when:
//   - A pod is created with the annotation
//   - The annotation value changes
//   - A pod gets an IP for the first time (and has the annotation)
//   - A pod is being deleted and has our finalizer
//
// The concurrentReconciles parameter controls how many pods can be reconciled in parallel.
func (r *PodReconciler) SetupWithManager(mgr ctrl.Manager, concurrentReconciles int) error {
	key := r.AnnotationKey
	if key == "" {
		key = AnnotationKey
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Pod{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: concurrentReconciles}).
		WithEventFilter(predicate.Funcs{
			CreateFunc: func(e event.CreateEvent) bool {
				pod := e.Object.(*corev1.Pod)
				_, hasAnnotation := pod.Annotations[key]
				return hasAnnotation
			},
			UpdateFunc: func(e event.UpdateEvent) bool {
				oldPod := e.ObjectOld.(*corev1.Pod)
				newPod := e.ObjectNew.(*corev1.Pod)

				oldAnnotation := oldPod.Annotations[key]
				newAnnotation := newPod.Annotations[key]

				// Reconcile if annotation changed
				if oldAnnotation != newAnnotation {
					return true
				}

				// Reconcile if pod got an IP for the first time
				if oldPod.Status.PodIP == "" && newPod.Status.PodIP != "" {
					_, hasAnnotation := newPod.Annotations[key]
					return hasAnnotation
				}

				// Reconcile if pod is being deleted and has our finalizer
				if newPod.DeletionTimestamp != nil && controllerutil.ContainsFinalizer(newPod, finalizerName) {
					return true
				}

				return false
			},
			DeleteFunc: func(e event.DeleteEvent) bool {
				// We handle deletion via finalizers
				return false
			},
		}).
		Complete(r)
}
