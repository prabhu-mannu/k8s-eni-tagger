package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

// Config holds all application configuration
type Config struct {
	MetricsBindAddress         string        `mapstructure:"metrics-bind-address"`
	HealthProbeBindAddress     string        `mapstructure:"health-probe-bind-address"`
	EnableLeaderElection       bool          `mapstructure:"leader-elect"`
	AnnotationKey              string        `mapstructure:"annotation-key"`
	MaxConcurrentReconciles    int           `mapstructure:"max-concurrent-reconciles"`
	DryRun                     bool          `mapstructure:"dry-run"`
	WatchNamespace             string        `mapstructure:"watch-namespace"`
	PrintVersion               bool          `mapstructure:"version"`
	SubnetIDs                  []string      `mapstructure:"subnet-ids"`
	AllowSharedENITagging      bool          `mapstructure:"allow-shared-eni-tagging"`
	EnableENICache             bool          `mapstructure:"enable-eni-cache"`
	EnableCacheConfigMap       bool          `mapstructure:"enable-cache-configmap"`
	CacheBatchInterval         time.Duration `mapstructure:"cache-batch-interval"`
	CacheBatchSize             int           `mapstructure:"cache-batch-size"`
	AWSRateLimitQPS            float64       `mapstructure:"aws-rate-limit-qps"`
	AWSRateLimitBurst          int           `mapstructure:"aws-rate-limit-burst"`
	PprofBindAddress           string        `mapstructure:"pprof-bind-address"`
	TagNamespace               string        `mapstructure:"tag-namespace"`
	PodRateLimitQPS            float64       `mapstructure:"pod-rate-limit-qps"`
	PodRateLimitBurst          int           `mapstructure:"pod-rate-limit-burst"`
	RateLimiterCleanupInterval time.Duration `mapstructure:"rate-limiter-cleanup-interval"`
}

// Load parses flags and environment variables to create a Config
func Load() (*Config, error) {
	cfg := &Config{}

	// Initialize viper
	v := viper.New()

	// Set environment variable prefix and automatic env binding
	v.SetEnvPrefix("ENI_TAGGER")
	v.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
	v.AutomaticEnv()

	// Define flags
	defineFlags(v)

	// Parse flags
	pflag.Parse()

	// Bind flags to viper
	v.BindPFlags(pflag.CommandLine)

	// Set defaults
	setDefaults(v)

	// Parse subnet IDs (special handling for comma-separated values)
	if subnetStr := v.GetString("subnet-ids"); subnetStr != "" {
		parts := strings.Split(subnetStr, ",")
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

	// Unmarshal config
	if err := v.Unmarshal(cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	// Early return for version flag
	if cfg.PrintVersion {
		return cfg, nil
	}

	// Normalize bind addresses
	cfg.MetricsBindAddress = normalizeBindAddress(cfg.MetricsBindAddress)
	cfg.HealthProbeBindAddress = normalizeBindAddress(cfg.HealthProbeBindAddress)
	cfg.PprofBindAddress = normalizeBindAddress(cfg.PprofBindAddress)

	// Validate annotation key
	if cfg.AnnotationKey == "" {
		return nil, fmt.Errorf("annotation-key cannot be empty")
	}

	// Validate tag namespace
	if cfg.TagNamespace != "" && cfg.TagNamespace != "enable" {
		fmt.Fprintf(os.Stderr, "Warning: invalid tag-namespace value '%s', treating as disabled. Use 'enable' to enable pod namespace-based tag namespacing.\n", cfg.TagNamespace)
	}

	return cfg, nil
}

func defineFlags(v *viper.Viper) {
	pflag.String("metrics-bind-address", "8090", "Port (or address) the metrics endpoint binds to. Use plain port (e.g., 8090) or address:port (e.g., 0.0.0.0:8090).")
	pflag.String("health-probe-bind-address", "8081", "Port (or address) the health probe endpoint binds to. Use plain port (e.g., 8081) or address:port.")
	pflag.Bool("leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	pflag.String("annotation-key", "eni-tagger.io/tags", "The annotation key to watch for tags.")
	pflag.Int("max-concurrent-reconciles", 1, "Maximum number of concurrent reconciles.")
	pflag.Bool("dry-run", false, "Enable dry-run mode (no AWS changes).")
	pflag.String("watch-namespace", "", "Namespace to watch for Pods. If empty, watches all namespaces.")
	pflag.Bool("version", false, "Print version information and exit.")
	pflag.String("subnet-ids", "", "Comma-separated list of allowed Subnet IDs. If empty, all subnets are allowed (subject to safety checks). Can also be set via ENI_TAGGER_SUBNET_IDS env var.")
	pflag.Bool("allow-shared-eni-tagging", false, "Allow tagging of shared ENIs (e.g. standard EKS nodes). WARNING: This can cause tag thrashing.")

	// ENI Cache flags
	pflag.Bool("enable-eni-cache", true, "Enable in-memory ENI caching (cached until pod deletion).")
	pflag.Bool("enable-cache-configmap", false, "Enable ConfigMap persistence for ENI cache (survives restarts).")
	pflag.Duration("cache-batch-interval", 2*time.Second, "Batch interval for ConfigMap cache persistence (e.g., 2s).")
	pflag.Int("cache-batch-size", 20, "Batch size for ConfigMap cache persistence.")

	// Rate limiting flags
	pflag.Float64("aws-rate-limit-qps", 10, "AWS API rate limit (requests per second).")
	pflag.Int("aws-rate-limit-burst", 20, "AWS API rate limit burst size.")

	// Pprof flag
	pflag.String("pprof-bind-address", "0", "The address the pprof endpoint binds to. Set to '0' to disable.")

	// Tag namespace flag
	pflag.String("tag-namespace", "", "Control automatic pod namespace-based tag namespacing. Set to 'enable' to use the pod's Kubernetes namespace as tag prefix. Any other value (including empty) disables namespacing.")

	// Per-pod rate limiting flags
	pflag.Float64("pod-rate-limit-qps", 0.1, "Per-pod reconciliation rate limit (requests per second). Default 0.1 = 1 reconciliation every 10 seconds per pod.")
	pflag.Int("pod-rate-limit-burst", 1, "Per-pod rate limit burst size (allows brief bursts above QPS).")
	pflag.Duration("rate-limiter-cleanup-interval", 1*time.Minute, "Interval for cleaning up stale pod rate limiters (e.g., 1m).")
}

func setDefaults(v *viper.Viper) {
	v.SetDefault("metrics-bind-address", "8090")
	v.SetDefault("health-probe-bind-address", "8081")
	v.SetDefault("leader-elect", false)
	v.SetDefault("annotation-key", "eni-tagger.io/tags")
	v.SetDefault("max-concurrent-reconciles", 1)
	v.SetDefault("dry-run", false)
	v.SetDefault("watch-namespace", "")
	v.SetDefault("version", false)
	v.SetDefault("subnet-ids", "")
	v.SetDefault("allow-shared-eni-tagging", false)
	v.SetDefault("enable-eni-cache", true)
	v.SetDefault("enable-cache-configmap", false)
	v.SetDefault("cache-batch-interval", 2*time.Second)
	v.SetDefault("cache-batch-size", 20)
	v.SetDefault("aws-rate-limit-qps", 10.0)
	v.SetDefault("aws-rate-limit-burst", 20)
	v.SetDefault("pprof-bind-address", "0")
	v.SetDefault("tag-namespace", "")
	v.SetDefault("pod-rate-limit-qps", 0.1)
	v.SetDefault("pod-rate-limit-burst", 1)
	v.SetDefault("rate-limiter-cleanup-interval", 1*time.Minute)
}
