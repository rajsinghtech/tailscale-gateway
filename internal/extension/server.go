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
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	pb "github.com/envoyproxy/gateway/proto/extension"
	clusterv3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	endpointv3 "github.com/envoyproxy/go-control-plane/envoy/config/endpoint/v3"
	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"
	gwapiv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	gatewayv1alpha1 "github.com/rajsinghtech/tailscale-gateway/api/v1alpha1"
	"github.com/rajsinghtech/tailscale-gateway/internal/service"
	"github.com/rajsinghtech/tailscale-gateway/internal/tailscale"
)

// TailscaleExtensionServer implements the Envoy Gateway extension server for Tailscale integration
type TailscaleExtensionServer struct {
	pb.UnimplementedEnvoyGatewayExtensionServer

	client             client.Client
	tailscaleManager   *TailscaleManager
	xdsDiscovery       *XDSServiceDiscovery
	logger             *slog.Logger
	serviceCoordinator *service.ServiceCoordinator
	configCache        *ConfigCache
	resourceIndex      *ResourceIndex
	metrics            *ExtensionServerMetrics
	mu                 sync.RWMutex
}

// ConfigCache provides cached configuration for hot reloading
type ConfigCache struct {
	routeGenerationConfig map[string]*gatewayv1alpha1.RouteGenerationConfig
	tailscaleEndpoints    map[string]*gatewayv1alpha1.TailscaleEndpoints
	gatewayConfigs        map[string]*gatewayv1alpha1.TailscaleGateway
	lastUpdate            time.Time
	mu                    sync.RWMutex
}

// ResourceIndex provides efficient indexing and relationship tracking
type ResourceIndex struct {
	// TailscaleEndpoints to HTTPRoutes mapping
	endpointsToHTTPRoutes map[string][]string // key: namespace/name -> []route-namespace/route-name
	httpRoutesToEndpoints map[string][]string // key: route-namespace/route-name -> []endpoints-namespace/endpoints-name

	// TailscaleEndpoints to VIP Services mapping
	endpointsToVIPServices map[string][]VIPServiceReference // key: namespace/name -> []VIPServiceReference
	vipServicesToEndpoints map[string][]string              // key: service-name -> []endpoints-namespace/endpoints-name

	// Gateway API route type mappings
	endpointsToTCPRoutes  map[string][]string // key: namespace/name -> []route-namespace/route-name
	endpointsToUDPRoutes  map[string][]string
	endpointsToTLSRoutes  map[string][]string
	endpointsToGRPCRoutes map[string][]string

	// Service dependency tracking
	serviceDependencies map[string][]string // key: service-name -> []dependent-service-names

	// Performance metrics
	indexUpdateCount int64
	lastIndexUpdate  time.Time

	mu sync.RWMutex
}

// VIPServiceReference represents a reference to a VIP service
type VIPServiceReference struct {
	ServiceName string                 `json:"serviceName"`
	ClusterName string                 `json:"clusterName"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
}

// ExtensionServerMetrics tracks extension server performance
type ExtensionServerMetrics struct {
	totalHookCalls      int64
	successfulCalls     int64
	failedCalls         int64
	lastCallDuration    time.Duration
	averageCallDuration time.Duration
	hookTypeMetrics     map[string]*HookTypeMetrics
	mu                  sync.RWMutex
}

// HookTypeMetrics tracks metrics for specific hook types
type HookTypeMetrics struct {
	calls           int64
	successes       int64
	failures        int64
	averageResponse time.Duration
}

// TailscaleServiceConfig defines configuration-driven service discovery rules
type TailscaleServiceConfig struct {
	UUID              string                    `json:"uuid,omitempty"`
	MetadataNamespace string                    `json:"metadataNamespace"`
	Rules             []TailscaleServiceRule    `json:"rules"`
	VIPServiceMapping map[string]VIPServiceInfo `json:"vipServiceMapping"`
}

// TailscaleServiceRule defines service discovery rules
type TailscaleServiceRule struct {
	Name           string       `json:"name"`
	PathMatches    []PathMatch  `json:"pathMatches"`
	ServiceRefs    []ServiceRef `json:"serviceRefs"`
	ExternalTarget string       `json:"externalTarget"`
	TailnetName    string       `json:"tailnetName"`
}

// PathMatch defines path matching criteria
type PathMatch struct {
	Type  string `json:"type"` // PathPrefix, PathExact, PathRegexp
	Value string `json:"value"`
}

// ServiceRef references a Tailscale service
type ServiceRef struct {
	Group     string `json:"group"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Port      int32  `json:"port"`
	Weight    int32  `json:"weight"`
}

// VIPServiceInfo contains VIP service metadata
type VIPServiceInfo struct {
	ServiceName    string    `json:"serviceName"`
	VIPAddresses   []string  `json:"vipAddresses"`
	OwnerCluster   string    `json:"ownerCluster"`
	Consumers      []string  `json:"consumers"`
	LastUpdated    time.Time `json:"lastUpdated"`
	ExternalTarget string    `json:"externalTarget"`
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
	PathPrefix      string // e.g., "/api/web-service" or "/tailscale/web-service/" based on config
	TailnetName     string // e.g., "cluster1-tailnet"
	GatewayKey      string // e.g., "default/main-gateway"
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

// InboundVIPServiceMapping represents a VIP service for Gateway listeners (inbound traffic)
type InboundVIPServiceMapping struct {
	ServiceName  string   // VIP service name
	VIPAddresses []string // VIP addresses allocated to this service
	OwnerCluster string   // Cluster that owns this service
	PathPrefix   string   // Path prefix for routing (e.g., "/gateway/service-name/")
	TailnetName  string   // Tailnet name
	GatewayKey   string   // Gateway namespace/name
	Protocol     string   // Protocol (HTTP, HTTPS, TCP, UDP)
	Port         uint32   // Port number
}

// CrossClusterVIPServiceMapping represents a VIP service from another cluster
type CrossClusterVIPServiceMapping struct {
	ServiceName   string    // VIP service name
	VIPAddresses  []string  // VIP addresses
	OwnerCluster  string    // Cluster that owns this service
	ConsumerCount int       // Number of clusters consuming this service
	PathPrefix    string    // Path prefix for routing (e.g., "/cross-cluster/service-name/")
	Protocol      string    // Protocol (HTTP, HTTPS, TCP, UDP)
	Port          uint32    // Port number
	LastUpdated   time.Time // When this service was last updated
}

// ServiceDiscoveryContext contains comprehensive service discovery information
type ServiceDiscoveryContext struct {
	HTTPRoutes               []gwapiv1.HTTPRoute                  // Discovered HTTPRoutes
	TCPRoutes                []gwapiv1alpha2.TCPRoute             // Discovered TCPRoutes
	UDPRoutes                []gwapiv1alpha2.UDPRoute             // Discovered UDPRoutes
	TLSRoutes                []gwapiv1alpha2.TLSRoute             // Discovered TLSRoutes
	GRPCRoutes               []gwapiv1.GRPCRoute                  // Discovered GRPCRoutes
	TailscaleEndpoints       []gatewayv1alpha1.TailscaleEndpoints // Discovered TailscaleEndpoints
	KubernetesServices       []corev1.Service                     // Discovered Kubernetes Services
	HTTPRouteBackends        []HTTPRouteBackendMapping            // HTTPRoute backend mappings
	TCPRouteBackends         []TCPRouteBackendMapping             // TCPRoute backend mappings
	UDPRouteBackends         []UDPRouteBackendMapping             // UDPRoute backend mappings
	TLSRouteBackends         []TLSRouteBackendMapping             // TLSRoute backend mappings
	GRPCRouteBackends        []GRPCRouteBackendMapping            // GRPCRoute backend mappings
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

// TCPRouteBackendMapping represents a backend mapping from TCPRoute to Kubernetes Service
type TCPRouteBackendMapping struct {
	ServiceName string // Kubernetes service name
	Namespace   string // Kubernetes service namespace
	ClusterName string // Envoy cluster name for this backend
	Port        uint32 // Service port
	Weight      int32  // Backend weight
}

// UDPRouteBackendMapping represents a backend mapping from UDPRoute to Kubernetes Service
type UDPRouteBackendMapping struct {
	ServiceName string // Kubernetes service name
	Namespace   string // Kubernetes service namespace
	ClusterName string // Envoy cluster name for this backend
	Port        uint32 // Service port
	Weight      int32  // Backend weight
}

// TLSRouteBackendMapping represents a backend mapping from TLSRoute to Kubernetes Service
type TLSRouteBackendMapping struct {
	ServiceName string   // Kubernetes service name
	Namespace   string   // Kubernetes service namespace
	ClusterName string   // Envoy cluster name for this backend
	Port        uint32   // Service port
	Weight      int32    // Backend weight
	SNIMatches  []string // SNI matches from TLSRoute rule
}

// GRPCRouteBackendMapping represents a backend mapping from GRPCRoute to Kubernetes Service
type GRPCRouteBackendMapping struct {
	ServiceName   string            // Kubernetes service name
	Namespace     string            // Kubernetes service namespace
	ClusterName   string            // Envoy cluster name for this backend
	Port          uint32            // Service port
	Weight        int32             // Backend weight
	MethodMatches []string          // Method matches from GRPCRoute rule
	MatchHeaders  map[string]string // Header matches from GRPCRoute rule
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
		configCache: &ConfigCache{
			routeGenerationConfig: make(map[string]*gatewayv1alpha1.RouteGenerationConfig),
			tailscaleEndpoints:    make(map[string]*gatewayv1alpha1.TailscaleEndpoints),
			gatewayConfigs:        make(map[string]*gatewayv1alpha1.TailscaleGateway),
			lastUpdate:            time.Now(),
		},
		metrics: &ExtensionServerMetrics{
			hookTypeMetrics: make(map[string]*HookTypeMetrics),
		},
	}

	// Initialize xDS service discovery
	server.xdsDiscovery = &XDSServiceDiscovery{
		discoveredEndpoints: make(map[string]*DiscoveredEndpoint),
		tailscaleEndpoints:  make(map[string]*gatewayv1alpha1.TailscaleEndpoint),
		server:              server,
	}

	// Start configuration cache updater
	go server.startConfigCacheUpdater(context.Background())

	return server
}

// PostRouteModify modifies individual routes after Envoy Gateway generates them
func (s *TailscaleExtensionServer) PostRouteModify(ctx context.Context, req *pb.PostRouteModifyRequest) (*pb.PostRouteModifyResponse, error) {
	s.logger.Info("PostRouteModify called", "route", req.Route.Name)

	// For now, pass through without modification
	// In the future, we could modify specific routes based on Tailscale configuration
	// Track metrics for this hook call
	start := time.Now()
	defer func() {
		s.recordHookMetrics("PostRouteModify", time.Since(start), nil)
	}()

	return &pb.PostRouteModifyResponse{
		Route: req.Route,
	}, nil
}

// PostVirtualHostModify modifies virtual hosts and injects new routes for Tailscale services
func (s *TailscaleExtensionServer) PostVirtualHostModify(ctx context.Context, req *pb.PostVirtualHostModifyRequest) (*pb.PostVirtualHostModifyResponse, error) {
	s.logger.Info("PostVirtualHostModify called", "virtualHost", req.VirtualHost.Name, "domains", req.VirtualHost.Domains)

	// Track metrics for this hook call
	start := time.Now()
	defer func() {
		s.recordHookMetrics("PostVirtualHostModify", time.Since(start), nil)
	}()

	// Clone the virtual host to avoid modifying the original
	modifiedVH := proto.Clone(req.VirtualHost).(*routev3.VirtualHost)

	// Step 1: Get Tailscale egress service mappings that should route to external backends
	// Now uses configuration-driven route generation
	serviceMappings, err := s.getTailscaleEgressMappingsWithConfig(ctx)
	if err != nil {
		s.logger.Error("Failed to get Tailscale egress mappings", "error", err)
		return &pb.PostVirtualHostModifyResponse{VirtualHost: modifiedVH}, err
	}

	// Step 2: Get inbound VIP service mappings for Gateway listeners
	inboundMappings, err := s.getInboundVIPServiceMappings(ctx)
	if err != nil {
		s.logger.Error("Failed to get inbound VIP service mappings", "error", err)
		// Continue with egress only - don't fail the entire operation
	}

	// Step 3: Get cross-cluster VIP service mappings
	crossClusterMappings, err := s.getCrossClusterVIPServiceMappings(ctx)
	if err != nil {
		s.logger.Error("Failed to get cross-cluster VIP service mappings", "error", err)
		// Continue without cross-cluster services
	}

	// Inject routes for each Tailscale egress service to external backends
	for _, mapping := range serviceMappings {
		route := s.createTailscaleEgressRouteWithConfig(mapping)
		modifiedVH.Routes = append(modifiedVH.Routes, route)
		s.logger.Info("Injected Tailscale egress route", "service", mapping.ServiceName, "backend", mapping.ExternalBackend, "pathPrefix", mapping.PathPrefix)
	}

	// Inject routes for inbound VIP services (from Tailscale to Gateway)
	for _, mapping := range inboundMappings {
		route := s.createInboundVIPRoute(mapping)
		modifiedVH.Routes = append(modifiedVH.Routes, route)
		s.logger.Info("Injected inbound VIP route", "service", mapping.ServiceName, "vips", mapping.VIPAddresses, "pathPrefix", mapping.PathPrefix)
	}

	// Inject routes for cross-cluster VIP services
	for _, mapping := range crossClusterMappings {
		route := s.createCrossClusterVIPRoute(mapping)
		modifiedVH.Routes = append(modifiedVH.Routes, route)
		s.logger.Info("Injected cross-cluster VIP route", "service", mapping.ServiceName, "cluster", mapping.OwnerCluster, "pathPrefix", mapping.PathPrefix)
	}

	s.logger.Info("PostVirtualHostModify completed",
		"totalRoutes", len(modifiedVH.Routes),
		"egressRoutes", len(serviceMappings),
		"inboundRoutes", len(inboundMappings),
		"crossClusterRoutes", len(crossClusterMappings))

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

	// Discover TCPRoutes
	tcpRoutes, err := s.discoverTCPRoutes(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to discover TCPRoutes: %w", err)
	}
	discoveryCtx.TCPRoutes = tcpRoutes

	// Discover UDPRoutes
	udpRoutes, err := s.discoverUDPRoutes(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to discover UDPRoutes: %w", err)
	}
	discoveryCtx.UDPRoutes = udpRoutes

	// Discover TLSRoutes
	tlsRoutes, err := s.discoverTLSRoutes(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to discover TLSRoutes: %w", err)
	}
	discoveryCtx.TLSRoutes = tlsRoutes

	// Discover GRPCRoutes
	grpcRoutes, err := s.discoverGRPCRoutes(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to discover GRPCRoutes: %w", err)
	}
	discoveryCtx.GRPCRoutes = grpcRoutes

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

// discoverTCPRoutes discovers TCPRoutes that reference this Gateway
func (s *TailscaleExtensionServer) discoverTCPRoutes(ctx context.Context) ([]gwapiv1alpha2.TCPRoute, error) {
	var tcpRoutes []gwapiv1alpha2.TCPRoute

	// List all TCPRoutes in cluster
	tcpRouteList := &gwapiv1alpha2.TCPRouteList{}
	if err := s.client.List(ctx, tcpRouteList); err != nil {
		return nil, fmt.Errorf("failed to list TCPRoutes: %w", err)
	}

	// Filter TCPRoutes that reference extension-enabled Gateways
	for _, route := range tcpRouteList.Items {
		if s.isTCPRouteRelevant(&route) {
			tcpRoutes = append(tcpRoutes, route)
		}
	}

	s.logger.Info("Discovered TCPRoutes", "count", len(tcpRoutes))
	return tcpRoutes, nil
}

// discoverUDPRoutes discovers UDPRoutes that reference this Gateway
func (s *TailscaleExtensionServer) discoverUDPRoutes(ctx context.Context) ([]gwapiv1alpha2.UDPRoute, error) {
	var udpRoutes []gwapiv1alpha2.UDPRoute

	// List all UDPRoutes in cluster
	udpRouteList := &gwapiv1alpha2.UDPRouteList{}
	if err := s.client.List(ctx, udpRouteList); err != nil {
		return nil, fmt.Errorf("failed to list UDPRoutes: %w", err)
	}

	// Filter UDPRoutes that reference extension-enabled Gateways
	for _, route := range udpRouteList.Items {
		if s.isUDPRouteRelevant(&route) {
			udpRoutes = append(udpRoutes, route)
		}
	}

	s.logger.Info("Discovered UDPRoutes", "count", len(udpRoutes))
	return udpRoutes, nil
}

// discoverTLSRoutes discovers TLSRoutes that reference this Gateway
func (s *TailscaleExtensionServer) discoverTLSRoutes(ctx context.Context) ([]gwapiv1alpha2.TLSRoute, error) {
	var tlsRoutes []gwapiv1alpha2.TLSRoute

	// List all TLSRoutes in cluster
	tlsRouteList := &gwapiv1alpha2.TLSRouteList{}
	if err := s.client.List(ctx, tlsRouteList); err != nil {
		return nil, fmt.Errorf("failed to list TLSRoutes: %w", err)
	}

	// Filter TLSRoutes that reference extension-enabled Gateways
	for _, route := range tlsRouteList.Items {
		if s.isTLSRouteRelevant(&route) {
			tlsRoutes = append(tlsRoutes, route)
		}
	}

	s.logger.Info("Discovered TLSRoutes", "count", len(tlsRoutes))
	return tlsRoutes, nil
}

// discoverGRPCRoutes discovers GRPCRoutes that reference this Gateway
func (s *TailscaleExtensionServer) discoverGRPCRoutes(ctx context.Context) ([]gwapiv1.GRPCRoute, error) {
	var grpcRoutes []gwapiv1.GRPCRoute

	// List all GRPCRoutes in cluster
	grpcRouteList := &gwapiv1.GRPCRouteList{}
	if err := s.client.List(ctx, grpcRouteList); err != nil {
		return nil, fmt.Errorf("failed to list GRPCRoutes: %w", err)
	}

	// Filter GRPCRoutes that reference extension-enabled Gateways
	for _, route := range grpcRouteList.Items {
		if s.isGRPCRouteRelevant(&route) {
			grpcRoutes = append(grpcRoutes, route)
		}
	}

	s.logger.Info("Discovered GRPCRoutes", "count", len(grpcRoutes))
	return grpcRoutes, nil
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

	// Check if HTTPRoute has TailscaleEndpoints backends
	for _, rule := range route.Spec.Rules {
		for _, backendRef := range rule.BackendRefs {
			if backendRef.Group != nil && *backendRef.Group == "gateway.tailscale.com" &&
				backendRef.Kind != nil && *backendRef.Kind == "TailscaleEndpoints" {
				return true
			}
		}
	}

	return false
}

// isTCPRouteRelevant checks if a TCPRoute is relevant for this extension server
func (s *TailscaleExtensionServer) isTCPRouteRelevant(route *gwapiv1alpha2.TCPRoute) bool {
	// Check if TCPRoute has Tailscale annotations or references extension-enabled Gateways
	annotations := route.GetAnnotations()
	if annotations != nil {
		if _, hasTailscaleAnnotation := annotations["gateway.tailscale.com/gateway"]; hasTailscaleAnnotation {
			return true
		}
	}

	// Check if TCPRoute references TailscaleEndpoints backends
	for _, rule := range route.Spec.Rules {
		for _, backendRef := range rule.BackendRefs {
			if backendRef.Kind != nil && *backendRef.Kind == "TailscaleEndpoints" {
				return true
			}
		}
	}

	return false
}

// isUDPRouteRelevant checks if a UDPRoute is relevant for this extension server
func (s *TailscaleExtensionServer) isUDPRouteRelevant(route *gwapiv1alpha2.UDPRoute) bool {
	// Check if UDPRoute has Tailscale annotations or references extension-enabled Gateways
	annotations := route.GetAnnotations()
	if annotations != nil {
		if _, hasTailscaleAnnotation := annotations["gateway.tailscale.com/gateway"]; hasTailscaleAnnotation {
			return true
		}
	}

	// Check if UDPRoute references TailscaleEndpoints backends
	for _, rule := range route.Spec.Rules {
		for _, backendRef := range rule.BackendRefs {
			if backendRef.Kind != nil && *backendRef.Kind == "TailscaleEndpoints" {
				return true
			}
		}
	}

	return false
}

// isTLSRouteRelevant checks if a TLSRoute is relevant for this extension server
func (s *TailscaleExtensionServer) isTLSRouteRelevant(route *gwapiv1alpha2.TLSRoute) bool {
	// Check if TLSRoute has Tailscale annotations or references extension-enabled Gateways
	annotations := route.GetAnnotations()
	if annotations != nil {
		if _, hasTailscaleAnnotation := annotations["gateway.tailscale.com/gateway"]; hasTailscaleAnnotation {
			return true
		}
	}

	// Check if TLSRoute references TailscaleEndpoints backends
	for _, rule := range route.Spec.Rules {
		for _, backendRef := range rule.BackendRefs {
			if backendRef.Kind != nil && *backendRef.Kind == "TailscaleEndpoints" {
				return true
			}
		}
	}

	return false
}

// isGRPCRouteRelevant checks if a GRPCRoute is relevant for this extension server
func (s *TailscaleExtensionServer) isGRPCRouteRelevant(route *gwapiv1.GRPCRoute) bool {
	// Check if GRPCRoute has Tailscale annotations or references extension-enabled Gateways
	annotations := route.GetAnnotations()
	if annotations != nil {
		if _, hasTailscaleAnnotation := annotations["gateway.tailscale.com/gateway"]; hasTailscaleAnnotation {
			return true
		}
	}

	// Check if GRPCRoute references TailscaleEndpoints backends
	for _, rule := range route.Spec.Rules {
		for _, backendRef := range rule.BackendRefs {
			if backendRef.Kind != nil && *backendRef.Kind == "TailscaleEndpoints" {
				return true
			}
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
	return s.discoverKubernetesServicesFromAllRoutes(ctx, &ServiceDiscoveryContext{
		HTTPRoutes: httpRoutes,
	})
}

// discoverKubernetesServicesFromAllRoutes discovers services from all Gateway API route types
func (s *TailscaleExtensionServer) discoverKubernetesServicesFromAllRoutes(ctx context.Context, discoveryCtx *ServiceDiscoveryContext) ([]corev1.Service, error) {
	serviceMap := make(map[string]*corev1.Service)
	tailscaleEndpointsMap := make(map[string]*gatewayv1alpha1.TailscaleEndpoints)

	// Process HTTPRoutes
	for _, route := range discoveryCtx.HTTPRoutes {
		if err := s.processHTTPRouteBackends(ctx, &route, serviceMap, tailscaleEndpointsMap); err != nil {
			s.logger.Warn("Failed to process HTTPRoute backends", "route", route.Name, "error", err)
		}
	}

	// Process TCPRoutes
	for _, route := range discoveryCtx.TCPRoutes {
		if err := s.processTCPRouteBackends(ctx, &route, serviceMap, tailscaleEndpointsMap); err != nil {
			s.logger.Warn("Failed to process TCPRoute backends", "route", route.Name, "error", err)
		}
	}

	// Process UDPRoutes
	for _, route := range discoveryCtx.UDPRoutes {
		if err := s.processUDPRouteBackends(ctx, &route, serviceMap, tailscaleEndpointsMap); err != nil {
			s.logger.Warn("Failed to process UDPRoute backends", "route", route.Name, "error", err)
		}
	}

	// Process TLSRoutes
	for _, route := range discoveryCtx.TLSRoutes {
		if err := s.processTLSRouteBackends(ctx, &route, serviceMap, tailscaleEndpointsMap); err != nil {
			s.logger.Warn("Failed to process TLSRoute backends", "route", route.Name, "error", err)
		}
	}

	// Process GRPCRoutes
	for _, route := range discoveryCtx.GRPCRoutes {
		if err := s.processGRPCRouteBackends(ctx, &route, serviceMap, tailscaleEndpointsMap); err != nil {
			s.logger.Warn("Failed to process GRPCRoute backends", "route", route.Name, "error", err)
		}
	}

	// Convert map to slice
	var services []corev1.Service
	for _, service := range serviceMap {
		services = append(services, *service)
	}

	s.logger.Info("Discovered services from all route types",
		"httpRoutes", len(discoveryCtx.HTTPRoutes),
		"tcpRoutes", len(discoveryCtx.TCPRoutes),
		"udpRoutes", len(discoveryCtx.UDPRoutes),
		"tlsRoutes", len(discoveryCtx.TLSRoutes),
		"grpcRoutes", len(discoveryCtx.GRPCRoutes),
		"services", len(services))
	return services, nil
}

// processHTTPRouteBackends processes backends from HTTPRoute
func (s *TailscaleExtensionServer) processHTTPRouteBackends(ctx context.Context, route *gwapiv1.HTTPRoute, serviceMap map[string]*corev1.Service, tailscaleEndpointsMap map[string]*gatewayv1alpha1.TailscaleEndpoints) error {
	for _, rule := range route.Spec.Rules {
		for _, backendRef := range rule.BackendRefs {
			// Process TailscaleEndpoints backends (Gateway API compliance)
			if backendRef.Group != nil && *backendRef.Group == "gateway.tailscale.com" &&
				backendRef.Kind != nil && *backendRef.Kind == "TailscaleEndpoints" {
				if err := s.processTailscaleEndpointsBackend(ctx, &backendRef, route, tailscaleEndpointsMap); err != nil {
					s.logger.Warn("Failed to process TailscaleEndpoints backend",
						"backend", backendRef.Name, "error", err)
				}
				continue
			}

			// Process standard Kubernetes Service backends
			if backendRef.Kind != nil && *backendRef.Kind != "Service" {
				continue // Skip other non-Service backends
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

			serviceMap[serviceKey] = service
		}
	}
	return nil
}

// createHTTPRouteBackendMappings creates backend mappings from HTTPRoutes and Services
func (s *TailscaleExtensionServer) createHTTPRouteBackendMappings(ctx context.Context, httpRoutes []gwapiv1.HTTPRoute, services []corev1.Service) ([]HTTPRouteBackendMapping, error) {
	var mappings []HTTPRouteBackendMapping

	// Create service lookup map
	serviceMap := make(map[string]*corev1.Service)
	for _, service := range services {
		key := fmt.Sprintf("%s/%s", service.Namespace, service.Name)
		serviceMap[key] = &service
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

// reloadConfigCache reloads the configuration cache from Kubernetes resources
func (s *TailscaleExtensionServer) reloadConfigCache(ctx context.Context) error {
	if s.configCache == nil {
		return fmt.Errorf("config cache not initialized")
	}

	s.logger.Info("Reloading configuration cache")

	// Load TailscaleEndpoints
	endpointsList, err := s.discoverTailscaleEndpoints(ctx)
	if err != nil {
		return fmt.Errorf("failed to discover TailscaleEndpoints: %w", err)
	}

	// Update endpoints cache
	for _, endpoints := range endpointsList {
		key := fmt.Sprintf("%s/%s", endpoints.Namespace, endpoints.Name)
		s.configCache.tailscaleEndpoints[key] = &endpoints
	}

	// Load TailscaleGateways
	var gatewayList gatewayv1alpha1.TailscaleGatewayList
	if err := s.client.List(ctx, &gatewayList); err != nil {
		return fmt.Errorf("failed to list TailscaleGateways: %w", err)
	}

	// Update gateway configs cache
	for _, gateway := range gatewayList.Items {
		key := fmt.Sprintf("%s/%s", gateway.Namespace, gateway.Name)
		s.configCache.gatewayConfigs[key] = &gateway

		// Cache route generation config from tailnets if present
		for _, tailnet := range gateway.Spec.Tailnets {
			if tailnet.RouteGeneration != nil {
				tailnetKey := fmt.Sprintf("%s/%s", key, tailnet.Name)
				s.configCache.routeGenerationConfig[tailnetKey] = tailnet.RouteGeneration
			}
		}
	}

	s.configCache.lastUpdate = time.Now()
	s.logger.Info("Configuration cache reloaded successfully")

	// Rebuild resource index after config reload
	if err := s.rebuildIndex(ctx); err != nil {
		s.logger.Error("Failed to rebuild resource index", "error", err)
		// Don't fail the config reload if index rebuild fails
	}

	return nil
}

// generateRoutePrefixFromConfig generates route prefix using configuration
func (s *TailscaleExtensionServer) generateRoutePrefixFromConfig(serviceName, namespace string) string {
	if s.configCache == nil {
		return "/api/" + serviceName // fallback to default
	}

	// Look for route generation config in the cache
	for key, config := range s.configCache.routeGenerationConfig {
		if strings.Contains(key, namespace) && config.Egress != nil {
			// Replace {service} placeholder with actual service name
			pathPrefix := config.Egress.PathPrefix
			if pathPrefix != "" {
				return strings.ReplaceAll(pathPrefix, "{service}", serviceName)
			}
		}
	}

	// Fallback to default pattern
	return "/api/" + serviceName
}

// NewResourceIndex creates a new ResourceIndex
func NewResourceIndex() *ResourceIndex {
	return &ResourceIndex{
		endpointsToHTTPRoutes:  make(map[string][]string),
		httpRoutesToEndpoints:  make(map[string][]string),
		endpointsToVIPServices: make(map[string][]VIPServiceReference),
		vipServicesToEndpoints: make(map[string][]string),
		endpointsToTCPRoutes:   make(map[string][]string),
		endpointsToUDPRoutes:   make(map[string][]string),
		endpointsToTLSRoutes:   make(map[string][]string),
		endpointsToGRPCRoutes:  make(map[string][]string),
		serviceDependencies:    make(map[string][]string),
		lastIndexUpdate:        time.Now(),
	}
}

// rebuildIndex rebuilds the entire resource index from current state
func (s *TailscaleExtensionServer) rebuildIndex(ctx context.Context) error {
	if s.resourceIndex == nil {
		s.resourceIndex = NewResourceIndex()
	}

	s.resourceIndex.mu.Lock()
	defer s.resourceIndex.mu.Unlock()

	s.logger.Info("Rebuilding resource index")

	// Clear existing indices
	s.clearIndices()

	// Index HTTPRoutes and their TailscaleEndpoints backends
	if err := s.indexHTTPRoutes(ctx); err != nil {
		return fmt.Errorf("failed to index HTTPRoutes: %w", err)
	}

	// Index TCP, UDP, TLS, GRPC routes
	if err := s.indexTCPRoutes(ctx); err != nil {
		return fmt.Errorf("failed to index TCPRoutes: %w", err)
	}

	if err := s.indexUDPRoutes(ctx); err != nil {
		return fmt.Errorf("failed to index UDPRoutes: %w", err)
	}

	if err := s.indexTLSRoutes(ctx); err != nil {
		return fmt.Errorf("failed to index TLSRoutes: %w", err)
	}

	if err := s.indexGRPCRoutes(ctx); err != nil {
		return fmt.Errorf("failed to index GRPCRoutes: %w", err)
	}

	// Index VIP services
	if err := s.indexVIPServices(ctx); err != nil {
		return fmt.Errorf("failed to index VIP services: %w", err)
	}

	s.resourceIndex.indexUpdateCount++
	s.resourceIndex.lastIndexUpdate = time.Now()

	s.logger.Info("Resource index rebuild completed",
		"httpRoutes", len(s.resourceIndex.httpRoutesToEndpoints),
		"tcpRoutes", len(s.resourceIndex.endpointsToTCPRoutes),
		"vipServices", len(s.resourceIndex.vipServicesToEndpoints))

	return nil
}

// clearIndices clears all index maps
func (s *TailscaleExtensionServer) clearIndices() {
	s.resourceIndex.endpointsToHTTPRoutes = make(map[string][]string)
	s.resourceIndex.httpRoutesToEndpoints = make(map[string][]string)
	s.resourceIndex.endpointsToVIPServices = make(map[string][]VIPServiceReference)
	s.resourceIndex.vipServicesToEndpoints = make(map[string][]string)
	s.resourceIndex.endpointsToTCPRoutes = make(map[string][]string)
	s.resourceIndex.endpointsToUDPRoutes = make(map[string][]string)
	s.resourceIndex.endpointsToTLSRoutes = make(map[string][]string)
	s.resourceIndex.endpointsToGRPCRoutes = make(map[string][]string)
	s.resourceIndex.serviceDependencies = make(map[string][]string)
}

// indexHTTPRoutes indexes all HTTPRoutes and their TailscaleEndpoints relationships
func (s *TailscaleExtensionServer) indexHTTPRoutes(ctx context.Context) error {
	routes, err := s.discoverHTTPRoutes(ctx)
	if err != nil {
		return err
	}

	for _, route := range routes {
		routeKey := fmt.Sprintf("%s/%s", route.Namespace, route.Name)

		for _, rule := range route.Spec.Rules {
			for _, backendRef := range rule.BackendRefs {
				if backendRef.Group != nil && *backendRef.Group == "gateway.tailscale.com" &&
					backendRef.Kind != nil && *backendRef.Kind == "TailscaleEndpoints" {

					// Determine namespace
					namespace := route.Namespace
					if backendRef.Namespace != nil {
						namespace = string(*backendRef.Namespace)
					}

					endpointsKey := fmt.Sprintf("%s/%s", namespace, backendRef.Name)

					// Add bidirectional mapping
					s.addToStringSlice(s.resourceIndex.endpointsToHTTPRoutes, endpointsKey, routeKey)
					s.addToStringSlice(s.resourceIndex.httpRoutesToEndpoints, routeKey, endpointsKey)
				}
			}
		}
	}

	return nil
}

// indexTCPRoutes indexes all TCPRoutes and their TailscaleEndpoints relationships
func (s *TailscaleExtensionServer) indexTCPRoutes(ctx context.Context) error {
	routes, err := s.discoverTCPRoutes(ctx)
	if err != nil {
		return err
	}

	for _, route := range routes {
		routeKey := fmt.Sprintf("%s/%s", route.Namespace, route.Name)

		for _, rule := range route.Spec.Rules {
			for _, backendRef := range rule.BackendRefs {
				if backendRef.Kind != nil && *backendRef.Kind == "TailscaleEndpoints" {

					// Determine namespace
					namespace := route.Namespace
					if backendRef.Namespace != nil {
						namespace = string(*backendRef.Namespace)
					}

					endpointsKey := fmt.Sprintf("%s/%s", namespace, backendRef.Name)
					s.addToStringSlice(s.resourceIndex.endpointsToTCPRoutes, endpointsKey, routeKey)
				}
			}
		}
	}

	return nil
}

// indexUDPRoutes indexes all UDPRoutes and their TailscaleEndpoints relationships
func (s *TailscaleExtensionServer) indexUDPRoutes(ctx context.Context) error {
	routes, err := s.discoverUDPRoutes(ctx)
	if err != nil {
		return err
	}

	for _, route := range routes {
		routeKey := fmt.Sprintf("%s/%s", route.Namespace, route.Name)

		for _, rule := range route.Spec.Rules {
			for _, backendRef := range rule.BackendRefs {
				if backendRef.Kind != nil && *backendRef.Kind == "TailscaleEndpoints" {

					namespace := route.Namespace
					if backendRef.Namespace != nil {
						namespace = string(*backendRef.Namespace)
					}

					endpointsKey := fmt.Sprintf("%s/%s", namespace, backendRef.Name)
					s.addToStringSlice(s.resourceIndex.endpointsToUDPRoutes, endpointsKey, routeKey)
				}
			}
		}
	}

	return nil
}

// indexTLSRoutes indexes all TLSRoutes and their TailscaleEndpoints relationships
func (s *TailscaleExtensionServer) indexTLSRoutes(ctx context.Context) error {
	routes, err := s.discoverTLSRoutes(ctx)
	if err != nil {
		return err
	}

	for _, route := range routes {
		routeKey := fmt.Sprintf("%s/%s", route.Namespace, route.Name)

		for _, rule := range route.Spec.Rules {
			for _, backendRef := range rule.BackendRefs {
				if backendRef.Kind != nil && *backendRef.Kind == "TailscaleEndpoints" {

					namespace := route.Namespace
					if backendRef.Namespace != nil {
						namespace = string(*backendRef.Namespace)
					}

					endpointsKey := fmt.Sprintf("%s/%s", namespace, backendRef.Name)
					s.addToStringSlice(s.resourceIndex.endpointsToTLSRoutes, endpointsKey, routeKey)
				}
			}
		}
	}

	return nil
}

// indexGRPCRoutes indexes all GRPCRoutes and their TailscaleEndpoints relationships
func (s *TailscaleExtensionServer) indexGRPCRoutes(ctx context.Context) error {
	routes, err := s.discoverGRPCRoutes(ctx)
	if err != nil {
		return err
	}

	for _, route := range routes {
		routeKey := fmt.Sprintf("%s/%s", route.Namespace, route.Name)

		for _, rule := range route.Spec.Rules {
			for _, backendRef := range rule.BackendRefs {
				if backendRef.Kind != nil && *backendRef.Kind == "TailscaleEndpoints" {

					namespace := route.Namespace
					if backendRef.Namespace != nil {
						namespace = string(*backendRef.Namespace)
					}

					endpointsKey := fmt.Sprintf("%s/%s", namespace, backendRef.Name)
					s.addToStringSlice(s.resourceIndex.endpointsToGRPCRoutes, endpointsKey, routeKey)
				}
			}
		}
	}

	return nil
}

// indexVIPServices indexes VIP service relationships with TailscaleEndpoints
func (s *TailscaleExtensionServer) indexVIPServices(ctx context.Context) error {
	// This would integrate with the ServiceCoordinator to index VIP services
	// For now, we'll index based on TailscaleEndpoints ExternalTarget mappings

	endpointsList, err := s.discoverTailscaleEndpoints(ctx)
	if err != nil {
		return err
	}

	for _, endpoints := range endpointsList {
		endpointsKey := fmt.Sprintf("%s/%s", endpoints.Namespace, endpoints.Name)

		for _, endpoint := range endpoints.Spec.Endpoints {
			if endpoint.ExternalTarget != "" {
				// Create VIP service reference
				vipService := VIPServiceReference{
					ServiceName: endpoint.Name,
					ClusterName: fmt.Sprintf("external-backend-%s", endpoint.Name),
					Metadata: map[string]interface{}{
						"protocol":      endpoint.Protocol,
						"port":          endpoint.Port,
						"tailscaleIP":   endpoint.TailscaleIP,
						"tailscaleFQDN": endpoint.TailscaleFQDN,
					},
				}

				// Add to indices
				s.resourceIndex.endpointsToVIPServices[endpointsKey] = append(
					s.resourceIndex.endpointsToVIPServices[endpointsKey], vipService)

				s.addToStringSlice(s.resourceIndex.vipServicesToEndpoints,
					vipService.ServiceName, endpointsKey)
			}
		}
	}

	return nil
}

// addToStringSlice adds a value to a string slice if it doesn't already exist
func (s *TailscaleExtensionServer) addToStringSlice(m map[string][]string, key, value string) {
	if m[key] == nil {
		m[key] = make([]string, 0)
	}

	// Check if value already exists
	for _, existing := range m[key] {
		if existing == value {
			return
		}
	}

	m[key] = append(m[key], value)
}

// GetRoutesForEndpoints returns all routes that reference a TailscaleEndpoints resource
func (s *TailscaleExtensionServer) GetRoutesForEndpoints(endpointsKey string) map[string][]string {
	if s.resourceIndex == nil {
		return nil
	}

	s.resourceIndex.mu.RLock()
	defer s.resourceIndex.mu.RUnlock()

	result := make(map[string][]string)

	if httpRoutes := s.resourceIndex.endpointsToHTTPRoutes[endpointsKey]; len(httpRoutes) > 0 {
		result["HTTPRoute"] = httpRoutes
	}
	if tcpRoutes := s.resourceIndex.endpointsToTCPRoutes[endpointsKey]; len(tcpRoutes) > 0 {
		result["TCPRoute"] = tcpRoutes
	}
	if udpRoutes := s.resourceIndex.endpointsToUDPRoutes[endpointsKey]; len(udpRoutes) > 0 {
		result["UDPRoute"] = udpRoutes
	}
	if tlsRoutes := s.resourceIndex.endpointsToTLSRoutes[endpointsKey]; len(tlsRoutes) > 0 {
		result["TLSRoute"] = tlsRoutes
	}
	if grpcRoutes := s.resourceIndex.endpointsToGRPCRoutes[endpointsKey]; len(grpcRoutes) > 0 {
		result["GRPCRoute"] = grpcRoutes
	}

	return result
}

// GetVIPServicesForEndpoints returns all VIP services associated with a TailscaleEndpoints resource
func (s *TailscaleExtensionServer) GetVIPServicesForEndpoints(endpointsKey string) []VIPServiceReference {
	if s.resourceIndex == nil {
		return nil
	}

	s.resourceIndex.mu.RLock()
	defer s.resourceIndex.mu.RUnlock()

	return s.resourceIndex.endpointsToVIPServices[endpointsKey]
}

// GetEndpointsForRoute returns all TailscaleEndpoints referenced by a specific route
func (s *TailscaleExtensionServer) GetEndpointsForRoute(routeKey string) []string {
	if s.resourceIndex == nil {
		return nil
	}

	s.resourceIndex.mu.RLock()
	defer s.resourceIndex.mu.RUnlock()

	return s.resourceIndex.httpRoutesToEndpoints[routeKey]
}

// GetResourceIndexStats returns statistics about the resource index
func (s *TailscaleExtensionServer) GetResourceIndexStats() map[string]interface{} {
	if s.resourceIndex == nil {
		return nil
	}

	s.resourceIndex.mu.RLock()
	defer s.resourceIndex.mu.RUnlock()

	return map[string]interface{}{
		"indexUpdateCount":    s.resourceIndex.indexUpdateCount,
		"lastIndexUpdate":     s.resourceIndex.lastIndexUpdate,
		"httpRouteCount":      len(s.resourceIndex.httpRoutesToEndpoints),
		"tcpRouteCount":       len(s.resourceIndex.endpointsToTCPRoutes),
		"udpRouteCount":       len(s.resourceIndex.endpointsToUDPRoutes),
		"tlsRouteCount":       len(s.resourceIndex.endpointsToTLSRoutes),
		"grpcRouteCount":      len(s.resourceIndex.endpointsToGRPCRoutes),
		"vipServiceCount":     len(s.resourceIndex.vipServicesToEndpoints),
		"endpointsCount":      len(s.resourceIndex.endpointsToHTTPRoutes),
		"serviceDependencies": len(s.resourceIndex.serviceDependencies),
	}
}

// processTailscaleEndpointsBackend processes TailscaleEndpoints backends for Gateway API compliance
func (s *TailscaleExtensionServer) processTailscaleEndpointsBackend(ctx context.Context, backendRef *gwapiv1.HTTPBackendRef, route *gwapiv1.HTTPRoute, tailscaleEndpointsMap map[string]*gatewayv1alpha1.TailscaleEndpoints) error {
	// Extract TailscaleEndpoints reference
	endpointsName := string(backendRef.Name)
	endpointsNamespace := route.Namespace
	if backendRef.Namespace != nil {
		endpointsNamespace = string(*backendRef.Namespace)
	}

	endpointsKey := fmt.Sprintf("%s/%s", endpointsNamespace, endpointsName)

	// Check if already processed
	if _, exists := tailscaleEndpointsMap[endpointsKey]; exists {
		return nil
	}

	// Fetch TailscaleEndpoints resource
	endpoints := &gatewayv1alpha1.TailscaleEndpoints{}
	endpointsObjectKey := types.NamespacedName{
		Name:      endpointsName,
		Namespace: endpointsNamespace,
	}

	if err := s.client.Get(ctx, endpointsObjectKey, endpoints); err != nil {
		return fmt.Errorf("failed to get TailscaleEndpoints %s: %w", endpointsKey, err)
	}

	tailscaleEndpointsMap[endpointsKey] = endpoints

	// Process each endpoint for VIP service creation
	for _, endpoint := range endpoints.Spec.Endpoints {
		if endpoint.ExternalTarget != "" {
			// Create or attach to VIP service if ServiceCoordinator is available
			if s.serviceCoordinator != nil {
				routeName := fmt.Sprintf("%s/%s", route.Namespace, route.Name)
				_, err := s.serviceCoordinator.EnsureServiceForRoute(ctx, routeName, endpoint.ExternalTarget)
				if err != nil {
					s.logger.Warn("Failed to ensure VIP service for TailscaleEndpoints backend",
						"endpoint", endpoint.Name, "target", endpoint.ExternalTarget, "error", err)
				}
			}
		}
	}

	s.logger.Info("Processed TailscaleEndpoints backend",
		"endpoints", endpointsKey, "count", len(endpoints.Spec.Endpoints))

	return nil
}

// recordHookMetrics records metrics for hook call performance
func (s *TailscaleExtensionServer) recordHookMetrics(hookType string, duration time.Duration, err error) {
	s.metrics.mu.Lock()
	defer s.metrics.mu.Unlock()

	s.metrics.totalHookCalls++
	s.metrics.lastCallDuration = duration

	// Update average call duration
	if s.metrics.totalHookCalls == 1 {
		s.metrics.averageCallDuration = duration
	} else {
		s.metrics.averageCallDuration = time.Duration(
			(int64(s.metrics.averageCallDuration)*s.metrics.totalHookCalls + int64(duration)) / (s.metrics.totalHookCalls + 1),
		)
	}

	if err != nil {
		s.metrics.failedCalls++
	} else {
		s.metrics.successfulCalls++
	}

	// Update hook-type specific metrics
	if _, exists := s.metrics.hookTypeMetrics[hookType]; !exists {
		s.metrics.hookTypeMetrics[hookType] = &HookTypeMetrics{}
	}

	hookMetrics := s.metrics.hookTypeMetrics[hookType]
	hookMetrics.calls++

	if err != nil {
		hookMetrics.failures++
	} else {
		hookMetrics.successes++
	}

	// Update hook-type average response time
	if hookMetrics.calls == 1 {
		hookMetrics.averageResponse = duration
	} else {
		hookMetrics.averageResponse = time.Duration(
			(int64(hookMetrics.averageResponse)*hookMetrics.calls + int64(duration)) / (hookMetrics.calls + 1),
		)
	}
}

// startConfigCacheUpdater starts a background goroutine to update configuration cache
func (s *TailscaleExtensionServer) startConfigCacheUpdater(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second) // Update every 30 seconds
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.updateConfigCache(ctx); err != nil {
				s.logger.Warn("Failed to update config cache", "error", err)
			}
		}
	}
}

// updateConfigCache updates the configuration cache with latest resources
func (s *TailscaleExtensionServer) updateConfigCache(ctx context.Context) error {
	s.configCache.mu.Lock()
	defer s.configCache.mu.Unlock()

	// Update TailscaleEndpoints
	var endpointsList gatewayv1alpha1.TailscaleEndpointsList
	if err := s.client.List(ctx, &endpointsList); err != nil {
		return fmt.Errorf("failed to list TailscaleEndpoints: %w", err)
	}

	newEndpoints := make(map[string]*gatewayv1alpha1.TailscaleEndpoints)
	for _, endpoints := range endpointsList.Items {
		key := fmt.Sprintf("%s/%s", endpoints.Namespace, endpoints.Name)
		newEndpoints[key] = &endpoints
	}
	s.configCache.tailscaleEndpoints = newEndpoints

	// Update TailscaleGateways and their route generation configs
	var gatewaysList gatewayv1alpha1.TailscaleGatewayList
	if err := s.client.List(ctx, &gatewaysList); err != nil {
		return fmt.Errorf("failed to list TailscaleGateways: %w", err)
	}

	newGateways := make(map[string]*gatewayv1alpha1.TailscaleGateway)
	newRouteConfigs := make(map[string]*gatewayv1alpha1.RouteGenerationConfig)

	for _, gateway := range gatewaysList.Items {
		key := fmt.Sprintf("%s/%s", gateway.Namespace, gateway.Name)
		newGateways[key] = &gateway

		// Cache route generation configs for each tailnet
		for _, tailnet := range gateway.Spec.Tailnets {
			if tailnet.RouteGeneration != nil {
				tailnetKey := fmt.Sprintf("%s/%s", key, tailnet.Name)
				newRouteConfigs[tailnetKey] = tailnet.RouteGeneration
			}
		}
	}

	s.configCache.gatewayConfigs = newGateways
	s.configCache.routeGenerationConfig = newRouteConfigs
	s.configCache.lastUpdate = time.Now()

	s.logger.Debug("Updated config cache",
		"endpoints", len(newEndpoints),
		"gateways", len(newGateways),
		"routeConfigs", len(newRouteConfigs))

	return nil
}

// getRouteGenerationConfig retrieves route generation config for a specific gateway/tailnet
func (s *TailscaleExtensionServer) getRouteGenerationConfig(gatewayKey, tailnetName string) *gatewayv1alpha1.RouteGenerationConfig {
	s.configCache.mu.RLock()
	defer s.configCache.mu.RUnlock()

	tailnetKey := fmt.Sprintf("%s/%s", gatewayKey, tailnetName)
	return s.configCache.routeGenerationConfig[tailnetKey]
}

// injectClusterMetadata injects Tailscale VIP service metadata into clusters
func (s *TailscaleExtensionServer) injectClusterMetadata(cluster *clusterv3.Cluster, vipInfo *VIPServiceInfo) {
	if cluster.LoadAssignment == nil || len(cluster.LoadAssignment.Endpoints) == 0 {
		return
	}

	for _, endpoints := range cluster.LoadAssignment.Endpoints {
		for _, endpoint := range endpoints.LbEndpoints {
			if endpoint.Metadata == nil {
				endpoint.Metadata = &corev3.Metadata{}
			}
			if endpoint.Metadata.FilterMetadata == nil {
				endpoint.Metadata.FilterMetadata = make(map[string]*structpb.Struct)
			}

			// Inject Tailscale VIP service metadata
			metadata := &structpb.Struct{Fields: make(map[string]*structpb.Value)}
			metadata.Fields["service_name"] = structpb.NewStringValue(vipInfo.ServiceName)
			metadata.Fields["owner_cluster"] = structpb.NewStringValue(vipInfo.OwnerCluster)
			metadata.Fields["external_target"] = structpb.NewStringValue(vipInfo.ExternalTarget)
			metadata.Fields["last_updated"] = structpb.NewStringValue(vipInfo.LastUpdated.Format(time.RFC3339))

			if len(vipInfo.VIPAddresses) > 0 {
				metadata.Fields["vip_addresses"] = structpb.NewStringValue(strings.Join(vipInfo.VIPAddresses, ","))
			}

			endpoint.Metadata.FilterMetadata["tailscale.gateway.io"] = metadata
		}
	}
}

// generateRouteFromConfig generates route paths based on RouteGenerationConfig
func (s *TailscaleExtensionServer) generateRouteFromConfig(serviceName, tailnetName string, config *gatewayv1alpha1.RouteGenerationConfig, routeType string) string {
	if config == nil {
		// Fallback to hardcoded patterns if no config
		if routeType == "egress" {
			return fmt.Sprintf("/api/%s", serviceName)
		}
		return "/"
	}

	var pathPattern string
	if routeType == "egress" && config.Egress != nil {
		pathPattern = config.Egress.PathPrefix
	} else if routeType == "ingress" && config.Ingress != nil {
		pathPattern = config.Ingress.PathPrefix
	} else {
		// Default patterns
		if routeType == "egress" {
			pathPattern = "/tailscale/{service}/"
		} else {
			pathPattern = "/"
		}
	}

	// Replace template variables
	pathPattern = strings.ReplaceAll(pathPattern, "{service}", serviceName)
	pathPattern = strings.ReplaceAll(pathPattern, "{tailnet}", tailnetName)

	return pathPattern
}

// GetMetrics returns current extension server metrics
func (s *TailscaleExtensionServer) GetMetrics() *ExtensionServerMetrics {
	s.metrics.mu.RLock()
	defer s.metrics.mu.RUnlock()

	// Return a copy to avoid data races
	metrics := &ExtensionServerMetrics{
		totalHookCalls:      s.metrics.totalHookCalls,
		successfulCalls:     s.metrics.successfulCalls,
		failedCalls:         s.metrics.failedCalls,
		lastCallDuration:    s.metrics.lastCallDuration,
		averageCallDuration: s.metrics.averageCallDuration,
		hookTypeMetrics:     make(map[string]*HookTypeMetrics),
	}

	for hookType, hookMetrics := range s.metrics.hookTypeMetrics {
		metrics.hookTypeMetrics[hookType] = &HookTypeMetrics{
			calls:           hookMetrics.calls,
			successes:       hookMetrics.successes,
			failures:        hookMetrics.failures,
			averageResponse: hookMetrics.averageResponse,
		}
	}

	return metrics
}

// SetServiceCoordinator sets the service coordinator for VIP service management
func (s *TailscaleExtensionServer) SetServiceCoordinator(coordinator *service.ServiceCoordinator) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.serviceCoordinator = coordinator
}

// getTailscaleEgressMappingsWithConfig gets Tailscale egress mappings using configuration-driven route generation
func (s *TailscaleExtensionServer) getTailscaleEgressMappingsWithConfig(ctx context.Context) ([]TailscaleServiceMapping, error) {
	var mappings []TailscaleServiceMapping

	// Get TailscaleEndpoints from cache or directly
	s.configCache.mu.RLock()
	tailscaleEndpoints := s.configCache.tailscaleEndpoints
	gatewayConfigs := s.configCache.gatewayConfigs
	s.configCache.mu.RUnlock()

	// If cache is empty, fetch directly
	if len(tailscaleEndpoints) == 0 {
		if err := s.updateConfigCache(ctx); err != nil {
			return nil, fmt.Errorf("failed to update config cache: %w", err)
		}
		s.configCache.mu.RLock()
		tailscaleEndpoints = s.configCache.tailscaleEndpoints
		gatewayConfigs = s.configCache.gatewayConfigs
		s.configCache.mu.RUnlock()
	}

	// Process each TailscaleEndpoints resource
	for endpointsKey, endpoints := range tailscaleEndpoints {
		parts := strings.Split(endpointsKey, "/")
		if len(parts) != 2 {
			continue
		}
		namespace, endpointsName := parts[0], parts[1]

		// Find corresponding TailscaleGateway for route generation config
		var routeConfig *gatewayv1alpha1.RouteGenerationConfig
		var tailnetName, gatewayKey string

		for gKey, gateway := range gatewayConfigs {
			for _, tailnet := range gateway.Spec.Tailnets {
				if tailnet.RouteGeneration != nil {
					gatewayKey = gKey
					tailnetName = tailnet.Name
					routeConfig = tailnet.RouteGeneration
					break
				}
			}
			if routeConfig != nil {
				break
			}
		}

		// Process each endpoint in the TailscaleEndpoints
		for _, endpoint := range endpoints.Spec.Endpoints {
			if endpoint.ExternalTarget == "" {
				continue // Skip endpoints without external targets
			}

			// Generate path prefix using configuration
			pathPrefix := s.generateRouteFromConfig(endpoint.Name, tailnetName, routeConfig, "egress")

			mapping := TailscaleServiceMapping{
				ServiceName:     endpoint.Name,
				ClusterName:     fmt.Sprintf("external-backend-%s", endpoint.Name),
				EgressService:   fmt.Sprintf("%s-%s-egress.%s.svc.cluster.local", endpointsName, endpoint.Name, namespace),
				ExternalBackend: endpoint.ExternalTarget,
				Port:            uint32(endpoint.Port),
				Protocol:        endpoint.Protocol,
				PathPrefix:      pathPrefix,
				TailnetName:     tailnetName,
				GatewayKey:      gatewayKey,
			}
			mappings = append(mappings, mapping)
		}
	}

	s.logger.Info("Generated Tailscale egress mappings with config", "count", len(mappings))
	return mappings, nil
}

// createTailscaleEgressRouteWithConfig creates a route using configuration-driven path generation
func (s *TailscaleExtensionServer) createTailscaleEgressRouteWithConfig(mapping TailscaleServiceMapping) *routev3.Route {
	// Use the configured path prefix instead of hardcoded pattern
	pathPrefix := mapping.PathPrefix
	if pathPrefix == "" {
		// Fallback to original pattern if no config available
		pathPrefix = fmt.Sprintf("/api/%s", mapping.ServiceName)
	}

	return &routev3.Route{
		Name: fmt.Sprintf("tailscale-egress-%s", mapping.ServiceName),
		Match: &routev3.RouteMatch{
			PathSpecifier: &routev3.RouteMatch_Prefix{
				Prefix: pathPrefix,
			},
		},
		Action: &routev3.Route_Route{
			Route: &routev3.RouteAction{
				ClusterSpecifier: &routev3.RouteAction_Cluster{
					Cluster: mapping.ClusterName,
				},
				// Remove the path prefix when forwarding to the backend
				PrefixRewrite: "/",
			},
		},
		RequestHeadersToAdd: []*corev3.HeaderValueOption{
			{
				Header: &corev3.HeaderValue{
					Key:   "X-Tailscale-Service",
					Value: mapping.ServiceName,
				},
			},
			{
				Header: &corev3.HeaderValue{
					Key:   "X-Tailscale-Backend",
					Value: mapping.ExternalBackend,
				},
			},
		},
	}
}

// processTailscaleEndpointsBackendHTTP processes TailscaleEndpoints backends for HTTP routes
func (s *TailscaleExtensionServer) processTailscaleEndpointsBackendHTTP(ctx context.Context, backend *gwapiv1.HTTPBackendRef, clusterName string) (*TailscaleServiceMapping, error) {
	// Create a simple mapping - this should use actual TailscaleEndpoints discovery
	mapping := &TailscaleServiceMapping{
		ServiceName:     string(backend.Name),
		ClusterName:     clusterName,
		ExternalBackend: fmt.Sprintf("%s:80", backend.Name), // placeholder
		Port:            uint32(*backend.Port),
		Protocol:        "HTTP",
	}
	return mapping, nil
}

// processTailscaleEndpointsBackendTCP processes TailscaleEndpoints backends for TCP routes
func (s *TailscaleExtensionServer) processTailscaleEndpointsBackendTCP(ctx context.Context, backend *gwapiv1alpha2.BackendRef, clusterName string) (*TailscaleServiceMapping, error) {
	port := uint32(80)
	if backend.Port != nil {
		port = uint32(*backend.Port)
	}
	mapping := &TailscaleServiceMapping{
		ServiceName:     string(backend.Name),
		ClusterName:     clusterName,
		ExternalBackend: fmt.Sprintf("%s:%d", backend.Name, port),
		Port:            port,
		Protocol:        "TCP",
	}
	return mapping, nil
}

// processTailscaleEndpointsBackendUDP processes TailscaleEndpoints backends for UDP routes
func (s *TailscaleExtensionServer) processTailscaleEndpointsBackendUDP(ctx context.Context, backend *gwapiv1alpha2.BackendRef, clusterName string) (*TailscaleServiceMapping, error) {
	port := uint32(53)
	if backend.Port != nil {
		port = uint32(*backend.Port)
	}
	mapping := &TailscaleServiceMapping{
		ServiceName:     string(backend.Name),
		ClusterName:     clusterName,
		ExternalBackend: fmt.Sprintf("%s:%d", backend.Name, port),
		Port:            port,
		Protocol:        "UDP",
	}
	return mapping, nil
}

// processTailscaleEndpointsBackendTLS processes TailscaleEndpoints backends for TLS routes
func (s *TailscaleExtensionServer) processTailscaleEndpointsBackendTLS(ctx context.Context, backend *gwapiv1alpha2.BackendRef, clusterName string) (*TailscaleServiceMapping, error) {
	port := uint32(443)
	if backend.Port != nil {
		port = uint32(*backend.Port)
	}
	mapping := &TailscaleServiceMapping{
		ServiceName:     string(backend.Name),
		ClusterName:     clusterName,
		ExternalBackend: fmt.Sprintf("%s:%d", backend.Name, port),
		Port:            port,
		Protocol:        "TLS",
	}
	return mapping, nil
}

// processTailscaleEndpointsBackendGRPC processes TailscaleEndpoints backends for GRPC routes
func (s *TailscaleExtensionServer) processTailscaleEndpointsBackendGRPC(ctx context.Context, backend *gwapiv1.GRPCBackendRef, clusterName string) (*TailscaleServiceMapping, error) {
	port := uint32(9090)
	if backend.Port != nil {
		port = uint32(*backend.Port)
	}
	mapping := &TailscaleServiceMapping{
		ServiceName:     string(backend.Name),
		ClusterName:     clusterName,
		ExternalBackend: fmt.Sprintf("%s:%d", backend.Name, port),
		Port:            port,
		Protocol:        "GRPC",
	}
	return mapping, nil
}

// processTCPRouteBackends processes backends for TCP routes
func (s *TailscaleExtensionServer) processTCPRouteBackends(ctx context.Context, tcpRoute *gwapiv1alpha2.TCPRoute, serviceMap map[string]*corev1.Service, tailscaleEndpointsMap map[string]*gatewayv1alpha1.TailscaleEndpoints) error {
	s.logger.Info("Processing TCP route backends", "route", tcpRoute.Name, "namespace", tcpRoute.Namespace)

	for _, rule := range tcpRoute.Spec.Rules {
		for _, backendRef := range rule.BackendRefs {
			if backendRef.Kind != nil && *backendRef.Kind == "TailscaleEndpoints" {
				clusterName := fmt.Sprintf("tcp-backend-%s-%s", tcpRoute.Name, backendRef.Name)
				if _, err := s.processTailscaleEndpointsBackendTCP(ctx, &backendRef, clusterName); err != nil {
					s.logger.Error("Failed to process TailscaleEndpoints backend for TCP route", "error", err, "backend", backendRef.Name)
					continue
				}
			}
		}
	}

	return nil
}

// processUDPRouteBackends processes backends for UDP routes
func (s *TailscaleExtensionServer) processUDPRouteBackends(ctx context.Context, udpRoute *gwapiv1alpha2.UDPRoute, serviceMap map[string]*corev1.Service, tailscaleEndpointsMap map[string]*gatewayv1alpha1.TailscaleEndpoints) error {
	s.logger.Info("Processing UDP route backends", "route", udpRoute.Name, "namespace", udpRoute.Namespace)

	for _, rule := range udpRoute.Spec.Rules {
		for _, backendRef := range rule.BackendRefs {
			if backendRef.Kind != nil && *backendRef.Kind == "TailscaleEndpoints" {
				clusterName := fmt.Sprintf("udp-backend-%s-%s", udpRoute.Name, backendRef.Name)
				if _, err := s.processTailscaleEndpointsBackendUDP(ctx, &backendRef, clusterName); err != nil {
					s.logger.Error("Failed to process TailscaleEndpoints backend for UDP route", "error", err, "backend", backendRef.Name)
					continue
				}
			}
		}
	}

	return nil
}

// processTLSRouteBackends processes backends for TLS routes
func (s *TailscaleExtensionServer) processTLSRouteBackends(ctx context.Context, tlsRoute *gwapiv1alpha2.TLSRoute, serviceMap map[string]*corev1.Service, tailscaleEndpointsMap map[string]*gatewayv1alpha1.TailscaleEndpoints) error {
	s.logger.Info("Processing TLS route backends", "route", tlsRoute.Name, "namespace", tlsRoute.Namespace)

	for _, rule := range tlsRoute.Spec.Rules {
		for _, backendRef := range rule.BackendRefs {
			if backendRef.Kind != nil && *backendRef.Kind == "TailscaleEndpoints" {
				clusterName := fmt.Sprintf("tls-backend-%s-%s", tlsRoute.Name, backendRef.Name)
				if _, err := s.processTailscaleEndpointsBackendTLS(ctx, &backendRef, clusterName); err != nil {
					s.logger.Error("Failed to process TailscaleEndpoints backend for TLS route", "error", err, "backend", backendRef.Name)
					continue
				}
			}
		}
	}

	return nil
}

// processGRPCRouteBackends processes backends for GRPC routes
func (s *TailscaleExtensionServer) processGRPCRouteBackends(ctx context.Context, grpcRoute *gwapiv1.GRPCRoute, serviceMap map[string]*corev1.Service, tailscaleEndpointsMap map[string]*gatewayv1alpha1.TailscaleEndpoints) error {
	s.logger.Info("Processing GRPC route backends", "route", grpcRoute.Name, "namespace", grpcRoute.Namespace)

	for _, rule := range grpcRoute.Spec.Rules {
		for _, backendRef := range rule.BackendRefs {
			if backendRef.Kind != nil && *backendRef.Kind == "TailscaleEndpoints" {
				clusterName := fmt.Sprintf("grpc-backend-%s-%s", grpcRoute.Name, backendRef.Name)
				if _, err := s.processTailscaleEndpointsBackendGRPC(ctx, &backendRef, clusterName); err != nil {
					s.logger.Error("Failed to process TailscaleEndpoints backend for GRPC route", "error", err, "backend", backendRef.Name)
					continue
				}
			}
		}
	}

	return nil
}

// HTTP handlers for metrics and health endpoints

// metricsHandler serves extension server metrics in JSON format
func (s *TailscaleExtensionServer) metricsHandler(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	metrics := s.metrics
	s.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")

	if metrics == nil {
		http.Error(w, "metrics not available", http.StatusServiceUnavailable)
		return
	}

	response := map[string]interface{}{
		"hook_calls": map[string]interface{}{
			"total_calls":        metrics.totalHookCalls,
			"successful_calls":   metrics.successfulCalls,
			"failed_calls":       metrics.failedCalls,
			"last_call_duration": metrics.lastCallDuration.String(),
			"average_duration":   metrics.averageCallDuration.String(),
		},
		"hook_type_metrics": make(map[string]interface{}),
		"timestamp":         time.Now().Format(time.RFC3339),
	}

	// Add hook type specific metrics
	for hookType, hookMetrics := range metrics.hookTypeMetrics {
		response["hook_type_metrics"].(map[string]interface{})[hookType] = map[string]interface{}{
			"calls":                 hookMetrics.calls,
			"successes":             hookMetrics.successes,
			"failures":              hookMetrics.failures,
			"average_response_time": hookMetrics.averageResponse.String(),
		}
	}

	if err := json.NewEncoder(w).Encode(response); err != nil {
		s.logger.Error("Failed to encode metrics response", "error", err)
		http.Error(w, "failed to encode metrics", http.StatusInternalServerError)
		return
	}
}

// healthHandler serves health check endpoint
func (s *TailscaleExtensionServer) healthHandler(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	metrics := s.metrics
	configCache := s.configCache
	resourceIndex := s.resourceIndex
	s.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")

	health := map[string]interface{}{
		"status":    "healthy",
		"timestamp": time.Now().Format(time.RFC3339),
		"checks": map[string]interface{}{
			"metrics_available":     metrics != nil,
			"config_cache_active":   configCache != nil,
			"resource_index_active": resourceIndex != nil,
		},
	}

	// Check if we've had hook calls recently
	if metrics != nil && metrics.totalHookCalls > 0 {
		health["checks"].(map[string]interface{})["hook_calls_recorded"] = true
		health["total_hook_calls"] = metrics.totalHookCalls
		health["successful_calls"] = metrics.successfulCalls
		health["failed_calls"] = metrics.failedCalls

		// Health is good if we have successful calls and low failure rate
		failureRate := float64(metrics.failedCalls) / float64(metrics.totalHookCalls)
		health["checks"].(map[string]interface{})["low_failure_rate"] = failureRate < 0.1 // Less than 10% failure rate
		health["failure_rate"] = failureRate
	}

	// Check cache health
	if configCache != nil {
		timeSinceLastUpdate := time.Since(configCache.lastUpdate)
		cacheUpdateThreshold := 10 * time.Minute

		health["checks"].(map[string]interface{})["cache_recently_updated"] = timeSinceLastUpdate < cacheUpdateThreshold
		health["last_cache_update"] = configCache.lastUpdate.Format(time.RFC3339)
		health["time_since_cache_update"] = timeSinceLastUpdate.String()
	}

	// Check resource index health
	if resourceIndex != nil {
		timeSinceLastIndexUpdate := time.Since(resourceIndex.lastIndexUpdate)
		indexUpdateThreshold := 10 * time.Minute

		health["checks"].(map[string]interface{})["index_recently_updated"] = timeSinceLastIndexUpdate < indexUpdateThreshold
		health["last_index_update"] = resourceIndex.lastIndexUpdate.Format(time.RFC3339)
		health["index_update_count"] = resourceIndex.indexUpdateCount
	}

	// Determine overall health status
	checks := health["checks"].(map[string]interface{})
	allHealthy := true
	for _, check := range checks {
		if healthy, ok := check.(bool); ok && !healthy {
			allHealthy = false
			break
		}
	}

	if !allHealthy {
		health["status"] = "degraded"
		w.WriteHeader(http.StatusServiceUnavailable)
	}

	if err := json.NewEncoder(w).Encode(health); err != nil {
		s.logger.Error("Failed to encode health response", "error", err)
		http.Error(w, "failed to encode health", http.StatusInternalServerError)
		return
	}
}

// StartHTTPServer starts the HTTP server for metrics and health endpoints
func (s *TailscaleExtensionServer) StartHTTPServer(addr string) error {
	mux := http.NewServeMux()

	// Register endpoints
	mux.HandleFunc("/metrics", s.metricsHandler)
	mux.HandleFunc("/health", s.healthHandler)
	mux.HandleFunc("/healthz", s.healthHandler) // Kubernetes-style health endpoint
	mux.HandleFunc("/ready", s.healthHandler)   // Readiness endpoint

	server := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  30 * time.Second,
	}

	s.logger.Info("Starting HTTP server for metrics and health endpoints", "address", addr)

	return server.ListenAndServe()
}

// getInboundVIPServiceMappings discovers VIP services for Gateway listeners (inbound traffic)
func (s *TailscaleExtensionServer) getInboundVIPServiceMappings(ctx context.Context) ([]InboundVIPServiceMapping, error) {
	var mappings []InboundVIPServiceMapping

	// Get all TailscaleGateways that might have created inbound VIP services
	gatewayList := &gatewayv1alpha1.TailscaleGatewayList{}
	if err := s.client.List(ctx, gatewayList); err != nil {
		return nil, fmt.Errorf("failed to list TailscaleGateways: %w", err)
	}

	for _, gateway := range gatewayList.Items {
		// Process VIP services in gateway status
		for _, serviceInfo := range gateway.Status.Services {
			// Look for inbound services (created for Gateway listeners)
			if strings.HasPrefix(serviceInfo.Name, "inbound-") {
				mapping := InboundVIPServiceMapping{
					ServiceName:  serviceInfo.Name,
					VIPAddresses: serviceInfo.VIPAddresses,
					OwnerCluster: serviceInfo.OwnerCluster,
					PathPrefix:   fmt.Sprintf("/gateway/%s/", strings.TrimPrefix(serviceInfo.Name, "inbound-")),
					TailnetName:  "default", // Could be extracted from gateway config
					GatewayKey:   fmt.Sprintf("%s/%s", gateway.Namespace, gateway.Name),
					Protocol:     "HTTP",
					Port:         80, // Could be extracted from Gateway listener
				}
				mappings = append(mappings, mapping)
			}
		}
	}

	s.logger.Info("Discovered inbound VIP service mappings", "count", len(mappings))
	return mappings, nil
}

// getCrossClusterVIPServiceMappings discovers VIP services from other clusters
func (s *TailscaleExtensionServer) getCrossClusterVIPServiceMappings(ctx context.Context) ([]CrossClusterVIPServiceMapping, error) {
	var mappings []CrossClusterVIPServiceMapping

	// Use ServiceCoordinator to discover VIP services across clusters
	if s.serviceCoordinator == nil {
		s.logger.Info("ServiceCoordinator not available, skipping cross-cluster discovery")
		return mappings, nil
	}

	// Get all VIP services from the tailnet
	vipServices, err := s.serviceCoordinator.DiscoverVIPServices(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to discover VIP services: %w", err)
	}

	for _, vipService := range vipServices {
		// Only include services from other clusters/operators
		if vipService.OwnerOperator != s.serviceCoordinator.GetOperatorID() {
			mapping := CrossClusterVIPServiceMapping{
				ServiceName:   string(vipService.ServiceName),
				VIPAddresses:  vipService.VIPAddresses,
				OwnerCluster:  vipService.OwnerOperator,
				ConsumerCount: len(vipService.ConsumerClusters),
				PathPrefix:    fmt.Sprintf("/cross-cluster/%s/", vipService.ServiceName),
				Protocol:      "HTTP",
				Port:          80,
				LastUpdated:   vipService.LastUpdated,
			}
			mappings = append(mappings, mapping)
		}
	}

	s.logger.Info("Discovered cross-cluster VIP service mappings", "count", len(mappings))
	return mappings, nil
}

// createInboundVIPRoute creates a route for inbound VIP services
func (s *TailscaleExtensionServer) createInboundVIPRoute(mapping InboundVIPServiceMapping) *routev3.Route {
	// Create route that accepts traffic from VIP addresses and forwards to Gateway
	route := &routev3.Route{
		Name: fmt.Sprintf("inbound-vip-%s", mapping.ServiceName),
		Match: &routev3.RouteMatch{
			PathSpecifier: &routev3.RouteMatch_Prefix{
				Prefix: mapping.PathPrefix,
			},
		},
		Action: &routev3.Route_Route{
			Route: &routev3.RouteAction{
				ClusterSpecifier: &routev3.RouteAction_Cluster{
					Cluster: fmt.Sprintf("inbound-gateway-%s", mapping.ServiceName),
				},
				PrefixRewrite: "/", // Strip the VIP prefix
			},
		},
	}

	return route
}

// createCrossClusterVIPRoute creates a route for cross-cluster VIP services
func (s *TailscaleExtensionServer) createCrossClusterVIPRoute(mapping CrossClusterVIPServiceMapping) *routev3.Route {
	// Create route that forwards traffic to VIP services in other clusters
	route := &routev3.Route{
		Name: fmt.Sprintf("cross-cluster-vip-%s", mapping.ServiceName),
		Match: &routev3.RouteMatch{
			PathSpecifier: &routev3.RouteMatch_Prefix{
				Prefix: mapping.PathPrefix,
			},
		},
		Action: &routev3.Route_Route{
			Route: &routev3.RouteAction{
				ClusterSpecifier: &routev3.RouteAction_Cluster{
					Cluster: fmt.Sprintf("cross-cluster-%s", mapping.ServiceName),
				},
				PrefixRewrite: "/", // Strip the cross-cluster prefix
			},
		},
	}

	return route
}
