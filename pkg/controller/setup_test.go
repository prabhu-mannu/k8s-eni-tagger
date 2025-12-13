package controller

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

func TestCreatePredicate(t *testing.T) {
	r := &PodReconciler{AnnotationKey: AnnotationKey, PodRateLimiters: &sync.Map{}, PodRateLimitQPS: 0.1, PodRateLimitBurst: 1}
	p := r.createPredicate()

	t.Run("Create", func(t *testing.T) {
		// With annotation -> true
		e1 := event.CreateEvent{
			Object: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{AnnotationKey: "val"},
				},
			},
		}
		assert.True(t, p.Create(e1))

		// Without annotation -> false
		e2 := event.CreateEvent{
			Object: &corev1.Pod{},
		}
		assert.False(t, p.Create(e2))
	})

	t.Run("Update", func(t *testing.T) {
		// Annotation changed -> true
		e1 := event.UpdateEvent{
			ObjectOld: &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{AnnotationKey: "v1"}}},
			ObjectNew: &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{AnnotationKey: "v2"}}},
		}
		assert.True(t, p.Update(e1))

		// IP assigned (first time) -> true
		e2 := event.UpdateEvent{
			ObjectOld: &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{AnnotationKey: "v1"}}},
			ObjectNew: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{AnnotationKey: "v1"}},
				Status:     corev1.PodStatus{PodIP: "1.2.3.4"},
			},
		}
		assert.True(t, p.Update(e2))

		// Deletion with finalizer -> true
		now := metav1.Now()
		e3 := event.UpdateEvent{
			ObjectOld: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{AnnotationKey: "v1"}, Finalizers: []string{finalizerName}},
			},
			ObjectNew: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{AnnotationKey: "v1"}, Finalizers: []string{finalizerName}, DeletionTimestamp: &now},
			},
		}
		assert.True(t, p.Update(e3))

		// Nothing changed -> false
		e4 := event.UpdateEvent{
			ObjectOld: &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{AnnotationKey: "v1"}}},
			ObjectNew: &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{AnnotationKey: "v1"}}},
		}
		assert.False(t, p.Update(e4))
	})

	t.Run("Delete", func(t *testing.T) {
		assert.False(t, p.Delete(event.DeleteEvent{}))
	})
}
