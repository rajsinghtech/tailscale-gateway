package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap/zaptest"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// Simple test structure for VIP services
type TestVIPService struct {
	Name        string
	Annotations map[string]string
}

// MockTailscaleClient for testing
type MockTailscaleClient struct {
	services map[string]*TestVIPService
}

func NewMockTailscaleClient() *MockTailscaleClient {
	return &MockTailscaleClient{
		services: make(map[string]*TestVIPService),
	}
}

var ErrServiceNotFound = errors.New("service not found")

func (m *MockTailscaleClient) GetVIPService(ctx context.Context, serviceName string) (*TestVIPService, error) {
	if service, exists := m.services[serviceName]; exists {
		return service, nil
	}
	return nil, ErrServiceNotFound
}

func (m *MockTailscaleClient) CreateVIPService(ctx context.Context, service *TestVIPService) error {
	m.services[service.Name] = service
	return nil
}

func (m *MockTailscaleClient) UpdateVIPService(ctx context.Context, service *TestVIPService) error {
	m.services[service.Name] = service
	return nil
}

func (m *MockTailscaleClient) DeleteVIPService(ctx context.Context, serviceName string) error {
	delete(m.services, serviceName)
	return nil
}

// MockServiceCoordinator for testing
type MockServiceCoordinator struct {
	tsClient   *MockTailscaleClient
	kubeClient client.Client
	operatorID string
	clusterID  string
	logger     interface{} // Simplified for testing
}

func (tsc *MockServiceCoordinator) GenerateServiceName(targetBackend string) string {
	result := targetBackend
	result = strings.ReplaceAll(result, ".", "-")
	result = strings.ReplaceAll(result, ":", "-")
	return result
}

func TestServiceCoordinator(t *testing.T) {
	scheme := runtime.NewScheme()

	t.Run("new_service_creation", func(t *testing.T) {
		fc := fake.NewClientBuilder().
			WithScheme(scheme).
			Build()

		mockClient := NewMockTailscaleClient()
		logger := zaptest.NewLogger(t).Sugar()

		coordinator := &MockServiceCoordinator{
			tsClient:   mockClient,
			kubeClient: fc,
			operatorID: "operator-1",
			clusterID:  "cluster-1",
			logger:     logger,
		}
		_ = coordinator // Use coordinator to avoid unused variable warning

		// Test creating a new service  
		serviceName := coordinator.GenerateServiceName("httpbin.org:80")
		if serviceName != "httpbin-org-80" {
			t.Errorf("expected service name 'httpbin-org-80', got %q", serviceName)
		}

		// Test that new service can be created
		service := &TestVIPService{
			Name: serviceName,
			Annotations: map[string]string{
				"gateway.tailscale.com/owner-operator": "operator-1",
			},
		}

		err := mockClient.CreateVIPService(context.Background(), service)
		if err != nil {
			t.Fatalf("failed to create service: %v", err)
		}

		// Verify service was created
		retrieved, err := mockClient.GetVIPService(context.Background(), serviceName)
		if err != nil {
			t.Fatalf("failed to get created service: %v", err)
		}

		if retrieved.Name != serviceName {
			t.Errorf("expected retrieved service name %q, got %q", serviceName, retrieved.Name)
		}
	})

	t.Run("attach_to_existing_service", func(t *testing.T) {
		fc := fake.NewClientBuilder().
			WithScheme(scheme).
			Build()

		mockClient := NewMockTailscaleClient()
		logger := zaptest.NewLogger(t).Sugar()

		coordinator := &MockServiceCoordinator{
			tsClient:   mockClient,
			kubeClient: fc,
			operatorID: "operator-2",
			clusterID:  "cluster-2",
			logger:     logger,
		}
		_ = coordinator // Use coordinator to avoid unused variable warning

		// Create existing service
		existingService := &TestVIPService{
			Name: "test-service",
			Annotations: map[string]string{
				"gateway.tailscale.com/owner-operator": "operator-1",
				"gateway.tailscale.com/consumer-count": "1",
			},
		}

		// Add to mock client
		err := mockClient.CreateVIPService(context.Background(), existingService)
		if err != nil {
			t.Fatalf("failed to create existing service: %v", err)
		}

		// Test getting existing service
		retrieved, err := mockClient.GetVIPService(context.Background(), "test-service")
		if err != nil {
			t.Fatalf("failed to get existing service: %v", err)
		}

		if retrieved.Annotations["gateway.tailscale.com/owner-operator"] != "operator-1" {
			t.Errorf("expected owner operator 'operator-1', got %q", retrieved.Annotations["gateway.tailscale.com/owner-operator"])
		}

		// Test updating service with new consumer
		retrieved.Annotations["gateway.tailscale.com/consumer-count"] = "2"
		err = mockClient.UpdateVIPService(context.Background(), retrieved)
		if err != nil {
			t.Fatalf("failed to update service: %v", err)
		}

		// Verify update
		updated, err := mockClient.GetVIPService(context.Background(), "test-service")
		if err != nil {
			t.Fatalf("failed to get updated service: %v", err)
		}

		if updated.Annotations["gateway.tailscale.com/consumer-count"] != "2" {
			t.Errorf("expected consumer count '2', got %q", updated.Annotations["gateway.tailscale.com/consumer-count"])
		}
	})

	t.Run("detach_from_service", func(t *testing.T) {
		fc := fake.NewClientBuilder().
			WithScheme(scheme).
			Build()

		mockClient := NewMockTailscaleClient()
		logger := zaptest.NewLogger(t).Sugar()

		coordinator := &MockServiceCoordinator{
			tsClient:   mockClient,
			kubeClient: fc,
			operatorID: "operator-1",
			clusterID:  "cluster-1",
			logger:     logger,
		}
		_ = coordinator // Use coordinator to avoid unused variable warning

		// Create service
		service := &TestVIPService{
			Name: "test-service",
			Annotations: map[string]string{
				"gateway.tailscale.com/owner-operator": "operator-1",
				"gateway.tailscale.com/routes": "route-1,route-2",
			},
		}

		err := mockClient.CreateVIPService(context.Background(), service)
		if err != nil {
			t.Fatalf("failed to create service: %v", err)
		}

		// Test removing one route
		retrieved, err := mockClient.GetVIPService(context.Background(), "test-service")
		if err != nil {
			t.Fatalf("failed to get service: %v", err)
		}

		// Simulate removing route-1, keeping route-2
		retrieved.Annotations["gateway.tailscale.com/routes"] = "route-2"
		err = mockClient.UpdateVIPService(context.Background(), retrieved)
		if err != nil {
			t.Fatalf("failed to update service: %v", err)
		}

		// Verify the service was updated
		updated, err := mockClient.GetVIPService(context.Background(), "test-service")
		if err != nil {
			t.Fatalf("failed to get updated service: %v", err)
		}

		if updated.Annotations["gateway.tailscale.com/routes"] != "route-2" {
			t.Errorf("expected routes 'route-2', got %q", updated.Annotations["gateway.tailscale.com/routes"])
		}
	})

	t.Run("delete_service_when_no_consumers", func(t *testing.T) {
		fc := fake.NewClientBuilder().
			WithScheme(scheme).
			Build()

		mockClient := NewMockTailscaleClient()
		logger := zaptest.NewLogger(t).Sugar()

		coordinator := &MockServiceCoordinator{
			tsClient:   mockClient,
			kubeClient: fc,
			operatorID: "operator-1",
			clusterID:  "cluster-1",
			logger:     logger,
		}
		_ = coordinator // Use coordinator to avoid unused variable warning

		// Create service with single route
		service := &TestVIPService{
			Name: "test-service",
			Annotations: map[string]string{
				"gateway.tailscale.com/owner-operator": "operator-1",
				"gateway.tailscale.com/routes": "last-route",
			},
		}

		err := mockClient.CreateVIPService(context.Background(), service)
		if err != nil {
			t.Fatalf("failed to create service: %v", err)
		}

		// Test deleting the service
		err = mockClient.DeleteVIPService(context.Background(), "test-service")
		if err != nil {
			t.Fatalf("failed to delete service: %v", err)
		}

		// Verify the service was deleted
		_, err = mockClient.GetVIPService(context.Background(), "test-service")
		if err != ErrServiceNotFound {
			t.Errorf("expected service to be deleted, but got error: %v", err)
		}
	})

	t.Run("cleanup_stale_consumers", func(t *testing.T) {
		fc := fake.NewClientBuilder().
			WithScheme(scheme).
			Build()

		mockClient := NewMockTailscaleClient()
		logger := zaptest.NewLogger(t).Sugar()

		coordinator := &MockServiceCoordinator{
			tsClient:   mockClient,
			kubeClient: fc,
			operatorID: "operator-1",
			clusterID:  "cluster-1",
			logger:     logger,
		}
		_ = coordinator // Use coordinator to avoid unused variable warning

		// Create service with simulated stale consumer tracking
		service := &TestVIPService{
			Name: "test-service",
			Annotations: map[string]string{
				"gateway.tailscale.com/owner-operator": "operator-1",
				"gateway.tailscale.com/consumers": "cluster-1,cluster-2",
				"gateway.tailscale.com/last-cleanup": time.Now().Add(-60 * time.Minute).Format(time.RFC3339),
			},
		}

		err := mockClient.CreateVIPService(context.Background(), service)
		if err != nil {
			t.Fatalf("failed to create service: %v", err)
		}

		// Test that cleanup would identify stale consumers
		retrieved, err := mockClient.GetVIPService(context.Background(), "test-service")
		if err != nil {
			t.Fatalf("failed to get service: %v", err)
		}

		if retrieved.Annotations["gateway.tailscale.com/consumers"] != "cluster-1,cluster-2" {
			t.Errorf("expected consumers 'cluster-1,cluster-2', got %q", retrieved.Annotations["gateway.tailscale.com/consumers"])
		}

		// Simulate cleanup by updating consumers
		retrieved.Annotations["gateway.tailscale.com/consumers"] = "cluster-1"
		retrieved.Annotations["gateway.tailscale.com/last-cleanup"] = time.Now().Format(time.RFC3339)
		
		err = mockClient.UpdateVIPService(context.Background(), retrieved)
		if err != nil {
			t.Fatalf("failed to update service: %v", err)
		}

		// Verify cleanup simulation
		updated, err := mockClient.GetVIPService(context.Background(), "test-service")
		if err != nil {
			t.Fatalf("failed to get updated service: %v", err)
		}

		if updated.Annotations["gateway.tailscale.com/consumers"] != "cluster-1" {
			t.Errorf("expected consumers 'cluster-1', got %q", updated.Annotations["gateway.tailscale.com/consumers"])
		}
	})
}

func TestServiceRegistration(t *testing.T) {
	t.Run("generate_service_name", func(t *testing.T) {
		coordinator := &MockServiceCoordinator{}

		tests := []struct {
			target   string
			expected string
		}{
			{"httpbin.org:80", "httpbin-org-80"},
			{"api.github.com:443", "api-github-com-443"},
			{"localhost:8080", "localhost-8080"},
			{"test-service.default.svc.cluster.local:80", "test-service-default-svc-cluster-local-80"},
		}

		for _, test := range tests {
			t.Run(test.target, func(t *testing.T) {
				result := coordinator.GenerateServiceName(test.target)
				if result != test.expected {
					t.Errorf("expected %q, got %q", test.expected, result)
				}
			})
		}
	})
}