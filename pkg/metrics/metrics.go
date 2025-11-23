package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	// AWSAPILatency tracks the duration of AWS API calls
	AWSAPILatency = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "eni_tagger_aws_api_latency_seconds",
			Help:    "Latency of AWS API calls in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"operation", "status"},
	)

	// TagOperationsTotal tracks the number of tag/untag operations
	TagOperationsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "eni_tagger_operations_total",
			Help: "Total number of ENI tagging operations",
		},
		[]string{"operation", "status"},
	)

	// ActiveWorkers tracks the number of active reconciliation workers
	ActiveWorkers = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "eni_tagger_active_workers",
			Help: "Number of currently active reconciliation workers",
		},
	)
)

func init() {
	// Register custom metrics with the global prometheus registry
	metrics.Registry.MustRegister(AWSAPILatency)
	metrics.Registry.MustRegister(TagOperationsTotal)
	metrics.Registry.MustRegister(ActiveWorkers)
}
