// Copyright (c) Tailscale Gateway Authors
// SPDX-License-Identifier: BSD-3-Clause

//go:generate go run ../../tools/generate/main.go

package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/go-logr/zapr"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"
	gwapiv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	gatewayv1alpha1 "github.com/rajsinghtech/tailscale-gateway/api/v1alpha1"
	"github.com/rajsinghtech/tailscale-gateway/internal/config"
	"github.com/rajsinghtech/tailscale-gateway/internal/controller"
	"github.com/rajsinghtech/tailscale-gateway/internal/extension"
	"github.com/rajsinghtech/tailscale-gateway/internal/proxy"
	//+kubebuilder:scaffold:imports
)

var (
	scheme = runtime.NewScheme()
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(gatewayv1alpha1.AddToScheme(scheme))
	utilruntime.Must(gwapiv1.Install(scheme))
	utilruntime.Must(gwapiv1alpha2.Install(scheme))
	//+kubebuilder:scaffold:scheme
}

func main() {
	// Create configuration with defaults
	cfg := config.NewOperatorConfig()

	// Parse command line flags
	var (
		metricsAddr           = flag.String("metrics-bind-address", cfg.GetMetricsAddress(), "The address the metric endpoint binds to.")
		probeAddr             = flag.String("health-probe-bind-address", cfg.GetHealthProbeAddress(), "The address the probe endpoint binds to.")
		enableLeaderElection  = flag.Bool("leader-elect", cfg.LeaderElection, "Enable leader election for controller manager.")
		secureMetrics         = flag.Bool("metrics-secure", false, "If set the metrics endpoint is served securely")
		enableHTTP2           = flag.Bool("enable-http2", false, "If set, HTTP/2 will be enabled for the metrics and webhook servers")
		logLevel              = flag.String("log-level", cfg.LogLevel, "Log level (debug, info, warn, error)")
		extensionGrpcPort     = flag.Int("extension-grpc-port", 5005, "Port for the integrated extension server")
		enableExtensionServer = flag.Bool("enable-extension-server", true, "Enable the integrated extension server")
	)
	flag.Parse()

	// Load configuration from environment
	if err := cfg.LoadFromEnvironment(); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load configuration: %v\n", err)
		os.Exit(1)
	}

	// Validate configuration
	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "Invalid configuration: %v\n", err)
		os.Exit(1)
	}

	// Setup structured logging
	rawLogger, err := setupLogger(*logLevel)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize logger: %v\n", err)
		os.Exit(1)
	}
	defer rawLogger.Sync()

	ctrl.SetLogger(zapr.NewLogger(rawLogger))
	logger := rawLogger.Sugar()

	setupLog := logger.Named("setup")

	// Setup webhook server options
	webhookOpts := webhook.Options{}
	if !*enableHTTP2 {
		webhookOpts.TLSOpts = []func(*tls.Config){
			func(cfg *tls.Config) {
				cfg.NextProtos = []string{"http/1.1"}
			},
		}
	}

	// Setup manager
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress:   *metricsAddr,
			SecureServing: *secureMetrics,
		},
		WebhookServer:          webhook.NewServer(webhookOpts),
		HealthProbeBindAddress: *probeAddr,
		LeaderElection:         *enableLeaderElection,
		LeaderElectionID:       "tailscale-gateway-operator",
	})
	if err != nil {
		setupLog.Error("unable to start manager", "error", err)
		os.Exit(1)
	}


	// Setup controllers
	if err = (&controller.TailscaleTailnetReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Logger:   logger.Named("tailnet-controller"),
		Recorder: mgr.GetEventRecorderFor("tailscale-tailnet-controller"),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error("unable to create controller", "controller", "TailscaleTailnet", "error", err)
		os.Exit(1)
	}


	// Setup TailscaleGateway controller (simplified)
	if err = (&controller.TailscaleGatewayReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Logger:   logger.Named("gateway-controller"),
		Recorder: mgr.GetEventRecorderFor("tailscale-gateway-controller"),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error("unable to create controller", "controller", "TailscaleGateway", "error", err)
		os.Exit(1)
	}



	// Setup simplified TailscaleService controller (singular)
	tailscaleServiceController := &controller.TailscaleServiceReconciler{
		Client:       mgr.GetClient(),
		Scheme:       mgr.GetScheme(),
		TsClient:     nil, // Will be initialized when first needed
		ManagerID:    "tailscale-gateway-operator",
		ProxyBuilder: proxy.NewProxyBuilder(mgr.GetClient()),
	}

	if err = tailscaleServiceController.SetupWithManager(mgr); err != nil {
		setupLog.Error("unable to create controller", "controller", "TailscaleService", "error", err)
		os.Exit(1)
	}

	//+kubebuilder:scaffold:builder

	// Setup health checks
	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error("unable to set up health check", "error", err)
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error("unable to set up ready check", "error", err)
		os.Exit(1)
	}

	// Start integrated extension server if enabled
	if *enableExtensionServer {
		go func() {
			extensionAddr := fmt.Sprintf(":%d", *extensionGrpcPort)
			extensionLogger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
			setupLog.Info("starting integrated extension server", "address", extensionAddr)
			
			if err := extension.StartServer(extensionAddr, mgr.GetClient(), extensionLogger); err != nil {
				setupLog.Error("extension server failed", "error", err)
			}
		}()
	}

	setupLog.Info("starting tailscale-gateway operator")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error("problem running manager", "error", err)
		os.Exit(1)
	}
}

// setupLogger creates a structured logger based on the log level
func setupLogger(level string) (*zap.Logger, error) {
	var zapLevel zapcore.Level
	switch level {
	case "debug":
		zapLevel = zapcore.DebugLevel
	case "info":
		zapLevel = zapcore.InfoLevel
	case "warn":
		zapLevel = zapcore.WarnLevel
	case "error":
		zapLevel = zapcore.ErrorLevel
	default:
		zapLevel = zapcore.InfoLevel
	}

	config := zap.NewProductionConfig()
	config.Level.SetLevel(zapLevel)
	config.EncoderConfig.TimeKey = "timestamp"
	config.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder

	logger, err := config.Build()
	if err != nil {
		return nil, fmt.Errorf("failed to build logger: %w", err)
	}

	return logger, nil
}
