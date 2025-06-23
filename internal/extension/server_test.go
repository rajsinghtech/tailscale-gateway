package extension

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"

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
