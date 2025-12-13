package controller

import (
	"sync"

	"k8s-eni-tagger/pkg/aws"
	enicache "k8s-eni-tagger/pkg/cache"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// PodReconciler reconciles Pod objects and manages ENI tags
type PodReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder

	// AWS client for ENI operations
	AWSClient aws.Client

	// ENI cache for reducing AWS API calls (optional)
	ENICache *enicache.ENICache

	// Configuration
	AnnotationKey         string
	DryRun                bool
	SubnetIDs             []string
	AllowSharedENITagging bool
	TagNamespace          string

	// Per-pod rate limiters for DoS protection
	PodRateLimiters   *sync.Map
	PodRateLimitQPS   float64 // Requests per second per pod
	PodRateLimitBurst int     // Burst size per pod
}
