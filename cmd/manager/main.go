package main

import (
	"flag"
	"net/http"
	"os"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	zeusv1 "github.com/zeus-broadcast/zeus-kernel/api/v1alpha1"
	"github.com/zeus-broadcast/zeus-kernel/internal/controller"
	"github.com/zeus-broadcast/zeus-kernel/internal/fabric"
	"github.com/zeus-broadcast/zeus-kernel/internal/nmos"
	"github.com/zeus-broadcast/zeus-kernel/internal/transport/hitless"
	"github.com/zeus-broadcast/zeus-kernel/internal/transport/srt"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(zeusv1.AddToScheme(scheme))
}

func main() {
	var metricsAddr, probeAddr string
	var enableLeaderElection bool
	var nmosRegistryURL, nmosTokenURL, nmosClientID, nmosClientSecret string
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false, "")
	flag.StringVar(&nmosRegistryURL, "nmos-registry-url", "", "NMOS IS-04 registry base URL (e.g. http://nmos-registry.nmos-system.svc.cluster.local)")
	flag.StringVar(&nmosTokenURL, "nmos-token-url", "", "OAuth2 token endpoint for IS-10 authorization")
	flag.StringVar(&nmosClientID, "nmos-client-id", "", "OAuth2 client ID")
	flag.StringVar(&nmosClientSecret, "nmos-client-secret", "", "OAuth2 client secret")
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: metricsAddr},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "zeus.broadcast",
		WebhookServer:          webhook.NewServer(webhook.Options{Port: 9443}),
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	var nmosClient *nmos.Client
	if nmosRegistryURL != "" {
		nmosClient = &nmos.Client{
			BaseURL:      nmosRegistryURL,
			TokenURL:     nmosTokenURL,
			ClientID:     nmosClientID,
			ClientSecret: nmosClientSecret,
			HTTP:         &http.Client{Timeout: 10 * time.Second},
		}
		setupLog.Info("nmos client configured", "registry", nmosRegistryURL)
	}

	if err := (&controller.ZeusApplicationPackageReconciler{
		Client: mgr.GetClient(), Scheme: mgr.GetScheme(),
		NMOS: nmosClient,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create NAP controller")
		os.Exit(1)
	}

	if err := (&controller.ZeusRunbookReconciler{
		Client: mgr.GetClient(), Scheme: mgr.GetScheme(),
		Fabric: fabric.NewRealClient(mgr.GetClient()),
		BridgeFactory: srt.NewBridge,
		HitlessMerger: hitless.NewMerger(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create Runbook controller")
		os.Exit(1)
	}

	if err := (&controller.ZeusApplicationPackageReconciler{
		Client: mgr.GetClient(), Scheme: mgr.GetScheme(),
		NMOS: nmosClient,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create NAP controller")
		os.Exit(1)
	}

	if err := (&controller.NAPRegistryReconciler{
		Client: mgr.GetClient(), Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create NAP registry controller")
		os.Exit(1)
	}

	if err := (&controller.RunbookTemplateReconciler{
		Client: mgr.GetClient(), Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create RunbookTemplate controller")
		os.Exit(1)
	}

	if err := (&zeusv1.ZeusRunbook{}).SetupWebhookWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create webhook")
		os.Exit(1)
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
