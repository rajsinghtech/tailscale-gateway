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
	"strconv"
	"syscall"

	pb "github.com/envoyproxy/gateway/proto/extension"
	clusterv3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	endpointv3 "github.com/envoyproxy/go-control-plane/envoy/config/endpoint/v3"
	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	gatewayv1alpha1 "github.com/rajsinghtech/tailscale-gateway/api/v1alpha1"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(gatewayv1alpha1.AddToScheme(scheme))
}

// TailscaleExtensionServer implements the Envoy Gateway extension server for Tailscale integration
type TailscaleExtensionServer struct {
	pb.UnimplementedEnvoyGatewayExtensionServer
	client client.Client
	logger *slog.Logger
}

// TailscaleServiceMapping represents a mapping from Tailscale egress services to external backends
type TailscaleServiceMapping struct {
	ServiceName     string // e.g., "web-service"
	ClusterName     string // e.g., "external-backend-web-service"
	ExternalBackend string // The actual backend service this egress connects to
	Port            uint32 // e.g., 80
	Protocol        string // e.g., "HTTP"
}

// NewTailscaleExtensionServer creates a new Tailscale extension server
func NewTailscaleExtensionServer(client client.Client, logger *slog.Logger) *TailscaleExtensionServer {
	return &TailscaleExtensionServer{
		client: client,
		logger: logger,
	}
}

// PostRouteModify modifies individual routes after Envoy Gateway generates them
func (s *TailscaleExtensionServer) PostRouteModify(ctx context.Context, req *pb.PostRouteModifyRequest) (*pb.PostRouteModifyResponse, error) {
	s.logger.Info("PostRouteModify called", "route", req.Route.Name)
	return &pb.PostRouteModifyResponse{Route: req.Route}, nil
}

// PostVirtualHostModify modifies virtual hosts and injects new routes for Tailscale services
func (s *TailscaleExtensionServer) PostVirtualHostModify(ctx context.Context, req *pb.PostVirtualHostModifyRequest) (*pb.PostVirtualHostModifyResponse, error) {
	s.logger.Info("PostVirtualHostModify called", "virtualHost", req.VirtualHost.Name, "domains", req.VirtualHost.Domains)

	// Clone the virtual host to avoid modifying the original
	modifiedVH := proto.Clone(req.VirtualHost).(*routev3.VirtualHost)

	// Get Tailscale egress service mappings that should route to external backends
	serviceMappings, err := s.getTailscaleEgressMappings(ctx)
	if err != nil {
		s.logger.Error("Failed to get Tailscale egress mappings", "error", err)
		return &pb.PostVirtualHostModifyResponse{VirtualHost: modifiedVH}, nil
	}

	// Inject routes for each Tailscale egress service to external backends
	for _, mapping := range serviceMappings {
		route := s.createTailscaleEgressRoute(mapping)
		modifiedVH.Routes = append(modifiedVH.Routes, route)
		s.logger.Info("Injected Tailscale egress route", "service", mapping.ServiceName, "backend", mapping.ExternalBackend)
	}

	s.logger.Info("PostVirtualHostModify completed", "totalRoutes", len(modifiedVH.Routes), "injectedRoutes", len(serviceMappings))
	return &pb.PostVirtualHostModifyResponse{VirtualHost: modifiedVH}, nil
}

// PostHTTPListenerModify modifies HTTP listeners
func (s *TailscaleExtensionServer) PostHTTPListenerModify(ctx context.Context, req *pb.PostHTTPListenerModifyRequest) (*pb.PostHTTPListenerModifyResponse, error) {
	s.logger.Info("PostHTTPListenerModify called", "listener", req.Listener.Name)
	return &pb.PostHTTPListenerModifyResponse{Listener: req.Listener}, nil
}

// PostTranslateModify modifies clusters and secrets in the final xDS configuration
func (s *TailscaleExtensionServer) PostTranslateModify(ctx context.Context, req *pb.PostTranslateModifyRequest) (*pb.PostTranslateModifyResponse, error) {
	s.logger.Info("PostTranslateModify called", "clusters", len(req.Clusters))

	// Copy existing clusters
	clusters := make([]*clusterv3.Cluster, len(req.Clusters))
	copy(clusters, req.Clusters)

	// Generate external backend clusters for Tailscale egress services
	externalClusters, err := s.generateExternalBackendClusters(ctx)
	if err != nil {
		s.logger.Error("Failed to generate external backend clusters", "error", err)
		return &pb.PostTranslateModifyResponse{Clusters: clusters, Secrets: req.Secrets}, nil
	}

	// Add external backend clusters
	clusters = append(clusters, externalClusters...)

	s.logger.Info("PostTranslateModify completed", "totalClusters", len(clusters), "injectedClusters", len(externalClusters))
	return &pb.PostTranslateModifyResponse{Clusters: clusters, Secrets: req.Secrets}, nil
}

// getTailscaleEgressMappings discovers Tailscale egress services that should route to external backends
func (s *TailscaleExtensionServer) getTailscaleEgressMappings(ctx context.Context) ([]TailscaleServiceMapping, error) {
	var mappings []TailscaleServiceMapping

	// Get all TailscaleEndpoints resources
	endpointsList := &gatewayv1alpha1.TailscaleEndpointsList{}
	if err := s.client.List(ctx, endpointsList); err != nil {
		return nil, fmt.Errorf("failed to list TailscaleEndpoints: %w", err)
	}

	// Create mappings from each TailscaleEndpoints resource
	for _, endpoints := range endpointsList.Items {
		for _, endpoint := range endpoints.Spec.Endpoints {
			// Map egress services that route from Tailscale to external backends
			mapping := TailscaleServiceMapping{
				ServiceName:     endpoint.Name,
				ClusterName:     fmt.Sprintf("external-backend-%s", endpoint.Name),
				ExternalBackend: endpoint.ExternalTarget,
				Port:            uint32(endpoint.Port),
				Protocol:        endpoint.Protocol,
			}

			// Only add mappings for endpoints that have external targets defined
			if mapping.ExternalBackend != "" {
				mappings = append(mappings, mapping)
			}
		}
	}

	s.logger.Info("Discovered Tailscale egress mappings", "count", len(mappings))
	return mappings, nil
}

// createTailscaleEgressRoute creates an Envoy route configuration for a Tailscale egress service
func (s *TailscaleExtensionServer) createTailscaleEgressRoute(mapping TailscaleServiceMapping) *routev3.Route {
	return &routev3.Route{
		Name: fmt.Sprintf("tailscale-egress-%s", mapping.ServiceName),
		Match: &routev3.RouteMatch{
			PathSpecifier: &routev3.RouteMatch_Prefix{
				Prefix: fmt.Sprintf("/api/%s", mapping.ServiceName),
			},
		},
		Action: &routev3.Route_Route{
			Route: &routev3.RouteAction{
				ClusterSpecifier: &routev3.RouteAction_Cluster{
					Cluster: mapping.ClusterName,
				},
				// Strip the /api/{service} prefix when forwarding to the external backend
				PrefixRewrite: "/",
			},
		},
	}
}

// generateExternalBackendClusters creates Envoy cluster configurations for external backends
func (s *TailscaleExtensionServer) generateExternalBackendClusters(ctx context.Context) ([]*clusterv3.Cluster, error) {
	var clusters []*clusterv3.Cluster

	// Get egress service mappings
	serviceMappings, err := s.getTailscaleEgressMappings(ctx)
	if err != nil {
		return nil, err
	}

	// Create a cluster for each external backend
	for _, mapping := range serviceMappings {
		cluster := &clusterv3.Cluster{
			Name: mapping.ClusterName,
			ClusterDiscoveryType: &clusterv3.Cluster_Type{
				Type: clusterv3.Cluster_STATIC,
			},
			LoadAssignment: &endpointv3.ClusterLoadAssignment{
				ClusterName: mapping.ClusterName,
				Endpoints: []*endpointv3.LocalityLbEndpoints{{
					LbEndpoints: []*endpointv3.LbEndpoint{{
						HostIdentifier: &endpointv3.LbEndpoint_Endpoint{
							Endpoint: &endpointv3.Endpoint{
								Address: &corev3.Address{
									Address: &corev3.Address_SocketAddress{
										SocketAddress: &corev3.SocketAddress{
											Address: mapping.ExternalBackend, // Points to external service
											PortSpecifier: &corev3.SocketAddress_PortValue{
												PortValue: mapping.Port,
											},
											Protocol: corev3.SocketAddress_TCP,
										},
									},
								},
							},
						},
					}},
				}},
			},
		}
		clusters = append(clusters, cluster)

		s.logger.Info("Generated external backend cluster", "name", mapping.ClusterName, "backend", mapping.ExternalBackend)
	}

	return clusters, nil
}

// StartGRPCServer starts the extension server gRPC server
func (s *TailscaleExtensionServer) StartGRPCServer(addr string) error {
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", addr, err)
	}

	grpcServer := grpc.NewServer()
	pb.RegisterEnvoyGatewayExtensionServer(grpcServer, s)

	s.logger.Info("Starting Tailscale extension server", "address", addr)
	return grpcServer.Serve(lis)
}

func main() {
	var grpcPort int

	flag.IntVar(&grpcPort, "grpc-port", 5005, "The port for the gRPC extension server.")
	flag.Parse()

	// Create structured logger
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	// Override gRPC port from environment if set
	if envPort := os.Getenv("GRPC_PORT"); envPort != "" {
		if port, err := strconv.Atoi(envPort); err == nil {
			grpcPort = port
		}
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme: scheme,
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	// Create the Tailscale Extension Server
	server := NewTailscaleExtensionServer(mgr.GetClient(), logger)

	// Set up context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle shutdown signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Start the Extension Server in a goroutine
	go func() {
		addr := fmt.Sprintf(":%d", grpcPort)
		setupLog.Info("Starting Tailscale Gateway Extension Server", "address", addr)
		if err := server.StartGRPCServer(addr); err != nil {
			setupLog.Error(err, "Extension Server failed to start")
			cancel()
		}
	}()

	// Start the manager in a goroutine
	go func() {
		setupLog.Info("Starting manager")
		if err := mgr.Start(ctx); err != nil {
			setupLog.Error(err, "Manager failed to start")
			cancel()
		}
	}()

	// Wait for shutdown signal
	select {
	case sig := <-sigChan:
		setupLog.Info("Received shutdown signal", "signal", sig)
	case <-ctx.Done():
		setupLog.Info("Context cancelled")
	}

	// Cancel context to trigger graceful shutdown
	cancel()

	setupLog.Info("Tailscale Gateway Extension Server shutting down")
}