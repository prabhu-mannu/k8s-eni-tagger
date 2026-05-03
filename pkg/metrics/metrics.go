package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	// AWSAPILatency tracks the latency of AWS API calls
	AWSAPILatency = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "k8s_eni_tagger_aws_api_latency_seconds",
			Help:    "Latency of AWS API calls in seconds",
			Buckets: prometheus.ExponentialBuckets(0.001, 2, 10), // 1ms to ~1s
		},
		[]string{"operation", "status"},
	)

	// CacheHitsTotal tracks the number of cache hits
	CacheHitsTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "k8s_eni_tagger_cache_hits_total",
			Help: "Total number of ENI cache hits",
		},
	)

	// CacheMissesTotal tracks the number of cache misses
	CacheMissesTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "k8s_eni_tagger_cache_misses_total",
			Help: "Total number of ENI cache misses",
		},
	)

	// CachePersistDroppedTotal tracks ConfigMap persistence updates dropped
	// because the worker queue was full. Drops are safe (the in-memory cache
	// is updated, and Pod-UID validation catches staleness on restart) but
	// indicate the persister cannot keep up with reconcile rate.
	CachePersistDroppedTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "k8s_eni_tagger_cache_persist_dropped_total",
			Help: "Total number of ConfigMap persistence updates dropped due to a full worker queue",
		},
	)
)

func init() {
	// Register custom metrics with the controller-runtime metrics registry
	metrics.Registry.MustRegister(
		AWSAPILatency,
		CacheHitsTotal,
		CacheMissesTotal,
		CachePersistDroppedTotal,
	)
}
