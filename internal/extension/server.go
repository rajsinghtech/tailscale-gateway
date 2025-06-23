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
	"strconv"
	"strings"

	pb "github.com/envoyproxy/gateway/proto/extension"
	clusterv3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	endpointv3 "github.com/envoyproxy/go-control-plane/envoy/config/endpoint/v3"
	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	gatewayv1alpha1 "github.com/rajsinghtech/tailscale-gateway/api/v1alpha1"
	"github.com/rajsinghtech/tailscale-gateway/internal/tailscale"
)

// TailscaleExtensionServer implements the Envoy Gateway extension server for Tailscale integration
type TailscaleExtensionServer struct {
	pb.UnimplementedEnvoyGatewayExtensionServer

	client           client.Client
	tailscaleManager *TailscaleManager
	xdsDiscovery     *XDSServiceDiscovery
	logger           *slog.Logger
}

// TailscaleManager manages multiple Tailscale client connections for service discovery
type TailscaleManager struct {
	clients map[string]tailscale.Client
}

// TailscaleServiceMapping represents a mapping from Tailscale egress services to external backends
type TailscaleServiceMapping struct {
	ServiceName     string // e.g., "web-service"
	ClusterName     string // e.g., "external-backend-web-service"
	EgressService   string // e.g., "test-endpoints-web-service-egress.default.svc.cluster.local"
	ExternalBackend string // The actual backend service this egress connects to
	Port            uint32 // e.g., 80
	Protocol        string // e.g., "HTTP"
}

// DiscoveredEndpoint represents an endpoint discovered from xDS cluster configuration
type DiscoveredEndpoint struct {
	Address       string            // IP address or FQDN
	Port          uint32            // Port number
	Protocol      string            // TCP/UDP
	ClusterName   string            // Envoy cluster name
	Weight        uint32            // Load balancing weight
	Healthy       bool              // Health status
	Zone          string            // Locality zone
	Metadata      map[string]string // Additional metadata
	TailnetTarget string            // Target Tailscale tailnet (if applicable)
}

// ServiceDiscoveryContext contains comprehensive service discovery information
type ServiceDiscoveryContext struct {
	HTTPRoutes               []gwapiv1.HTTPRoute                  // Discovered HTTPRoutes
	TailscaleEndpoints       []gatewayv1alpha1.TailscaleEndpoints // Discovered TailscaleEndpoints
	KubernetesServices       []corev1.Service                     // Discovered Kubernetes Services
	HTTPRouteBackends        []HTTPRouteBackendMapping            // HTTPRoute backend mappings
	TailscaleServiceMappings []TailscaleServiceMapping            // Tailscale service mappings
}

// HTTPRouteBackendMapping represents a backend mapping from HTTPRoute to Kubernetes Service
type HTTPRouteBackendMapping struct {
	ServiceName  string            // Kubernetes service name
	Namespace    string            // Kubernetes service namespace
	ClusterName  string            // Envoy cluster name for this backend
	Port         uint32            // Service port
	Weight       int32             // Backend weight
	MatchPath    string            // Path match from HTTPRoute rule
	MatchHeaders map[string]string // Header matches from HTTPRoute rule
}

// XDSServiceDiscovery handles comprehensive service discovery from xDS cluster configurations
type XDSServiceDiscovery struct {
	// Discovered endpoints from all route types (HTTP, TCP, UDP, etc.)
	discoveredEndpoints map[string]*DiscoveredEndpoint

	// Tailscale endpoint mappings (from CRDs)
	tailscaleEndpoints map[string]*gatewayv1alpha1.TailscaleEndpoint

	// Extension server reference for logging
	server *TailscaleExtensionServer
}

// NewTailscaleExtensionServer creates a new Tailscale extension server
func NewTailscaleExtensionServer(client client.Client, logger *slog.Logger) *TailscaleExtensionServer {
	server := &TailscaleExtensionServer{
		client: client,
		tailscaleManager: &TailscaleManager{
			clients: make(map[string]tailscale.Client),
		},
		logger: logger,
	}

	// Initialize xDS service discovery
	server.xdsDiscovery = &XDSServiceDiscovery{
		discoveredEndpoints: make(map[string]*DiscoveredEndpoint),
		tailscaleEndpoints:  make(map[string]*gatewayv1alpha1.TailscaleEndpoint),
		server:              server,
	}

	return server
}

// PostRouteModify modifies individual routes after Envoy Gateway generates them
func (s *TailscaleExtensionServer) PostRouteModify(ctx context.Context, req *pb.PostRouteModifyRequest) (*pb.PostRouteModifyResponse, error) {
	s.logger.Info("PostRouteModify called", "route", req.Route.Name)

	// For now, pass through without modification
	// In the future, we could modify specific routes based on Tailscale configuration
	return &pb.PostRouteModifyResponse{
		Route: req.Route,
	}, nil
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

	return &pb.PostVirtualHostModifyResponse{
		VirtualHost: modifiedVH,
	}, nil
}

// PostHTTPListenerModify modifies HTTP listeners
func (s *TailscaleExtensionServer) PostHTTPListenerModify(ctx context.Context, req *pb.PostHTTPListenerModifyRequest) (*pb.PostHTTPListenerModifyResponse, error) {
	s.logger.Info("PostHTTPListenerModify called", "listener", req.Listener.Name)

	// For now, pass through without modification
	return &pb.PostHTTPListenerModifyResponse{
		Listener: req.Listener,
	}, nil
}

// PostTranslateModify modifies clusters and secrets in the final xDS configuration
// This is the primary integration point for comprehensive service discovery across all Gateway API route types
func (s *TailscaleExtensionServer) PostTranslateModify(ctx context.Context, req *pb.PostTranslateModifyRequest) (*pb.PostTranslateModifyResponse, error) {
	s.logger.Info("PostTranslateModify called - comprehensive xDS service discovery", "clusters", len(req.Clusters))

	// Copy existing clusters
	clusters := make([]*clusterv3.Cluster, len(req.Clusters))
	copy(clusters, req.Clusters)

	// Perform comprehensive service discovery from xDS clusters
	// This handles ALL Gateway API route types: HTTPRoute, TCPRoute, UDPRoute, TLSRoute, GRPCRoute
	discoveredEndpoints := s.xdsDiscovery.extractEndpointsFromClusters(ctx, req.Clusters)
	s.logger.Info("Discovered endpoints from xDS", "endpoints", len(discoveredEndpoints))

	// Load TailscaleEndpoints for context and mappings
	if err := s.xdsDiscovery.loadTailscaleEndpoints(ctx, s.client); err != nil {
		s.logger.Error("Failed to load TailscaleEndpoints", "error", err)
	}

	// Process discovered endpoints for Tailscale integration
	tailscaleIntegratedClusters, err := s.xdsDiscovery.createTailscaleIntegratedClusters(ctx, discoveredEndpoints)
	if err != nil {
		s.logger.Error("Failed to create Tailscale integrated clusters", "error", err)
	} else {
		// Add Tailscale integrated clusters to the response
		clusters = append(clusters, tailscaleIntegratedClusters...)
		s.logger.Info("Added Tailscale integrated clusters", "tailscaleClusters", len(tailscaleIntegratedClusters))
	}

	// Generate external backend clusters for TailscaleEndpoints with external targets
	externalClusters, err := s.generateExternalBackendClusters(ctx)
	if err != nil {
		s.logger.Error("Failed to generate external backend clusters", "error", err)
	} else {
		clusters = append(clusters, externalClusters...)
		s.logger.Info("Added external backend clusters", "externalClusters", len(externalClusters))
	}

	s.logger.Info("PostTranslateModify completed",
		"totalClusters", len(clusters),
		"originalClusters", len(req.Clusters),
		"discoveredEndpoints", len(discoveredEndpoints),
		"tailscaleIntegratedClusters", len(tailscaleIntegratedClusters),
		"externalClusters", len(externalClusters))

	return &pb.PostTranslateModifyResponse{
		Clusters: clusters,
		Secrets:  req.Secrets,
	}, nil
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
			// The egress service connects Tailscale clients to external services
			mapping := TailscaleServiceMapping{
				ServiceName:     endpoint.Name,
				ClusterName:     fmt.Sprintf("external-backend-%s", endpoint.Name),
				EgressService:   fmt.Sprintf("%s-%s-egress.%s.svc.cluster.local", endpoints.Name, endpoint.Name, endpoints.Namespace),
				ExternalBackend: endpoint.ExternalTarget, // This should be defined in the CRD
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

// discoverAllServiceMappings performs comprehensive service discovery
// Discovers HTTPRoute backends, TailscaleEndpoints services, and creates unified mappings
func (s *TailscaleExtensionServer) discoverAllServiceMappings(ctx context.Context) (*ServiceDiscoveryContext, error) {
	discoveryCtx := &ServiceDiscoveryContext{}

	// Discover HTTPRoutes
	httpRoutes, err := s.discoverHTTPRoutes(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to discover HTTPRoutes: %w", err)
	}
	discoveryCtx.HTTPRoutes = httpRoutes

	// Discover TailscaleEndpoints
	tailscaleEndpoints, err := s.discoverTailscaleEndpoints(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to discover TailscaleEndpoints: %w", err)
	}
	discoveryCtx.TailscaleEndpoints = tailscaleEndpoints

	// Discover Kubernetes Services for HTTPRoute backends
	kubernetesServices, err := s.discoverKubernetesServices(ctx, httpRoutes)
	if err != nil {
		return nil, fmt.Errorf("failed to discover Kubernetes services: %w", err)
	}
	discoveryCtx.KubernetesServices = kubernetesServices

	// Create HTTPRoute backend mappings
	httpRouteBackends, err := s.createHTTPRouteBackendMappings(ctx, httpRoutes, kubernetesServices)
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTPRoute backend mappings: %w", err)
	}
	discoveryCtx.HTTPRouteBackends = httpRouteBackends

	// Create TailscaleEndpoints service mappings (existing logic)
	tailscaleMappings, err := s.getTailscaleEgressMappings(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get Tailscale mappings: %w", err)
	}
	discoveryCtx.TailscaleServiceMappings = tailscaleMappings

	s.logger.Info("Comprehensive service discovery completed",
		"httpRoutes", len(httpRoutes),
		"tailscaleEndpoints", len(tailscaleEndpoints),
		"kubernetesServices", len(kubernetesServices),
		"httpRouteBackends", len(httpRouteBackends),
		"tailscaleMappings", len(tailscaleMappings))

	return discoveryCtx, nil
}

// discoverHTTPRoutes discovers HTTPRoutes that reference this Gateway
func (s *TailscaleExtensionServer) discoverHTTPRoutes(ctx context.Context) ([]gwapiv1.HTTPRoute, error) {
	var httpRoutes []gwapiv1.HTTPRoute

	// List all HTTPRoutes in cluster
	httpRouteList := &gwapiv1.HTTPRouteList{}
	if err := s.client.List(ctx, httpRouteList); err != nil {
		return nil, fmt.Errorf("failed to list HTTPRoutes: %w", err)
	}

	// Filter HTTPRoutes that reference extension-enabled Gateways
	for _, route := range httpRouteList.Items {
		if s.isHTTPRouteRelevant(&route) {
			httpRoutes = append(httpRoutes, route)
		}
	}

	s.logger.Info("Discovered HTTPRoutes", "count", len(httpRoutes))
	return httpRoutes, nil
}

// isHTTPRouteRelevant checks if an HTTPRoute is relevant for this extension server
func (s *TailscaleExtensionServer) isHTTPRouteRelevant(route *gwapiv1.HTTPRoute) bool {
	// Check if HTTPRoute has Tailscale annotations or references extension-enabled Gateways
	annotations := route.GetAnnotations()
	if annotations != nil {
		if _, hasTailscaleAnnotation := annotations["gateway.tailscale.com/gateway"]; hasTailscaleAnnotation {
			return true
		}
	}

	// Check parent refs for extension-enabled Gateways
	for _, parentRef := range route.Spec.ParentRefs {
		if parentRef.Name == "tailscale-gateway" ||
			strings.Contains(string(parentRef.Name), "tailscale") {
			return true
		}
	}

	return false
}

// discoverTailscaleEndpoints discovers all TailscaleEndpoints resources
func (s *TailscaleExtensionServer) discoverTailscaleEndpoints(ctx context.Context) ([]gatewayv1alpha1.TailscaleEndpoints, error) {
	endpointsList := &gatewayv1alpha1.TailscaleEndpointsList{}
	if err := s.client.List(ctx, endpointsList); err != nil {
		return nil, fmt.Errorf("failed to list TailscaleEndpoints: %w", err)
	}

	s.logger.Info("Discovered TailscaleEndpoints", "count", len(endpointsList.Items))
	return endpointsList.Items, nil
}

// discoverKubernetesServices discovers Kubernetes Services referenced by HTTPRoutes
func (s *TailscaleExtensionServer) discoverKubernetesServices(ctx context.Context, httpRoutes []gwapiv1.HTTPRoute) ([]corev1.Service, error) {
	serviceMap := make(map[string]corev1.Service)

	for _, route := range httpRoutes {
		for _, rule := range route.Spec.Rules {
			for _, backendRef := range rule.BackendRefs {
				if backendRef.Kind != nil && *backendRef.Kind != "Service" {
					continue // Skip non-Service backends
				}

				serviceName := string(backendRef.Name)
				serviceNamespace := route.Namespace
				if backendRef.Namespace != nil {
					serviceNamespace = string(*backendRef.Namespace)
				}

				serviceKey := fmt.Sprintf("%s/%s", serviceNamespace, serviceName)
				if _, exists := serviceMap[serviceKey]; exists {
					continue // Already discovered
				}

				// Fetch the Service
				service := &corev1.Service{}
				serviceObjectKey := types.NamespacedName{
					Name:      serviceName,
					Namespace: serviceNamespace,
				}

				if err := s.client.Get(ctx, serviceObjectKey, service); err != nil {
					s.logger.Warn("Failed to get Service for HTTPRoute backend",
						"service", serviceKey, "error", err)
					continue
				}

				serviceMap[serviceKey] = *service
			}
		}
	}

	// Convert map to slice
	var services []corev1.Service
	for _, service := range serviceMap {
		services = append(services, service)
	}

	s.logger.Info("Discovered Kubernetes Services", "count", len(services))
	return services, nil
}

// createHTTPRouteBackendMappings creates backend mappings from HTTPRoutes and Services
func (s *TailscaleExtensionServer) createHTTPRouteBackendMappings(ctx context.Context, httpRoutes []gwapiv1.HTTPRoute, services []corev1.Service) ([]HTTPRouteBackendMapping, error) {
	var mappings []HTTPRouteBackendMapping

	// Create service lookup map
	serviceMap := make(map[string]corev1.Service)
	for _, service := range services {
		key := fmt.Sprintf("%s/%s", service.Namespace, service.Name)
		serviceMap[key] = service
	}

	for _, route := range httpRoutes {
		for _, rule := range route.Spec.Rules {
			// Extract path match from rule
			pathMatch := "/"
			if len(rule.Matches) > 0 && rule.Matches[0].Path != nil {
				if rule.Matches[0].Path.Value != nil {
					pathMatch = *rule.Matches[0].Path.Value
				}
			}

			// Extract header matches
			headerMatches := make(map[string]string)
			if len(rule.Matches) > 0 {
				for _, headerMatch := range rule.Matches[0].Headers {
					headerMatches[string(headerMatch.Name)] = headerMatch.Value
				}
			}

			for _, backendRef := range rule.BackendRefs {
				if backendRef.Kind != nil && *backendRef.Kind != "Service" {
					continue
				}

				serviceName := string(backendRef.Name)
				serviceNamespace := route.Namespace
				if backendRef.Namespace != nil {
					serviceNamespace = string(*backendRef.Namespace)
				}

				serviceKey := fmt.Sprintf("%s/%s", serviceNamespace, serviceName)
				service, exists := serviceMap[serviceKey]
				if !exists {
					continue
				}

				// Get service port
				var servicePort uint32 = 80 // default
				if backendRef.Port != nil {
					servicePort = uint32(*backendRef.Port)
				} else if len(service.Spec.Ports) > 0 {
					servicePort = uint32(service.Spec.Ports[0].Port)
				}

				// Get backend weight
				weight := int32(1)
				if backendRef.Weight != nil {
					weight = *backendRef.Weight
				}

				mapping := HTTPRouteBackendMapping{
					ServiceName:  serviceName,
					Namespace:    serviceNamespace,
					ClusterName:  fmt.Sprintf("httproute-backend-%s-%s", serviceNamespace, serviceName),
					Port:         servicePort,
					Weight:       weight,
					MatchPath:    pathMatch,
					MatchHeaders: headerMatches,
				}

				mappings = append(mappings, mapping)
			}
		}
	}

	s.logger.Info("Created HTTPRoute backend mappings", "count", len(mappings))
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
		// Parse hostname and port from ExternalBackend
		hostname, portStr, err := net.SplitHostPort(mapping.ExternalBackend)
		if err != nil {
			s.logger.Error("Failed to parse external backend", "backend", mapping.ExternalBackend, "error", err)
			continue
		}

		port, err := strconv.Atoi(portStr)
		if err != nil {
			s.logger.Error("Failed to parse port", "port", portStr, "error", err)
			continue
		}

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
											Address: hostname,
											PortSpecifier: &corev3.SocketAddress_PortValue{
												PortValue: uint32(port),
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

		s.logger.Info("Generated external backend cluster", "name", mapping.ClusterName, "hostname", hostname, "port", port)
	}

	return clusters, nil
}

// extractEndpointsFromClusters performs comprehensive endpoint discovery from xDS clusters
// This replaces manual HTTPRoute/Service watching and handles ALL Gateway API route types
func (xds *XDSServiceDiscovery) extractEndpointsFromClusters(ctx context.Context, clusters []*clusterv3.Cluster) []*DiscoveredEndpoint {
	var endpoints []*DiscoveredEndpoint

	// Clear previous discovery
	xds.discoveredEndpoints = make(map[string]*DiscoveredEndpoint)

	for _, cluster := range clusters {
		clusterEndpoints := xds.extractEndpointsFromCluster(cluster)
		endpoints = append(endpoints, clusterEndpoints...)

		// Store in discovery cache
		for _, endpoint := range clusterEndpoints {
			key := fmt.Sprintf("%s:%d", endpoint.Address, endpoint.Port)
			xds.discoveredEndpoints[key] = endpoint
		}
	}

	xds.server.logger.Info("Extracted endpoints from xDS clusters",
		"totalClusters", len(clusters),
		"totalEndpoints", len(endpoints))

	return endpoints
}

// extractEndpointsFromCluster extracts endpoint information from a single cluster
func (xds *XDSServiceDiscovery) extractEndpointsFromCluster(cluster *clusterv3.Cluster) []*DiscoveredEndpoint {
	var endpoints []*DiscoveredEndpoint

	// Handle different cluster types
	switch {
	case cluster.GetLoadAssignment() != nil:
		// Static endpoints (includes DNS names and IPs)
		endpoints = xds.extractFromLoadAssignment(cluster)
	case cluster.GetEdsClusterConfig() != nil:
		// EDS endpoints - Kubernetes Services resolved via EndpointSlices
		endpoints = xds.extractFromEDSCluster(cluster)
	case cluster.GetClusterType() != nil:
		// Dynamic resolver endpoints (e.g., forward proxy)
		endpoints = xds.extractFromDynamicCluster(cluster)
	default:
		xds.server.logger.Debug("Unknown cluster type", "cluster", cluster.Name)
	}

	return endpoints
}

// extractFromLoadAssignment extracts endpoints from ClusterLoadAssignment (static/DNS)
func (xds *XDSServiceDiscovery) extractFromLoadAssignment(cluster *clusterv3.Cluster) []*DiscoveredEndpoint {
	var endpoints []*DiscoveredEndpoint

	loadAssignment := cluster.GetLoadAssignment()
	for _, locality := range loadAssignment.GetEndpoints() {
		for _, lbEndpoint := range locality.GetLbEndpoints() {
			endpoint := &DiscoveredEndpoint{
				ClusterName: cluster.Name,
				Weight:      lbEndpoint.GetLoadBalancingWeight().GetValue(),
				Healthy:     lbEndpoint.GetHealthStatus() != corev3.HealthStatus_UNHEALTHY,
				Zone:        locality.GetLocality().GetZone(),
				Metadata:    make(map[string]string),
			}

			// Extract address and port
			if socketAddr := lbEndpoint.GetEndpoint().GetAddress().GetSocketAddress(); socketAddr != nil {
				endpoint.Address = socketAddr.GetAddress()
				endpoint.Port = socketAddr.GetPortValue()
				endpoint.Protocol = socketAddr.GetProtocol().String()
			}

			// Extract metadata
			if metadata := lbEndpoint.GetMetadata(); metadata != nil {
				for key, value := range metadata.GetFilterMetadata() {
					if stringValue := value.GetFields()["value"].GetStringValue(); stringValue != "" {
						endpoint.Metadata[key] = stringValue
					}
				}
			}

			endpoints = append(endpoints, endpoint)
		}
	}

	return endpoints
}

// extractFromEDSCluster extracts endpoints from EDS cluster (Kubernetes Services)
func (xds *XDSServiceDiscovery) extractFromEDSCluster(cluster *clusterv3.Cluster) []*DiscoveredEndpoint {
	var endpoints []*DiscoveredEndpoint

	// For EDS clusters, the actual endpoints are resolved separately via EDS
	// We can capture the service reference and mark it for Tailscale integration
	serviceName := cluster.GetEdsClusterConfig().GetServiceName()

	endpoint := &DiscoveredEndpoint{
		ClusterName: cluster.Name,
		Address:     serviceName, // This is the service name, not resolved IP
		Port:        80,          // Default, actual ports from EDS
		Protocol:    "TCP",
		Healthy:     true,
		Metadata: map[string]string{
			"type":        "kubernetes-service",
			"serviceName": serviceName,
		},
	}

	endpoints = append(endpoints, endpoint)
	return endpoints
}

// extractFromDynamicCluster extracts information from dynamic clusters (forward proxy, etc.)
func (xds *XDSServiceDiscovery) extractFromDynamicCluster(cluster *clusterv3.Cluster) []*DiscoveredEndpoint {
	var endpoints []*DiscoveredEndpoint

	// Dynamic clusters don't have static endpoints
	// We can still capture them for metadata purposes
	endpoint := &DiscoveredEndpoint{
		ClusterName: cluster.Name,
		Address:     "dynamic",
		Port:        0,
		Protocol:    "TCP",
		Healthy:     true,
		Metadata: map[string]string{
			"type": "dynamic-resolver",
		},
	}

	endpoints = append(endpoints, endpoint)
	return endpoints
}

// loadTailscaleEndpoints loads TailscaleEndpoints CRDs for context and mapping
func (xds *XDSServiceDiscovery) loadTailscaleEndpoints(ctx context.Context, client client.Client) error {
	endpointsList := &gatewayv1alpha1.TailscaleEndpointsList{}
	if err := client.List(ctx, endpointsList); err != nil {
		return fmt.Errorf("failed to list TailscaleEndpoints: %w", err)
	}

	// Clear and reload
	xds.tailscaleEndpoints = make(map[string]*gatewayv1alpha1.TailscaleEndpoint)

	for _, endpoints := range endpointsList.Items {
		for _, endpoint := range endpoints.Spec.Endpoints {
			key := fmt.Sprintf("%s:%d", endpoint.TailscaleIP, endpoint.Port)
			endpointCopy := endpoint
			xds.tailscaleEndpoints[key] = &endpointCopy
		}
	}

	xds.server.logger.Info("Loaded TailscaleEndpoints for context", "count", len(xds.tailscaleEndpoints))
	return nil
}

// createTailscaleIntegratedClusters creates clusters that integrate discovered endpoints with Tailscale
func (xds *XDSServiceDiscovery) createTailscaleIntegratedClusters(ctx context.Context, discoveredEndpoints []*DiscoveredEndpoint) ([]*clusterv3.Cluster, error) {
	var clusters []*clusterv3.Cluster

	// For each discovered endpoint, check if it should be integrated with Tailscale
	for _, endpoint := range discoveredEndpoints {
		// Check if this endpoint matches any TailscaleEndpoints configurations
		if tsEndpoint := xds.findMatchingTailscaleEndpoint(endpoint); tsEndpoint != nil {
			// Create Tailscale-integrated cluster
			cluster := xds.createTailscaleCluster(endpoint, tsEndpoint)
			if cluster != nil {
				clusters = append(clusters, cluster)
			}
		}
	}

	xds.server.logger.Info("Created Tailscale integrated clusters", "count", len(clusters))
	return clusters, nil
}

// findMatchingTailscaleEndpoint finds a TailscaleEndpoint that matches the discovered endpoint
func (xds *XDSServiceDiscovery) findMatchingTailscaleEndpoint(discovered *DiscoveredEndpoint) *gatewayv1alpha1.TailscaleEndpoint {
	// Try exact match first
	key := fmt.Sprintf("%s:%d", discovered.Address, discovered.Port)
	if tsEndpoint, exists := xds.tailscaleEndpoints[key]; exists {
		return tsEndpoint
	}

	// Try service name matching for EDS clusters
	if serviceName, exists := discovered.Metadata["serviceName"]; exists {
		for _, tsEndpoint := range xds.tailscaleEndpoints {
			if tsEndpoint.Name == serviceName {
				return tsEndpoint
			}
		}
	}

	return nil
}

// createTailscaleCluster creates an Envoy cluster that routes to a Tailscale endpoint
func (xds *XDSServiceDiscovery) createTailscaleCluster(discovered *DiscoveredEndpoint, tsEndpoint *gatewayv1alpha1.TailscaleEndpoint) *clusterv3.Cluster {
	clusterName := fmt.Sprintf("tailscale-%s", tsEndpoint.Name)

	cluster := &clusterv3.Cluster{
		Name: clusterName,
		ClusterDiscoveryType: &clusterv3.Cluster_Type{
			Type: clusterv3.Cluster_STATIC,
		},
		LoadAssignment: &endpointv3.ClusterLoadAssignment{
			ClusterName: clusterName,
			Endpoints: []*endpointv3.LocalityLbEndpoints{{
				LbEndpoints: []*endpointv3.LbEndpoint{{
					HostIdentifier: &endpointv3.LbEndpoint_Endpoint{
						Endpoint: &endpointv3.Endpoint{
							Address: &corev3.Address{
								Address: &corev3.Address_SocketAddress{
									SocketAddress: &corev3.SocketAddress{
										Address: tsEndpoint.TailscaleIP,
										PortSpecifier: &corev3.SocketAddress_PortValue{
											PortValue: uint32(tsEndpoint.Port),
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

	xds.server.logger.Info("Created Tailscale cluster",
		"cluster", clusterName,
		"tailscaleIP", tsEndpoint.TailscaleIP,
		"port", tsEndpoint.Port)

	return cluster
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
