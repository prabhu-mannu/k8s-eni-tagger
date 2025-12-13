package config

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds all application configuration
type Config struct {
	MetricsBindAddress      string
	HealthProbeBindAddress  string
	EnableLeaderElection    bool
	AnnotationKey           string
	MaxConcurrentReconciles int
	DryRun                  bool
	WatchNamespace          string
	PrintVersion            bool
	SubnetIDs               []string
	AllowSharedENITagging   bool
	EnableENICache          bool
	EnableCacheConfigMap    bool
	CacheBatchInterval      time.Duration
	CacheBatchSize          int
	AWSRateLimitQPS         float64
	AWSRateLimitBurst       int
	PprofBindAddress        string
	TagNamespace            string

	// Per-pod rate limiting for DoS protection
	PodRateLimitQPS            float64
	PodRateLimitBurst          int
	RateLimiterCleanupInterval time.Duration
}

// Load parses flags and environment variables to create a Config
func Load() (*Config, error) {
	cfg := &Config{}
	var subnetIDs string

	flag.StringVar(&cfg.MetricsBindAddress, "metrics-bind-address", "8090", "Port (or address) the metrics endpoint binds to. Use plain port (e.g., 8090) or address:port (e.g., 0.0.0.0:8090).")
	flag.StringVar(&cfg.HealthProbeBindAddress, "health-probe-bind-address", "8081", "Port (or address) the health probe endpoint binds to. Use plain port (e.g., 8081) or address:port.")
	flag.BoolVar(&cfg.EnableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.StringVar(&cfg.AnnotationKey, "annotation-key", "eni-tagger.io/tags", "The annotation key to watch for tags.")
	flag.IntVar(&cfg.MaxConcurrentReconciles, "max-concurrent-reconciles", 1, "Maximum number of concurrent reconciles.")
	flag.BoolVar(&cfg.DryRun, "dry-run", false, "Enable dry-run mode (no AWS changes).")
	flag.StringVar(&cfg.WatchNamespace, "watch-namespace", "", "Namespace to watch for Pods. If empty, watches all namespaces.")
	flag.BoolVar(&cfg.PrintVersion, "version", false, "Print version information and exit.")
	flag.StringVar(&subnetIDs, "subnet-ids", "", "Comma-separated list of allowed Subnet IDs. If empty, all subnets are allowed (subject to safety checks). Can also be set via ENI_TAGGER_SUBNET_IDS env var.")
	flag.BoolVar(&cfg.AllowSharedENITagging, "allow-shared-eni-tagging", false, "Allow tagging of shared ENIs (e.g. standard EKS nodes). WARNING: This can cause tag thrashing.")

	// ENI Cache flags
	flag.BoolVar(&cfg.EnableENICache, "enable-eni-cache", true, "Enable in-memory ENI caching (cached until pod deletion).")
	flag.BoolVar(&cfg.EnableCacheConfigMap, "enable-cache-configmap", false, "Enable ConfigMap persistence for ENI cache (survives restarts).")
	flag.DurationVar(&cfg.CacheBatchInterval, "cache-batch-interval", 2*time.Second, "Batch interval for ConfigMap cache persistence (e.g., 2s).")
	flag.IntVar(&cfg.CacheBatchSize, "cache-batch-size", 20, "Batch size for ConfigMap cache persistence.")

	// Rate limiting flags
	flag.Float64Var(&cfg.AWSRateLimitQPS, "aws-rate-limit-qps", 10, "AWS API rate limit (requests per second).")
	flag.IntVar(&cfg.AWSRateLimitBurst, "aws-rate-limit-burst", 20, "AWS API rate limit burst size.")

	// Pprof flag
	flag.StringVar(&cfg.PprofBindAddress, "pprof-bind-address", "0", "The address the pprof endpoint binds to. Set to '0' to disable.")

	// Tag namespace flag
	flag.StringVar(&cfg.TagNamespace, "tag-namespace", "", "Control automatic pod namespace-based tag namespacing. Set to 'enable' to use the pod's Kubernetes namespace as tag prefix. Any other value (including empty) disables namespacing.")

	// Per-pod rate limiting flags
	flag.Float64Var(&cfg.PodRateLimitQPS, "pod-rate-limit-qps", 0.1, "Per-pod reconciliation rate limit (requests per second). Default 0.1 = 1 reconciliation every 10 seconds per pod.")
	flag.IntVar(&cfg.PodRateLimitBurst, "pod-rate-limit-burst", 1, "Per-pod rate limit burst size (allows brief bursts above QPS).")
	flag.DurationVar(&cfg.RateLimiterCleanupInterval, "rate-limiter-cleanup-interval", 1*time.Minute, "Interval for cleaning up stale pod rate limiters (e.g., 1m).")

	flag.Parse()

	// Helper to get the current value & default value of a flag
	getFlagValues := func(name string) (string, string) {
		f := flag.Lookup(name)
		if f == nil {
			return "", ""
		}
		return f.Value.String(), f.DefValue
	}

	// Environment variable fallbacks: if the CLI flag was not provided
	// (the current value equals the flag DefValue), and an env var exists,
	// the env var will override the default.
	if v := os.Getenv("ENI_TAGGER_METRICS_BIND_ADDRESS"); v != "" {
		if curr, def := getFlagValues("metrics-bind-address"); curr == def {
			cfg.MetricsBindAddress = v
		}
	}
	if v := os.Getenv("ENI_TAGGER_HEALTH_PROBE_BIND_ADDRESS"); v != "" {
		if curr, def := getFlagValues("health-probe-bind-address"); curr == def {
			cfg.HealthProbeBindAddress = v
		}
	}
	if v := os.Getenv("ENI_TAGGER_LEADER_ELECT"); v != "" {
		if curr, def := getFlagValues("leader-elect"); curr == def {
			if b, err := strconv.ParseBool(v); err == nil {
				cfg.EnableLeaderElection = b
			}
		}
	}
	if v := os.Getenv("ENI_TAGGER_ANNOTATION_KEY"); v != "" {
		if curr, def := getFlagValues("annotation-key"); curr == def {
			cfg.AnnotationKey = v
		}
	}
	if v := os.Getenv("ENI_TAGGER_MAX_CONCURRENT_RECONCILES"); v != "" {
		if curr, def := getFlagValues("max-concurrent-reconciles"); curr == def {
			if i, err := strconv.Atoi(v); err == nil {
				cfg.MaxConcurrentReconciles = i
			}
		}
	}
	if v := os.Getenv("ENI_TAGGER_DRY_RUN"); v != "" {
		if curr, def := getFlagValues("dry-run"); curr == def {
			if b, err := strconv.ParseBool(v); err == nil {
				cfg.DryRun = b
			}
		}
	}
	if v := os.Getenv("ENI_TAGGER_WATCH_NAMESPACE"); v != "" {
		if curr, def := getFlagValues("watch-namespace"); curr == def {
			cfg.WatchNamespace = v
		}
	}

	if v := os.Getenv("ENI_TAGGER_ALLOW_SHARED_ENI_TAGGING"); v != "" {
		if curr, def := getFlagValues("allow-shared-eni-tagging"); curr == def {
			if b, err := strconv.ParseBool(v); err == nil {
				cfg.AllowSharedENITagging = b
			}
		}
	}
	if v := os.Getenv("ENI_TAGGER_ENABLE_ENI_CACHE"); v != "" {
		if curr, def := getFlagValues("enable-eni-cache"); curr == def {
			if b, err := strconv.ParseBool(v); err == nil {
				cfg.EnableENICache = b
			}
		}
	}
	if v := os.Getenv("ENI_TAGGER_ENABLE_CACHE_CONFIGMAP"); v != "" {
		if curr, def := getFlagValues("enable-cache-configmap"); curr == def {
			if b, err := strconv.ParseBool(v); err == nil {
				cfg.EnableCacheConfigMap = b
			}
		}
	}
	if v := os.Getenv("ENI_TAGGER_CACHE_BATCH_INTERVAL"); v != "" {
		if curr, def := getFlagValues("cache-batch-interval"); curr == def {
			if d, err := time.ParseDuration(v); err == nil {
				cfg.CacheBatchInterval = d
			}
		}
	}
	if v := os.Getenv("ENI_TAGGER_CACHE_BATCH_SIZE"); v != "" {
		if curr, def := getFlagValues("cache-batch-size"); curr == def {
			if i, err := strconv.Atoi(v); err == nil {
				cfg.CacheBatchSize = i
			}
		}
	}
	if v := os.Getenv("ENI_TAGGER_AWS_RATE_LIMIT_QPS"); v != "" {
		if curr, def := getFlagValues("aws-rate-limit-qps"); curr == def {
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				cfg.AWSRateLimitQPS = f
			}
		}
	}
	if v := os.Getenv("ENI_TAGGER_AWS_RATE_LIMIT_BURST"); v != "" {
		if curr, def := getFlagValues("aws-rate-limit-burst"); curr == def {
			if i, err := strconv.Atoi(v); err == nil {
				cfg.AWSRateLimitBurst = i
			}
		}
	}
	if v := os.Getenv("ENI_TAGGER_PPROF_BIND_ADDRESS"); v != "" {
		if curr, def := getFlagValues("pprof-bind-address"); curr == def {
			cfg.PprofBindAddress = v
		}
	}
	if v := os.Getenv("ENI_TAGGER_TAG_NAMESPACE"); v != "" {
		if curr, def := getFlagValues("tag-namespace"); curr == def {
			cfg.TagNamespace = v
		}
	}
	if v := os.Getenv("ENI_TAGGER_POD_RATE_LIMIT_QPS"); v != "" {
		if curr, def := getFlagValues("pod-rate-limit-qps"); curr == def {
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				cfg.PodRateLimitQPS = f
			}
		}
	}
	if v := os.Getenv("ENI_TAGGER_POD_RATE_LIMIT_BURST"); v != "" {
		if curr, def := getFlagValues("pod-rate-limit-burst"); curr == def {
			if i, err := strconv.Atoi(v); err == nil {
				cfg.PodRateLimitBurst = i
			}
		}
	}
	if v := os.Getenv("ENI_TAGGER_RATE_LIMITER_CLEANUP_INTERVAL"); v != "" {
		if curr, def := getFlagValues("rate-limiter-cleanup-interval"); curr == def {
			if d, err := time.ParseDuration(v); err == nil {
				cfg.RateLimiterCleanupInterval = d
			}
		}
	}

	if cfg.PrintVersion {
		return cfg, nil
	}

	// Normalize bind addresses so bare ports (e.g., "8090") become ":8090",
	// while existing addresses with ":" are left untouched.
	cfg.MetricsBindAddress = normalizeBindAddress(cfg.MetricsBindAddress)
	cfg.HealthProbeBindAddress = normalizeBindAddress(cfg.HealthProbeBindAddress)
	cfg.PprofBindAddress = normalizeBindAddress(cfg.PprofBindAddress)

	// Handle Env Var fallback for subnet-ids
	if subnetIDs == "" {
		subnetIDs = os.Getenv("ENI_TAGGER_SUBNET_IDS")
	}

	if subnetIDs != "" {
		parts := strings.Split(subnetIDs, ",")
		for _, p := range parts {
			trimmed := strings.TrimSpace(p)
			if trimmed != "" {
				if !strings.HasPrefix(trimmed, "subnet-") {
					return nil, fmt.Errorf("invalid subnet ID format: %s", trimmed)
				}
				cfg.SubnetIDs = append(cfg.SubnetIDs, trimmed)
			}
		}
	}

	// Validate annotation key
	if cfg.AnnotationKey == "" {
		return nil, fmt.Errorf("annotation-key cannot be empty")
	}

	// Validate tag namespace
	// Any value other than "enable" is treated as disabled (no error)
	if cfg.TagNamespace != "" && cfg.TagNamespace != "enable" {
		fmt.Fprintf(os.Stderr, "Warning: invalid tag-namespace value '%s', treating as disabled. Use 'enable' to enable pod namespace-based tag namespacing.\n", cfg.TagNamespace)
	}

	return cfg, nil
}
