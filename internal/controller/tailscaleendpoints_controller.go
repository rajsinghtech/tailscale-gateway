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

package controller

import (
	"context"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	tailscaleclient "tailscale.com/client/tailscale/v2"

	gatewayv1alpha1 "github.com/rajsinghtech/tailscale-gateway/api/v1alpha1"
	"github.com/rajsinghtech/tailscale-gateway/internal/tailscale"
)

const (
	// TailscaleEndpointsFinalizer is the finalizer for TailscaleEndpoints resources
	TailscaleEndpointsFinalizer = "gateway.tailscale.com/tailscaleendpoints"

	// Default sync interval for service discovery
	defaultSyncInterval = time.Second * 30
)

// TailscaleEndpointsReconciler reconciles a TailscaleEndpoints object
type TailscaleEndpointsReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	// TailscaleClientManager provides access to Tailscale APIs per tailnet
	TailscaleClientManager *MultiTailnetManager
}

// MultiTailnetManager manages multiple Tailscale client connections
type MultiTailnetManager struct {
	clients map[string]tailscale.Client
}

// NewMultiTailnetManager creates a new MultiTailnetManager
func NewMultiTailnetManager() *MultiTailnetManager {
	return &MultiTailnetManager{
		clients: make(map[string]tailscale.Client),
	}
}

// GetClient returns a Tailscale client for the specified tailnet
func (m *MultiTailnetManager) GetClient(ctx context.Context, k8sClient client.Client, tailnetName, namespace string) (tailscale.Client, error) {
	key := fmt.Sprintf("%s/%s", namespace, tailnetName)
	
	if client, exists := m.clients[key]; exists {
		return client, nil
	}

	// Find the TailscaleTailnet resource
	tailnet := &gatewayv1alpha1.TailscaleTailnet{}
	if err := k8sClient.Get(ctx, client.ObjectKey{Name: tailnetName, Namespace: namespace}, tailnet); err != nil {
		return nil, fmt.Errorf("failed to get TailscaleTailnet %s/%s: %w", namespace, tailnetName, err)
	}

	// Create client from TailscaleTailnet credentials
	tsClient, err := m.createClientFromTailnet(ctx, k8sClient, tailnet)
	if err != nil {
		return nil, fmt.Errorf("failed to create Tailscale client for %s/%s: %w", namespace, tailnetName, err)
	}

	m.clients[key] = tsClient
	return tsClient, nil
}

// createClientFromTailnet creates a Tailscale client from TailscaleTailnet resource
func (m *MultiTailnetManager) createClientFromTailnet(ctx context.Context, k8sClient client.Client, tailnet *gatewayv1alpha1.TailscaleTailnet) (tailscale.Client, error) {
	// Get OAuth credentials from the secret
	secretNamespace := tailnet.Spec.OAuthSecretNamespace
	if secretNamespace == "" {
		secretNamespace = tailnet.Namespace
	}

	secret := &corev1.Secret{}
	secretKey := client.ObjectKey{
		Name:      tailnet.Spec.OAuthSecretName,
		Namespace: secretNamespace,
	}

	if err := k8sClient.Get(ctx, secretKey, secret); err != nil {
		return nil, fmt.Errorf("failed to get OAuth secret %s/%s: %w", secretNamespace, tailnet.Spec.OAuthSecretName, err)
	}

	clientID, exists := secret.Data["client_id"]
	if !exists {
		return nil, fmt.Errorf("OAuth secret missing client_id")
	}

	clientSecret, exists := secret.Data["client_secret"]
	if !exists {
		return nil, fmt.Errorf("OAuth secret missing client_secret")
	}

	// Create Tailscale client with OAuth credentials
	return tailscale.NewClient(ctx, tailscale.ClientConfig{
		Tailnet:      tailnet.Spec.Tailnet,
		ClientID:     string(clientID),
		ClientSecret: string(clientSecret),
	})
}

//+kubebuilder:rbac:groups=gateway.tailscale.com,resources=tailscaleendpoints,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=gateway.tailscale.com,resources=tailscaleendpoints/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=gateway.tailscale.com,resources=tailscaleendpoints/finalizers,verbs=update
//+kubebuilder:rbac:groups=gateway.tailscale.com,resources=tailscaletailnets,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *TailscaleEndpointsReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Fetch the TailscaleEndpoints instance
	endpoints := &gatewayv1alpha1.TailscaleEndpoints{}
	if err := r.Get(ctx, req.NamespacedName, endpoints); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("TailscaleEndpoints resource not found, ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get TailscaleEndpoints")
		return ctrl.Result{}, err
	}

	// Handle deletion
	if endpoints.DeletionTimestamp != nil {
		return r.handleDeletion(ctx, endpoints)
	}

	// Add finalizer if not present
	if !controllerutil.ContainsFinalizer(endpoints, TailscaleEndpointsFinalizer) {
		controllerutil.AddFinalizer(endpoints, TailscaleEndpointsFinalizer)
		if err := r.Update(ctx, endpoints); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Reconcile the endpoints
	result, err := r.reconcileEndpoints(ctx, endpoints)
	if err != nil {
		logger.Error(err, "Failed to reconcile TailscaleEndpoints")
		r.updateCondition(endpoints, "Synced", metav1.ConditionFalse, "SyncError", err.Error())
		if statusErr := r.Status().Update(ctx, endpoints); statusErr != nil {
			logger.Error(statusErr, "Failed to update status")
		}
		return result, err
	}

	// Update status
	if err := r.Status().Update(ctx, endpoints); err != nil {
		logger.Error(err, "Failed to update TailscaleEndpoints status")
		return ctrl.Result{}, err
	}

	return result, nil
}

// reconcileEndpoints handles the main reconciliation logic
func (r *TailscaleEndpointsReconciler) reconcileEndpoints(ctx context.Context, endpoints *gatewayv1alpha1.TailscaleEndpoints) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Get sync interval
	syncInterval := defaultSyncInterval
	if endpoints.Spec.AutoDiscovery != nil && endpoints.Spec.AutoDiscovery.SyncInterval != nil {
		syncInterval = endpoints.Spec.AutoDiscovery.SyncInterval.Duration
	}

	// Perform service discovery if auto-discovery is enabled
	if endpoints.Spec.AutoDiscovery != nil && endpoints.Spec.AutoDiscovery.Enabled {
		if err := r.performServiceDiscovery(ctx, endpoints); err != nil {
			r.updateCondition(endpoints, "ServiceDiscovery", metav1.ConditionFalse, "DiscoveryFailed", err.Error())
			return ctrl.Result{RequeueAfter: syncInterval}, err
		}
		r.updateCondition(endpoints, "ServiceDiscovery", metav1.ConditionTrue, "DiscoverySuccessful", "Service discovery completed")
	}

	// Perform health checks on all endpoints
	if err := r.performHealthChecks(ctx, endpoints); err != nil {
		logger.Error(err, "Failed to perform health checks")
		// Don't fail reconciliation on health check errors, just log them
	}

	// Update overall sync condition
	endpoints.Status.LastSync = &metav1.Time{Time: time.Now()}
	r.updateCondition(endpoints, "Synced", metav1.ConditionTrue, "SyncSuccessful", "Endpoints synchronized successfully")

	logger.Info("Successfully reconciled TailscaleEndpoints", "endpoints", endpoints.Name, "totalEndpoints", endpoints.Status.TotalEndpoints)
	return ctrl.Result{RequeueAfter: syncInterval}, nil
}

// performServiceDiscovery discovers services from the Tailscale API
func (r *TailscaleEndpointsReconciler) performServiceDiscovery(ctx context.Context, endpoints *gatewayv1alpha1.TailscaleEndpoints) error {
	logger := log.FromContext(ctx)

	// Get Tailscale client for this tailnet
	tsClient, err := r.TailscaleClientManager.GetClient(ctx, r.Client, endpoints.Spec.Tailnet, endpoints.Namespace)
	if err != nil {
		return fmt.Errorf("failed to get Tailscale client: %w", err)
	}

	// Discover services from Tailscale API
	discoveredEndpoints, err := r.discoverEndpointsFromTailscale(ctx, tsClient, endpoints)
	if err != nil {
		return fmt.Errorf("failed to discover endpoints: %w", err)
	}

	// Merge discovered endpoints with manually configured ones
	allEndpoints := r.mergeEndpoints(endpoints.Spec.Endpoints, discoveredEndpoints)

	// Update the endpoints spec with discovered services
	endpoints.Spec.Endpoints = allEndpoints

	// Update status
	endpoints.Status.DiscoveredEndpoints = len(discoveredEndpoints)
	endpoints.Status.TotalEndpoints = len(allEndpoints)
	endpoints.Status.EndpointStatus = r.buildEndpointStatus(allEndpoints, discoveredEndpoints)

	logger.Info("Discovered endpoints", "tailnet", endpoints.Spec.Tailnet, "discovered", len(discoveredEndpoints), "total", len(allEndpoints))
	return nil
}

// discoverEndpointsFromTailscale discovers endpoints from the Tailscale API
func (r *TailscaleEndpointsReconciler) discoverEndpointsFromTailscale(ctx context.Context, tsClient tailscale.Client, endpoints *gatewayv1alpha1.TailscaleEndpoints) ([]gatewayv1alpha1.TailscaleEndpoint, error) {
	logger := log.FromContext(ctx)
	var discoveredEndpoints []gatewayv1alpha1.TailscaleEndpoint

	// Get all devices from the Tailscale API
	devices, err := tsClient.Devices(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list devices: %w", err)
	}

	logger.Info("Retrieved devices from Tailscale API", "count", len(devices), "tailnet", endpoints.Spec.Tailnet)

	for _, device := range devices {
		// Skip if device is not authorized
		if !device.Authorized {
			continue
		}

		// Apply include/exclude patterns if configured
		if endpoints.Spec.AutoDiscovery != nil {
			if !r.deviceMatchesPatterns(&device, endpoints.Spec.AutoDiscovery) {
				continue
			}
		}

		// Extract service information from device
		endpoint := r.deviceToEndpoint(&device, endpoints.Spec.Tailnet)
		if endpoint != nil {
			discoveredEndpoints = append(discoveredEndpoints, *endpoint)
		}
	}

	logger.Info("Discovered endpoints", "count", len(discoveredEndpoints), "tailnet", endpoints.Spec.Tailnet)
	return discoveredEndpoints, nil
}

// deviceMatchesPatterns checks if a device matches the discovery patterns
func (r *TailscaleEndpointsReconciler) deviceMatchesPatterns(device *tailscaleclient.Device, config *gatewayv1alpha1.EndpointAutoDiscovery) bool {
	deviceName := device.Name

	// Check include patterns
	if len(config.IncludePatterns) > 0 {
		if !r.matchesPattern(deviceName, config.IncludePatterns) {
			return false
		}
	}

	// Check exclude patterns
	if len(config.ExcludePatterns) > 0 {
		if r.matchesPattern(deviceName, config.ExcludePatterns) {
			return false
		}
	}

	// Check required tags - following Tailscale API patterns
	if len(config.RequiredTags) > 0 {
		if !r.deviceHasRequiredTags(device, config.RequiredTags) {
			return false
		}
	}

	return true
}

// deviceHasRequiredTags checks if a device has all required tags
// Implements client-side tag filtering pattern per Tailscale API design
func (r *TailscaleEndpointsReconciler) deviceHasRequiredTags(device *tailscaleclient.Device, requiredTags []string) bool {
	deviceTags := make(map[string]bool)
	for _, tag := range device.Tags {
		deviceTags[tag] = true
	}

	for _, requiredTag := range requiredTags {
		if !deviceTags[requiredTag] {
			return false
		}
	}

	return true
}

// deviceToEndpoint converts a Tailscale device to a TailscaleEndpoint
// Follows service discovery patterns with tag-based configuration
func (r *TailscaleEndpointsReconciler) deviceToEndpoint(device *tailscaleclient.Device, tailnetName string) *gatewayv1alpha1.TailscaleEndpoint {
	// Extract hostname (remove domain suffix if present)
	name := device.Name
	if idx := strings.Index(name, "."); idx != -1 {
		name = name[:idx]
	}

	// Determine protocol based on device tags or default to HTTP
	protocol := "HTTP"
	if r.deviceHasTag(device, "tag:https") {
		protocol = "HTTPS"
	}

	// Determine port based on tags or protocol defaults
	port := int32(80)
	if protocol == "HTTPS" {
		port = 443
	}
	
	// Check for custom port tags (e.g., "tag:port:8080")
	for _, tag := range device.Tags {
		if strings.HasPrefix(tag, "tag:port:") {
			if portStr := strings.TrimPrefix(tag, "tag:port:"); portStr != "" {
				// Parse port number (simplified - would need proper validation in production)
				// For now, keep default port
			}
		}
	}

	// Only include devices with valid addresses
	if len(device.Addresses) == 0 {
		return nil
	}

	return &gatewayv1alpha1.TailscaleEndpoint{
		Name:          name,
		TailscaleIP:   device.Addresses[0], // Primary Tailscale IP
		TailscaleFQDN: device.Name,
		Port:          port,
		Protocol:      protocol,
		Tags:          device.Tags,
		HealthCheck: &gatewayv1alpha1.EndpointHealthCheck{
			Enabled: true,
		},
		Weight: &[]int32{1}[0], // Default weight of 1
	}
}

// deviceHasTag checks if a device has a specific tag
func (r *TailscaleEndpointsReconciler) deviceHasTag(device *tailscaleclient.Device, tag string) bool {
	for _, deviceTag := range device.Tags {
		if deviceTag == tag {
			return true
		}
	}
	return false
}

// mergeEndpoints merges manually configured endpoints with discovered ones
func (r *TailscaleEndpointsReconciler) mergeEndpoints(manual, discovered []gatewayv1alpha1.TailscaleEndpoint) []gatewayv1alpha1.TailscaleEndpoint {
	endpointMap := make(map[string]gatewayv1alpha1.TailscaleEndpoint)

	// Add manual endpoints first (they take priority)
	for _, endpoint := range manual {
		endpointMap[endpoint.Name] = endpoint
	}

	// Add discovered endpoints that don't conflict with manual ones
	for _, endpoint := range discovered {
		if _, exists := endpointMap[endpoint.Name]; !exists {
			endpointMap[endpoint.Name] = endpoint
		}
	}

	// Convert map back to slice
	var result []gatewayv1alpha1.TailscaleEndpoint
	for _, endpoint := range endpointMap {
		result = append(result, endpoint)
	}

	return result
}

// buildEndpointStatus creates status information for each endpoint
func (r *TailscaleEndpointsReconciler) buildEndpointStatus(allEndpoints, discoveredEndpoints []gatewayv1alpha1.TailscaleEndpoint) []gatewayv1alpha1.EndpointStatus {
	var status []gatewayv1alpha1.EndpointStatus

	// Create a set of discovered endpoint names for quick lookup
	discoveredNames := make(map[string]bool)
	for _, endpoint := range discoveredEndpoints {
		discoveredNames[endpoint.Name] = true
	}

	for _, endpoint := range allEndpoints {
		source := "Manual"
		if discoveredNames[endpoint.Name] {
			source = "AutoDiscovery"
		}

		status = append(status, gatewayv1alpha1.EndpointStatus{
			Name:            endpoint.Name,
			TailscaleIP:     endpoint.TailscaleIP,
			HealthStatus:    "Unknown", // Will be updated by health checks
			DiscoverySource: source,
			Tags:            endpoint.Tags,
		})
	}

	return status
}

// performHealthChecks performs health checks on all endpoints
func (r *TailscaleEndpointsReconciler) performHealthChecks(ctx context.Context, endpoints *gatewayv1alpha1.TailscaleEndpoints) error {
	healthyCount := 0

	for i, endpointStatus := range endpoints.Status.EndpointStatus {
		// Find the corresponding endpoint configuration
		var endpointConfig *gatewayv1alpha1.TailscaleEndpoint
		for _, ep := range endpoints.Spec.Endpoints {
			if ep.Name == endpointStatus.Name {
				endpointConfig = &ep
				break
			}
		}

		if endpointConfig == nil {
			continue
		}

		// Perform health check if enabled
		if endpointConfig.HealthCheck != nil && endpointConfig.HealthCheck.Enabled {
			healthy := r.performHealthCheck(ctx, endpointConfig)
			if healthy {
				endpoints.Status.EndpointStatus[i].HealthStatus = "Healthy"
				healthyCount++
			} else {
				endpoints.Status.EndpointStatus[i].HealthStatus = "Unhealthy"
			}
			endpoints.Status.EndpointStatus[i].LastHealthCheck = &metav1.Time{Time: time.Now()}
		} else {
			endpoints.Status.EndpointStatus[i].HealthStatus = "Unknown"
			healthyCount++ // Assume healthy if not checked
		}
	}

	endpoints.Status.HealthyEndpoints = healthyCount
	return nil
}

// performHealthCheck performs a health check on a single endpoint
func (r *TailscaleEndpointsReconciler) performHealthCheck(ctx context.Context, endpoint *gatewayv1alpha1.TailscaleEndpoint) bool {
	logger := log.FromContext(ctx)
	
	switch endpoint.Protocol {
	case "HTTP", "HTTPS":
		return r.performHTTPHealthCheck(ctx, endpoint)
	case "TCP":
		return r.performTCPHealthCheck(ctx, endpoint)
	case "UDP":
		// UDP health checks are more complex and would require application-specific logic
		logger.Info("UDP health checks not implemented, assuming healthy", "endpoint", endpoint.Name)
		return true
	default:
		logger.Info("Unknown protocol for health check, assuming healthy", "endpoint", endpoint.Name, "protocol", endpoint.Protocol)
		return true
	}
}

// performHTTPHealthCheck performs HTTP/HTTPS health check
func (r *TailscaleEndpointsReconciler) performHTTPHealthCheck(ctx context.Context, endpoint *gatewayv1alpha1.TailscaleEndpoint) bool {
	// TODO: Implement actual HTTP health check
	// This would:
	// 1. Create HTTP client with timeout
	// 2. Make request to health check path
	// 3. Check response status code
	// 4. Handle retries and circuit breaking
	
	// For now, assume healthy
	return true
}

// performTCPHealthCheck performs TCP connection health check
func (r *TailscaleEndpointsReconciler) performTCPHealthCheck(ctx context.Context, endpoint *gatewayv1alpha1.TailscaleEndpoint) bool {
	// TODO: Implement actual TCP health check
	// This would:
	// 1. Attempt to establish TCP connection to endpoint IP:port
	// 2. Close connection immediately if successful
	// 3. Handle timeouts and connection errors
	
	// For now, assume healthy
	return true
}

// handleDeletion handles cleanup when TailscaleEndpoints is being deleted
func (r *TailscaleEndpointsReconciler) handleDeletion(ctx context.Context, endpoints *gatewayv1alpha1.TailscaleEndpoints) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	if controllerutil.ContainsFinalizer(endpoints, TailscaleEndpointsFinalizer) {
		// Perform cleanup
		logger.Info("Cleaning up TailscaleEndpoints resources", "endpoints", endpoints.Name)

		// Remove finalizer
		controllerutil.RemoveFinalizer(endpoints, TailscaleEndpointsFinalizer)
		if err := r.Update(ctx, endpoints); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

// updateCondition updates or adds a condition to the TailscaleEndpoints status
func (r *TailscaleEndpointsReconciler) updateCondition(endpoints *gatewayv1alpha1.TailscaleEndpoints, conditionType string, status metav1.ConditionStatus, reason, message string) {
	condition := metav1.Condition{
		Type:               conditionType,
		Status:             status,
		LastTransitionTime: metav1.Now(),
		Reason:             reason,
		Message:            message,
	}

	// Find and update existing condition or append new one
	for i, existingCondition := range endpoints.Status.Conditions {
		if existingCondition.Type == conditionType {
			endpoints.Status.Conditions[i] = condition
			return
		}
	}
	endpoints.Status.Conditions = append(endpoints.Status.Conditions, condition)
}

// matchesPattern checks if a name matches any of the given glob patterns
func (r *TailscaleEndpointsReconciler) matchesPattern(name string, patterns []string) bool {
	for _, pattern := range patterns {
		if r.globMatch(pattern, name) {
			return true
		}
	}
	return false
}

// globMatch performs simple glob pattern matching
func (r *TailscaleEndpointsReconciler) globMatch(pattern, name string) bool {
	// Simple implementation - just check for wildcard at the end
	if strings.HasSuffix(pattern, "*") {
		prefix := strings.TrimSuffix(pattern, "*")
		return strings.HasPrefix(name, prefix)
	}
	return pattern == name
}

// SetupWithManager sets up the controller with the Manager.
func (r *TailscaleEndpointsReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&gatewayv1alpha1.TailscaleEndpoints{}).
		Complete(r)
}