package extension

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"
	gwapiv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	gatewayv1alpha1 "github.com/rajsinghtech/tailscale-gateway/api/v1alpha1"
)

// TestGatewayAPICompliance tests complete Gateway API compliance with TailscaleEndpoints backends
func TestGatewayAPICompliance(t *testing.T) {
	scheme := runtime.NewScheme()
	gatewayv1alpha1.AddToScheme(scheme)
	gwapiv1.AddToScheme(scheme)
	gwapiv1alpha2.AddToScheme(scheme)
	corev1.AddToScheme(scheme)

	t.Run("http_route_with_tailscale_endpoints_backend", func(t *testing.T) {
		fc := fake.NewClientBuilder().WithScheme(scheme).Build()

		// Create TailscaleEndpoints resource
		endpoints := &gatewayv1alpha1.TailscaleEndpoints{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "api-endpoints",
				Namespace: "production",
			},
			Spec: gatewayv1alpha1.TailscaleEndpointsSpec{
				Tailnet: "company.tailnet.ts.net",
				Endpoints: []gatewayv1alpha1.TailscaleEndpoint{
					{
						Name:           "payment-api",
						TailscaleIP:    "100.64.0.10",
						TailscaleFQDN:  "payment-api.company.tailnet.ts.net",
						Port:           8080,
						Protocol:       "HTTP",
						ExternalTarget: "payment.internal.company.com:8080",
					},
					{
						Name:           "user-api",
						TailscaleIP:    "100.64.0.11",
						TailscaleFQDN:  "user-api.company.tailnet.ts.net",
						Port:           9000,
						Protocol:       "HTTP",
						ExternalTarget: "users.internal.company.com:9000",
					},
				},
			},
		}

		// Create HTTPRoute that references TailscaleEndpoints as backend
		httpRoute := &gwapiv1.HTTPRoute{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "api-gateway-route",
				Namespace: "production",
			},
			Spec: gwapiv1.HTTPRouteSpec{
				Hostnames: []gwapiv1.Hostname{"api.company.com"},
				Rules: []gwapiv1.HTTPRouteRule{
					{
						Matches: []gwapiv1.HTTPRouteMatch{
							{
								Path: &gwapiv1.HTTPPathMatch{
									Type:  pathTypePtr(gwapiv1.PathMatchPathPrefix),
									Value: stringPtr("/payments/"),
								},
							},
						},
						BackendRefs: []gwapiv1.HTTPBackendRef{
							{
								BackendRef: gwapiv1.BackendRef{
									BackendObjectReference: gwapiv1.BackendObjectReference{
										Group:     groupPtr("gateway.tailscale.com"),
										Kind:      kindPtr("TailscaleEndpoints"),
										Name:      "api-endpoints",
										Namespace: namespacePtr("production"),
									},
								},
								Filters: []gwapiv1.HTTPRouteFilter{
									{
										Type: gwapiv1.HTTPRouteFilterURLRewrite,
										URLRewrite: &gwapiv1.HTTPURLRewriteFilter{
											Path: &gwapiv1.HTTPPathModifier{
												Type:               gwapiv1.PrefixMatchHTTPPathModifier,
												ReplacePrefixMatch: stringPtr("/api/v1/payments/"),
											},
										},
									},
								},
							},
						},
					},
					{
						Matches: []gwapiv1.HTTPRouteMatch{
							{
								Path: &gwapiv1.HTTPPathMatch{
									Type:  pathTypePtr(gwapiv1.PathMatchPathPrefix),
									Value: stringPtr("/users/"),
								},
							},
						},
						BackendRefs: []gwapiv1.HTTPBackendRef{
							{
								BackendRef: gwapiv1.BackendRef{
									BackendObjectReference: gwapiv1.BackendObjectReference{
										Group:     groupPtr("gateway.tailscale.com"),
										Kind:      kindPtr("TailscaleEndpoints"),
										Name:      "api-endpoints",
										Namespace: namespacePtr("production"),
									},
								},
							},
						},
					},
				},
			},
		}

		// Create all resources
		mustCreate(t, fc, endpoints)
		mustCreate(t, fc, httpRoute)

		// Create extension server
		server := &TailscaleExtensionServer{
			client: fc,
			logger: slog.New(slog.NewTextHandler(os.Stderr, nil)),
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

		// Test HTTPRoute discovery
		routes, err := server.discoverHTTPRoutes(context.Background())
		if err != nil {
			t.Fatalf("failed to discover HTTPRoutes: %v", err)
		}

		if len(routes) != 1 {
			t.Fatalf("expected 1 HTTPRoute, got %d", len(routes))
		}

		route := routes[0]
		if route.Name != "api-gateway-route" {
			t.Errorf("expected route name 'api-gateway-route', got %q", route.Name)
		}

		// Test that route has TailscaleEndpoints backends
		foundTailscaleBackend := false
		for _, rule := range route.Spec.Rules {
			for _, backendRef := range rule.BackendRefs {
				if backendRef.Group != nil && *backendRef.Group == "gateway.tailscale.com" &&
					backendRef.Kind != nil && *backendRef.Kind == "TailscaleEndpoints" {
					foundTailscaleBackend = true
					break
				}
			}
		}

		if !foundTailscaleBackend {
			t.Error("expected HTTPRoute to have TailscaleEndpoints backend")
		}

		// Test TailscaleEndpoints discovery
		endpointsList, err := server.discoverTailscaleEndpoints(context.Background())
		if err != nil {
			t.Fatalf("failed to discover TailscaleEndpoints: %v", err)
		}

		if len(endpointsList) != 1 {
			t.Fatalf("expected 1 TailscaleEndpoints, got %d", len(endpointsList))
		}

		endpoints = &endpointsList[0]
		if endpoints.Name != "api-endpoints" {
			t.Errorf("expected endpoints name 'api-endpoints', got %q", endpoints.Name)
		}

		if len(endpoints.Spec.Endpoints) != 2 {
			t.Errorf("expected 2 endpoint definitions, got %d", len(endpoints.Spec.Endpoints))
		}

		// Test backend processing for TailscaleEndpoints
		for _, rule := range route.Spec.Rules {
			for _, backendRef := range rule.BackendRefs {
				if backendRef.Group != nil && *backendRef.Group == "gateway.tailscale.com" &&
					backendRef.Kind != nil && *backendRef.Kind == "TailscaleEndpoints" {

					// Test that we can process this backend
					tailscaleEndpointsMap := make(map[string]*gatewayv1alpha1.TailscaleEndpoints)
					tailscaleEndpointsMap["production/api-endpoints"] = endpoints

					err := server.processTailscaleEndpointsBackend(context.Background(), &backendRef, &route, tailscaleEndpointsMap)
					if err != nil {
						t.Errorf("failed to process TailscaleEndpoints backend: %v", err)
					}
				}
			}
		}
	})

	t.Run("tcp_route_with_tailscale_endpoints_backend", func(t *testing.T) {
		fc := fake.NewClientBuilder().WithScheme(scheme).Build()

		// Create TailscaleEndpoints for database services
		endpoints := &gatewayv1alpha1.TailscaleEndpoints{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "database-endpoints",
				Namespace: "production",
			},
			Spec: gatewayv1alpha1.TailscaleEndpointsSpec{
				Tailnet: "company.tailnet.ts.net",
				Endpoints: []gatewayv1alpha1.TailscaleEndpoint{
					{
						Name:           "postgres-primary",
						TailscaleIP:    "100.64.0.20",
						TailscaleFQDN:  "postgres-primary.company.tailnet.ts.net",
						Port:           5432,
						Protocol:       "TCP",
						ExternalTarget: "postgres-primary.internal.company.com:5432",
					},
					{
						Name:           "redis-cluster",
						TailscaleIP:    "100.64.0.21",
						TailscaleFQDN:  "redis-cluster.company.tailnet.ts.net",
						Port:           6379,
						Protocol:       "TCP",
						ExternalTarget: "redis.internal.company.com:6379",
					},
				},
			},
		}

		// Create TCPRoute that references TailscaleEndpoints as backend
		tcpRoute := &gwapiv1alpha2.TCPRoute{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "database-route",
				Namespace: "production",
			},
			Spec: gwapiv1alpha2.TCPRouteSpec{
				Rules: []gwapiv1alpha2.TCPRouteRule{
					{
						BackendRefs: []gwapiv1alpha2.BackendRef{
							{
								BackendObjectReference: gwapiv1alpha2.BackendObjectReference{
									Group:     groupPtr("gateway.tailscale.com"),
									Kind:      kindPtr("TailscaleEndpoints"),
									Name:      "database-endpoints",
									Namespace: namespacePtr("production"),
									Port:      portNumberPtrV1alpha2(5432),
								},
							},
						},
					},
				},
			},
		}

		// Create all resources
		mustCreate(t, fc, endpoints)
		mustCreate(t, fc, tcpRoute)

		// Create extension server
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

		route := routes[0]
		if route.Name != "database-route" {
			t.Errorf("expected route name 'database-route', got %q", route.Name)
		}

		// Test that route is relevant due to TailscaleEndpoints backend
		if !server.isTCPRouteRelevant(&route) {
			t.Error("expected TCP route to be relevant (has TailscaleEndpoints backend)")
		}

		// Test backend processing
		serviceMap := make(map[string]*corev1.Service)
		tailscaleEndpointsMap := make(map[string]*gatewayv1alpha1.TailscaleEndpoints)
		tailscaleEndpointsMap["production/database-endpoints"] = endpoints

		err = server.processTCPRouteBackends(context.Background(), &route, serviceMap, tailscaleEndpointsMap)
		if err != nil {
			t.Errorf("failed to process TCP route backends: %v", err)
		}
	})

	t.Run("multi_protocol_integration", func(t *testing.T) {
		fc := fake.NewClientBuilder().WithScheme(scheme).Build()

		// Create comprehensive TailscaleEndpoints with multiple protocols
		endpoints := &gatewayv1alpha1.TailscaleEndpoints{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "multi-protocol-endpoints",
				Namespace: "default",
			},
			Spec: gatewayv1alpha1.TailscaleEndpointsSpec{
				Tailnet: "test.tailnet.ts.net",
				Endpoints: []gatewayv1alpha1.TailscaleEndpoint{
					{
						Name:           "web-app",
						TailscaleIP:    "100.64.0.100",
						TailscaleFQDN:  "web-app.test.tailnet.ts.net",
						Port:           80,
						Protocol:       "HTTP",
						ExternalTarget: "webapp.internal:80",
					},
					{
						Name:           "database",
						TailscaleIP:    "100.64.0.101",
						TailscaleFQDN:  "database.test.tailnet.ts.net",
						Port:           3306,
						Protocol:       "TCP",
						ExternalTarget: "mysql.internal:3306",
					},
					{
						Name:           "dns-server",
						TailscaleIP:    "100.64.0.102",
						TailscaleFQDN:  "dns-server.test.tailnet.ts.net",
						Port:           53,
						Protocol:       "UDP",
						ExternalTarget: "dns.internal:53",
					},
					{
						Name:           "grpc-api",
						TailscaleIP:    "100.64.0.103",
						TailscaleFQDN:  "grpc-api.test.tailnet.ts.net",
						Port:           9090,
						Protocol:       "GRPC",
						ExternalTarget: "grpc.internal:9090",
					},
				},
			},
		}

		// Create HTTPRoute
		httpRoute := &gwapiv1.HTTPRoute{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "http-route",
				Namespace: "default",
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
										Name:  "multi-protocol-endpoints",
									},
								},
							},
						},
					},
				},
			},
		}

		// Create TCPRoute
		tcpRoute := &gwapiv1alpha2.TCPRoute{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "tcp-route",
				Namespace: "default",
			},
			Spec: gwapiv1alpha2.TCPRouteSpec{
				Rules: []gwapiv1alpha2.TCPRouteRule{
					{
						BackendRefs: []gwapiv1alpha2.BackendRef{
							{
								BackendObjectReference: gwapiv1alpha2.BackendObjectReference{
									Group: groupPtr("gateway.tailscale.com"),
									Kind:  kindPtr("TailscaleEndpoints"),
									Name:  "multi-protocol-endpoints",
									Port:  portNumberPtrV1alpha2(3306),
								},
							},
						},
					},
				},
			},
		}

		// Create UDPRoute
		udpRoute := &gwapiv1alpha2.UDPRoute{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "udp-route",
				Namespace: "default",
			},
			Spec: gwapiv1alpha2.UDPRouteSpec{
				Rules: []gwapiv1alpha2.UDPRouteRule{
					{
						BackendRefs: []gwapiv1alpha2.BackendRef{
							{
								BackendObjectReference: gwapiv1alpha2.BackendObjectReference{
									Group: groupPtr("gateway.tailscale.com"),
									Kind:  kindPtr("TailscaleEndpoints"),
									Name:  "multi-protocol-endpoints",
									Port:  portNumberPtrV1alpha2(53),
								},
							},
						},
					},
				},
			},
		}

		// Create GRPCRoute
		grpcRoute := &gwapiv1.GRPCRoute{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "grpc-route",
				Namespace: "default",
			},
			Spec: gwapiv1.GRPCRouteSpec{
				Rules: []gwapiv1.GRPCRouteRule{
					{
						BackendRefs: []gwapiv1.GRPCBackendRef{
							{
								BackendRef: gwapiv1.BackendRef{
									BackendObjectReference: gwapiv1.BackendObjectReference{
										Group: groupPtr("gateway.tailscale.com"),
										Kind:  kindPtr("TailscaleEndpoints"),
										Name:  "multi-protocol-endpoints",
										Port:  portNumberPtr(9090),
									},
								},
							},
						},
					},
				},
			},
		}

		// Create all resources
		mustCreate(t, fc, endpoints)
		mustCreate(t, fc, httpRoute)
		mustCreate(t, fc, tcpRoute)
		mustCreate(t, fc, udpRoute)
		mustCreate(t, fc, grpcRoute)

		// Create extension server
		server := &TailscaleExtensionServer{
			client: fc,
			logger: slog.New(slog.NewTextHandler(os.Stderr, nil)),
		}

		// Test comprehensive service discovery
		discoveryCtx, err := server.discoverAllServiceMappings(context.Background())
		if err != nil {
			t.Fatalf("failed comprehensive service discovery: %v", err)
		}

		// Verify all route types were discovered
		if len(discoveryCtx.HTTPRoutes) != 1 {
			t.Errorf("expected 1 HTTPRoute, got %d", len(discoveryCtx.HTTPRoutes))
		}

		if len(discoveryCtx.TCPRoutes) != 1 {
			t.Errorf("expected 1 TCPRoute, got %d", len(discoveryCtx.TCPRoutes))
		}

		if len(discoveryCtx.UDPRoutes) != 1 {
			t.Errorf("expected 1 UDPRoute, got %d", len(discoveryCtx.UDPRoutes))
		}

		if len(discoveryCtx.GRPCRoutes) != 1 {
			t.Errorf("expected 1 GRPCRoute, got %d", len(discoveryCtx.GRPCRoutes))
		}

		if len(discoveryCtx.TailscaleEndpoints) != 1 {
			t.Errorf("expected 1 TailscaleEndpoints, got %d", len(discoveryCtx.TailscaleEndpoints))
		}

		// Verify TailscaleEndpoints has all protocols
		endpoint := discoveryCtx.TailscaleEndpoints[0]
		if len(endpoint.Spec.Endpoints) != 4 {
			t.Errorf("expected 4 endpoint definitions, got %d", len(endpoint.Spec.Endpoints))
		}

		// Verify protocols are correctly mapped
		protocolsFound := make(map[string]bool)
		for _, ep := range endpoint.Spec.Endpoints {
			protocolsFound[ep.Protocol] = true
		}

		expectedProtocols := []string{"HTTP", "TCP", "UDP", "GRPC"}
		for _, protocol := range expectedProtocols {
			if !protocolsFound[protocol] {
				t.Errorf("expected to find protocol %s in TailscaleEndpoints", protocol)
			}
		}

		// Test that all routes are relevant due to TailscaleEndpoints backends
		if !server.isHTTPRouteRelevant(&discoveryCtx.HTTPRoutes[0]) {
			t.Error("expected HTTP route to be relevant")
		}

		if !server.isTCPRouteRelevant(&discoveryCtx.TCPRoutes[0]) {
			t.Error("expected TCP route to be relevant")
		}

		if !server.isUDPRouteRelevant(&discoveryCtx.UDPRoutes[0]) {
			t.Error("expected UDP route to be relevant")
		}

		if !server.isGRPCRouteRelevant(&discoveryCtx.GRPCRoutes[0]) {
			t.Error("expected GRPC route to be relevant")
		}
	})
}

// TestEndToEndGatewayAPIWorkflow tests complete end-to-end Gateway API workflow
func TestEndToEndGatewayAPIWorkflow(t *testing.T) {
	scheme := runtime.NewScheme()
	gatewayv1alpha1.AddToScheme(scheme)
	gwapiv1.AddToScheme(scheme)
	gwapiv1alpha2.AddToScheme(scheme)
	corev1.AddToScheme(scheme)

	fc := fake.NewClientBuilder().WithScheme(scheme).Build()

	// Step 1: Create TailscaleEndpoints
	endpoints := &gatewayv1alpha1.TailscaleEndpoints{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "backend-services",
			Namespace: "production",
		},
		Spec: gatewayv1alpha1.TailscaleEndpointsSpec{
			Tailnet: "production.company.ts.net",
			Endpoints: []gatewayv1alpha1.TailscaleEndpoint{
				{
					Name:           "api-server",
					TailscaleIP:    "100.64.1.10",
					TailscaleFQDN:  "api-server.production.company.ts.net",
					Port:           8080,
					Protocol:       "HTTP",
					ExternalTarget: "api.internal.company.com:8080",
				},
			},
		},
	}

	// Step 2: Create Gateway API Gateway
	gateway := &gwapiv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "production-gateway",
			Namespace: "production",
		},
		Spec: gwapiv1.GatewaySpec{
			GatewayClassName: "envoy-gateway",
			Listeners: []gwapiv1.Listener{
				{
					Name:     "http",
					Protocol: gwapiv1.HTTPProtocolType,
					Port:     80,
				},
			},
		},
	}

	// Step 3: Create HTTPRoute referencing Gateway and TailscaleEndpoints
	httpRoute := &gwapiv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "api-route",
			Namespace: "production",
		},
		Spec: gwapiv1.HTTPRouteSpec{
			CommonRouteSpec: gwapiv1.CommonRouteSpec{
				ParentRefs: []gwapiv1.ParentReference{
					{
						Name: "production-gateway",
					},
				},
			},
			Hostnames: []gwapiv1.Hostname{"api.company.com"},
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
									Group:     groupPtr("gateway.tailscale.com"),
									Kind:      kindPtr("TailscaleEndpoints"),
									Name:      "backend-services",
									Namespace: namespacePtr("production"),
									Port:      portNumberPtr(8080),
								},
							},
						},
					},
				},
			},
		},
	}

	// Create all resources
	mustCreate(t, fc, endpoints)
	mustCreate(t, fc, gateway)
	mustCreate(t, fc, httpRoute)

	// Step 4: Create and test extension server
	server := &TailscaleExtensionServer{
		client: fc,
		logger: slog.New(slog.NewTextHandler(os.Stderr, nil)),
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

	// Test 1: Verify resource discovery
	httpRoutes, err := server.discoverHTTPRoutes(context.Background())
	if err != nil {
		t.Fatalf("failed to discover HTTPRoutes: %v", err)
	}

	if len(httpRoutes) != 1 {
		t.Fatalf("expected 1 HTTPRoute, got %d", len(httpRoutes))
	}

	endpointsList, err := server.discoverTailscaleEndpoints(context.Background())
	if err != nil {
		t.Fatalf("failed to discover TailscaleEndpoints: %v", err)
	}

	if len(endpointsList) != 1 {
		t.Fatalf("expected 1 TailscaleEndpoints, got %d", len(endpointsList))
	}

	// Test 2: Verify Gateway API compliance
	route := httpRoutes[0]
	tailscaleBackendsFound := 0

	for _, rule := range route.Spec.Rules {
		for _, backendRef := range rule.BackendRefs {
			if backendRef.Group != nil && *backendRef.Group == "gateway.tailscale.com" &&
				backendRef.Kind != nil && *backendRef.Kind == "TailscaleEndpoints" {
				tailscaleBackendsFound++

				// Verify backend reference is correct
				if string(backendRef.Name) != "backend-services" {
					t.Errorf("expected backend name 'backend-services', got %q", backendRef.Name)
				}

				if backendRef.Namespace != nil && string(*backendRef.Namespace) != "production" {
					t.Errorf("expected backend namespace 'production', got %q", *backendRef.Namespace)
				}

				if backendRef.Port != nil && *backendRef.Port != 8080 {
					t.Errorf("expected backend port 8080, got %d", *backendRef.Port)
				}
			}
		}
	}

	if tailscaleBackendsFound != 1 {
		t.Errorf("expected 1 TailscaleEndpoints backend, got %d", tailscaleBackendsFound)
	}

	// Test 3: Verify comprehensive service discovery
	discoveryCtx, err := server.discoverAllServiceMappings(context.Background())
	if err != nil {
		t.Fatalf("failed comprehensive service discovery: %v", err)
	}

	if len(discoveryCtx.HTTPRoutes) != 1 {
		t.Errorf("expected 1 HTTPRoute in discovery context, got %d", len(discoveryCtx.HTTPRoutes))
	}

	if len(discoveryCtx.TailscaleEndpoints) != 1 {
		t.Errorf("expected 1 TailscaleEndpoints in discovery context, got %d", len(discoveryCtx.TailscaleEndpoints))
	}

	if len(discoveryCtx.TailscaleServiceMappings) != 1 {
		t.Errorf("expected 1 TailscaleServiceMapping, got %d", len(discoveryCtx.TailscaleServiceMappings))
	}

	// Test 4: Verify service mapping properties
	mapping := discoveryCtx.TailscaleServiceMappings[0]
	expectedMapping := TailscaleServiceMapping{
		ServiceName:     "api-server",
		ClusterName:     "external-backend-api-server",
		EgressService:   "backend-services-api-server-egress.production.svc.cluster.local",
		ExternalBackend: "api.internal.company.com:8080",
		Port:            8080,
		Protocol:        "HTTP",
		PathPrefix:      "/tailscale/api-server/",
		TailnetName:     "production.company.ts.net",
	}

	if diff := cmp.Diff(mapping, expectedMapping); diff != "" {
		t.Errorf("service mapping mismatch (-got +want):\n%s", diff)
	}

	// Test 5: Verify route relevance
	if !server.isHTTPRouteRelevant(&route) {
		t.Error("expected HTTPRoute to be relevant (has TailscaleEndpoints backend)")
	}
}

// Helper functions for integration tests

func groupPtr(s string) *gwapiv1.Group {
	g := gwapiv1.Group(s)
	return &g
}
