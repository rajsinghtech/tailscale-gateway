/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-logr/zapr"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	pb "github.com/envoyproxy/gateway/proto/extension"
	gatewayv1alpha1 "github.com/rajsinghtech/tailscale-gateway/api/v1alpha1"
	"github.com/rajsinghtech/tailscale-gateway/internal/extension"
)

var (
	scheme = runtime.NewScheme()
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(gatewayv1alpha1.AddToScheme(scheme))
}

func main() {
	var (
		grpcPort              = flag.Int("grpc-port", 5005, "Port for the gRPC extension server")
		healthProbeBindAddr   = flag.String("health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
		metricsBindAddr       = flag.String("metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
		logLevel              = flag.String("log-level", "info", "Log level (debug, info, warn, error)")
		enableLeaderElection  = flag.Bool("leader-elect", false, "Enable leader election for controller manager.")
	)
	flag.Parse()

	// Setup structured logging
	rawLogger := setupLogger(*logLevel)
	defer rawLogger.Sync()

	ctrl.SetLogger(zapr.NewLogger(rawLogger))
	logger := rawLogger.Sugar()
	setupLog := logger.Named("setup")

	// Setup Kubernetes client for reading CRDs
	config := ctrl.GetConfigOrDie()
	kubeClient, err := client.New(config, client.Options{
		Scheme: scheme,
	})
	if err != nil {
		setupLog.Error("Failed to create Kubernetes client", "error", err)
		os.Exit(1)
	}

	// Create structured logger for extension server
	structuredLogger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: parseLogLevel(*logLevel),
	}))

	// Create extension server
	extensionServer := extension.NewTailscaleExtensionServer(kubeClient, structuredLogger)

	// Setup metrics server (in a separate goroutine)
	mgr, err := ctrl.NewManager(config, ctrl.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress: *metricsBindAddr,
		},
		HealthProbeBindAddress: *healthProbeBindAddr,
		LeaderElection:         *enableLeaderElection,
		LeaderElectionID:       "tailscale-gateway-extension-server",
	})
	if err != nil {
		setupLog.Error("Failed to start manager", "error", err)
		os.Exit(1)
	}

	// Add health checks
	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error("Unable to set up health check", "error", err)
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error("Unable to set up ready check", "error", err)
		os.Exit(1)
	}

	// Start metrics server in background
	go func() {
		setupLog.Info("Starting metrics server")
		if err := mgr.Start(context.Background()); err != nil {
			setupLog.Error("Problem running metrics manager", "error", err)
		}
	}()

	// Setup gRPC server
	grpcServer := grpc.NewServer()
	pb.RegisterEnvoyGatewayExtensionServer(grpcServer, extensionServer)

	// Add health service
	healthService := health.NewServer()
	grpc_health_v1.RegisterHealthServer(grpcServer, healthService)
	healthService.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)

	// Health checks are handled by controller-runtime manager

	// Listen on gRPC port
	grpcAddr := fmt.Sprintf(":%d", *grpcPort)
	lis, err := net.Listen("tcp", grpcAddr)
	if err != nil {
		setupLog.Error("Failed to listen on gRPC port", "port", *grpcPort, "error", err)
		os.Exit(1)
	}

	// Setup graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-quit
		setupLog.Info("Shutting down extension server...")
		
		// Stop health service
		healthService.SetServingStatus("", grpc_health_v1.HealthCheckResponse_NOT_SERVING)
		
		// Graceful shutdown with timeout
		stopped := make(chan struct{})
		go func() {
			grpcServer.GracefulStop()
			close(stopped)
		}()
		
		select {
		case <-stopped:
			setupLog.Info("Extension server stopped gracefully")
		case <-time.After(30 * time.Second):
			setupLog.Warn("Extension server shutdown timeout, forcing stop")
			grpcServer.Stop()
		}
	}()

	setupLog.Info("Starting Tailscale Extension Server", 
		"grpcPort", *grpcPort,
		"healthPort", *healthProbeBindAddr,
		"metricsPort", *metricsBindAddr)

	// Start gRPC server
	if err := grpcServer.Serve(lis); err != nil {
		setupLog.Error("Extension server failed", "error", err)
		os.Exit(1)
	}
}

func setupLogger(level string) *zap.Logger {
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
	config.Development = false
	config.Sampling = nil

	logger, err := config.Build()
	if err != nil {
		panic(fmt.Sprintf("Failed to initialize logger: %v", err))
	}

	return logger
}

func parseLogLevel(level string) slog.Level {
	switch level {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}