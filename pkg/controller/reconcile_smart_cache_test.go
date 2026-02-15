package controller

import (
	"context"
	"k8s-eni-tagger/pkg/aws"
	"k8s-eni-tagger/pkg/cache"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// TestReconcile_SmartCache_IPReused verifies that the controller handles IP reuse correctly.
// Scenario:
// 1. Cache contains STALE entry: IP=10.0.0.100 -> ENI-OLD (bound to PodUID="old-pod")
// 2. New Pod appears: IP=10.0.0.100, PodUID="new-pod"
// 3. Controller should detect UID mismatch, invalidate cache, fetch ENI-NEW, and tag ENI-NEW.
func TestReconcile_SmartCache_IPReused(t *testing.T) {
	scheme := runtime.NewScheme()
	err := corev1.AddToScheme(scheme)
	require.NoError(t, err)

	podIP := "10.0.0.100"
	validTags := `{"cost-center":"123","team":"platform"}`

	// New Pod reusing the IP
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-ip-reuse",
			Namespace: "default",
			UID:       "new-pod-uid", // NEW UID
			Annotations: map[string]string{
				AnnotationKey: validTags,
			},
			Finalizers: []string{finalizerName},
		},
		Status: corev1.PodStatus{PodIP: podIP},
	}

	mockAWS := new(MockAWSClient)

	// Setup Cache with STALE data (bound to OLD UID)
	eniCache := cache.NewENICache(mockAWS)

	// Helper to populate cache with stale data.
	// The cache.Set is private, but we can access it via GetENIInfoByIP if we mock the backend.
	// Step 1: Prime cache with "old-pod-uid" -> "eni-old"
	mockAWS.On("GetENIInfoByIP", mock.Anything, podIP).Return(&aws.ENIInfo{
		ID: "eni-old",
	}, nil).Once()

	_, err = eniCache.GetENIInfoByIP(context.Background(), podIP, "old-pod-uid")
	require.NoError(t, err)

	// Step 2: Now run reconciliation for "new-pod-uid".
	// The cache has ("10.0.0.100", "old-pod-uid") -> "eni-old".
	// Request is ("10.0.0.100", "new-pod-uid").
	// Expected: Cache Miss (UID Mismatch) -> AWS Call -> Tag ENI-NEW.

	// AWS Mock Expectation for Reality (ENI-NEW)
	mockAWS.On("GetENIInfoByIP", mock.Anything, podIP).Return(&aws.ENIInfo{
		ID: "eni-new",
	}, nil).Once()

	// Expect Tagging on ENI-NEW
	mockAWS.On("TagENI", mock.Anything, "eni-new", mock.Anything).Return(nil).Once()

	r := &PodReconciler{
		Client:            fake.NewClientBuilder().WithScheme(scheme).WithObjects(pod).Build(),
		Scheme:            scheme,
		Recorder:          record.NewFakeRecorder(10),
		AWSClient:         mockAWS,
		ENICache:          eniCache, // Use the primed cache
		AnnotationKey:     AnnotationKey,
		PodRateLimiters:   &sync.Map{},
		PodRateLimitQPS:   100,
		PodRateLimitBurst: 10,
	}

	req := reconcile.Request{
		NamespacedName: client.ObjectKey{
			Name:      pod.Name,
			Namespace: pod.Namespace,
		},
	}

	_, err = r.Reconcile(context.Background(), req)
	assert.NoError(t, err)

	mockAWS.AssertExpectations(t)
}
