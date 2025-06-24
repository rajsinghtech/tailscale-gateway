package extension

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"
	gwapiv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	gatewayv1alpha1 "github.com/rajsinghtech/tailscale-gateway/api/v1alpha1"
)

// MockTailscaleServiceMapping is a test helper
type MockTailscaleServiceMapping struct {
	ServiceName     string
	ClusterName     string
	EgressService   string
	ExternalBackend string
	Port            uint32
	Protocol        string
}

func TestExtensionServer(t *testing.T) {
	scheme := runtime.NewScheme()
	gatewayv1alpha1.AddToScheme(scheme)
	gwapiv1.AddToScheme(scheme)

	t.Run("service_discovery_basic", func(t *testing.T) {
		fc := fake.NewClientBuilder().
			WithScheme(scheme).
			Build()

		server := &TailscaleExtensionServer{
			client: fc,
		}
		_ = server // Use server to avoid unused variable warning

		// Test basic initialization
		if server.client == nil {
			t.Fatal("expected kube client to be set")
		}

		// Test service mapping discovery would go here
		// For now, just test that we can create the server structure
		if server == nil {
			t.Fatal("expected server to be created")
		}
	})

	t.Run("httproute_discovery", func(t *testing.T) {
		fc := fake.NewClientBuilder().
			WithScheme(scheme).
			Build()

		// Create test HTTPRoute
		httpRoute := &gwapiv1.HTTPRoute{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-route",
				Namespace: "default",
			},
			Spec: gwapiv1.HTTPRouteSpec{
				Hostnames: []gwapiv1.Hostname{"example.com"},
				Rules: []gwapiv1.HTTPRouteRule{
					{
						Matches: []gwapiv1.HTTPRouteMatch{
							{
								Path: &gwapiv1.HTTPPathMatch{
									Type:  pathTypePtr(gwapiv1.PathMatchPathPrefix),
									Value: stringPtr("/api/"),
								},
							},
						},
						BackendRefs: []gwapiv1.HTTPBackendRef{
							{
								BackendRef: gwapiv1.BackendRef{
									BackendObjectReference: gwapiv1.BackendObjectReference{
										Name: "external-service",
										Port: portNumberPtr(80),
									},
								},
							},
						},
					},
				},
			},
		}

		mustCreate(t, fc, httpRoute)

		server := &TailscaleExtensionServer{
			client: fc,
		}
		_ = server // Use server to avoid unused variable warning

		// Test HTTPRoute exists in cluster
		var routes gwapiv1.HTTPRouteList
		err := fc.List(context.Background(), &routes)
		if err != nil {
			t.Fatalf("failed to list HTTPRoutes: %v", err)
		}

		if len(routes.Items) != 1 {
			t.Fatalf("expected 1 HTTPRoute, got %d", len(routes.Items))
		}

		route := routes.Items[0]
		if route.Name != "test-route" {
			t.Errorf("expected route name 'test-route', got %q", route.Name)
		}

		if len(route.Spec.Rules) != 1 {
			t.Errorf("expected 1 rule, got %d", len(route.Spec.Rules))
		}
	})

	t.Run("tailscale_endpoints_discovery", func(t *testing.T) {
		fc := fake.NewClientBuilder().
			WithScheme(scheme).
			Build()

		// Create test TailscaleEndpoints
		endpoints := &gatewayv1alpha1.TailscaleEndpoints{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-endpoints",
				Namespace: "default",
			},
			Spec: gatewayv1alpha1.TailscaleEndpointsSpec{
				Tailnet: "test.tailnet.ts.net",
				Endpoints: []gatewayv1alpha1.TailscaleEndpoint{
					{
						Name:           "web-service",
						TailscaleIP:    "100.64.0.1",
						TailscaleFQDN:  "web-service.test.tailnet.ts.net",
						Port:           80,
						Protocol:       "HTTP",
						ExternalTarget: "httpbin.org:80",
					},
				},
			},
		}

		mustCreate(t, fc, endpoints)

		server := &TailscaleExtensionServer{
			client: fc,
		}
		_ = server // Use server to avoid unused variable warning

		// Test TailscaleEndpoints exists in cluster
		var endpointsList gatewayv1alpha1.TailscaleEndpointsList
		err := fc.List(context.Background(), &endpointsList)
		if err != nil {
			t.Fatalf("failed to list TailscaleEndpoints: %v", err)
		}

		if len(endpointsList.Items) != 1 {
			t.Fatalf("expected 1 TailscaleEndpoints, got %d", len(endpointsList.Items))
		}

		endpoint := endpointsList.Items[0]
		if endpoint.Name != "test-endpoints" {
			t.Errorf("expected endpoint name 'test-endpoints', got %q", endpoint.Name)
		}

		if len(endpoint.Spec.Endpoints) != 1 {
			t.Errorf("expected 1 endpoint definition, got %d", len(endpoint.Spec.Endpoints))
		}

		endpointDef := endpoint.Spec.Endpoints[0]
		if endpointDef.ExternalTarget != "httpbin.org:80" {
			t.Errorf("expected external target 'httpbin.org:80', got %q", endpointDef.ExternalTarget)
		}
	})

	t.Run("service_mapping_generation", func(t *testing.T) {
		fc := fake.NewClientBuilder().
			WithScheme(scheme).
			Build()

		// Create TailscaleEndpoints with external targets
		endpoints := &gatewayv1alpha1.TailscaleEndpoints{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-endpoints",
				Namespace: "default",
			},
			Spec: gatewayv1alpha1.TailscaleEndpointsSpec{
				Tailnet: "test.tailnet.ts.net",
				Endpoints: []gatewayv1alpha1.TailscaleEndpoint{
					{
						Name:           "web-service",
						TailscaleIP:    "100.64.0.1",
						TailscaleFQDN:  "web-service.test.tailnet.ts.net",
						Port:           80,
						Protocol:       "HTTP",
						ExternalTarget: "httpbin.org:80",
					},
					{
						Name:           "api-service",
						TailscaleIP:    "100.64.0.2",
						TailscaleFQDN:  "api-service.test.tailnet.ts.net",
						Port:           443,
						Protocol:       "HTTPS",
						ExternalTarget: "api.github.com:443",
					},
				},
			},
		}

		mustCreate(t, fc, endpoints)

		server := &TailscaleExtensionServer{
			client: fc,
		}
		_ = server // Use server to avoid unused variable warning

		// Test that we can generate service mappings from the endpoints
		var endpointsList gatewayv1alpha1.TailscaleEndpointsList
		err := fc.List(context.Background(), &endpointsList)
		if err != nil {
			t.Fatalf("failed to list TailscaleEndpoints: %v", err)
		}

		if len(endpointsList.Items) != 1 {
			t.Fatalf("expected 1 TailscaleEndpoints resource, got %d", len(endpointsList.Items))
		}

		endpoint := endpointsList.Items[0]
		if len(endpoint.Spec.Endpoints) != 2 {
			t.Fatalf("expected 2 endpoint definitions, got %d", len(endpoint.Spec.Endpoints))
		}

		// Test generating mappings for each endpoint
		for _, ep := range endpoint.Spec.Endpoints {
			mapping := generateServiceMapping(ep, "default", "test-endpoints")

			if mapping.ServiceName != ep.Name {
				t.Errorf("expected service name %q, got %q", ep.Name, mapping.ServiceName)
			}

			if mapping.ExternalBackend != ep.ExternalTarget {
				t.Errorf("expected external backend %q, got %q", ep.ExternalTarget, mapping.ExternalBackend)
			}

			if mapping.Port != uint32(ep.Port) {
				t.Errorf("expected port %d, got %d", ep.Port, mapping.Port)
			}
		}
	})

	t.Run("route_prefix_generation", func(t *testing.T) {
		testCases := []struct {
			name           string
			serviceName    string
			expectedPrefix string
		}{
			{
				name:           "simple_service",
				serviceName:    "web-service",
				expectedPrefix: "/api/web-service",
			},
			{
				name:           "hyphenated_service",
				serviceName:    "api-gateway-service",
				expectedPrefix: "/api/api-gateway-service",
			},
			{
				name:           "single_char",
				serviceName:    "a",
				expectedPrefix: "/api/a",
			},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				// Test route prefix generation logic
				prefix := generateRoutePrefix(tc.serviceName)
				if prefix != tc.expectedPrefix {
					t.Errorf("expected prefix %q, got %q", tc.expectedPrefix, prefix)
				}
			})
		}
	})
}

func TestTailscaleServiceMappingGeneration(t *testing.T) {
	tests := []struct {
		name          string
		endpoint      gatewayv1alpha1.TailscaleEndpoint
		namespace     string
		endpointsName string
		want          TailscaleServiceMapping
	}{
		{
			name: "http_service",
			endpoint: gatewayv1alpha1.TailscaleEndpoint{
				Name:           "web-service",
				TailscaleIP:    "100.64.0.1",
				TailscaleFQDN:  "web-service.test.tailnet.ts.net",
				Port:           80,
				Protocol:       "HTTP",
				ExternalTarget: "httpbin.org:80",
			},
			namespace:     "default",
			endpointsName: "test-endpoints",
			want: TailscaleServiceMapping{
				ServiceName:     "web-service",
				ClusterName:     "external-backend-web-service",
				EgressService:   "test-endpoints-web-service-egress.default.svc.cluster.local",
				ExternalBackend: "httpbin.org:80",
				Port:            80,
				Protocol:        "HTTP",
			},
		},
		{
			name: "https_service",
			endpoint: gatewayv1alpha1.TailscaleEndpoint{
				Name:           "secure-api",
				TailscaleIP:    "100.64.0.2",
				TailscaleFQDN:  "secure-api.test.tailnet.ts.net",
				Port:           443,
				Protocol:       "HTTPS",
				ExternalTarget: "api.example.com:443",
			},
			namespace:     "production",
			endpointsName: "prod-endpoints",
			want: TailscaleServiceMapping{
				ServiceName:     "secure-api",
				ClusterName:     "external-backend-secure-api",
				EgressService:   "prod-endpoints-secure-api-egress.production.svc.cluster.local",
				ExternalBackend: "api.example.com:443",
				Port:            443,
				Protocol:        "HTTPS",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mapping := generateServiceMapping(tt.endpoint, tt.namespace, tt.endpointsName)

			if diff := cmp.Diff(mapping, tt.want); diff != "" {
				t.Errorf("generateServiceMapping() mismatch (-got +want):\n%s", diff)
			}
		})
	}
}

// Test helper functions

func mustCreate(t *testing.T, client client.Client, obj client.Object) {
	t.Helper()
	if err := client.Create(context.Background(), obj); err != nil {
		t.Fatalf("creating %q: %v", obj.GetName(), err)
	}
}

func stringPtr(s string) *string {
	return &s
}

func pathTypePtr(t gwapiv1.PathMatchType) *gwapiv1.PathMatchType {
	return &t
}

func portNumberPtr(p gwapiv1.PortNumber) *gwapiv1.PortNumber {
	return &p
}

// Helper function for route prefix generation
func generateRoutePrefix(serviceName string) string {
	return "/api/" + serviceName
}

// Mock helper function that would be implemented in the actual server
func generateServiceMapping(endpoint gatewayv1alpha1.TailscaleEndpoint, namespace, endpointsName string) TailscaleServiceMapping {
	return TailscaleServiceMapping{
		ServiceName:     endpoint.Name,
		ClusterName:     "external-backend-" + endpoint.Name,
		EgressService:   endpointsName + "-" + endpoint.Name + "-egress." + namespace + ".svc.cluster.local",
		ExternalBackend: endpoint.ExternalTarget,
		Port:            uint32(endpoint.Port),
		Protocol:        endpoint.Protocol,
	}
}

// TestMultiRouteTypeSupport tests the new multi-route type discovery and processing
func TestMultiRouteTypeSupport(t *testing.T) {
	scheme := runtime.NewScheme()
	gatewayv1alpha1.AddToScheme(scheme)
	gwapiv1.AddToScheme(scheme)
	gwapiv1alpha2.AddToScheme(scheme)

	t.Run("tcp_route_discovery", func(t *testing.T) {
		fc := fake.NewClientBuilder().WithScheme(scheme).Build()

		// Create test TCPRoute with TailscaleEndpoints backend
		tcpRoute := &gwapiv1alpha2.TCPRoute{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-tcp-route",
				Namespace: "default",
				Annotations: map[string]string{
					"gateway.tailscale.com/gateway": "enabled",
				},
			},
			Spec: gwapiv1alpha2.TCPRouteSpec{
				Rules: []gwapiv1alpha2.TCPRouteRule{
					{
						BackendRefs: []gwapiv1alpha2.BackendRef{
							{
								BackendObjectReference: gwapiv1alpha2.BackendObjectReference{
									Kind: kindPtr("TailscaleEndpoints"),
									Name: "tcp-endpoints",
									Port: portNumberPtrV1alpha2(8080),
								},
							},
						},
					},
				},
			},
		}

		mustCreate(t, fc, tcpRoute)

		server := &TailscaleExtensionServer{
			client: fc,
			logger: slog.New(slog.NewTextHandler(os.Stderr, nil)),
		}

		// Test TCPRoute discovery
		routes, err := server.discoverTCPRoutes(context.Background())
		if err != nil {
			t.Fatalf("failed to discover TCPRoutes: %v", err)
		}

		if len(routes) != 1 {
			t.Fatalf("expected 1 TCPRoute, got %d", len(routes))
		}

		if routes[0].Name != "test-tcp-route" {
			t.Errorf("expected route name 'test-tcp-route', got %q", routes[0].Name)
		}

		// Test relevance check
		if !server.isTCPRouteRelevant(&routes[0]) {
			t.Error("expected TCP route to be relevant (has Tailscale annotation)")
		}
	})

	t.Run("udp_route_discovery", func(t *testing.T) {
		fc := fake.NewClientBuilder().WithScheme(scheme).Build()

		udpRoute := &gwapiv1alpha2.UDPRoute{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-udp-route",
				Namespace: "default",
			},
			Spec: gwapiv1alpha2.UDPRouteSpec{
				Rules: []gwapiv1alpha2.UDPRouteRule{
					{
						BackendRefs: []gwapiv1alpha2.BackendRef{
							{
								BackendObjectReference: gwapiv1alpha2.BackendObjectReference{
									Kind: kindPtr("TailscaleEndpoints"),
									Name: "udp-endpoints",
									Port: portNumberPtrV1alpha2(53),
								},
							},
						},
					},
				},
			},
		}

		mustCreate(t, fc, udpRoute)

		server := &TailscaleExtensionServer{
			client: fc,
			logger: slog.New(slog.NewTextHandler(os.Stderr, nil)),
		}

		routes, err := server.discoverUDPRoutes(context.Background())
		if err != nil {
			t.Fatalf("failed to discover UDPRoutes: %v", err)
		}

		if len(routes) != 1 {
			t.Fatalf("expected 1 UDPRoute, got %d", len(routes))
		}

		// Test relevance check - should be relevant due to TailscaleEndpoints backend
		if !server.isUDPRouteRelevant(&routes[0]) {
			t.Error("expected UDP route to be relevant (has TailscaleEndpoints backend)")
		}
	})

	t.Run("tls_route_discovery", func(t *testing.T) {
		fc := fake.NewClientBuilder().WithScheme(scheme).Build()

		tlsRoute := &gwapiv1alpha2.TLSRoute{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-tls-route",
				Namespace: "default",
			},
			Spec: gwapiv1alpha2.TLSRouteSpec{
				Rules: []gwapiv1alpha2.TLSRouteRule{
					{
						BackendRefs: []gwapiv1alpha2.BackendRef{
							{
								BackendObjectReference: gwapiv1alpha2.BackendObjectReference{
									Kind: kindPtr("TailscaleEndpoints"),
									Name: "tls-endpoints",
									Port: portNumberPtrV1alpha2(443),
								},
							},
						},
					},
				},
			},
		}

		mustCreate(t, fc, tlsRoute)

		server := &TailscaleExtensionServer{
			client: fc,
			logger: slog.New(slog.NewTextHandler(os.Stderr, nil)),
		}

		routes, err := server.discoverTLSRoutes(context.Background())
		if err != nil {
			t.Fatalf("failed to discover TLSRoutes: %v", err)
		}

		if len(routes) != 1 {
			t.Fatalf("expected 1 TLSRoute, got %d", len(routes))
		}

		if !server.isTLSRouteRelevant(&routes[0]) {
			t.Error("expected TLS route to be relevant")
		}
	})

	t.Run("grpc_route_discovery", func(t *testing.T) {
		fc := fake.NewClientBuilder().WithScheme(scheme).Build()

		grpcRoute := &gwapiv1.GRPCRoute{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-grpc-route",
				Namespace: "default",
			},
			Spec: gwapiv1.GRPCRouteSpec{
				Rules: []gwapiv1.GRPCRouteRule{
					{
						BackendRefs: []gwapiv1.GRPCBackendRef{
							{
								BackendRef: gwapiv1.BackendRef{
									BackendObjectReference: gwapiv1.BackendObjectReference{
										Kind: kindPtr("TailscaleEndpoints"),
										Name: "grpc-endpoints",
										Port: portNumberPtr(9090),
									},
								},
							},
						},
					},
				},
			},
		}

		mustCreate(t, fc, grpcRoute)

		server := &TailscaleExtensionServer{
			client: fc,
			logger: slog.New(slog.NewTextHandler(os.Stderr, nil)),
		}

		routes, err := server.discoverGRPCRoutes(context.Background())
		if err != nil {
			t.Fatalf("failed to discover GRPCRoutes: %v", err)
		}

		if len(routes) != 1 {
			t.Fatalf("expected 1 GRPCRoute, got %d", len(routes))
		}

		if !server.isGRPCRouteRelevant(&routes[0]) {
			t.Error("expected GRPC route to be relevant")
		}
	})
}

// TestTailscaleEndpointsBackendProcessing tests Gateway API compliance
func TestTailscaleEndpointsBackendProcessing(t *testing.T) {
	t.Run("http_backend_processing", func(t *testing.T) {
		server := &TailscaleExtensionServer{
			logger: slog.New(slog.NewTextHandler(os.Stderr, nil)),
		}

		backend := &gwapiv1.HTTPBackendRef{
			BackendRef: gwapiv1.BackendRef{
				BackendObjectReference: gwapiv1.BackendObjectReference{
					Name:      "test-service",
					Namespace: namespacePtr("test-ns"),
					Port:      portNumberPtr(8080),
				},
			},
		}

		mapping, err := server.processTailscaleEndpointsBackendHTTP(context.Background(), backend, "test-cluster")
		if err != nil {
			t.Fatalf("failed to process HTTP backend: %v", err)
		}

		if mapping.ServiceName != "test-service" {
			t.Errorf("expected service name 'test-service', got %q", mapping.ServiceName)
		}

		if mapping.ClusterName != "test-cluster" {
			t.Errorf("expected cluster name 'test-cluster', got %q", mapping.ClusterName)
		}

		if mapping.Protocol != "HTTP" {
			t.Errorf("expected protocol 'HTTP', got %q", mapping.Protocol)
		}

		if mapping.Port != 8080 {
			t.Errorf("expected port 8080, got %d", mapping.Port)
		}
	})

	t.Run("tcp_backend_processing", func(t *testing.T) {
		server := &TailscaleExtensionServer{
			logger: slog.New(slog.NewTextHandler(os.Stderr, nil)),
		}

		backend := &gwapiv1alpha2.BackendRef{
			BackendObjectReference: gwapiv1alpha2.BackendObjectReference{
				Name:      "tcp-service",
				Namespace: namespacePtr("test-ns"),
				Port:      portNumberPtrV1alpha2(3306),
			},
		}

		mapping, err := server.processTailscaleEndpointsBackendTCP(context.Background(), backend, "tcp-cluster")
		if err != nil {
			t.Fatalf("failed to process TCP backend: %v", err)
		}

		if mapping.Protocol != "TCP" {
			t.Errorf("expected protocol 'TCP', got %q", mapping.Protocol)
		}

		if mapping.Port != 3306 {
			t.Errorf("expected port 3306, got %d", mapping.Port)
		}
	})

	t.Run("udp_backend_processing", func(t *testing.T) {
		server := &TailscaleExtensionServer{
			logger: slog.New(slog.NewTextHandler(os.Stderr, nil)),
		}

		backend := &gwapiv1alpha2.BackendRef{
			BackendObjectReference: gwapiv1alpha2.BackendObjectReference{
				Name: "dns-service",
				Port: portNumberPtrV1alpha2(53),
			},
		}

		mapping, err := server.processTailscaleEndpointsBackendUDP(context.Background(), backend, "udp-cluster")
		if err != nil {
			t.Fatalf("failed to process UDP backend: %v", err)
		}

		if mapping.Protocol != "UDP" {
			t.Errorf("expected protocol 'UDP', got %q", mapping.Protocol)
		}

		if mapping.Port != 53 {
			t.Errorf("expected port 53, got %d", mapping.Port)
		}
	})

	t.Run("tls_backend_processing", func(t *testing.T) {
		server := &TailscaleExtensionServer{
			logger: slog.New(slog.NewTextHandler(os.Stderr, nil)),
		}

		backend := &gwapiv1alpha2.BackendRef{
			BackendObjectReference: gwapiv1alpha2.BackendObjectReference{
				Name: "secure-service",
				Port: portNumberPtrV1alpha2(443),
			},
		}

		mapping, err := server.processTailscaleEndpointsBackendTLS(context.Background(), backend, "tls-cluster")
		if err != nil {
			t.Fatalf("failed to process TLS backend: %v", err)
		}

		if mapping.Protocol != "TLS" {
			t.Errorf("expected protocol 'TLS', got %q", mapping.Protocol)
		}

		if mapping.Port != 443 {
			t.Errorf("expected port 443, got %d", mapping.Port)
		}
	})

	t.Run("grpc_backend_processing", func(t *testing.T) {
		server := &TailscaleExtensionServer{
			logger: slog.New(slog.NewTextHandler(os.Stderr, nil)),
		}

		backend := &gwapiv1.GRPCBackendRef{
			BackendRef: gwapiv1.BackendRef{
				BackendObjectReference: gwapiv1.BackendObjectReference{
					Name: "grpc-service",
					Port: portNumberPtr(9090),
				},
			},
		}

		mapping, err := server.processTailscaleEndpointsBackendGRPC(context.Background(), backend, "grpc-cluster")
		if err != nil {
			t.Fatalf("failed to process GRPC backend: %v", err)
		}

		if mapping.Protocol != "GRPC" {
			t.Errorf("expected protocol 'GRPC', got %q", mapping.Protocol)
		}

		if mapping.Port != 9090 {
			t.Errorf("expected port 9090, got %d", mapping.Port)
		}
	})
}

// TestConfigurationDrivenRouteGeneration tests the configuration-driven routing features
func TestConfigurationDrivenRouteGeneration(t *testing.T) {
	scheme := runtime.NewScheme()
	gatewayv1alpha1.AddToScheme(scheme)

	t.Run("config_cache_initialization", func(t *testing.T) {
		server := &TailscaleExtensionServer{
			configCache: &ConfigCache{
				routeGenerationConfig: make(map[string]*gatewayv1alpha1.RouteGenerationConfig),
				tailscaleEndpoints:    make(map[string]*gatewayv1alpha1.TailscaleEndpoints),
				gatewayConfigs:        make(map[string]*gatewayv1alpha1.TailscaleGateway),
				lastUpdate:            time.Now(),
			},
			logger: slog.New(slog.NewTextHandler(os.Stderr, nil)),
		}

		if server.configCache == nil {
			t.Fatal("expected config cache to be initialized")
		}

		if server.configCache.routeGenerationConfig == nil {
			t.Error("expected route generation config map to be initialized")
		}

		if server.configCache.tailscaleEndpoints == nil {
			t.Error("expected tailscale endpoints map to be initialized")
		}
	})

	t.Run("metrics_tracking", func(t *testing.T) {
		server := &TailscaleExtensionServer{
			metrics: &ExtensionServerMetrics{
				hookTypeMetrics: make(map[string]*HookTypeMetrics),
			},
			logger: slog.New(slog.NewTextHandler(os.Stderr, nil)),
		}

		// Test metrics recording
		hookType := "PostVirtualHostModify"
		duration := 100 * time.Millisecond

		server.recordHookMetrics(hookType, duration, nil)

		if server.metrics.totalHookCalls != 1 {
			t.Errorf("expected total hook calls to be 1, got %d", server.metrics.totalHookCalls)
		}

		if server.metrics.successfulCalls != 1 {
			t.Errorf("expected successful calls to be 1, got %d", server.metrics.successfulCalls)
		}

		hookMetrics, exists := server.metrics.hookTypeMetrics[hookType]
		if !exists {
			t.Fatal("expected hook type metrics to exist")
		}

		if hookMetrics.calls != 1 {
			t.Errorf("expected hook type calls to be 1, got %d", hookMetrics.calls)
		}

		if hookMetrics.successes != 1 {
			t.Errorf("expected hook type successes to be 1, got %d", hookMetrics.successes)
		}
	})

	t.Run("route_generation_config_processing", func(t *testing.T) {
		fc := fake.NewClientBuilder().WithScheme(scheme).Build()

		// Create TailscaleGateway with RouteGenerationConfig
		gateway := &gatewayv1alpha1.TailscaleGateway{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-gateway",
				Namespace: "production",
			},
			Spec: gatewayv1alpha1.TailscaleGatewaySpec{
				GatewayRef: gatewayv1alpha1.LocalPolicyTargetReference{
					Kind: "Gateway",
					Name: "envoy-gateway",
				},
				Tailnets: []gatewayv1alpha1.TailnetConfig{
					{
						Name: "prod-tailnet",
						TailscaleTailnetRef: gatewayv1alpha1.LocalPolicyTargetReference{
							Kind: "TailscaleTailnet",
							Name: "prod-tailnet",
						},
						RouteGeneration: &gatewayv1alpha1.RouteGenerationConfig{
							Ingress: &gatewayv1alpha1.IngressRouteConfig{
								HostPattern: "{service}.custom.local",
								PathPrefix:  "/custom",
							},
							Egress: &gatewayv1alpha1.EgressRouteConfig{
								HostPattern: "{service}.egress.local",
								PathPrefix:  "/services/{service}",
							},
						},
					},
				},
			},
		}

		mustCreate(t, fc, gateway)

		server := &TailscaleExtensionServer{
			client: fc,
			logger: slog.New(slog.NewTextHandler(os.Stderr, nil)),
			configCache: &ConfigCache{
				routeGenerationConfig: make(map[string]*gatewayv1alpha1.RouteGenerationConfig),
				tailscaleEndpoints:    make(map[string]*gatewayv1alpha1.TailscaleEndpoints),
				gatewayConfigs:        make(map[string]*gatewayv1alpha1.TailscaleGateway),
				lastUpdate:            time.Now(),
			},
		}

		// Test that cache can load gateway configs
		err := server.reloadConfigCache(context.Background())
		if err != nil {
			t.Fatalf("failed to reload config cache: %v", err)
		}

		// Verify config was cached
		cacheKey := "production/test-gateway"
		cached, exists := server.configCache.gatewayConfigs[cacheKey]
		if !exists {
			t.Fatal("expected TailscaleGateway to be cached")
		}

		if len(cached.Spec.Tailnets) == 0 || cached.Spec.Tailnets[0].RouteGeneration == nil {
			t.Fatal("expected RouteGenerationConfig to be present")
		}

		routeConfig := cached.Spec.Tailnets[0].RouteGeneration
		if routeConfig.Ingress.PathPrefix != "/custom" {
			t.Errorf("expected path prefix '/custom', got %q", routeConfig.Ingress.PathPrefix)
		}

		if routeConfig.Egress.PathPrefix != "/services/{service}" {
			t.Errorf("expected egress path prefix '/services/{service}', got %q", routeConfig.Egress.PathPrefix)
		}
	})

	t.Run("hot_configuration_reload", func(t *testing.T) {
		fc := fake.NewClientBuilder().WithScheme(scheme).Build()

		server := &TailscaleExtensionServer{
			client: fc,
			logger: slog.New(slog.NewTextHandler(os.Stderr, nil)),
			configCache: &ConfigCache{
				routeGenerationConfig: make(map[string]*gatewayv1alpha1.RouteGenerationConfig),
				tailscaleEndpoints:    make(map[string]*gatewayv1alpha1.TailscaleEndpoints),
				gatewayConfigs:        make(map[string]*gatewayv1alpha1.TailscaleGateway),
				lastUpdate:            time.Now().Add(-1 * time.Hour), // Old timestamp
			},
		}

		// Create initial TailscaleEndpoints
		endpoints := &gatewayv1alpha1.TailscaleEndpoints{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-endpoints",
				Namespace: "default",
			},
			Spec: gatewayv1alpha1.TailscaleEndpointsSpec{
				Tailnet: "test.ts.net",
				Endpoints: []gatewayv1alpha1.TailscaleEndpoint{
					{
						Name:           "service-v1",
						TailscaleIP:    "100.64.0.1",
						Port:           8080,
						Protocol:       "HTTP",
						ExternalTarget: "service-v1.internal:8080",
					},
				},
			},
		}

		mustCreate(t, fc, endpoints)

		// Test initial cache loading
		err := server.reloadConfigCache(context.Background())
		if err != nil {
			t.Fatalf("failed to reload config cache: %v", err)
		}

		// Verify cache was populated
		cacheKey := "default/test-endpoints"
		cached, exists := server.configCache.tailscaleEndpoints[cacheKey]
		if !exists {
			t.Fatal("expected TailscaleEndpoints to be cached")
		}

		if len(cached.Spec.Endpoints) != 1 {
			t.Errorf("expected 1 cached endpoint, got %d", len(cached.Spec.Endpoints))
		}

		// Update the endpoints to simulate hot reload
		endpoints.Spec.Endpoints = append(endpoints.Spec.Endpoints, gatewayv1alpha1.TailscaleEndpoint{
			Name:           "service-v2",
			TailscaleIP:    "100.64.0.2",
			Port:           8081,
			Protocol:       "HTTP",
			ExternalTarget: "service-v2.internal:8081",
		})

		err = fc.Update(context.Background(), endpoints)
		if err != nil {
			t.Fatalf("failed to update endpoints: %v", err)
		}

		// Test hot reload
		err = server.reloadConfigCache(context.Background())
		if err != nil {
			t.Fatalf("failed to hot reload config cache: %v", err)
		}

		// Verify cache was updated
		reloaded, exists := server.configCache.tailscaleEndpoints[cacheKey]
		if !exists {
			t.Fatal("expected TailscaleEndpoints to still be cached after reload")
		}

		if len(reloaded.Spec.Endpoints) != 2 {
			t.Errorf("expected 2 endpoints after hot reload, got %d", len(reloaded.Spec.Endpoints))
		}

		// Verify the new endpoint is present
		foundV2 := false
		for _, ep := range reloaded.Spec.Endpoints {
			if ep.Name == "service-v2" {
				foundV2 = true
				break
			}
		}
		if !foundV2 {
			t.Error("expected to find service-v2 endpoint after hot reload")
		}
	})

	t.Run("config_driven_route_prefix_generation", func(t *testing.T) {
		fc := fake.NewClientBuilder().WithScheme(scheme).Build()

		// Create TailscaleGateway with custom RouteGenerationConfig
		gateway := &gatewayv1alpha1.TailscaleGateway{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "api-gateway",
				Namespace: "production",
			},
			Spec: gatewayv1alpha1.TailscaleGatewaySpec{
				GatewayRef: gatewayv1alpha1.LocalPolicyTargetReference{
					Kind: "Gateway",
					Name: "envoy-gateway",
				},
				Tailnets: []gatewayv1alpha1.TailnetConfig{
					{
						Name: "prod-tailnet",
						TailscaleTailnetRef: gatewayv1alpha1.LocalPolicyTargetReference{
							Kind: "TailscaleTailnet",
							Name: "prod-tailnet",
						},
						RouteGeneration: &gatewayv1alpha1.RouteGenerationConfig{
							Egress: &gatewayv1alpha1.EgressRouteConfig{
								PathPrefix: "/services/{service}",
							},
						},
					},
				},
			},
		}

		mustCreate(t, fc, gateway)

		server := &TailscaleExtensionServer{
			client: fc,
			logger: slog.New(slog.NewTextHandler(os.Stderr, nil)),
			configCache: &ConfigCache{
				routeGenerationConfig: make(map[string]*gatewayv1alpha1.RouteGenerationConfig),
				tailscaleEndpoints:    make(map[string]*gatewayv1alpha1.TailscaleEndpoints),
				gatewayConfigs:        make(map[string]*gatewayv1alpha1.TailscaleGateway),
				lastUpdate:            time.Now(),
			},
		}

		// Load config into cache
		err := server.reloadConfigCache(context.Background())
		if err != nil {
			t.Fatalf("failed to reload config cache: %v", err)
		}

		// Test that route generation uses custom prefix from config
		serviceName := "user-service"
		expectedPrefixWithConfig := "/services/user-service"

		// Mock a method that would use the config (this would be in the actual implementation)
		actualPrefix := server.generateRoutePrefixFromConfig(serviceName, "production")

		if actualPrefix != expectedPrefixWithConfig {
			t.Errorf("expected route prefix %q, got %q", expectedPrefixWithConfig, actualPrefix)
		}
	})
}

// TestResourceIndexing tests the resource indexing functionality
func TestResourceIndexing(t *testing.T) {
	scheme := runtime.NewScheme()
	gatewayv1alpha1.AddToScheme(scheme)
	gwapiv1.AddToScheme(scheme)
	gwapiv1alpha2.AddToScheme(scheme)

	t.Run("resource_index_initialization", func(t *testing.T) {
		index := NewResourceIndex()

		if index == nil {
			t.Fatal("expected resource index to be created")
		}

		if index.endpointsToHTTPRoutes == nil {
			t.Error("expected HTTPRoute index to be initialized")
		}

		if index.vipServicesToEndpoints == nil {
			t.Error("expected VIP service index to be initialized")
		}
	})

	t.Run("http_route_indexing", func(t *testing.T) {
		fc := fake.NewClientBuilder().WithScheme(scheme).Build()

		// Create TailscaleEndpoints
		endpoints := &gatewayv1alpha1.TailscaleEndpoints{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "api-endpoints",
				Namespace: "production",
			},
			Spec: gatewayv1alpha1.TailscaleEndpointsSpec{
				Tailnet: "test.ts.net",
				Endpoints: []gatewayv1alpha1.TailscaleEndpoint{
					{
						Name:           "user-service",
						TailscaleIP:    "100.64.0.10",
						Port:           8080,
						Protocol:       "HTTP",
						ExternalTarget: "users.internal:8080",
					},
				},
			},
		}

		// Create HTTPRoute that references TailscaleEndpoints
		httpRoute := &gwapiv1.HTTPRoute{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "api-route",
				Namespace: "production",
			},
			Spec: gwapiv1.HTTPRouteSpec{
				Rules: []gwapiv1.HTTPRouteRule{
					{
						BackendRefs: []gwapiv1.HTTPBackendRef{
							{
								BackendRef: gwapiv1.BackendRef{
									BackendObjectReference: gwapiv1.BackendObjectReference{
										Group: groupPtr("gateway.tailscale.com"),
										Kind:  kindPtr("TailscaleEndpoints"),
										Name:  "api-endpoints",
									},
								},
							},
						},
					},
				},
			},
		}

		mustCreate(t, fc, endpoints)
		mustCreate(t, fc, httpRoute)

		server := &TailscaleExtensionServer{
			client:        fc,
			logger:        slog.New(slog.NewTextHandler(os.Stderr, nil)),
			resourceIndex: NewResourceIndex(),
		}

		// Test indexing
		err := server.rebuildIndex(context.Background())
		if err != nil {
			t.Fatalf("failed to rebuild index: %v", err)
		}

		// Verify the indexing worked
		endpointsKey := "production/api-endpoints"
		routes := server.GetRoutesForEndpoints(endpointsKey)

		if routes == nil {
			t.Fatal("expected routes map to be returned")
		}

		httpRoutes, exists := routes["HTTPRoute"]
		if !exists {
			t.Fatal("expected HTTPRoute entries in index")
		}

		if len(httpRoutes) != 1 {
			t.Errorf("expected 1 HTTPRoute, got %d", len(httpRoutes))
		}

		if httpRoutes[0] != "production/api-route" {
			t.Errorf("expected route 'production/api-route', got %q", httpRoutes[0])
		}

		// Test reverse lookup
		routeKey := "production/api-route"
		endpointsList := server.GetEndpointsForRoute(routeKey)

		if len(endpointsList) != 1 {
			t.Errorf("expected 1 TailscaleEndpoints, got %d", len(endpointsList))
		}

		if endpointsList[0] != endpointsKey {
			t.Errorf("expected endpoints key %q, got %q", endpointsKey, endpointsList[0])
		}
	})

	t.Run("multi_route_type_indexing", func(t *testing.T) {
		fc := fake.NewClientBuilder().WithScheme(scheme).Build()

		// Create TailscaleEndpoints
		endpoints := &gatewayv1alpha1.TailscaleEndpoints{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "multi-endpoints",
				Namespace: "default",
			},
			Spec: gatewayv1alpha1.TailscaleEndpointsSpec{
				Tailnet: "test.ts.net",
				Endpoints: []gatewayv1alpha1.TailscaleEndpoint{
					{
						Name:           "web-service",
						TailscaleIP:    "100.64.0.20",
						Port:           80,
						Protocol:       "HTTP",
						ExternalTarget: "web.internal:80",
					},
				},
			},
		}

		// Create multiple route types
		httpRoute := &gwapiv1.HTTPRoute{
			ObjectMeta: metav1.ObjectMeta{Name: "http-route", Namespace: "default"},
			Spec: gwapiv1.HTTPRouteSpec{
				Rules: []gwapiv1.HTTPRouteRule{{
					BackendRefs: []gwapiv1.HTTPBackendRef{{
						BackendRef: gwapiv1.BackendRef{
							BackendObjectReference: gwapiv1.BackendObjectReference{
								Group: groupPtr("gateway.tailscale.com"),
								Kind:  kindPtr("TailscaleEndpoints"),
								Name:  "multi-endpoints",
							},
						},
					}},
				}},
			},
		}

		tcpRoute := &gwapiv1alpha2.TCPRoute{
			ObjectMeta: metav1.ObjectMeta{Name: "tcp-route", Namespace: "default"},
			Spec: gwapiv1alpha2.TCPRouteSpec{
				Rules: []gwapiv1alpha2.TCPRouteRule{{
					BackendRefs: []gwapiv1alpha2.BackendRef{{
						BackendObjectReference: gwapiv1alpha2.BackendObjectReference{
							Kind: kindPtr("TailscaleEndpoints"),
							Name: "multi-endpoints",
						},
					}},
				}},
			},
		}

		grpcRoute := &gwapiv1.GRPCRoute{
			ObjectMeta: metav1.ObjectMeta{Name: "grpc-route", Namespace: "default"},
			Spec: gwapiv1.GRPCRouteSpec{
				Rules: []gwapiv1.GRPCRouteRule{{
					BackendRefs: []gwapiv1.GRPCBackendRef{{
						BackendRef: gwapiv1.BackendRef{
							BackendObjectReference: gwapiv1.BackendObjectReference{
								Kind: kindPtr("TailscaleEndpoints"),
								Name: "multi-endpoints",
							},
						},
					}},
				}},
			},
		}

		mustCreate(t, fc, endpoints)
		mustCreate(t, fc, httpRoute)
		mustCreate(t, fc, tcpRoute)
		mustCreate(t, fc, grpcRoute)

		server := &TailscaleExtensionServer{
			client:        fc,
			logger:        slog.New(slog.NewTextHandler(os.Stderr, nil)),
			resourceIndex: NewResourceIndex(),
		}

		// Test indexing
		err := server.rebuildIndex(context.Background())
		if err != nil {
			t.Fatalf("failed to rebuild index: %v", err)
		}

		// Verify all route types are indexed
		endpointsKey := "default/multi-endpoints"
		routes := server.GetRoutesForEndpoints(endpointsKey)

		expectedRouteTypes := []string{"HTTPRoute", "TCPRoute", "GRPCRoute"}
		for _, routeType := range expectedRouteTypes {
			if routes[routeType] == nil {
				t.Errorf("expected %s to be indexed", routeType)
				continue
			}
			if len(routes[routeType]) != 1 {
				t.Errorf("expected 1 %s, got %d", routeType, len(routes[routeType]))
			}
		}
	})

	t.Run("vip_service_indexing", func(t *testing.T) {
		fc := fake.NewClientBuilder().WithScheme(scheme).Build()

		// Create TailscaleEndpoints with external targets
		endpoints := &gatewayv1alpha1.TailscaleEndpoints{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "vip-endpoints",
				Namespace: "production",
			},
			Spec: gatewayv1alpha1.TailscaleEndpointsSpec{
				Tailnet: "test.ts.net",
				Endpoints: []gatewayv1alpha1.TailscaleEndpoint{
					{
						Name:           "payment-service",
						TailscaleIP:    "100.64.0.30",
						Port:           8080,
						Protocol:       "HTTP",
						ExternalTarget: "payments.internal:8080",
					},
					{
						Name:           "auth-service",
						TailscaleIP:    "100.64.0.31",
						Port:           9000,
						Protocol:       "HTTPS",
						ExternalTarget: "auth.internal:9000",
					},
				},
			},
		}

		mustCreate(t, fc, endpoints)

		server := &TailscaleExtensionServer{
			client:        fc,
			logger:        slog.New(slog.NewTextHandler(os.Stderr, nil)),
			resourceIndex: NewResourceIndex(),
		}

		// Test VIP service indexing
		err := server.rebuildIndex(context.Background())
		if err != nil {
			t.Fatalf("failed to rebuild index: %v", err)
		}

		// Verify VIP services are indexed
		endpointsKey := "production/vip-endpoints"
		vipServices := server.GetVIPServicesForEndpoints(endpointsKey)

		if len(vipServices) != 2 {
			t.Errorf("expected 2 VIP services, got %d", len(vipServices))
		}

		// Verify service details
		serviceNames := make(map[string]bool)
		for _, service := range vipServices {
			serviceNames[service.ServiceName] = true

			// Check metadata
			if service.Metadata == nil {
				t.Error("expected metadata to be set")
				continue
			}

			if service.Metadata["protocol"] == nil {
				t.Error("expected protocol in metadata")
			}
		}

		expectedServices := []string{"payment-service", "auth-service"}
		for _, serviceName := range expectedServices {
			if !serviceNames[serviceName] {
				t.Errorf("expected service %s to be indexed", serviceName)
			}
		}
	})

	t.Run("index_statistics", func(t *testing.T) {
		fc := fake.NewClientBuilder().WithScheme(scheme).Build()

		server := &TailscaleExtensionServer{
			client:        fc,
			logger:        slog.New(slog.NewTextHandler(os.Stderr, nil)),
			resourceIndex: NewResourceIndex(),
		}

		// Test initial stats
		stats := server.GetResourceIndexStats()
		if stats == nil {
			t.Fatal("expected stats to be returned")
		}

		if stats["indexUpdateCount"].(int64) != 0 {
			t.Error("expected initial update count to be 0")
		}

		// Rebuild index to update stats
		err := server.rebuildIndex(context.Background())
		if err != nil {
			t.Fatalf("failed to rebuild index: %v", err)
		}

		// Check updated stats
		stats = server.GetResourceIndexStats()
		if stats["indexUpdateCount"].(int64) != 1 {
			t.Errorf("expected update count to be 1, got %d", stats["indexUpdateCount"])
		}

		if stats["lastIndexUpdate"] == nil {
			t.Error("expected lastIndexUpdate to be set")
		}
	})
}

// TestComprehensiveServiceDiscovery tests the enhanced service discovery across all route types
func TestComprehensiveServiceDiscovery(t *testing.T) {
	scheme := runtime.NewScheme()
	gatewayv1alpha1.AddToScheme(scheme)
	gwapiv1.AddToScheme(scheme)
	gwapiv1alpha2.AddToScheme(scheme)

	fc := fake.NewClientBuilder().WithScheme(scheme).Build()

	// Create comprehensive test data with Tailscale annotations to make them relevant
	httpRoute := &gwapiv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-http",
			Namespace: "default",
			Annotations: map[string]string{
				"gateway.tailscale.com/gateway": "enabled",
			},
		},
		Spec: gwapiv1.HTTPRouteSpec{
			Rules: []gwapiv1.HTTPRouteRule{
				{
					BackendRefs: []gwapiv1.HTTPBackendRef{
						{BackendRef: gwapiv1.BackendRef{BackendObjectReference: gwapiv1.BackendObjectReference{Name: "http-svc"}}},
					},
				},
			},
		},
	}

	tcpRoute := &gwapiv1alpha2.TCPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-tcp",
			Namespace: "default",
			Annotations: map[string]string{
				"gateway.tailscale.com/gateway": "enabled",
			},
		},
		Spec: gwapiv1alpha2.TCPRouteSpec{
			Rules: []gwapiv1alpha2.TCPRouteRule{
				{
					BackendRefs: []gwapiv1alpha2.BackendRef{
						{BackendObjectReference: gwapiv1alpha2.BackendObjectReference{Name: "tcp-svc"}},
					},
				},
			},
		},
	}

	endpoints := &gatewayv1alpha1.TailscaleEndpoints{
		ObjectMeta: metav1.ObjectMeta{Name: "test-endpoints", Namespace: "default"},
		Spec: gatewayv1alpha1.TailscaleEndpointsSpec{
			Tailnet: "test.tailnet.ts.net",
			Endpoints: []gatewayv1alpha1.TailscaleEndpoint{
				{Name: "web", Port: 80, Protocol: "HTTP", ExternalTarget: "httpbin.org:80"},
			},
		},
	}

	// Create all resources
	mustCreate(t, fc, httpRoute)
	mustCreate(t, fc, tcpRoute)
	mustCreate(t, fc, endpoints)

	server := &TailscaleExtensionServer{
		client: fc,
		logger: slog.New(slog.NewTextHandler(os.Stderr, nil)),
	}

	t.Run("comprehensive_discovery", func(t *testing.T) {
		discoveryCtx, err := server.discoverAllServiceMappings(context.Background())
		if err != nil {
			t.Fatalf("failed comprehensive service discovery: %v", err)
		}

		if len(discoveryCtx.HTTPRoutes) != 1 {
			t.Errorf("expected 1 HTTPRoute, got %d", len(discoveryCtx.HTTPRoutes))
		}

		if len(discoveryCtx.TCPRoutes) != 1 {
			t.Errorf("expected 1 TCPRoute, got %d", len(discoveryCtx.TCPRoutes))
		}

		if len(discoveryCtx.TailscaleEndpoints) != 1 {
			t.Errorf("expected 1 TailscaleEndpoints, got %d", len(discoveryCtx.TailscaleEndpoints))
		}

		if len(discoveryCtx.TailscaleServiceMappings) != 1 {
			t.Errorf("expected 1 TailscaleServiceMapping, got %d", len(discoveryCtx.TailscaleServiceMappings))
		}
	})
}

// Helper functions for tests

func kindPtr(s string) *gwapiv1.Kind {
	k := gwapiv1.Kind(s)
	return &k
}

func namespacePtr(s string) *gwapiv1.Namespace {
	ns := gwapiv1.Namespace(s)
	return &ns
}

func portNumberPtrV1alpha2(p gwapiv1alpha2.PortNumber) *gwapiv1alpha2.PortNumber {
	return &p
}

// TestHTTPEndpoints tests the metrics and health check HTTP endpoints
func TestHTTPEndpoints(t *testing.T) {
	scheme := runtime.NewScheme()
	gatewayv1alpha1.AddToScheme(scheme)
	gwapiv1.AddToScheme(scheme)

	fc := fake.NewClientBuilder().WithScheme(scheme).Build()

	// Create server with metrics initialized
	server := &TailscaleExtensionServer{
		client: fc,
		logger: slog.New(slog.NewTextHandler(os.Stderr, nil)),
		metrics: &ExtensionServerMetrics{
			totalHookCalls:      100,
			successfulCalls:     90,
			failedCalls:         10,
			lastCallDuration:    500 * time.Millisecond,
			averageCallDuration: 300 * time.Millisecond,
			hookTypeMetrics: map[string]*HookTypeMetrics{
				"PostVirtualHostModify": {
					calls:           50,
					successes:       48,
					failures:        2,
					averageResponse: 250 * time.Millisecond,
				},
				"PostTranslateModify": {
					calls:           50,
					successes:       42,
					failures:        8,
					averageResponse: 350 * time.Millisecond,
				},
			},
		},
		configCache: &ConfigCache{
			routeGenerationConfig: make(map[string]*gatewayv1alpha1.RouteGenerationConfig),
			tailscaleEndpoints:    make(map[string]*gatewayv1alpha1.TailscaleEndpoints),
			gatewayConfigs:        make(map[string]*gatewayv1alpha1.TailscaleGateway),
			lastUpdate:            time.Now().Add(-15 * time.Minute), // Older than 10-minute threshold
		},
		resourceIndex: &ResourceIndex{
			endpointsToHTTPRoutes: make(map[string][]string),
			httpRoutesToEndpoints: make(map[string][]string),
			indexUpdateCount:      5,
			lastIndexUpdate:       time.Now().Add(-15 * time.Minute), // Older than 10-minute threshold
		},
	}

	t.Run("metrics_endpoint", func(t *testing.T) {
		// Test metrics endpoint
		req := httptest.NewRequest("GET", "/metrics", nil)
		w := httptest.NewRecorder()

		server.metricsHandler(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
		}

		if w.Header().Get("Content-Type") != "application/json" {
			t.Errorf("expected Content-Type application/json, got %s", w.Header().Get("Content-Type"))
		}

		var response map[string]interface{}
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatalf("failed to parse JSON response: %v", err)
		}

		// Verify hook calls metrics
		hookCalls, ok := response["hook_calls"].(map[string]interface{})
		if !ok {
			t.Fatal("expected hook_calls to be present in response")
		}

		if hookCalls["total_calls"] != float64(100) {
			t.Errorf("expected total_calls to be 100, got %v", hookCalls["total_calls"])
		}

		if hookCalls["successful_calls"] != float64(90) {
			t.Errorf("expected successful_calls to be 90, got %v", hookCalls["successful_calls"])
		}

		if hookCalls["failed_calls"] != float64(10) {
			t.Errorf("expected failed_calls to be 10, got %v", hookCalls["failed_calls"])
		}

		// Verify hook type metrics
		hookTypeMetrics, ok := response["hook_type_metrics"].(map[string]interface{})
		if !ok {
			t.Fatal("expected hook_type_metrics to be present in response")
		}

		virtualHostMetrics, ok := hookTypeMetrics["PostVirtualHostModify"].(map[string]interface{})
		if !ok {
			t.Fatal("expected PostVirtualHostModify metrics to be present")
		}

		if virtualHostMetrics["calls"] != float64(50) {
			t.Errorf("expected PostVirtualHostModify calls to be 50, got %v", virtualHostMetrics["calls"])
		}

		if virtualHostMetrics["successes"] != float64(48) {
			t.Errorf("expected PostVirtualHostModify successes to be 48, got %v", virtualHostMetrics["successes"])
		}

		if virtualHostMetrics["failures"] != float64(2) {
			t.Errorf("expected PostVirtualHostModify failures to be 2, got %v", virtualHostMetrics["failures"])
		}

		// Verify timestamp is present
		if _, ok := response["timestamp"]; !ok {
			t.Error("expected timestamp to be present in response")
		}
	})

	t.Run("health_endpoint", func(t *testing.T) {
		// Test health endpoint
		req := httptest.NewRequest("GET", "/health", nil)
		w := httptest.NewRecorder()

		server.healthHandler(w, req)

		// Since cache is stale (older than 10 minutes), health should be degraded
		if w.Code != http.StatusServiceUnavailable {
			t.Errorf("expected status %d, got %d", http.StatusServiceUnavailable, w.Code)
		}

		if w.Header().Get("Content-Type") != "application/json" {
			t.Errorf("expected Content-Type application/json, got %s", w.Header().Get("Content-Type"))
		}

		var response map[string]interface{}
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatalf("failed to parse JSON response: %v", err)
		}

		// Verify basic health structure - should be degraded due to stale cache
		if response["status"] != "degraded" {
			t.Errorf("expected status to be degraded due to stale cache, got %v", response["status"])
		}

		checks, ok := response["checks"].(map[string]interface{})
		if !ok {
			t.Fatal("expected checks to be present in response")
		}

		if checks["metrics_available"] != true {
			t.Error("expected metrics_available to be true")
		}

		if checks["config_cache_active"] != true {
			t.Error("expected config_cache_active to be true")
		}

		if checks["resource_index_active"] != true {
			t.Error("expected resource_index_active to be true")
		}

		if checks["hook_calls_recorded"] != true {
			t.Error("expected hook_calls_recorded to be true")
		}

		if checks["low_failure_rate"] != false {
			t.Error("expected low_failure_rate to be false (10% is at threshold)")
		}

		// Check cache health checks - should be false due to stale cache
		if checks["cache_recently_updated"] != false {
			t.Error("expected cache_recently_updated to be false (cache is stale)")
		}

		if checks["index_recently_updated"] != false {
			t.Error("expected index_recently_updated to be false (index is stale)")
		}

		// Verify failure rate calculation
		if response["failure_rate"] != 0.1 {
			t.Errorf("expected failure_rate to be 0.1, got %v", response["failure_rate"])
		}

		// Verify hook call metrics are included
		if response["total_hook_calls"] != float64(100) {
			t.Errorf("expected total_hook_calls to be 100, got %v", response["total_hook_calls"])
		}

		if response["successful_calls"] != float64(90) {
			t.Errorf("expected successful_calls to be 90, got %v", response["successful_calls"])
		}

		if response["failed_calls"] != float64(10) {
			t.Errorf("expected failed_calls to be 10, got %v", response["failed_calls"])
		}

		// Verify timestamp is present
		if _, ok := response["timestamp"]; !ok {
			t.Error("expected timestamp to be present in response")
		}
	})

	t.Run("health_endpoint_healthy", func(t *testing.T) {
		// Test health endpoint with fresh cache and good metrics
		healthyServer := &TailscaleExtensionServer{
			client: fc,
			logger: slog.New(slog.NewTextHandler(os.Stderr, nil)),
			metrics: &ExtensionServerMetrics{
				totalHookCalls:  100,
				successfulCalls: 95,
				failedCalls:     5, // 5% failure rate - within threshold
				hookTypeMetrics: make(map[string]*HookTypeMetrics),
			},
			configCache: &ConfigCache{
				routeGenerationConfig: make(map[string]*gatewayv1alpha1.RouteGenerationConfig),
				tailscaleEndpoints:    make(map[string]*gatewayv1alpha1.TailscaleEndpoints),
				gatewayConfigs:        make(map[string]*gatewayv1alpha1.TailscaleGateway),
				lastUpdate:            time.Now().Add(-1 * time.Minute), // Fresh cache
			},
			resourceIndex: &ResourceIndex{
				endpointsToHTTPRoutes: make(map[string][]string),
				httpRoutesToEndpoints: make(map[string][]string),
				indexUpdateCount:      5,
				lastIndexUpdate:       time.Now().Add(-30 * time.Second), // Fresh index
			},
		}

		req := httptest.NewRequest("GET", "/health", nil)
		w := httptest.NewRecorder()

		healthyServer.healthHandler(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
		}

		var response map[string]interface{}
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatalf("failed to parse JSON response: %v", err)
		}

		if response["status"] != "healthy" {
			t.Errorf("expected status to be healthy, got %v", response["status"])
		}

		checks, ok := response["checks"].(map[string]interface{})
		if !ok {
			t.Fatal("expected checks to be present in response")
		}

		if checks["low_failure_rate"] != true {
			t.Error("expected low_failure_rate to be true (5% is within threshold)")
		}

		if checks["cache_recently_updated"] != true {
			t.Error("expected cache_recently_updated to be true (cache is fresh)")
		}

		if checks["index_recently_updated"] != true {
			t.Error("expected index_recently_updated to be true (index is fresh)")
		}

		if response["failure_rate"] != 0.05 {
			t.Errorf("expected failure_rate to be 0.05, got %v", response["failure_rate"])
		}
	})

	t.Run("health_endpoint_degraded", func(t *testing.T) {
		// Test health endpoint with degraded health (high failure rate)
		degradedServer := &TailscaleExtensionServer{
			client: fc,
			logger: slog.New(slog.NewTextHandler(os.Stderr, nil)),
			metrics: &ExtensionServerMetrics{
				totalHookCalls:  100,
				successfulCalls: 50, // 50% failure rate
				failedCalls:     50,
				hookTypeMetrics: make(map[string]*HookTypeMetrics),
			},
			configCache:   server.configCache,
			resourceIndex: server.resourceIndex,
		}

		req := httptest.NewRequest("GET", "/health", nil)
		w := httptest.NewRecorder()

		degradedServer.healthHandler(w, req)

		if w.Code != http.StatusServiceUnavailable {
			t.Errorf("expected status %d, got %d", http.StatusServiceUnavailable, w.Code)
		}

		var response map[string]interface{}
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatalf("failed to parse JSON response: %v", err)
		}

		if response["status"] != "degraded" {
			t.Errorf("expected status to be degraded, got %v", response["status"])
		}

		checks, ok := response["checks"].(map[string]interface{})
		if !ok {
			t.Fatal("expected checks to be present in response")
		}

		if checks["low_failure_rate"] != false {
			t.Error("expected low_failure_rate to be false (50% is above threshold)")
		}

		if response["failure_rate"] != 0.5 {
			t.Errorf("expected failure_rate to be 0.5, got %v", response["failure_rate"])
		}
	})

	t.Run("metrics_endpoint_no_metrics", func(t *testing.T) {
		// Test metrics endpoint when metrics are not available
		noMetricsServer := &TailscaleExtensionServer{
			client:  fc,
			logger:  slog.New(slog.NewTextHandler(os.Stderr, nil)),
			metrics: nil, // No metrics
		}

		req := httptest.NewRequest("GET", "/metrics", nil)
		w := httptest.NewRecorder()

		noMetricsServer.metricsHandler(w, req)

		if w.Code != http.StatusServiceUnavailable {
			t.Errorf("expected status %d, got %d", http.StatusServiceUnavailable, w.Code)
		}
	})
}
