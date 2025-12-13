package main

import (
	"flag"
	"fmt"
	"net/http"
	_ "net/http/pprof" // Register pprof handlers
	"os"
	"strings"
	"sync"

	"k8s-eni-tagger/pkg/aws"
	enicache "k8s-eni-tagger/pkg/cache"
	"k8s-eni-tagger/pkg/config"
	"k8s-eni-tagger/pkg/controller"
	"k8s-eni-tagger/pkg/health"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
	// Version information set by ldflags
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
}

// getControllerNamespace returns the namespace the controller is running in.
// Priority:
// 1. POD_NAMESPACE environment variable (when set via downward API)
// 2. Service account namespace file (in-cluster default)
// 3. Fallback: "default"
func getControllerNamespace() string {
	if ns := os.Getenv("POD_NAMESPACE"); ns != "" {
		return ns
	}

	const namespacePath = "/var/run/secrets/kubernetes.io/serviceaccount/namespace"
	if data, err := os.ReadFile(namespacePath); err == nil {
		if ns := strings.TrimSpace(string(data)); ns != "" {
			return ns
		}
	}

	return "default"
}

func startPprof(addr string) {
	if addr != "0" {
		go func() {
			setupLog.Info("Starting pprof server", "addr", addr)
			if err := http.ListenAndServe(addr, nil); err != nil {
				setupLog.Error(err, "Failed to start pprof server")
			}
		}()
	}
}

func main() {
	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		fmt.Printf("Error loading configuration: %v\n", err)
		os.Exit(1)
	}

	if cfg.PrintVersion {
		// Use fmt.Printf for version info when requested directly
		fmt.Printf("k8s-eni-tagger version=%s commit=%s date=%s\n", version, commit, date)
		os.Exit(0)
	}

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	setupLog.Info("Starting k8s-eni-tagger", "version", version, "commit", commit, "date", date)

	if len(cfg.SubnetIDs) > 0 {
		setupLog.Info("Subnet filtering enabled", "subnets", cfg.SubnetIDs)
	}

	if cfg.AllowSharedENITagging {
		setupLog.Info("WARNING: Shared ENI tagging is enabled. This may cause tag thrashing on standard EKS nodes.")
	}

	// Start pprof server
	startPprof(cfg.PprofBindAddress)

	mgrOptions := ctrl.Options{
		Scheme:                 scheme,
		Metrics:                server.Options{BindAddress: cfg.MetricsBindAddress},
		HealthProbeBindAddress: cfg.HealthProbeBindAddress,
		LeaderElection:         cfg.EnableLeaderElection,
		LeaderElectionID:       "k8s-eni-tagger.eni-tagger.io",
	}

	if cfg.WatchNamespace != "" {
		mgrOptions.Cache = cache.Options{
			DefaultNamespaces: map[string]cache.Config{
				cfg.WatchNamespace: {},
			},
		}
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), mgrOptions)
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	ctx := ctrl.SetupSignalHandler()

	// Create AWS client with rate limiting
	rlConfig := aws.RateLimitConfig{
		QPS:   cfg.AWSRateLimitQPS,
		Burst: cfg.AWSRateLimitBurst,
	}
	awsClient, err := aws.NewClientWithRateLimiter(ctx, rlConfig)
	if err != nil {
		setupLog.Error(err, "unable to create AWS client")
		os.Exit(1)
	}
	setupLog.Info("AWS client initialized with rate limiting", "qps", cfg.AWSRateLimitQPS, "burst", cfg.AWSRateLimitBurst)

	// Add AWS connectivity check for startup validation only
	// This runs once at startup to verify AWS permissions
	// Moving from readyz to healthz prevents continuous AWS API calls from readiness probes
	ec2HealthClient := &health.EC2HealthClient{EC2: awsClient.GetEC2Client()}
	if err := ec2HealthClient.Validate(); err != nil {
		setupLog.Error(err, "unable to initialize EC2 health client")
		os.Exit(1)
	}
	awsChecker := health.NewAWSChecker(ec2HealthClient)
	if err := mgr.AddHealthzCheck("aws", awsChecker.Check); err != nil {
		setupLog.Error(err, "unable to add AWS health check")
		os.Exit(1)
	}

	// Initialize ENI cache if enabled
	var eniCache *enicache.ENICache
	if cfg.EnableENICache {
		eniCache = enicache.NewENICache(awsClient)
		// Apply batch settings before enabling persistence
		eniCache.SetBatchConfig(cfg.CacheBatchInterval, cfg.CacheBatchSize)

		// Add ConfigMap persistence if enabled
		if cfg.EnableCacheConfigMap {
			namespace := getControllerNamespace()
			cmPersister := enicache.NewConfigMapPersister(mgr.GetClient(), namespace)
			eniCache.WithConfigMapPersister(cmPersister)
			if err := eniCache.LoadFromConfigMap(ctx); err != nil {
				setupLog.Error(err, "Failed to load cache from ConfigMap, starting fresh")
			}
			setupLog.Info("ENI cache ConfigMap persistence enabled", "namespace", namespace)
		}

		setupLog.Info("ENI caching enabled (lifecycle-based)", "configMapPersistence", cfg.EnableCacheConfigMap)
	}

	podReconciler := &controller.PodReconciler{
		Client:                mgr.GetClient(),
		Scheme:                mgr.GetScheme(),
		AWSClient:             awsClient,
		ENICache:              eniCache,
		Recorder:              mgr.GetEventRecorderFor("k8s-eni-tagger"),
		AnnotationKey:         cfg.AnnotationKey,
		DryRun:                cfg.DryRun,
		SubnetIDs:             cfg.SubnetIDs,
		AllowSharedENITagging: cfg.AllowSharedENITagging,
		TagNamespace:          cfg.TagNamespace,
		PodRateLimiters:       &sync.Map{},
		PodRateLimitQPS:       cfg.PodRateLimitQPS,
		PodRateLimitBurst:     cfg.PodRateLimitBurst,
	}

	if err = podReconciler.SetupWithManager(mgr, cfg.MaxConcurrentReconciles); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Pod")
		os.Exit(1)
	}

	// Start rate limiter cleanup goroutine
	podReconciler.StartRateLimiterCleanup(ctx, cfg.RateLimiterCleanupInterval)

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	// Readiness check: simple ping, no AWS API calls
	// This ensures the controller manager is ready to process events
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctx); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
