package controller

import (
	"k8s-eni-tagger/pkg/aws"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// PodReconciler reconciles Pod objects to manage ENI tags based on pod annotations.
// It watches for pods with the configured annotation key and applies the specified
// tags to the pod's associated ENI. The reconciler supports features like:
//   - Optimistic locking via hash-based conflict detection
//   - Subnet filtering to limit which ENIs can be tagged
//   - Dry-run mode for testing without making changes
//   - Finalizers for cleanup on pod deletion
type PodReconciler struct {
	// Client is the Kubernetes client for reading and updating pod resources.
	client.Client

	// Scheme is the runtime scheme for the Kubernetes API.
	Scheme *runtime.Scheme

	// AWSClient is the client for interacting with AWS EC2 API to manage ENI tags.
	AWSClient aws.Client

	// Recorder is used to emit Kubernetes events for tagging operations.
	Recorder record.EventRecorder

	// AnnotationKey is the annotation key to watch for tag specifications.
	// If empty, defaults to AnnotationKey constant.
	AnnotationKey string

	// DryRun when true prevents actual AWS API calls, only logging what would be done.
	DryRun bool

	// SubnetIDs is an optional list of subnet IDs to filter which ENIs can be tagged.
	// If empty, all ENIs are eligible for tagging.
	SubnetIDs []string

	// AllowSharedENITagging when true disables hash-based conflict detection.
	// WARNING: This can lead to tag thrashing when multiple pods share an ENI.
	AllowSharedENITagging bool
}
