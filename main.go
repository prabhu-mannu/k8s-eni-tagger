package main

import (
	"context"
	"flag"
	"net/http"
	_ "net/http/pprof" // Register pprof handlers
	"os"
	"strings"

	"k8s-eni-tagger/pkg/aws"
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

	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
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
				parsedSubnetIDs = append(parsedSubnetIDs, trimmed)
			}
		}
		setupLog.Info("Subnet filtering enabled", "subnets", parsedSubnetIDs)
	}

	if allowSharedENITagging {
		setupLog.Info("WARNING: Shared ENI tagging is enabled. This may cause tag thrashing on standard EKS nodes.")
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

	awsClient, err := aws.NewClient(context.TODO())
	if err != nil {
		setupLog.Error(err, "unable to create AWS client")
		os.Exit(1)
	}

	// Add health check
	awsChecker, err := health.NewAWSChecker(context.TODO())
	if err != nil {
		setupLog.Error(err, "unable to create AWS health checker")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("aws-connectivity", awsChecker.Check); err != nil {
		setupLog.Error(err, "unable to add readiness check")
		os.Exit(1)
	}

	if err = (&controller.PodReconciler{
		Client:                mgr.GetClient(),
		Scheme:                mgr.GetScheme(),
		AWSClient:             awsClient,
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
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
