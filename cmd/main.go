package main

import (
	"flag"
	"os"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	forgev1 "github.com/forgeplatform/forge-operator/api/v1alpha1"
	"github.com/forgeplatform/forge-operator/internal/controller"
	"github.com/forgeplatform/forge-operator/internal/forgeapi"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(forgev1.AddToScheme(scheme))
}

func main() {
	var (
		metricsAddr          string
		probeAddr            string
		enableLeaderElection bool
		forgeURL        string
		forgeToken      string
		forgeHostHeader string
		forgeInsecure   bool
	)
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "Metrics endpoint")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "Health probe endpoint")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false, "Enable leader election")
	flag.StringVar(&forgeURL, "forge-url", os.Getenv("FORGE_URL"), "Forge API base URL (e.g. https://forge-web.forge.svc.cluster.local:8013)")
	flag.StringVar(&forgeToken, "forge-token", os.Getenv("FORGE_TOKEN"), "Forge OAuth2 personal access token (Bearer)")
	flag.StringVar(&forgeHostHeader, "forge-host-header", os.Getenv("FORGE_HOST_HEADER"), "Host header to send (when reaching Forge via host-routed Ingress)")
	flag.BoolVar(&forgeInsecure, "forge-insecure-skip-verify", os.Getenv("FORGE_INSECURE") == "true", "Skip TLS verify on Forge API (test only)")
	opts := zap.Options{Development: true}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	if forgeURL == "" || forgeToken == "" {
		setupLog.Error(nil, "missing Forge API config — set --forge-url and --forge-token (or FORGE_URL / FORGE_TOKEN env)")
		os.Exit(1)
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: metricsAddr},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "forge-operator-leader.forge.forgeplatform.io",
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	forgeClient := forgeapi.New(forgeURL, forgeToken, forgeHostHeader, forgeInsecure)

	if err := (&controller.JobTemplateReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
		Forge:  forgeClient,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to set up JobTemplate controller")
		os.Exit(1)
	}

	if err := (&controller.InventoryReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
		Forge:  forgeClient,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to set up Inventory controller")
		os.Exit(1)
	}

	if err := (&controller.CredentialReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
		Forge:  forgeClient,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to set up Credential controller")
		os.Exit(1)
	}

	if err := (&controller.ScheduleReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
		Forge:  forgeClient,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to set up Schedule controller")
		os.Exit(1)
	}

	if err := (&controller.ProjectReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
		Forge:  forgeClient,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to set up Project controller")
		os.Exit(1)
	}

	if err := (&controller.OrganizationReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
		Forge:  forgeClient,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to set up Organization controller")
		os.Exit(1)
	}

	if err := (&controller.TeamReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
		Forge:  forgeClient,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to set up Team controller")
		os.Exit(1)
	}

	if err := (&controller.WorkflowReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
		Forge:  forgeClient,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to set up Workflow controller")
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

	setupLog.Info("starting manager", "forgeURL", forgeURL)
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "manager exited with error")
		os.Exit(1)
	}
}
