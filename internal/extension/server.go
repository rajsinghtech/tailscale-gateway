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

package extension

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	pb "github.com/envoyproxy/gateway/proto/extension"
	clusterv3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	endpointv3 "github.com/envoyproxy/go-control-plane/envoy/config/endpoint/v3"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/durationpb"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	gatewayv1alpha1 "github.com/rajsinghtech/tailscale-gateway/api/v1alpha1"
)

// TailscaleExtensionServer implements the Envoy Gateway extension server for Tailscale integration
type TailscaleExtensionServer struct {
	pb.UnimplementedEnvoyGatewayExtensionServer
	client client.Client
	logger *slog.Logger
	mu     sync.RWMutex
}

// TailscaleServiceMapping represents a simplified TailscaleService backend mapping
type TailscaleServiceMapping struct {
	ServiceName        string             // e.g., "web-service"
	ServiceNamespace   string             // e.g., "default"
	ClusterName        string             // e.g., "tailscale-service-default-web-service"
	VIPServiceName     string             // e.g., "svc:web-service"
	BackendAddresses   []string           // VIP service addresses
	HTTPRoute          *gwapiv1.HTTPRoute // Associated HTTPRoute
}

// NewTailscaleExtensionServer creates a new TailscaleExtensionServer
func NewTailscaleExtensionServer(client client.Client, logger *slog.Logger) *TailscaleExtensionServer {
	return &TailscaleExtensionServer{
		client: client,
		logger: logger,
	}
}

// PostTranslateModify modifies the xDS configuration after translation
func (s *TailscaleExtensionServer) PostTranslateModify(ctx context.Context, req *pb.PostTranslateModifyRequest) (*pb.PostTranslateModifyResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.logger.Info("PostTranslateModify called", "clusters", len(req.GetClusters()))

	// Discover TailscaleService backends from HTTPRoutes
	if err := s.discoverAndInjectTailscaleServices(ctx, req); err != nil {
		s.logger.Error("Failed to inject TailscaleService clusters", "error", err)
		return &pb.PostTranslateModifyResponse{}, err
	}

	return &pb.PostTranslateModifyResponse{
		Clusters: req.GetClusters(),
	}, nil
}

// discoverAndInjectTailscaleServices discovers TailscaleService backends and injects clusters
func (s *TailscaleExtensionServer) discoverAndInjectTailscaleServices(ctx context.Context, req *pb.PostTranslateModifyRequest) error {
	// List all HTTPRoutes
	httpRoutes := &gwapiv1.HTTPRouteList{}
	if err := s.client.List(ctx, httpRoutes); err != nil {
		return fmt.Errorf("failed to list HTTPRoutes: %w", err)
	}

	for _, route := range httpRoutes.Items {
		if err := s.processHTTPRoute(ctx, &route, req); err != nil {
			s.logger.Error("Failed to process HTTPRoute", "route", route.Name, "namespace", route.Namespace, "error", err)
		}
	}

	return nil
}

// processHTTPRoute processes an HTTPRoute for TailscaleService backends
func (s *TailscaleExtensionServer) processHTTPRoute(ctx context.Context, route *gwapiv1.HTTPRoute, req *pb.PostTranslateModifyRequest) error {
	for _, rule := range route.Spec.Rules {
		for _, backendRef := range rule.BackendRefs {
			// Check if this is a TailscaleService backend
			if backendRef.Group != nil && *backendRef.Group == "gateway.tailscale.com" &&
				backendRef.Kind != nil && *backendRef.Kind == "TailscaleService" {

				if err := s.processTailscaleServiceBackend(ctx, &backendRef, route, req); err != nil {
					s.logger.Error("Failed to process TailscaleService backend", "error", err)
				}
			}
		}
	}
	return nil
}

// processTailscaleServiceBackend processes a TailscaleService backend
func (s *TailscaleExtensionServer) processTailscaleServiceBackend(ctx context.Context, backendRef *gwapiv1.HTTPBackendRef, route *gwapiv1.HTTPRoute, req *pb.PostTranslateModifyRequest) error {
	// Get the TailscaleService
	namespace := route.Namespace
	if backendRef.Namespace != nil {
		namespace = string(*backendRef.Namespace)
	}

	tailscaleService := &gatewayv1alpha1.TailscaleService{}
	key := types.NamespacedName{
		Name:      string(backendRef.Name),
		Namespace: namespace,
	}

	if err := s.client.Get(ctx, key, tailscaleService); err != nil {
		return fmt.Errorf("failed to get TailscaleService %s: %w", key, err)
	}

	// Create cluster for VIP service
	clusterName := fmt.Sprintf("tailscale-service-%s-%s", namespace, tailscaleService.Name)
	cluster := s.createTailscaleServiceCluster(tailscaleService, clusterName)
	
	// Add cluster to the request
	req.Clusters = append(req.Clusters, cluster)

	s.logger.Info("Added TailscaleService cluster", 
		"cluster", clusterName, 
		"service", tailscaleService.Name,
		"vipService", tailscaleService.Spec.VIPService.Name)

	return nil
}

// createTailscaleServiceCluster creates an Envoy cluster for a TailscaleService
func (s *TailscaleExtensionServer) createTailscaleServiceCluster(tailscaleService *gatewayv1alpha1.TailscaleService, clusterName string) *clusterv3.Cluster {
	// For TailscaleService, we create a cluster that points to the VIP service
	// In a real implementation, you would resolve the VIP service addresses
	
	// Use placeholder addresses for now - in production this would resolve actual VIP addresses
	vipServiceName := tailscaleService.Spec.VIPService.Name
	if vipServiceName == "" {
		vipServiceName = fmt.Sprintf("svc:%s", tailscaleService.Name)
	}

	// Create endpoints - for simplicity, use a placeholder address
	// In production, this would resolve the actual VIP service addresses
	endpoints := []*endpointv3.LocalityLbEndpoints{
		{
			LbEndpoints: []*endpointv3.LbEndpoint{
				{
					HostIdentifier: &endpointv3.LbEndpoint_Endpoint{
						Endpoint: &endpointv3.Endpoint{
							Address: &corev3.Address{
								Address: &corev3.Address_SocketAddress{
									SocketAddress: &corev3.SocketAddress{
										Address: "100.100.100.1", // Placeholder VIP address
										PortSpecifier: &corev3.SocketAddress_PortValue{
											PortValue: 80, // Default port
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	cluster := &clusterv3.Cluster{
		Name: clusterName,
		ClusterDiscoveryType: &clusterv3.Cluster_Type{
			Type: clusterv3.Cluster_STATIC,
		},
		LoadAssignment: &endpointv3.ClusterLoadAssignment{
			ClusterName: clusterName,
			Endpoints:   endpoints,
		},
		ConnectTimeout: durationpb.New(5 * time.Second),
	}

	return cluster
}


// StartServer starts the extension server
func StartServer(address string, client client.Client, logger *slog.Logger) error {
	lis, err := net.Listen("tcp", address)
	if err != nil {
		return fmt.Errorf("failed to listen: %w", err)
	}

	server := NewTailscaleExtensionServer(client, logger)
	
	grpcServer := grpc.NewServer()
	pb.RegisterEnvoyGatewayExtensionServer(grpcServer, server)

	logger.Info("Starting Tailscale extension server", "address", address)

	// Start HTTP metrics server
	go func() {
		if err := startMetricsServer(":8080", server, logger); err != nil {
			logger.Error("Failed to start metrics server", "error", err)
		}
	}()

	return grpcServer.Serve(lis)
}

// startMetricsServer starts the HTTP metrics server
func startMetricsServer(address string, server *TailscaleExtensionServer, logger *slog.Logger) error {
	mux := http.NewServeMux()
	
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})
	
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status": "healthy", "uptime": "` + time.Since(time.Now()).String() + `"}`))
	})

	logger.Info("Starting HTTP metrics server", "address", address)
	return http.ListenAndServe(address, mux)
}

// Required implementations for the interface (simplified)
func (s *TailscaleExtensionServer) PostVirtualHostModify(ctx context.Context, req *pb.PostVirtualHostModifyRequest) (*pb.PostVirtualHostModifyResponse, error) {
	return &pb.PostVirtualHostModifyResponse{
		VirtualHost: req.GetVirtualHost(),
	}, nil
}

func (s *TailscaleExtensionServer) PostRouteModify(ctx context.Context, req *pb.PostRouteModifyRequest) (*pb.PostRouteModifyResponse, error) {
	return &pb.PostRouteModifyResponse{
		Route: req.GetRoute(),
	}, nil
}

func (s *TailscaleExtensionServer) PostHTTPListenerModify(ctx context.Context, req *pb.PostHTTPListenerModifyRequest) (*pb.PostHTTPListenerModifyResponse, error) {
	return &pb.PostHTTPListenerModifyResponse{
		Listener: req.GetListener(),
	}, nil
}