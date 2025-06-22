// Copyright (c) Tailscale Gateway Authors
// SPDX-License-Identifier: BSD-3-Clause

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/rajsinghtech/tailscale-gateway/internal/tailscale"
)

// ServiceCoordinator manages multi-operator service coordination
type ServiceCoordinator struct {
	tsClient   tailscale.Client
	kubeClient client.Client
	operatorID string
	clusterID  string
	logger     *zap.SugaredLogger
}

// ServiceRegistration tracks how operators use shared services
type ServiceRegistration struct {
	ServiceName      tailscale.ServiceName   `json:"serviceName"`
	OwnerOperator    string                  `json:"ownerOperator"` // First operator to create
	ConsumerClusters map[string]ConsumerInfo `json:"consumers"`     // All operators using this service
	VIPAddresses     []string                `json:"vipAddresses"`
	LastUpdated      time.Time               `json:"lastUpdated"`
}

// ConsumerInfo tracks cluster usage of a service
type ConsumerInfo struct {
	OperatorID string    `json:"operatorId"`
	ClusterID  string    `json:"clusterId"`
	LastSeen   time.Time `json:"lastSeen"`
	Routes     []string  `json:"routes"` // HTTPRoutes using this service
}

// SharedServiceMapping represents a service shared across clusters
type SharedServiceMapping struct {
	ServiceName      string                  `json:"serviceName"`
	NormalizedName   string                  `json:"normalizedName"`
	TailscaleFQDN    string                  `json:"tailscaleFQDN"`
	ClusterName      string                  `json:"clusterName"`
	VIPAddresses     []string                `json:"vipAddresses"`
	ConsumerClusters map[string]ConsumerInfo `json:"consumerClusters"`
	OwnerOperator    string                  `json:"ownerOperator"`
}

// NewServiceCoordinator creates a new service coordinator
func NewServiceCoordinator(tsClient tailscale.Client, kubeClient client.Client, operatorID, clusterID string, logger *zap.SugaredLogger) *ServiceCoordinator {
	return &ServiceCoordinator{
		tsClient:   tsClient,
		kubeClient: kubeClient,
		operatorID: operatorID,
		clusterID:  clusterID,
		logger:     logger,
	}
}

// EnsureServiceForRoute ensures a service exists for a route, creating or attaching as needed
func (sc *ServiceCoordinator) EnsureServiceForRoute(
	ctx context.Context,
	routeName string,
	targetBackend string,
) (*ServiceRegistration, error) {
	serviceName := sc.GenerateServiceName(targetBackend)

	// First, check if service already exists
	existingService, err := sc.tsClient.GetVIPService(ctx, serviceName)
	if err != nil && !isNotFoundError(err) {
		return nil, fmt.Errorf("failed to check existing service: %w", err)
	}

	if existingService != nil {
		// Service exists - attach to it
		return sc.attachToExistingService(ctx, existingService, routeName)
	}

	// Service doesn't exist - create new one
	return sc.createNewService(ctx, serviceName, targetBackend, routeName)
}

// attachToExistingService attaches this operator to an existing service
func (sc *ServiceCoordinator) attachToExistingService(
	ctx context.Context,
	service *tailscale.VIPService,
	routeName string,
) (*ServiceRegistration, error) {
	// Parse existing service registry from annotations
	registry, err := sc.parseServiceRegistry(service.Annotations)
	if err != nil {
		return nil, fmt.Errorf("failed to parse service registry: %w", err)
	}

	// Add this operator as a consumer
	if registry.ConsumerClusters == nil {
		registry.ConsumerClusters = make(map[string]ConsumerInfo)
	}

	consumerKey := fmt.Sprintf("%s-%s", sc.clusterID, sc.operatorID)
	consumer := registry.ConsumerClusters[consumerKey]
	consumer.OperatorID = sc.operatorID
	consumer.ClusterID = sc.clusterID
	consumer.LastSeen = time.Now()
	consumer.Routes = appendUnique(consumer.Routes, routeName)
	registry.ConsumerClusters[consumerKey] = consumer

	// Update service annotations with new registry
	updatedAnnotations := sc.encodeServiceRegistry(registry)
	service.Annotations = mergeAnnotations(service.Annotations, updatedAnnotations)

	if err := sc.tsClient.CreateOrUpdateVIPService(ctx, service); err != nil {
		return nil, fmt.Errorf("failed to update service registry: %w", err)
	}

	sc.logger.Infof("Attached to existing service %s (owner: %s)",
		service.Name, registry.OwnerOperator)
	return registry, nil
}

// createNewService creates a new VIP service with this operator as owner
func (sc *ServiceCoordinator) createNewService(
	ctx context.Context,
	serviceName tailscale.ServiceName,
	targetBackend string,
	routeName string,
) (*ServiceRegistration, error) {
	// Create new service registry
	registry := &ServiceRegistration{
		ServiceName:   serviceName,
		OwnerOperator: sc.operatorID,
		ConsumerClusters: map[string]ConsumerInfo{
			fmt.Sprintf("%s-%s", sc.clusterID, sc.operatorID): {
				OperatorID: sc.operatorID,
				ClusterID:  sc.clusterID,
				LastSeen:   time.Now(),
				Routes:     []string{routeName},
			},
		},
		LastUpdated: time.Now(),
	}

	// Create VIP service with registry metadata
	vipService := &tailscale.VIPService{
		Name:        serviceName,
		Tags:        []string{"tag:gateway-operator", "tag:multi-cluster-service"},
		Comment:     fmt.Sprintf("Multi-cluster service: %s", targetBackend),
		Annotations: sc.encodeServiceRegistry(registry),
		Ports:       []string{"tcp:80", "tcp:443"},
	}

	if err := sc.tsClient.CreateOrUpdateVIPService(ctx, vipService); err != nil {
		return nil, fmt.Errorf("failed to create VIP service: %w", err)
	}

	// Get the allocated VIP addresses
	createdService, err := sc.tsClient.GetVIPService(ctx, serviceName)
	if err != nil {
		return nil, fmt.Errorf("failed to get created service: %w", err)
	}

	registry.VIPAddresses = createdService.Addrs

	sc.logger.Infof("Created new service %s with VIPs: %v", serviceName, registry.VIPAddresses)
	return registry, nil
}

// DetachFromService detaches this operator from a service
func (sc *ServiceCoordinator) DetachFromService(
	ctx context.Context,
	serviceName tailscale.ServiceName,
	routeName string,
) error {
	service, err := sc.tsClient.GetVIPService(ctx, serviceName)
	if err != nil {
		if isNotFoundError(err) {
			return nil // Service already deleted
		}
		return fmt.Errorf("failed to get service: %w", err)
	}

	registry, err := sc.parseServiceRegistry(service.Annotations)
	if err != nil {
		return fmt.Errorf("failed to parse service registry: %w", err)
	}

	consumerKey := fmt.Sprintf("%s-%s", sc.clusterID, sc.operatorID)
	consumer := registry.ConsumerClusters[consumerKey]

	// Remove route from consumer
	consumer.Routes = removeString(consumer.Routes, routeName)

	if len(consumer.Routes) == 0 {
		// No more routes - remove consumer entirely
		delete(registry.ConsumerClusters, consumerKey)
		sc.logger.Infof("Removed consumer %s from service %s", consumerKey, serviceName)
	} else {
		// Update consumer with remaining routes
		registry.ConsumerClusters[consumerKey] = consumer
		sc.logger.Infof("Updated consumer %s routes for service %s", consumerKey, serviceName)
	}

	// Check if we should delete the service entirely
	if len(registry.ConsumerClusters) == 0 {
		// No consumers left - delete the service
		if err := sc.tsClient.DeleteVIPService(ctx, serviceName); err != nil {
			return fmt.Errorf("failed to delete unused service: %w", err)
		}
		sc.logger.Infof("Deleted unused service %s", serviceName)
		return nil
	}

	// Update service with new registry
	registry.LastUpdated = time.Now()
	service.Annotations = sc.encodeServiceRegistry(registry)

	if err := sc.tsClient.CreateOrUpdateVIPService(ctx, service); err != nil {
		return fmt.Errorf("failed to update service registry: %w", err)
	}

	return nil
}

// CleanupStaleConsumers removes stale consumers from services
func (sc *ServiceCoordinator) CleanupStaleConsumers(ctx context.Context) error {
	// Get all services managed by gateway operators
	allServices, err := sc.tsClient.GetVIPServices(ctx)
	if err != nil {
		return fmt.Errorf("failed to get VIP services: %w", err)
	}

	for _, service := range allServices {
		// Check if this is a gateway operator service
		if !sc.isGatewayOperatorService(service) {
			continue
		}

		registry, err := sc.parseServiceRegistry(service.Annotations)
		if err != nil {
			sc.logger.Warnf("Failed to parse registry for service %s: %v", service.Name, err)
			continue
		}

		updated := false
		staleThreshold := time.Now().Add(-30 * time.Minute) // 30 minutes

		for consumerKey, consumer := range registry.ConsumerClusters {
			if consumer.LastSeen.Before(staleThreshold) {
				delete(registry.ConsumerClusters, consumerKey)
				updated = true
				sc.logger.Infof("Removed stale consumer %s from service %s",
					consumerKey, service.Name)
			}
		}

		if updated {
			if len(registry.ConsumerClusters) == 0 {
				// Delete service if no consumers
				if err := sc.tsClient.DeleteVIPService(ctx, service.Name); err != nil {
					sc.logger.Errorf("Failed to delete unused service %s: %v", service.Name, err)
				}
			} else {
				// Update service registry
				registry.LastUpdated = time.Now()
				service.Annotations = sc.encodeServiceRegistry(registry)
				if err := sc.tsClient.CreateOrUpdateVIPService(ctx, service); err != nil {
					sc.logger.Errorf("Failed to update service %s: %v", service.Name, err)
				}
			}
		}
	}

	return nil
}

// GetSharedServiceMappings returns all services this cluster participates in
func (sc *ServiceCoordinator) GetSharedServiceMappings(ctx context.Context, tailnetDomain string) ([]SharedServiceMapping, error) {
	allServices, err := sc.tsClient.GetVIPServices(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get VIP services: %w", err)
	}

	var mappings []SharedServiceMapping
	for _, service := range allServices {
		if !sc.isGatewayOperatorService(service) {
			continue
		}

		registry, err := sc.parseServiceRegistry(service.Annotations)
		if err != nil {
			continue
		}

		bareName := strings.TrimPrefix(string(service.Name), "svc:")

		mappings = append(mappings, SharedServiceMapping{
			ServiceName:      string(service.Name),
			NormalizedName:   bareName,
			TailscaleFQDN:    fmt.Sprintf("%s.%s", bareName, tailnetDomain),
			ClusterName:      fmt.Sprintf("tailscale-shared-%s", bareName),
			VIPAddresses:     service.Addrs,
			ConsumerClusters: registry.ConsumerClusters,
			OwnerOperator:    registry.OwnerOperator,
		})
	}

	return mappings, nil
}

// IsConsumedByCluster checks if a service is consumed by this cluster
func (m *SharedServiceMapping) IsConsumedByCluster(clusterID string) bool {
	for _, consumer := range m.ConsumerClusters {
		if consumer.ClusterID == clusterID {
			return true
		}
	}
	return false
}

// encodeServiceRegistry encodes registry as service annotations
func (sc *ServiceCoordinator) encodeServiceRegistry(registry *ServiceRegistration) map[string]string {
	registryJSON, _ := json.Marshal(registry)

	return map[string]string{
		"gateway.tailscale.com/service-registry": string(registryJSON),
		"gateway.tailscale.com/owner-operator":   registry.OwnerOperator,
		"gateway.tailscale.com/consumer-count":   strconv.Itoa(len(registry.ConsumerClusters)),
		"gateway.tailscale.com/last-updated":     registry.LastUpdated.Format(time.RFC3339),
		"gateway.tailscale.com/schema-version":   "v1",
	}
}

// parseServiceRegistry parses registry from service annotations
func (sc *ServiceCoordinator) parseServiceRegistry(annotations map[string]string) (*ServiceRegistration, error) {
	registryJSON, exists := annotations["gateway.tailscale.com/service-registry"]
	if !exists {
		return nil, fmt.Errorf("service registry annotation not found")
	}

	var registry ServiceRegistration
	if err := json.Unmarshal([]byte(registryJSON), &registry); err != nil {
		return nil, fmt.Errorf("failed to unmarshal service registry: %w", err)
	}

	return &registry, nil
}

// GenerateServiceName generates a service name from target backend
func (sc *ServiceCoordinator) GenerateServiceName(targetBackend string) tailscale.ServiceName {
	// Convert to valid hostname format
	hostname := strings.ToLower(targetBackend)
	hostname = strings.ReplaceAll(hostname, ".", "-")
	hostname = strings.ReplaceAll(hostname, "/", "-")
	hostname = strings.Trim(hostname, "-")

	return tailscale.ServiceName(fmt.Sprintf("svc:%s", hostname))
}

// isGatewayOperatorService checks if service is managed by gateway operator
func (sc *ServiceCoordinator) isGatewayOperatorService(service *tailscale.VIPService) bool {
	for _, tag := range service.Tags {
		if tag == "tag:gateway-operator" {
			return true
		}
	}
	return false
}

// Utility functions

func isNotFoundError(err error) bool {
	return apierrors.IsNotFound(err) || strings.Contains(err.Error(), "not found")
}

func appendUnique(slice []string, item string) []string {
	for _, existing := range slice {
		if existing == item {
			return slice
		}
	}
	return append(slice, item)
}

func removeString(slice []string, item string) []string {
	var result []string
	for _, s := range slice {
		if s != item {
			result = append(result, s)
		}
	}
	return result
}

func mergeAnnotations(existing, new map[string]string) map[string]string {
	if existing == nil {
		existing = make(map[string]string)
	}
	for k, v := range new {
		existing[k] = v
	}
	return existing
}
