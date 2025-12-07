package main

import (
	"flag"
	"net/http"
	_ "net/http/pprof" // Register pprof handlers
	"os"
	"strings"

	"k8s-eni-tagger/pkg/aws"
	enicache "k8s-eni-tagger/pkg/cache"
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

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	var probeAddr string
	var annotationKey string
	var maxConcurrentReconciles int
	var dryRun bool
	var watchNamespace string
	var printVersion bool
	var subnetIDs string
	var allowSharedENITagging bool

	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8090", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.StringVar(&annotationKey, "annotation-key", "eni-tagger.io/tags", "The annotation key to watch for tags.")
	flag.IntVar(&maxConcurrentReconciles, "max-concurrent-reconciles", 1, "Maximum number of concurrent reconciles.")
	flag.BoolVar(&dryRun, "dry-run", false, "Enable dry-run mode (no AWS changes).")
	flag.StringVar(&watchNamespace, "watch-namespace", "", "Namespace to watch for Pods. If empty, watches all namespaces.")
	flag.BoolVar(&printVersion, "version", false, "Print version information and exit.")
	flag.StringVar(&subnetIDs, "subnet-ids", "", "Comma-separated list of allowed Subnet IDs. If empty, all subnets are allowed (subject to safety checks). Can also be set via ENI_TAGGER_SUBNET_IDS env var.")
	flag.BoolVar(&allowSharedENITagging, "allow-shared-eni-tagging", false, "Allow tagging of shared ENIs (e.g. standard EKS nodes). WARNING: This can cause tag thrashing.")

	// ENI Cache flags
	var enableENICache bool
	var enableCacheConfigMap bool
	flag.BoolVar(&enableENICache, "enable-eni-cache", true, "Enable in-memory ENI caching (cached until pod deletion).")
	flag.BoolVar(&enableCacheConfigMap, "enable-cache-configmap", false, "Enable ConfigMap persistence for ENI cache (survives restarts).")

	// Rate limiting flags
	var awsRateLimitQPS float64
	var awsRateLimitBurst int
	flag.Float64Var(&awsRateLimitQPS, "aws-rate-limit-qps", 10, "AWS API rate limit (requests per second).")
	flag.IntVar(&awsRateLimitBurst, "aws-rate-limit-burst", 20, "AWS API rate limit burst size.")

	// Pprof flag
	var pprofAddr string
	flag.StringVar(&pprofAddr, "pprof-bind-address", "0", "The address the pprof endpoint binds to. Set to '0' to disable.")

	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	if printVersion {
		setupLog.Info("Version Information", "version", version, "commit", commit, "date", date)
		os.Exit(0)
	}

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	setupLog.Info("Starting k8s-eni-tagger", "version", version, "commit", commit, "date", date)

	// Handle Env Var fallback for subnet-ids
	if subnetIDs == "" {
		subnetIDs = os.Getenv("ENI_TAGGER_SUBNET_IDS")
	}

	var parsedSubnetIDs []string
	if subnetIDs != "" {
		parts := strings.Split(subnetIDs, ",")
		for _, p := range parts {
			trimmed := strings.TrimSpace(p)
			if trimmed != "" {
				if !strings.HasPrefix(trimmed, "subnet-") {
					setupLog.Error(nil, "Invalid subnet ID format", "subnet", trimmed)
					os.Exit(1)
				}
				parsedSubnetIDs = append(parsedSubnetIDs, trimmed)
			}
		}
		setupLog.Info("Subnet filtering enabled", "subnets", parsedSubnetIDs)
	}

	if allowSharedENITagging {
		setupLog.Info("WARNING: Shared ENI tagging is enabled. This may cause tag thrashing on standard EKS nodes.")
	}

	// Validate annotation key
	if annotationKey == "" {
		setupLog.Error(nil, "annotation-key cannot be empty")
		os.Exit(1)
	}

	// Start pprof server if enabled
	if pprofAddr != "0" {
		go func() {
			setupLog.Info("Starting pprof server", "addr", pprofAddr)
			if err := http.ListenAndServe(pprofAddr, nil); err != nil {
				setupLog.Error(err, "Failed to start pprof server")
			}
		}()
	}

	mgrOptions := ctrl.Options{
		Scheme:                 scheme,
		Metrics:                server.Options{BindAddress: metricsAddr},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "k8s-eni-tagger.eni-tagger.io",
	}

	if watchNamespace != "" {
		mgrOptions.Cache = cache.Options{
			DefaultNamespaces: map[string]cache.Config{
				watchNamespace: {},
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
		QPS:   awsRateLimitQPS,
		Burst: awsRateLimitBurst,
	}
	awsClient, err := aws.NewClientWithRateLimiter(ctx, rlConfig)
	if err != nil {
		setupLog.Error(err, "unable to create AWS client")
		os.Exit(1)
	}
	setupLog.Info("AWS client initialized with rate limiting", "qps", awsRateLimitQPS, "burst", awsRateLimitBurst)

	// Add health check using the shared EC2 client
	awsChecker := health.NewAWSChecker(awsClient.GetEC2Client())
	if err := mgr.AddReadyzCheck("aws-connectivity", awsChecker.Check); err != nil {
		setupLog.Error(err, "unable to add readiness check")
		os.Exit(1)
	}

	// Initialize ENI cache if enabled
	var eniCache *enicache.ENICache
	if enableENICache {
		eniCache = enicache.NewENICache(awsClient)

		// Add ConfigMap persistence if enabled
		if enableCacheConfigMap {
			// Get namespace from environment or use default
			namespace := os.Getenv("POD_NAMESPACE")
			if namespace == "" {
				namespace = "kube-system"
			}
			cmPersister := enicache.NewConfigMapPersister(mgr.GetClient(), namespace)
			eniCache.WithConfigMapPersister(cmPersister)
			if err := eniCache.LoadFromConfigMap(ctx); err != nil {
				setupLog.Error(err, "Failed to load cache from ConfigMap, starting fresh")
			}
		}

		setupLog.Info("ENI caching enabled (lifecycle-based)", "configMapPersistence", enableCacheConfigMap)
	}

	if err = (&controller.PodReconciler{
		Client:                mgr.GetClient(),
		Scheme:                mgr.GetScheme(),
		AWSClient:             awsClient,
		ENICache:              eniCache,
		Recorder:              mgr.GetEventRecorderFor("k8s-eni-tagger"),
		AnnotationKey:         annotationKey,
		DryRun:                dryRun,
		SubnetIDs:             parsedSubnetIDs,
		AllowSharedENITagging: allowSharedENITagging,
	}).SetupWithManager(mgr, maxConcurrentReconciles); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Pod")
		os.Exit(1)
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
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
