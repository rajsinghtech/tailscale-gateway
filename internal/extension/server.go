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
	gwapiv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	gatewayv1alpha1 "github.com/rajsinghtech/tailscale-gateway/api/v1alpha1"
)

// TailscaleExtensionServer implements the Envoy Gateway extension server for Tailscale integration
type TailscaleExtensionServer struct {
	pb.UnimplementedEnvoyGatewayExtensionServer
	client client.Client
	logger *slog.Logger
	mu     sync.RWMutex
	gatewayRegistry map[string]*gatewayv1alpha1.TailscaleGateway // Track which gateways to process
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
	server := &TailscaleExtensionServer{
		client:          client,
		logger:          logger,
		gatewayRegistry: make(map[string]*gatewayv1alpha1.TailscaleGateway),
	}
	
	// Start watching TailscaleGateway resources to populate registry
	go server.watchTailscaleGateways(context.Background())
	
	return server
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
	// Process all Gateway API route types that support backendRefs
	
	// 1. HTTPRoutes (v1)
	if err := s.processHTTPRoutes(ctx, req); err != nil {
		s.logger.Error("Failed to process HTTPRoutes", "error", err)
	}
	
	// 2. GRPCRoutes (v1)
	if err := s.processGRPCRoutes(ctx, req); err != nil {
		s.logger.Error("Failed to process GRPCRoutes", "error", err)
	}
	
	// 3. TCPRoutes (v1alpha2)
	if err := s.processTCPRoutes(ctx, req); err != nil {
		s.logger.Error("Failed to process TCPRoutes", "error", err)
	}
	
	// 4. UDPRoutes (v1alpha2)
	if err := s.processUDPRoutes(ctx, req); err != nil {
		s.logger.Error("Failed to process UDPRoutes", "error", err)
	}
	
	// 5. TLSRoutes (v1alpha2)
	if err := s.processTLSRoutes(ctx, req); err != nil {
		s.logger.Error("Failed to process TLSRoutes", "error", err)
	}

	return nil
}

// processHTTPRoutes processes all HTTPRoutes for TailscaleService backends
func (s *TailscaleExtensionServer) processHTTPRoutes(ctx context.Context, req *pb.PostTranslateModifyRequest) error {
	httpRoutes := &gwapiv1.HTTPRouteList{}
	if err := s.client.List(ctx, httpRoutes); err != nil {
		return fmt.Errorf("failed to list HTTPRoutes: %w", err)
	}

	for _, route := range httpRoutes.Items {
		if !s.shouldProcessRoute(route.Spec.CommonRouteSpec) {
			continue
		}
		
		routeInfo := &RouteWrapper{Name: route.Name, Namespace: route.Namespace}
		for _, rule := range route.Spec.Rules {
			for _, backendRef := range rule.BackendRefs {
				if err := s.processTailscaleServiceBackendRef(ctx, backendRef.BackendRef, routeInfo, req, "http"); err != nil {
					s.logger.Error("Failed to process HTTPRoute TailscaleService backend", "route", route.Name, "error", err)
				}
			}
		}
	}
	return nil
}

// processGRPCRoutes processes all GRPCRoutes for TailscaleService backends
func (s *TailscaleExtensionServer) processGRPCRoutes(ctx context.Context, req *pb.PostTranslateModifyRequest) error {
	grpcRoutes := &gwapiv1.GRPCRouteList{}
	if err := s.client.List(ctx, grpcRoutes); err != nil {
		return fmt.Errorf("failed to list GRPCRoutes: %w", err)
	}

	for _, route := range grpcRoutes.Items {
		if !s.shouldProcessRoute(route.Spec.CommonRouteSpec) {
			continue
		}
		
		routeInfo := &RouteWrapper{Name: route.Name, Namespace: route.Namespace}
		for _, rule := range route.Spec.Rules {
			for _, backendRef := range rule.BackendRefs {
				if err := s.processTailscaleServiceBackendRef(ctx, backendRef.BackendRef, routeInfo, req, "grpc"); err != nil {
					s.logger.Error("Failed to process GRPCRoute TailscaleService backend", "route", route.Name, "error", err)
				}
			}
		}
	}
	return nil
}

// processTCPRoutes processes all TCPRoutes for TailscaleService backends
func (s *TailscaleExtensionServer) processTCPRoutes(ctx context.Context, req *pb.PostTranslateModifyRequest) error {
	tcpRoutes := &gwapiv1alpha2.TCPRouteList{}
	if err := s.client.List(ctx, tcpRoutes); err != nil {
		return fmt.Errorf("failed to list TCPRoutes: %w", err)
	}

	for _, route := range tcpRoutes.Items {
		if !s.shouldProcessRoute(route.Spec.CommonRouteSpec) {
			continue
		}
		
		routeInfo := &RouteWrapper{Name: route.Name, Namespace: route.Namespace}
		for _, rule := range route.Spec.Rules {
			for _, backendRef := range rule.BackendRefs {
				if err := s.processTailscaleServiceBackendRef(ctx, backendRef, routeInfo, req, "tcp"); err != nil {
					s.logger.Error("Failed to process TCPRoute TailscaleService backend", "route", route.Name, "error", err)
				}
			}
		}
	}
	return nil
}

// processUDPRoutes processes all UDPRoutes for TailscaleService backends
func (s *TailscaleExtensionServer) processUDPRoutes(ctx context.Context, req *pb.PostTranslateModifyRequest) error {
	udpRoutes := &gwapiv1alpha2.UDPRouteList{}
	if err := s.client.List(ctx, udpRoutes); err != nil {
		return fmt.Errorf("failed to list UDPRoutes: %w", err)
	}

	for _, route := range udpRoutes.Items {
		if !s.shouldProcessRoute(route.Spec.CommonRouteSpec) {
			continue
		}
		
		routeInfo := &RouteWrapper{Name: route.Name, Namespace: route.Namespace}
		for _, rule := range route.Spec.Rules {
			for _, backendRef := range rule.BackendRefs {
				if err := s.processTailscaleServiceBackendRef(ctx, backendRef, routeInfo, req, "udp"); err != nil {
					s.logger.Error("Failed to process UDPRoute TailscaleService backend", "route", route.Name, "error", err)
				}
			}
		}
	}
	return nil
}

// processTLSRoutes processes all TLSRoutes for TailscaleService backends
func (s *TailscaleExtensionServer) processTLSRoutes(ctx context.Context, req *pb.PostTranslateModifyRequest) error {
	tlsRoutes := &gwapiv1alpha2.TLSRouteList{}
	if err := s.client.List(ctx, tlsRoutes); err != nil {
		return fmt.Errorf("failed to list TLSRoutes: %w", err)
	}

	for _, route := range tlsRoutes.Items {
		if !s.shouldProcessRoute(route.Spec.CommonRouteSpec) {
			continue
		}
		
		routeInfo := &RouteWrapper{Name: route.Name, Namespace: route.Namespace}
		for _, rule := range route.Spec.Rules {
			for _, backendRef := range rule.BackendRefs {
				if err := s.processTailscaleServiceBackendRef(ctx, backendRef, routeInfo, req, "tls"); err != nil {
					s.logger.Error("Failed to process TLSRoute TailscaleService backend", "route", route.Name, "error", err)
				}
			}
		}
	}
	return nil
}

// RouteInfo interface for common route information
type RouteInfo interface {
	GetName() string
	GetNamespace() string
}

// RouteWrapper wraps different route types to provide common interface
type RouteWrapper struct {
	Name      string
	Namespace string
}

func (r *RouteWrapper) GetName() string      { return r.Name }
func (r *RouteWrapper) GetNamespace() string { return r.Namespace }

// shouldProcessRoute determines if a route should be processed based on TailscaleGateway configuration
func (s *TailscaleExtensionServer) shouldProcessRoute(commonSpec gwapiv1.CommonRouteSpec) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// If no TailscaleGateway resources exist, process everything (backward compatibility)
	if len(s.gatewayRegistry) == 0 {
		return true
	}

	// Check if the route references any gateway that has a corresponding TailscaleGateway
	for _, parentRef := range commonSpec.ParentRefs {
		// Default namespace handling would need route namespace context
		gatewayKey := fmt.Sprintf("%s/%s", "default", string(parentRef.Name)) // Simplified - would need actual namespace
		if _, exists := s.gatewayRegistry[gatewayKey]; exists {
			return true
		}
	}

	return false
}

// processTailscaleServiceBackendRef processes a TailscaleService backend reference
func (s *TailscaleExtensionServer) processTailscaleServiceBackendRef(ctx context.Context, backendRef gwapiv1.BackendRef, route RouteInfo, req *pb.PostTranslateModifyRequest, protocol string) error {
	// Check if this is a TailscaleService backend
	if backendRef.Group != nil && *backendRef.Group == "gateway.tailscale.com" &&
		backendRef.Kind != nil && *backendRef.Kind == "TailscaleService" {

		// Get the TailscaleService
		namespace := route.GetNamespace()
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

		// Create cluster for VIP service with protocol-specific naming
		clusterName := fmt.Sprintf("tailscale-service-%s-%s-%s", protocol, namespace, tailscaleService.Name)
		cluster := s.createTailscaleServiceCluster(tailscaleService, clusterName)
		
		// Add cluster to the request
		req.Clusters = append(req.Clusters, cluster)

		s.logger.Info("Added TailscaleService cluster", 
			"cluster", clusterName, 
			"service", tailscaleService.Name,
			"protocol", protocol,
			"route", route.GetName(),
			"vipService", tailscaleService.Spec.VIPService.Name)
	}

	return nil
}

// createTailscaleServiceCluster creates an Envoy cluster for a TailscaleService
func (s *TailscaleExtensionServer) createTailscaleServiceCluster(tailscaleService *gatewayv1alpha1.TailscaleService, clusterName string) *clusterv3.Cluster {
	// Resolve endpoints from TailscaleService backends or VIP service status
	endpoints := s.resolveTailscaleServiceEndpoints(tailscaleService)
	
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

// resolveTailscaleServiceEndpoints resolves actual endpoints from TailscaleService
func (s *TailscaleExtensionServer) resolveTailscaleServiceEndpoints(tailscaleService *gatewayv1alpha1.TailscaleService) []*endpointv3.LocalityLbEndpoints {
	var lbEndpoints []*endpointv3.LbEndpoint

	// First, try to use VIP service addresses from status if available
	if tailscaleService.Status.VIPServiceStatus != nil && len(tailscaleService.Status.VIPServiceStatus.Addresses) > 0 {
		for _, addr := range tailscaleService.Status.VIPServiceStatus.Addresses {
			// Parse address (assuming format "ip:port" or just "ip")
			host, port := s.parseAddress(addr)
			if host != "" {
				lbEndpoints = append(lbEndpoints, s.createLbEndpoint(host, port))
			}
		}
	}

	// If no VIP addresses available, fall back to configured backends
	if len(lbEndpoints) == 0 {
		for _, backend := range tailscaleService.Spec.Backends {
			switch backend.Type {
			case "kubernetes":
				if backend.Service != "" {
					host, port := s.parseServiceReference(backend.Service)
					if host != "" {
						lbEndpoints = append(lbEndpoints, s.createLbEndpoint(host, port))
					}
				}
			case "external":
				if backend.Address != "" {
					host, port := s.parseAddress(backend.Address)
					if host != "" {
						lbEndpoints = append(lbEndpoints, s.createLbEndpoint(host, port))
					}
				}
			case "tailscale":
				// For tailscale backends, we would need to resolve the referenced TailscaleService
				// This could be implemented as a recursive lookup
				s.logger.Info("TailscaleService backend references not yet implemented", "backend", backend.TailscaleService)
			}
		}
	}

	// If still no endpoints, use default ports from VIP service configuration
	if len(lbEndpoints) == 0 {
		for _, portStr := range tailscaleService.Spec.VIPService.Ports {
			port := s.parsePortFromVIPPortString(portStr)
			// Use a placeholder address - in real deployments this would be resolved
			// through Tailscale's coordination server or DNS
			lbEndpoints = append(lbEndpoints, s.createLbEndpoint("100.100.100.1", port))
		}
	}

	// If absolutely no configuration, default to port 80
	if len(lbEndpoints) == 0 {
		lbEndpoints = append(lbEndpoints, s.createLbEndpoint("100.100.100.1", 80))
	}

	return []*endpointv3.LocalityLbEndpoints{
		{
			LbEndpoints: lbEndpoints,
		},
	}
}

// parseAddress parses "host:port" or "host" format
func (s *TailscaleExtensionServer) parseAddress(addr string) (host string, port uint32) {
	if addr == "" {
		return "", 80
	}
	
	// Split on last colon to handle IPv6 addresses
	lastColon := -1
	for i := len(addr) - 1; i >= 0; i-- {
		if addr[i] == ':' {
			lastColon = i
			break
		}
	}
	
	if lastColon == -1 {
		return addr, 80 // Default port
	}
	
	host = addr[:lastColon]
	portStr := addr[lastColon+1:]
	
	if parsedPort, err := fmt.Sscanf(portStr, "%d", &port); err != nil || parsedPort != 1 {
		return host, 80 // Default port on parse error
	}
	
	return host, port
}

// parseServiceReference parses Kubernetes service references like "service.namespace.svc.cluster.local:port"
func (s *TailscaleExtensionServer) parseServiceReference(serviceRef string) (host string, port uint32) {
	return s.parseAddress(serviceRef) // Same parsing logic applies
}

// parsePortFromVIPPortString parses port from VIP port strings like "tcp:80", "udp:53"
func (s *TailscaleExtensionServer) parsePortFromVIPPortString(portStr string) uint32 {
	if portStr == "" {
		return 80
	}
	
	// Handle "protocol:port" format
	colonIndex := -1
	for i, char := range portStr {
		if char == ':' {
			colonIndex = i
			break
		}
	}
	
	if colonIndex != -1 && colonIndex < len(portStr)-1 {
		portPart := portStr[colonIndex+1:]
		var port uint32
		if n, err := fmt.Sscanf(portPart, "%d", &port); err == nil && n == 1 {
			return port
		}
	}
	
	// Try to parse as plain port number
	var port uint32
	if n, err := fmt.Sscanf(portStr, "%d", &port); err == nil && n == 1 {
		return port
	}
	
	return 80 // Default
}

// createLbEndpoint creates a load balancer endpoint
func (s *TailscaleExtensionServer) createLbEndpoint(host string, port uint32) *endpointv3.LbEndpoint {
	return &endpointv3.LbEndpoint{
		HostIdentifier: &endpointv3.LbEndpoint_Endpoint{
			Endpoint: &endpointv3.Endpoint{
				Address: &corev3.Address{
					Address: &corev3.Address_SocketAddress{
						SocketAddress: &corev3.SocketAddress{
							Address: host,
							PortSpecifier: &corev3.SocketAddress_PortValue{
								PortValue: port,
							},
						},
					},
				},
			},
		},
	}
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

// watchTailscaleGateways watches TailscaleGateway resources and maintains the registry
func (s *TailscaleExtensionServer) watchTailscaleGateways(ctx context.Context) {
	// This is a simplified implementation - in production, you'd use controller-runtime's watching mechanism
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(30 * time.Second): // Poll every 30 seconds
			s.refreshGatewayRegistry(ctx)
		}
	}
}

// refreshGatewayRegistry refreshes the gateway registry by listing all TailscaleGateway resources
func (s *TailscaleExtensionServer) refreshGatewayRegistry(ctx context.Context) {
	gatewayList := &gatewayv1alpha1.TailscaleGatewayList{}
	if err := s.client.List(ctx, gatewayList); err != nil {
		s.logger.Error("Failed to list TailscaleGateway resources", "error", err)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Clear existing registry
	s.gatewayRegistry = make(map[string]*gatewayv1alpha1.TailscaleGateway)

	// Populate registry with gateway references
	for _, gateway := range gatewayList.Items {
		gatewayRef := gateway.Spec.GatewayRef
		
		// Determine namespace - use gatewayRef namespace if specified, otherwise use TailscaleGateway namespace
		namespace := gateway.Namespace
		if gatewayRef.Namespace != nil {
			namespace = string(*gatewayRef.Namespace)
		}
		
		gatewayKey := fmt.Sprintf("%s/%s", namespace, string(gatewayRef.Name))
		s.gatewayRegistry[gatewayKey] = gateway.DeepCopy()
		s.logger.Info("Registered TailscaleGateway", "tailscaleGateway", gateway.Name, "envoyGateway", gatewayKey)
	}

	s.logger.Info("Gateway registry refreshed", "totalGateways", len(s.gatewayRegistry))
}

