package config

import (
	"flag"
	"fmt"
	"os"
	"regexp"
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
}

// Load parses flags and environment variables to create a Config
func Load() (*Config, error) {
	cfg := &Config{}
	var subnetIDs string

	flag.StringVar(&cfg.MetricsBindAddress, "metrics-bind-address", ":8090", "The address the metric endpoint binds to.")
	flag.StringVar(&cfg.HealthProbeBindAddress, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
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
	flag.StringVar(&cfg.TagNamespace, "tag-namespace", "", "Optional namespace prefix for all tags (max 63 chars, e.g., 'acme-corp' becomes 'acme-corp:CostCenter'). If empty, uses the pod's Kubernetes namespace. Useful for multi-tenant scenarios.")

	flag.Parse()

	if cfg.PrintVersion {
		return cfg, nil
	}

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
	if cfg.TagNamespace != "" {
		if strings.Contains(cfg.TagNamespace, ":") {
			return nil, fmt.Errorf("tag-namespace cannot contain ':' character")
		}
		if strings.HasPrefix(cfg.TagNamespace, "aws:") || strings.HasPrefix(cfg.TagNamespace, "kubernetes.io/") {
			return nil, fmt.Errorf("tag-namespace cannot use reserved prefixes 'aws:' or 'kubernetes.io/'")
		}
		// Check for invalid characters: only allow alphanumeric, spaces, + - = . _ /
		if matched, _ := regexp.MatchString(`^[a-zA-Z0-9 +\-=._/]*$`, cfg.TagNamespace); !matched {
			return nil, fmt.Errorf("tag-namespace contains invalid characters, only alphanumeric, spaces, and symbols + - = . _ / are allowed")
		}
		// Length check: ensure namespace itself is reasonable, < 64 to align with k8s namespace limits
		if len(cfg.TagNamespace) > 63 {
			return nil, fmt.Errorf("tag-namespace is too long, maximum 63 characters")
		}
	}

	return cfg, nil
}
