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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log"
	tailscaleclient "tailscale.com/client/tailscale/v2"

	gatewayv1alpha1 "github.com/rajsinghtech/tailscale-gateway/api/v1alpha1"
	"github.com/rajsinghtech/tailscale-gateway/internal/metrics"
	"github.com/rajsinghtech/tailscale-gateway/internal/tailscale"
	"github.com/rajsinghtech/tailscale-gateway/internal/validation"
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
	TailscaleClientManager *tailscale.MultiTailnetManager

	// Metrics for tracking reconciliation performance
	metrics *metrics.ReconcilerMetrics

	// Event channels for controller coordination
	GatewayEventChan   chan event.GenericEvent
	HTTPRouteEventChan chan event.GenericEvent
	TCPRouteEventChan  chan event.GenericEvent
	UDPRouteEventChan  chan event.GenericEvent
	TLSRouteEventChan  chan event.GenericEvent
	GRPCRouteEventChan chan event.GenericEvent
}

// MultiTailnetManager manages multiple Tailscale client connections with cleanup and refresh logic
type MultiTailnetManager struct {
	clients       map[string]*clientCacheEntry
	mu            sync.RWMutex
	cleanupTicker *time.Ticker
	stopCleanup   chan struct{}
}

// clientCacheEntry represents a cached Tailscale client with metadata
type clientCacheEntry struct {
	client     tailscale.Client
	createdAt  time.Time
	lastUsed   time.Time
	secretHash string // Hash of the secret used to create the client
	tailnetKey string // Key for the TailscaleTailnet resource
}

// NewMultiTailnetManager creates a new MultiTailnetManager with cleanup logic
func NewMultiTailnetManager() *MultiTailnetManager {
	m := &MultiTailnetManager{
		clients:     make(map[string]*clientCacheEntry),
		stopCleanup: make(chan struct{}),
	}

	// Start cleanup goroutine
	m.cleanupTicker = time.NewTicker(5 * time.Minute) // Cleanup every 5 minutes
	go m.cleanupLoop()

	return m
}

// GetClient returns a Tailscale client for the specified tailnet with automatic refresh logic
func (m *MultiTailnetManager) GetClient(ctx context.Context, k8sClient client.Client, tailnetName, namespace string) (tailscale.Client, error) {
	key := fmt.Sprintf("%s/%s", namespace, tailnetName)

	// Check if we have a cached client
	m.mu.RLock()
	entry, exists := m.clients[key]
	m.mu.RUnlock()

	if exists {
		// Update last used time
		m.mu.Lock()
		entry.lastUsed = time.Now()
		m.mu.Unlock()

		// Check if the client needs to be refreshed
		if m.shouldRefreshClient(ctx, k8sClient, entry, tailnetName, namespace) {
			// Remove the stale client and create a new one
			m.mu.Lock()
			delete(m.clients, key)
			m.mu.Unlock()
		} else {
			return entry.client, nil
		}
	}

	// Create new client
	return m.createAndCacheClient(ctx, k8sClient, tailnetName, namespace, key)
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

// createAndCacheClient creates a new Tailscale client and caches it
func (m *MultiTailnetManager) createAndCacheClient(ctx context.Context, k8sClient client.Client, tailnetName, namespace, key string) (tailscale.Client, error) {
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

	// Calculate secret hash for refresh detection
	secretHash, err := m.calculateSecretHash(ctx, k8sClient, tailnet)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate secret hash: %w", err)
	}

	// Cache the client
	entry := &clientCacheEntry{
		client:     tsClient,
		createdAt:  time.Now(),
		lastUsed:   time.Now(),
		secretHash: secretHash,
		tailnetKey: fmt.Sprintf("%s/%s", tailnet.Namespace, tailnet.Name),
	}

	m.mu.Lock()
	m.clients[key] = entry
	m.mu.Unlock()

	return tsClient, nil
}

// shouldRefreshClient determines if a cached client should be refreshed
func (m *MultiTailnetManager) shouldRefreshClient(ctx context.Context, k8sClient client.Client, entry *clientCacheEntry, tailnetName, namespace string) bool {
	// Check if client is too old (refresh after 1 hour)
	if time.Since(entry.createdAt) > time.Hour {
		return true
	}

	// Check if the underlying secret has changed
	tailnet := &gatewayv1alpha1.TailscaleTailnet{}
	if err := k8sClient.Get(ctx, client.ObjectKey{Name: tailnetName, Namespace: namespace}, tailnet); err != nil {
		// If we can't get the tailnet, refresh the client
		return true
	}

	currentSecretHash, err := m.calculateSecretHash(ctx, k8sClient, tailnet)
	if err != nil {
		// If we can't calculate hash, refresh the client
		return true
	}

	// Refresh if secret hash changed
	return currentSecretHash != entry.secretHash
}

// calculateSecretHash calculates a hash of the OAuth secret for change detection
func (m *MultiTailnetManager) calculateSecretHash(ctx context.Context, k8sClient client.Client, tailnet *gatewayv1alpha1.TailscaleTailnet) (string, error) {
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
		return "", fmt.Errorf("failed to get OAuth secret: %w", err)
	}

	// Create hash of the secret data
	hasher := sha256.New()
	hasher.Write(secret.Data["client_id"])
	hasher.Write(secret.Data["client_secret"])

	return hex.EncodeToString(hasher.Sum(nil)), nil
}

// cleanupLoop runs periodic cleanup of stale clients
func (m *MultiTailnetManager) cleanupLoop() {
	for {
		select {
		case <-m.cleanupTicker.C:
			m.cleanupStaleClients()
		case <-m.stopCleanup:
			return
		}
	}
}

// cleanupStaleClients removes unused and old clients from the cache
func (m *MultiTailnetManager) cleanupStaleClients() {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	for key, entry := range m.clients {
		// Remove clients that haven't been used in the last 30 minutes
		// OR are older than 2 hours
		if now.Sub(entry.lastUsed) > 30*time.Minute || now.Sub(entry.createdAt) > 2*time.Hour {
			delete(m.clients, key)
		}
	}
}

// InvalidateClient removes a specific client from the cache (useful for error handling)
func (m *MultiTailnetManager) InvalidateClient(tailnetName, namespace string) {
	key := fmt.Sprintf("%s/%s", namespace, tailnetName)
	m.mu.Lock()
	delete(m.clients, key)
	m.mu.Unlock()
}

// Stop gracefully shuts down the MultiTailnetManager
func (m *MultiTailnetManager) Stop() {
	if m.cleanupTicker != nil {
		m.cleanupTicker.Stop()
	}
	close(m.stopCleanup)

	m.mu.Lock()
	m.clients = make(map[string]*clientCacheEntry)
	m.mu.Unlock()
}

// GetCacheStats returns statistics about the client cache (useful for monitoring)
func (m *MultiTailnetManager) GetCacheStats() map[string]interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()

	stats := map[string]interface{}{
		"total_clients": len(m.clients),
		"clients":       make([]map[string]interface{}, 0, len(m.clients)),
	}

	for key, entry := range m.clients {
		clientStats := map[string]interface{}{
			"key":        key,
			"created_at": entry.createdAt,
			"last_used":  entry.lastUsed,
			"age":        time.Since(entry.createdAt).String(),
			"idle_time":  time.Since(entry.lastUsed).String(),
		}
		stats["clients"] = append(stats["clients"].([]map[string]interface{}), clientStats)
	}

	return stats
}

// ValidateClient performs a lightweight validation of a Tailscale client
func (m *MultiTailnetManager) ValidateClient(ctx context.Context, tsClient tailscale.Client) error {
	// Try a simple API call to validate the client is working
	_, err := tsClient.Devices(ctx)
	return err
}

// GetClientWithValidation returns a validated Tailscale client, refreshing if validation fails
func (m *MultiTailnetManager) GetClientWithValidation(ctx context.Context, k8sClient client.Client, tailnetName, namespace string) (tailscale.Client, error) {
	tsClient, err := m.GetClient(ctx, k8sClient, tailnetName, namespace)
	if err != nil {
		return nil, err
	}

	// Validate the client with a lightweight API call
	if err := m.ValidateClient(ctx, tsClient); err != nil {
		// Client is invalid, remove it and try again
		m.InvalidateClient(tailnetName, namespace)

		// Try once more with a fresh client
		tsClient, err = m.GetClient(ctx, k8sClient, tailnetName, namespace)
		if err != nil {
			return nil, fmt.Errorf("failed to get client after invalidation: %w", err)
		}

		// Validate the fresh client
		if err := m.ValidateClient(ctx, tsClient); err != nil {
			return nil, fmt.Errorf("fresh client also failed validation: %w", err)
		}
	}

	return tsClient, nil
}

// Event notification helper functions for controller coordination

// notifyGatewayController sends an event to the Gateway controller
func (r *TailscaleEndpointsReconciler) notifyGatewayController(ctx context.Context, obj client.Object) {
	if r.GatewayEventChan == nil {
		return
	}

	logger := log.FromContext(ctx)
	select {
	case r.GatewayEventChan <- event.GenericEvent{Object: obj}:
		logger.V(1).Info("Notified gateway controller", "object", obj.GetName(), "namespace", obj.GetNamespace())
	case <-ctx.Done():
		return
	default:
		logger.V(1).Info("Gateway event channel full, dropping notification", "object", obj.GetName())
	}
}

// notifyHTTPRouteController sends an event to trigger HTTPRoute reconciliation
func (r *TailscaleEndpointsReconciler) notifyHTTPRouteController(ctx context.Context, obj client.Object) {
	if r.HTTPRouteEventChan == nil {
		return
	}

	logger := log.FromContext(ctx)
	select {
	case r.HTTPRouteEventChan <- event.GenericEvent{Object: obj}:
		logger.V(1).Info("Notified HTTPRoute controller", "object", obj.GetName(), "namespace", obj.GetNamespace())
	case <-ctx.Done():
		return
	default:
		logger.V(1).Info("HTTPRoute event channel full, dropping notification", "object", obj.GetName())
	}
}

// notifyRouteControllers sends events to all route controllers
func (r *TailscaleEndpointsReconciler) notifyRouteControllers(ctx context.Context, obj client.Object) {
	// Notify all route controllers about endpoint changes
	r.notifyHTTPRouteController(ctx, obj)

	if r.TCPRouteEventChan != nil {
		select {
		case r.TCPRouteEventChan <- event.GenericEvent{Object: obj}:
		case <-ctx.Done():
			return
		default:
		}
	}

	if r.UDPRouteEventChan != nil {
		select {
		case r.UDPRouteEventChan <- event.GenericEvent{Object: obj}:
		case <-ctx.Done():
			return
		default:
		}
	}

	if r.TLSRouteEventChan != nil {
		select {
		case r.TLSRouteEventChan <- event.GenericEvent{Object: obj}:
		case <-ctx.Done():
			return
		default:
		}
	}

	if r.GRPCRouteEventChan != nil {
		select {
		case r.GRPCRouteEventChan <- event.GenericEvent{Object: obj}:
		case <-ctx.Done():
			return
		default:
		}
	}
}

// notifyAllControllers sends events to all dependent controllers
func (r *TailscaleEndpointsReconciler) notifyAllControllers(ctx context.Context, obj client.Object) {
	r.notifyGatewayController(ctx, obj)
	r.notifyRouteControllers(ctx, obj)
}

//+kubebuilder:rbac:groups=gateway.tailscale.com,resources=tailscaleendpoints,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=gateway.tailscale.com,resources=tailscaleendpoints/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=gateway.tailscale.com,resources=tailscaleendpoints/finalizers,verbs=update
//+kubebuilder:rbac:groups=gateway.tailscale.com,resources=tailscaletailnets,verbs=get;list;watch
//+kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=roles,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=rolebindings,verbs=get;list;watch;create;update;patch;delete

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *TailscaleEndpointsReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	reconcileStart := time.Now()

	// Initialize metrics if not present
	if r.metrics == nil {
		r.metrics = &metrics.ReconcilerMetrics{}
	}

	// Increment reconcile count
	r.metrics.ReconcileCount++

	// Fetch the TailscaleEndpoints instance
	endpoints := &gatewayv1alpha1.TailscaleEndpoints{}
	if err := r.Get(ctx, req.NamespacedName, endpoints); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("TailscaleEndpoints resource not found, ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get TailscaleEndpoints")
		r.recordError(endpoints, gatewayv1alpha1.ErrorCodeResourceNotFound, "Failed to get TailscaleEndpoints", gatewayv1alpha1.ComponentController, err)
		return ctrl.Result{}, err
	}

	// Handle deletion
	if endpoints.DeletionTimestamp != nil {
		return r.handleDeletion(ctx, endpoints)
	}

	// Validate the TailscaleEndpoints resource
	if err := validation.ValidateTailscaleEndpoints(endpoints); err != nil {
		logger.Error(err, "TailscaleEndpoints validation failed")
		r.recordError(endpoints, gatewayv1alpha1.ErrorCodeValidation, "TailscaleEndpoints validation failed", gatewayv1alpha1.ComponentController, err)
		gatewayv1alpha1.SetCondition(&endpoints.Status.Conditions, gatewayv1alpha1.ConditionReady, metav1.ConditionFalse, gatewayv1alpha1.ReasonValidationFailed, err.Error())
		if statusErr := r.Status().Update(ctx, endpoints); statusErr != nil {
			logger.Error(statusErr, "Failed to update status")
		}
		return ctrl.Result{RequeueAfter: time.Minute}, err
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
	reconcileDuration := time.Since(reconcileStart)

	// Update operational metrics
	r.updateOperationalMetrics(endpoints, reconcileDuration, err == nil)

	if err != nil {
		logger.Error(err, "Failed to reconcile TailscaleEndpoints")
		r.recordError(endpoints, gatewayv1alpha1.ErrorCodeConfiguration, "Failed to reconcile TailscaleEndpoints", gatewayv1alpha1.ComponentController, err)
		gatewayv1alpha1.SetCondition(&endpoints.Status.Conditions, gatewayv1alpha1.ConditionReady, metav1.ConditionFalse, gatewayv1alpha1.ReasonFailed, err.Error())
		if statusErr := r.Status().Update(ctx, endpoints); statusErr != nil {
			logger.Error(statusErr, "Failed to update status")
		}
		return result, err
	}

	// Set ready condition
	gatewayv1alpha1.SetCondition(&endpoints.Status.Conditions, gatewayv1alpha1.ConditionReady, metav1.ConditionTrue, gatewayv1alpha1.ReasonReady, "TailscaleEndpoints reconciled successfully")

	// Update status
	if err := r.Status().Update(ctx, endpoints); err != nil {
		logger.Error(err, "Failed to update TailscaleEndpoints status")
		r.recordError(endpoints, gatewayv1alpha1.ErrorCodeConfiguration, "Failed to update status", gatewayv1alpha1.ComponentController, err)
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

	// Initialize auto-discovery status
	if endpoints.Status.AutoDiscoveryStatus == nil {
		endpoints.Status.AutoDiscoveryStatus = &gatewayv1alpha1.AutoDiscoveryStatus{}
	}

	// Perform service discovery if auto-discovery is enabled
	if endpoints.Spec.AutoDiscovery != nil && endpoints.Spec.AutoDiscovery.Enabled {
		endpoints.Status.AutoDiscoveryStatus.Enabled = true
		discoveryStart := time.Now()

		if err := r.performServiceDiscovery(ctx, endpoints); err != nil {
			r.recordError(endpoints, gatewayv1alpha1.ErrorCodeServiceDiscovery, "Service discovery failed", gatewayv1alpha1.ComponentServiceDiscovery, err)
			gatewayv1alpha1.SetCondition(&endpoints.Status.Conditions, "ServiceDiscovery", metav1.ConditionFalse, "DiscoveryFailed", err.Error())

			// Update discovery status with error
			detailedErr := gatewayv1alpha1.DetailedError{
				Code:      gatewayv1alpha1.ErrorCodeServiceDiscovery,
				Message:   err.Error(),
				Component: gatewayv1alpha1.ComponentServiceDiscovery,
				Timestamp: metav1.Now(),
				Severity:  gatewayv1alpha1.SeverityHigh,
			}
			endpoints.Status.AutoDiscoveryStatus.DiscoveryErrors = append(endpoints.Status.AutoDiscoveryStatus.DiscoveryErrors, detailedErr)

			return ctrl.Result{RequeueAfter: syncInterval}, err
		}

		// Update successful discovery status
		discoveryDuration := time.Since(discoveryStart)
		endpoints.Status.AutoDiscoveryStatus.LastDiscoveryTime = &metav1.Time{Time: discoveryStart}
		endpoints.Status.AutoDiscoveryStatus.DiscoveryDuration = &metav1.Duration{Duration: discoveryDuration}
		gatewayv1alpha1.SetCondition(&endpoints.Status.Conditions, "ServiceDiscovery", metav1.ConditionTrue, "DiscoverySuccessful", "Service discovery completed")

		// Step: Perform local service discovery for bidirectional exposure
		if err := r.performLocalServiceDiscovery(ctx, endpoints); err != nil {
			logger.Error(err, "Failed to perform local service discovery")
			// Don't fail the entire reconciliation for local service discovery errors
		}
	} else {
		// Set status for manual endpoints configuration
		endpoints.Status.AutoDiscoveryStatus.Enabled = false
		endpoints.Status.DiscoveredEndpoints = 0
		endpoints.Status.TotalEndpoints = len(endpoints.Spec.Endpoints)
		endpoints.Status.EndpointStatus = r.buildEndpointStatus(endpoints.Spec.Endpoints, []gatewayv1alpha1.TailscaleEndpoint{})
	}

	// Create/update StatefulSets for each endpoint
	if err := r.reconcileStatefulSets(ctx, endpoints); err != nil {
		r.recordError(endpoints, gatewayv1alpha1.ErrorCodeConfiguration, "StatefulSet creation failed", gatewayv1alpha1.ComponentStatefulSet, err)
		gatewayv1alpha1.SetCondition(&endpoints.Status.Conditions, "StatefulSetsReady", metav1.ConditionFalse, "StatefulSetFailed", err.Error())
		return ctrl.Result{RequeueAfter: time.Minute}, err
	}
	gatewayv1alpha1.SetCondition(&endpoints.Status.Conditions, "StatefulSetsReady", metav1.ConditionTrue, "StatefulSetsCreated", "All StatefulSets created successfully")

	// Notify controllers about StatefulSet changes (routes may need to be updated)
	r.notifyGatewayController(ctx, endpoints)

	// Update device status after StatefulSets are reconciled
	if err := r.updateDeviceStatus(ctx, endpoints); err != nil {
		logger.Error(err, "Failed to update device status")
		// Don't fail reconciliation on device status errors, just log them
	}

	// Perform health checks on all endpoints
	if err := r.performHealthChecks(ctx, endpoints); err != nil {
		logger.Error(err, "Failed to perform health checks")
		// Don't fail reconciliation on health check errors, just log them
	}

	// Update overall sync condition
	endpoints.Status.LastSync = &metav1.Time{Time: time.Now()}
	gatewayv1alpha1.SetCondition(&endpoints.Status.Conditions, "Synced", metav1.ConditionTrue, "SyncSuccessful", "Endpoints synchronized successfully")

	logger.Info("Successfully reconciled TailscaleEndpoints", "endpoints", endpoints.Name, "totalEndpoints", endpoints.Status.TotalEndpoints)
	return ctrl.Result{RequeueAfter: syncInterval}, nil
}

// performServiceDiscovery discovers services from the Tailscale API
func (r *TailscaleEndpointsReconciler) performServiceDiscovery(ctx context.Context, endpoints *gatewayv1alpha1.TailscaleEndpoints) error {
	logger := log.FromContext(ctx)
	discoveryStart := time.Now()

	// Skip service discovery if TailscaleClientManager is not available (e.g., in tests)
	if r.TailscaleClientManager == nil {
		logger.Info("TailscaleClientManager not available, skipping service discovery")
		endpoints.Status.DiscoveredEndpoints = 0
		endpoints.Status.TotalEndpoints = len(endpoints.Spec.Endpoints)
		endpoints.Status.EndpointStatus = r.buildEndpointStatus(endpoints.Spec.Endpoints, []gatewayv1alpha1.TailscaleEndpoint{})
		return nil
	}

	// Get Tailscale client for this tailnet
	tsClient, err := r.TailscaleClientManager.GetClient(ctx, r.Client, endpoints.Spec.Tailnet, endpoints.Namespace)
	if err != nil {
		r.recordError(endpoints, gatewayv1alpha1.ErrorCodeAuthentication, "Failed to get Tailscale client", gatewayv1alpha1.ComponentTailscaleAPI, err)
		return fmt.Errorf("failed to get Tailscale client: %w", err)
	}

	// Track API call metrics
	if r.metrics != nil {
		r.metrics.APICallCount++
	}

	// Discover services from Tailscale API
	discoveredEndpoints, err := r.discoverEndpointsFromTailscale(ctx, tsClient, endpoints)
	if err != nil {
		// If the error appears to be authentication related, invalidate the client
		if strings.Contains(err.Error(), "403") || strings.Contains(err.Error(), "401") || strings.Contains(err.Error(), "oauth") {
			r.TailscaleClientManager.InvalidateClient(endpoints.Spec.Tailnet, endpoints.Namespace)
		}
		r.recordError(endpoints, gatewayv1alpha1.ErrorCodeServiceDiscovery, "Failed to discover endpoints", gatewayv1alpha1.ComponentServiceDiscovery, err)

		// Update API status with error
		r.updateAPIStatus(endpoints, false, err)
		return fmt.Errorf("failed to discover endpoints: %w", err)
	}

	// Update API status with success
	r.updateAPIStatus(endpoints, true, nil)

	// Notify other controllers about endpoint changes
	r.notifyAllControllers(ctx, endpoints)

	// Merge discovered endpoints with manually configured ones
	allEndpoints := r.mergeEndpoints(endpoints.Spec.Endpoints, discoveredEndpoints)

	// Update the endpoints spec with discovered services
	endpoints.Spec.Endpoints = allEndpoints

	// Update status
	endpoints.Status.DiscoveredEndpoints = len(discoveredEndpoints)
	endpoints.Status.TotalEndpoints = len(allEndpoints)
	endpoints.Status.UnhealthyEndpoints = 0 // Will be updated by health checks
	endpoints.Status.EndpointStatus = r.buildEndpointStatus(allEndpoints, discoveredEndpoints)

	// Update auto-discovery status
	if endpoints.Status.AutoDiscoveryStatus != nil {
		discoveryDuration := time.Since(discoveryStart)
		endpoints.Status.AutoDiscoveryStatus.LastDiscoveryTime = &metav1.Time{Time: discoveryStart}
		endpoints.Status.AutoDiscoveryStatus.DiscoveryDuration = &metav1.Duration{Duration: discoveryDuration}
		endpoints.Status.AutoDiscoveryStatus.DevicesMatchingCriteria = len(discoveredEndpoints)
	}

	logger.Info("Discovered endpoints", "tailnet", endpoints.Spec.Tailnet, "discovered", len(discoveredEndpoints), "total", len(allEndpoints))
	return nil
}

// discoverEndpointsFromTailscale discovers endpoints from the Tailscale API
func (r *TailscaleEndpointsReconciler) discoverEndpointsFromTailscale(ctx context.Context, tsClient tailscale.Client, endpoints *gatewayv1alpha1.TailscaleEndpoints) ([]gatewayv1alpha1.TailscaleEndpoint, error) {
	logger := log.FromContext(ctx)
	var discoveredEndpoints []gatewayv1alpha1.TailscaleEndpoint

	// Perform tag-based discovery if TagSelectors are configured
	if endpoints.Spec.AutoDiscovery != nil && len(endpoints.Spec.AutoDiscovery.TagSelectors) > 0 {
		tagBasedEndpoints, err := r.discoverEndpointsByTags(ctx, tsClient, endpoints)
		if err != nil {
			return nil, fmt.Errorf("failed to discover endpoints by tags: %w", err)
		}
		discoveredEndpoints = append(discoveredEndpoints, tagBasedEndpoints...)
	}

	// Perform VIP service discovery if ServiceDiscovery is enabled
	if endpoints.Spec.AutoDiscovery != nil && endpoints.Spec.AutoDiscovery.ServiceDiscovery != nil && endpoints.Spec.AutoDiscovery.ServiceDiscovery.Enabled {
		serviceBasedEndpoints, err := r.discoverEndpointsByServices(ctx, tsClient, endpoints)
		if err != nil {
			return nil, fmt.Errorf("failed to discover VIP services: %w", err)
		}
		discoveredEndpoints = append(discoveredEndpoints, serviceBasedEndpoints...)
	}

	logger.Info("Discovered endpoints", "count", len(discoveredEndpoints), "tailnet", endpoints.Spec.Tailnet)
	return discoveredEndpoints, nil
}

// discoverEndpointsByTags discovers endpoints using TagSelector rules
func (r *TailscaleEndpointsReconciler) discoverEndpointsByTags(ctx context.Context, tsClient tailscale.Client, endpoints *gatewayv1alpha1.TailscaleEndpoints) ([]gatewayv1alpha1.TailscaleEndpoint, error) {
	logger := log.FromContext(ctx)
	var discoveredEndpoints []gatewayv1alpha1.TailscaleEndpoint

	// Get all devices from the Tailscale API
	devices, err := tsClient.Devices(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list devices: %w", err)
	}

	logger.Info("Retrieved devices for tag-based discovery", "count", len(devices), "tailnet", endpoints.Spec.Tailnet)

	for _, device := range devices {
		// Skip if device is not authorized
		if !device.Authorized {
			continue
		}

		// Check if device matches any TagSelector
		if !r.deviceMatchesTagSelectors(&device, endpoints.Spec.AutoDiscovery.TagSelectors) {
			continue
		}

		// Extract service information from device
		endpoint := r.deviceToEndpoint(&device, endpoints.Spec.Tailnet)
		if endpoint != nil {
			discoveredEndpoints = append(discoveredEndpoints, *endpoint)
		}
	}

	logger.Info("Discovered endpoints by tags", "count", len(discoveredEndpoints), "tailnet", endpoints.Spec.Tailnet)
	return discoveredEndpoints, nil
}

// discoverEndpointsByServices discovers VIP services using VIPServiceDiscoveryConfig
func (r *TailscaleEndpointsReconciler) discoverEndpointsByServices(ctx context.Context, tsClient tailscale.Client, endpoints *gatewayv1alpha1.TailscaleEndpoints) ([]gatewayv1alpha1.TailscaleEndpoint, error) {
	// Validate inputs
	if endpoints == nil {
		return nil, fmt.Errorf("endpoints parameter cannot be nil")
	}
	if endpoints.Spec.AutoDiscovery == nil || endpoints.Spec.AutoDiscovery.ServiceDiscovery == nil {
		return nil, fmt.Errorf("service discovery configuration is missing")
	}
	if tsClient == nil {
		return nil, fmt.Errorf("Tailscale client cannot be nil")
	}

	logger := log.FromContext(ctx)
	var discoveredEndpoints []gatewayv1alpha1.TailscaleEndpoint

	serviceConfig := endpoints.Spec.AutoDiscovery.ServiceDiscovery

	// Get VIP services from Tailscale API
	vipServices, err := tsClient.GetVIPServices(ctx)
	if err != nil {
		logger.Error(err, "Failed to get VIP services from Tailscale API")
		return nil, fmt.Errorf("failed to get VIP services: %w", err)
	}

	// Filter services by specific service names if configured
	if len(serviceConfig.ServiceNames) > 0 {
		for _, serviceName := range serviceConfig.ServiceNames {
			// Validate service name
			if serviceName == "" {
				logger.V(1).Info("Skipping empty service name")
				continue
			}

			// Ensure service name has "svc:" prefix for matching
			svcName := serviceName
			if !strings.HasPrefix(svcName, "svc:") {
				svcName = "svc:" + serviceName
			}

			// Validate the constructed service name
			serviceName := tailscale.ServiceName(svcName)
			if err := serviceName.Validate(); err != nil {
				logger.Error(err, "Invalid service name constructed", "original", serviceName, "constructed", svcName)
				continue
			}

			// Find matching VIP service
			for _, vipService := range vipServices {
				if string(vipService.Name) == svcName {
					endpoint := r.convertVIPServiceToEndpoint(vipService, endpoints.Spec.Tailnet)
					if endpoint != nil {
						discoveredEndpoints = append(discoveredEndpoints, *endpoint)
					}
					break
				}
			}
		}
	} else {
		// If no specific service names, convert all VIP services
		for _, vipService := range vipServices {
			endpoint := r.convertVIPServiceToEndpoint(vipService, endpoints.Spec.Tailnet)
			discoveredEndpoints = append(discoveredEndpoints, *endpoint)
		}
	}

	// Handle tag-based service discovery
	if len(serviceConfig.ServiceTags) > 0 {
		taggedServices, err := tsClient.GetVIPServicesByTags(ctx, serviceConfig.ServiceTags)
		if err != nil {
			logger.Error(err, "Failed to get VIP services by tags")
			return nil, fmt.Errorf("failed to get VIP services by tags: %w", err)
		}

		// Convert tagged services to endpoints, avoiding duplicates
		existingNames := make(map[string]bool)
		for _, endpoint := range discoveredEndpoints {
			existingNames[endpoint.Name] = true
		}

		for _, vipService := range taggedServices {
			endpoint := r.convertVIPServiceToEndpoint(vipService, endpoints.Spec.Tailnet)
			if !existingNames[endpoint.Name] {
				discoveredEndpoints = append(discoveredEndpoints, *endpoint)
				existingNames[endpoint.Name] = true
			}
		}
	}

	logger.Info("Discovered VIP services", "count", len(discoveredEndpoints), "tailnet", endpoints.Spec.Tailnet)
	return discoveredEndpoints, nil
}

// convertVIPServiceToEndpoint converts a Tailscale VIP service to a TailscaleEndpoint
func (r *TailscaleEndpointsReconciler) convertVIPServiceToEndpoint(vipService *tailscale.VIPService, tailnetName string) *gatewayv1alpha1.TailscaleEndpoint {
	// Validate inputs
	if vipService == nil {
		return nil
	}
	if tailnetName == "" {
		return nil
	}

	// Validate the VIP service name
	if err := vipService.Name.Validate(); err != nil {
		return nil
	}

	// Extract service name without "svc:" prefix
	serviceName := strings.TrimPrefix(string(vipService.Name), "svc:")

	// Validate the extracted service name is not empty
	if serviceName == "" {
		return nil
	}

	// Use first address if available
	var tailscaleIP, tailscaleFQDN string
	if len(vipService.Addrs) > 0 {
		tailscaleIP = vipService.Addrs[0]
		// FQDN is the service name in the tailnet domain
		tailscaleFQDN = fmt.Sprintf("%s.%s", serviceName, tailnetName)
	}

	// Extract port from service configuration
	port := r.extractPortFromVIPService(vipService)
	protocol := r.extractProtocolFromVIPService(vipService)

	return &gatewayv1alpha1.TailscaleEndpoint{
		Name:          serviceName,
		TailscaleIP:   tailscaleIP,
		TailscaleFQDN: tailscaleFQDN,
		Port:          port,
		Protocol:      protocol,
		Tags:          vipService.Tags,
		HealthCheck: &gatewayv1alpha1.EndpointHealthCheck{
			Enabled: true,
		},
		Weight: &[]int32{1}[0],
	}
}

// extractPortFromVIPService extracts the port number from VIP service configuration
func (r *TailscaleEndpointsReconciler) extractPortFromVIPService(vipService *tailscale.VIPService) int32 {
	// Default port
	defaultPort := int32(80)

	if len(vipService.Ports) == 0 {
		return defaultPort
	}

	// Parse the first port specification
	portSpec := vipService.Ports[0]

	// Handle different port formats: "80", "80/tcp", "80:8080/tcp"
	parts := strings.Split(portSpec, "/")
	portPart := parts[0]

	// Handle port mapping format "external:internal"
	if strings.Contains(portPart, ":") {
		portParts := strings.Split(portPart, ":")
		if len(portParts) >= 2 {
			portPart = portParts[1] // Use internal port
		}
	}

	if port, err := strconv.ParseInt(portPart, 10, 32); err == nil {
		return int32(port)
	}

	return defaultPort
}

// extractProtocolFromVIPService extracts the protocol from VIP service configuration
func (r *TailscaleEndpointsReconciler) extractProtocolFromVIPService(vipService *tailscale.VIPService) string {
	// Default protocol
	defaultProtocol := "HTTP"

	if len(vipService.Ports) == 0 {
		return defaultProtocol
	}

	// Parse the first port specification
	portSpec := vipService.Ports[0]

	// Handle format like "80/tcp" or "443/https"
	if strings.Contains(portSpec, "/") {
		parts := strings.Split(portSpec, "/")
		if len(parts) >= 2 {
			protocol := strings.ToUpper(parts[1])
			switch protocol {
			case "TCP":
				return "TCP"
			case "UDP":
				return "UDP"
			case "HTTPS", "TLS":
				return "HTTPS"
			case "HTTP":
				return "HTTP"
			default:
				// For numeric ports, determine protocol based on common conventions
				if port, err := strconv.ParseInt(strings.Split(parts[0], ":")[0], 10, 32); err == nil {
					switch port {
					case 443, 8443:
						return "HTTPS"
					case 80, 8080, 8081, 3000, 8000:
						return "HTTP"
					default:
						return "TCP"
					}
				}
			}
		}
	}

	return defaultProtocol
}

// deviceMatchesTagSelectors checks if a device matches TagSelector rules
func (r *TailscaleEndpointsReconciler) deviceMatchesTagSelectors(device *tailscaleclient.Device, tagSelectors []gatewayv1alpha1.TagSelector) bool {
	// Device must match at least one TagSelector
	for _, selector := range tagSelectors {
		if r.deviceMatchesTagSelector(device, selector) {
			return true
		}
	}
	return false
}

// deviceMatchesTagSelector checks if a device matches a single TagSelector
func (r *TailscaleEndpointsReconciler) deviceMatchesTagSelector(device *tailscaleclient.Device, selector gatewayv1alpha1.TagSelector) bool {
	deviceTags := make(map[string]bool)
	for _, tag := range device.Tags {
		deviceTags[tag] = true
	}

	switch selector.Operator {
	case "In":
		// Device must have the tag with one of the specified values
		for _, value := range selector.Values {
			tag := selector.Tag + ":" + value
			if deviceTags[tag] {
				return true
			}
		}
		return false

	case "NotIn":
		// Device must not have the tag with any of the specified values
		for _, value := range selector.Values {
			tag := selector.Tag + ":" + value
			if deviceTags[tag] {
				return false
			}
		}
		return true

	case "DoesNotExist":
		// Device must not have any tag starting with the specified prefix
		for tag := range deviceTags {
			if strings.HasPrefix(tag, selector.Tag+":") || tag == selector.Tag {
				return false
			}
		}
		return true

	case "Exists":
		fallthrough
	default:
		// Device must have a tag starting with the specified prefix
		for tag := range deviceTags {
			if strings.HasPrefix(tag, selector.Tag+":") || tag == selector.Tag {
				return true
			}
		}
		return false
	}
}

// deviceToEndpoint converts a Tailscale device to a TailscaleEndpoint
// Follows service discovery patterns with tag-based configuration
func (r *TailscaleEndpointsReconciler) deviceToEndpoint(device *tailscaleclient.Device, tailnetName string) *gatewayv1alpha1.TailscaleEndpoint {
	// Extract hostname (remove domain suffix if present)
	name := device.Name
	if idx := strings.Index(name, "."); idx != -1 {
		name = name[:idx]
	}

	// Skip auto-discovering our own StatefulSet-created devices to avoid recursion
	// These will have names like "cluster1-endpoints-*" or start with "ts-"
	if strings.Contains(name, "-endpoints-") || strings.HasPrefix(name, "ts-") {
		return nil
	}

	// Clean up service names to remove redundant prefixes
	// Convert names like "cluster1-web-service" to just "web-service"
	name = r.cleanServiceName(name)

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

// cleanServiceName cleans up auto-discovered service names to remove redundant prefixes
func (r *TailscaleEndpointsReconciler) cleanServiceName(name string) string {
	// Remove common cluster prefixes
	prefixes := []string{
		"cluster1-", "cluster2-", "cluster3-",
		"prod-", "staging-", "dev-",
		"k8s-", "kube-",
	}

	for _, prefix := range prefixes {
		if strings.HasPrefix(name, prefix) {
			name = strings.TrimPrefix(name, prefix)
			break // Only remove one prefix
		}
	}

	// Simplify common service suffixes
	suffixes := []string{"-service", "-svc", "-app", "-api"}
	for _, suffix := range suffixes {
		if strings.HasSuffix(name, suffix) {
			// Keep the suffix for clarity, but could remove if desired
			break
		}
	}

	return name
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

		// Initialize health check details
		healthDetails := &gatewayv1alpha1.HealthCheckDetails{
			Enabled:       endpoint.HealthCheck != nil && endpoint.HealthCheck.Enabled,
			Configuration: endpoint.HealthCheck,
		}

		// Initialize connection status
		connectionStatus := &gatewayv1alpha1.ConnectionStatus{}

		status = append(status, gatewayv1alpha1.EndpointStatus{
			Name:               endpoint.Name,
			TailscaleIP:        endpoint.TailscaleIP,
			HealthStatus:       "Unknown", // Will be updated by health checks
			DiscoverySource:    source,
			Tags:               endpoint.Tags,
			HealthCheckDetails: healthDetails,
			ConnectionStatus:   connectionStatus,
			Weight:             endpoint.Weight,
			ExternalTarget:     endpoint.ExternalTarget,
		})
	}

	return status
}

// performHealthChecks performs health checks on all endpoints
func (r *TailscaleEndpointsReconciler) performHealthChecks(ctx context.Context, endpoints *gatewayv1alpha1.TailscaleEndpoints) error {
	healthyCount := 0
	unhealthyCount := 0

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

		// Initialize health check details if not present
		if endpoints.Status.EndpointStatus[i].HealthCheckDetails == nil {
			endpoints.Status.EndpointStatus[i].HealthCheckDetails = &gatewayv1alpha1.HealthCheckDetails{
				Enabled:       endpointConfig.HealthCheck != nil && endpointConfig.HealthCheck.Enabled,
				Configuration: endpointConfig.HealthCheck,
			}
		}

		healthDetails := endpoints.Status.EndpointStatus[i].HealthCheckDetails

		// Perform health check if enabled
		if endpointConfig.HealthCheck != nil && endpointConfig.HealthCheck.Enabled {
			checkStart := time.Now()
			healthy, response := r.performDetailedHealthCheck(ctx, endpointConfig)
			checkDuration := time.Since(checkStart)

			// Update health check details
			healthDetails.TotalChecks++
			healthDetails.LastCheckDuration = &metav1.Duration{Duration: checkDuration}
			healthDetails.LastCheckResponse = response

			if healthy {
				endpoints.Status.EndpointStatus[i].HealthStatus = "Healthy"
				healthDetails.SuccessfulChecks++
				healthDetails.SuccessiveSuccesses++
				healthDetails.SuccessiveFailures = 0
				healthyCount++
			} else {
				endpoints.Status.EndpointStatus[i].HealthStatus = "Unhealthy"
				healthDetails.SuccessiveFailures++
				healthDetails.SuccessiveSuccesses = 0
				unhealthyCount++

				// Add to recent failures if response indicates failure
				if response != nil && !response.Success {
					failure := gatewayv1alpha1.HealthCheckFailure{
						Timestamp:    metav1.Time{Time: checkStart},
						ErrorMessage: response.ErrorMessage,
						Duration:     &metav1.Duration{Duration: checkDuration},
					}

					// Determine error type based on the error
					if response.StatusCode != nil {
						failure.StatusCode = response.StatusCode
						if *response.StatusCode >= 400 {
							failure.ErrorType = "HTTPError"
						}
					} else {
						failure.ErrorType = "ConnectionRefused"
					}

					// Keep only last 5 failures
					healthDetails.RecentFailures = append([]gatewayv1alpha1.HealthCheckFailure{failure}, healthDetails.RecentFailures...)
					if len(healthDetails.RecentFailures) > 5 {
						healthDetails.RecentFailures = healthDetails.RecentFailures[:5]
					}
				}
			}

			// Update average response time
			if healthDetails.TotalChecks > 0 {
				// Simple moving average calculation
				if healthDetails.AverageResponseTime == nil {
					healthDetails.AverageResponseTime = &metav1.Duration{Duration: checkDuration}
				} else {
					currentAvg := healthDetails.AverageResponseTime.Duration
					newAvg := (currentAvg*time.Duration(healthDetails.TotalChecks-1) + checkDuration) / time.Duration(healthDetails.TotalChecks)
					healthDetails.AverageResponseTime.Duration = newAvg
				}
			}

			endpoints.Status.EndpointStatus[i].LastHealthCheck = &metav1.Time{Time: checkStart}
		} else {
			// Health checks disabled
			endpoints.Status.EndpointStatus[i].HealthStatus = "Disabled"
			healthDetails.Enabled = false
			healthyCount++ // Assume healthy if not checked
		}
	}

	endpoints.Status.HealthyEndpoints = healthyCount
	endpoints.Status.UnhealthyEndpoints = unhealthyCount
	return nil
}

// performDetailedHealthCheck performs a health check on a single endpoint and returns detailed response
func (r *TailscaleEndpointsReconciler) performDetailedHealthCheck(ctx context.Context, endpoint *gatewayv1alpha1.TailscaleEndpoint) (bool, *gatewayv1alpha1.HealthCheckResponse) {
	logger := log.FromContext(ctx)
	checkStart := time.Now()

	switch endpoint.Protocol {
	case "HTTP", "HTTPS":
		return r.performDetailedHTTPHealthCheck(ctx, endpoint, checkStart)
	case "TCP":
		return r.performDetailedTCPHealthCheck(ctx, endpoint, checkStart)
	case "UDP":
		return r.performDetailedUDPHealthCheck(ctx, endpoint, checkStart)
	default:
		logger.Info("Unknown protocol for health check, assuming healthy", "endpoint", endpoint.Name, "protocol", endpoint.Protocol)
		response := &gatewayv1alpha1.HealthCheckResponse{
			Success:      true,
			ResponseTime: metav1.Duration{Duration: time.Since(checkStart)},
			Timestamp:    metav1.Time{Time: checkStart},
			ErrorMessage: "Unknown protocol",
		}
		return true, response
	}
}

// performDetailedHTTPHealthCheck performs HTTP/HTTPS health check with detailed response
func (r *TailscaleEndpointsReconciler) performDetailedHTTPHealthCheck(ctx context.Context, endpoint *gatewayv1alpha1.TailscaleEndpoint, checkStart time.Time) (bool, *gatewayv1alpha1.HealthCheckResponse) {
	// Validate inputs
	if endpoint == nil {
		return false, &gatewayv1alpha1.HealthCheckResponse{
			Success:      false,
			ResponseTime: metav1.Duration{Duration: time.Since(checkStart)},
			Timestamp:    metav1.Time{Time: checkStart},
			ErrorMessage: "endpoint parameter cannot be nil",
		}
	}

	// Validate endpoint has required fields
	if endpoint.TailscaleIP == "" && endpoint.TailscaleFQDN == "" {
		return false, &gatewayv1alpha1.HealthCheckResponse{
			Success:      false,
			ResponseTime: metav1.Duration{Duration: time.Since(checkStart)},
			Timestamp:    metav1.Time{Time: checkStart},
			ErrorMessage: "endpoint must have either TailscaleIP or TailscaleFQDN",
		}
	}

	// Determine the URL for health check
	healthCheckURL := r.buildHealthCheckURL(endpoint)

	// Create HTTP client with timeout based on health check configuration
	timeout := 10 * time.Second // default timeout
	if endpoint.HealthCheck != nil && endpoint.HealthCheck.Timeout != nil {
		timeout = endpoint.HealthCheck.Timeout.Duration
	}

	// Create context with timeout
	healthCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Create HTTP request
	req, err := http.NewRequestWithContext(healthCtx, "GET", healthCheckURL, nil)
	if err != nil {
		response := &gatewayv1alpha1.HealthCheckResponse{
			Success:      false,
			ResponseTime: metav1.Duration{Duration: time.Since(checkStart)},
			Timestamp:    metav1.Time{Time: checkStart},
			ErrorMessage: fmt.Sprintf("failed to create request: %v", err),
		}
		return false, response
	}

	// Use a fresh transport for each health check (following Tailscale prober pattern)
	tr := http.DefaultTransport.(*http.Transport).Clone()
	defer tr.CloseIdleConnections()
	client := &http.Client{
		Transport: tr,
		Timeout:   timeout,
	}

	// Perform the health check
	resp, err := client.Do(req)
	if err != nil {
		response := &gatewayv1alpha1.HealthCheckResponse{
			Success:      false,
			ResponseTime: metav1.Duration{Duration: time.Since(checkStart)},
			Timestamp:    metav1.Time{Time: checkStart},
			ErrorMessage: fmt.Sprintf("request failed: %v", err),
		}
		return false, response
	}
	defer resp.Body.Close()

	responseTime := time.Since(checkStart)
	statusCode := int32(resp.StatusCode)

	// Extract response headers
	responseHeaders := make(map[string]string)
	for key, values := range resp.Header {
		if len(values) > 0 {
			responseHeaders[strings.ToLower(key)] = values[0]
		}
	}

	// Check if response is successful
	success := resp.StatusCode >= 200 && resp.StatusCode < 300

	// Read response body for basic validation
	var responseBody string
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20)) // 4MB limit
	if err != nil {
		success = false
		responseBody = fmt.Sprintf("failed to read body: %v", err)
	} else {
		responseBody = string(body)
	}

	response := &gatewayv1alpha1.HealthCheckResponse{
		Success:         success,
		ResponseTime:    metav1.Duration{Duration: responseTime},
		Timestamp:       metav1.Time{Time: checkStart},
		StatusCode:      &statusCode,
		ResponseHeaders: responseHeaders,
	}

	if !success {
		if responseBody != "" {
			response.ErrorMessage = fmt.Sprintf("health check failed: status %d, body: %s", resp.StatusCode, responseBody)
		} else {
			response.ErrorMessage = fmt.Sprintf("health check failed: status %d", resp.StatusCode)
		}
	}

	return success, response
}

// performDetailedTCPHealthCheck performs TCP connection health check with detailed response
func (r *TailscaleEndpointsReconciler) performDetailedTCPHealthCheck(ctx context.Context, endpoint *gatewayv1alpha1.TailscaleEndpoint, checkStart time.Time) (bool, *gatewayv1alpha1.HealthCheckResponse) {
	// Determine the address for TCP health check
	addr := r.buildTCPHealthCheckAddress(endpoint)

	// Create timeout context
	timeout := 10 * time.Second // default timeout
	if endpoint.HealthCheck != nil && endpoint.HealthCheck.Timeout != nil {
		timeout = endpoint.HealthCheck.Timeout.Duration
	}

	healthCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Perform TCP connection test following Tailscale prober pattern
	var d net.Dialer
	conn, err := d.DialContext(healthCtx, "tcp", addr)
	responseTime := time.Since(checkStart)

	if err != nil {
		response := &gatewayv1alpha1.HealthCheckResponse{
			Success:      false,
			ResponseTime: metav1.Duration{Duration: responseTime},
			Timestamp:    metav1.Time{Time: checkStart},
			ErrorMessage: fmt.Sprintf("TCP connection failed to %s: %v", addr, err),
		}
		return false, response
	}

	// Close connection immediately (we only test connectivity)
	conn.Close()

	response := &gatewayv1alpha1.HealthCheckResponse{
		Success:      true,
		ResponseTime: metav1.Duration{Duration: responseTime},
		Timestamp:    metav1.Time{Time: checkStart},
	}

	return true, response
}

// performDetailedUDPHealthCheck performs UDP health check with detailed response
func (r *TailscaleEndpointsReconciler) performDetailedUDPHealthCheck(ctx context.Context, endpoint *gatewayv1alpha1.TailscaleEndpoint, checkStart time.Time) (bool, *gatewayv1alpha1.HealthCheckResponse) {
	// Determine the address for UDP health check
	addr := r.buildTCPHealthCheckAddress(endpoint) // Same format as TCP

	// Create timeout context
	timeout := 10 * time.Second // default timeout
	if endpoint.HealthCheck != nil && endpoint.HealthCheck.Timeout != nil {
		timeout = endpoint.HealthCheck.Timeout.Duration
	}

	healthCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Perform UDP connection test - basic UDP socket creation and write
	conn, err := net.Dial("udp", addr)
	if err != nil {
		response := &gatewayv1alpha1.HealthCheckResponse{
			Success:      false,
			ResponseTime: metav1.Duration{Duration: time.Since(checkStart)},
			Timestamp:    metav1.Time{Time: checkStart},
			ErrorMessage: fmt.Sprintf("UDP connection failed to %s: %v", addr, err),
		}
		return false, response
	}
	defer conn.Close()

	// Set deadline for the connection
	deadline, _ := healthCtx.Deadline()
	conn.SetDeadline(deadline)

	// Send a simple UDP packet to test connectivity
	// Note: This is a basic connectivity test - real UDP health checks would be application-specific
	testData := []byte("health-check")
	_, err = conn.Write(testData)
	responseTime := time.Since(checkStart)

	if err != nil {
		response := &gatewayv1alpha1.HealthCheckResponse{
			Success:      false,
			ResponseTime: metav1.Duration{Duration: responseTime},
			Timestamp:    metav1.Time{Time: checkStart},
			ErrorMessage: fmt.Sprintf("UDP write failed to %s: %v", addr, err),
		}
		return false, response
	}

	// UDP is connectionless, so if we can send data without error, consider it healthy
	// Note: Real UDP services might require reading a response or application-specific logic
	response := &gatewayv1alpha1.HealthCheckResponse{
		Success:      true,
		ResponseTime: metav1.Duration{Duration: responseTime},
		Timestamp:    metav1.Time{Time: checkStart},
	}

	return true, response
}

// buildHealthCheckURL constructs the full URL for HTTP health checks
func (r *TailscaleEndpointsReconciler) buildHealthCheckURL(endpoint *gatewayv1alpha1.TailscaleEndpoint) string {
	// Validate endpoint
	if endpoint == nil {
		return "http://invalid:80/"
	}

	// Use TailscaleIP if available, otherwise fall back to FQDN
	host := endpoint.TailscaleIP
	if host == "" {
		host = endpoint.TailscaleFQDN
	}

	// Validate we have a host
	if host == "" {
		return "http://invalid:80/"
	}

	// Default to HTTP unless explicitly HTTPS
	scheme := "http"
	if endpoint.Protocol == "HTTPS" || endpoint.Port == 443 {
		scheme = "https"
	}

	// Default path
	path := "/"

	// Use custom health check configuration if available
	if endpoint.HealthCheck != nil && endpoint.HealthCheck.Path != "" {
		path = endpoint.HealthCheck.Path
	}

	// Construct URL
	url := fmt.Sprintf("%s://%s:%d%s", scheme, host, endpoint.Port, path)
	return url
}

// buildTCPHealthCheckAddress constructs the address for TCP health checks
func (r *TailscaleEndpointsReconciler) buildTCPHealthCheckAddress(endpoint *gatewayv1alpha1.TailscaleEndpoint) string {
	// Validate endpoint
	if endpoint == nil {
		return "invalid:80"
	}

	// Use TailscaleIP if available, otherwise fall back to FQDN
	host := endpoint.TailscaleIP
	if host == "" {
		host = endpoint.TailscaleFQDN
	}

	// Validate we have a host
	if host == "" {
		return "invalid:80"
	}

	// Use the endpoint port for TCP health checks
	port := endpoint.Port

	return fmt.Sprintf("%s:%d", host, port)
}

// reconcileStatefulSets creates and manages StatefulSets for each endpoint
// Following ProxyGroup patterns with ingress and egress connections
func (r *TailscaleEndpointsReconciler) reconcileStatefulSets(ctx context.Context, endpoints *gatewayv1alpha1.TailscaleEndpoints) error {
	logger := log.FromContext(ctx)

	// Skip StatefulSet creation if TailscaleClientManager is not available (e.g., in tests)
	if r.TailscaleClientManager == nil {
		logger.Info("TailscaleClientManager not available, skipping StatefulSet creation")
		return nil
	}

	// Get Tailscale client for auth key creation
	tsClient, err := r.TailscaleClientManager.GetClient(ctx, r.Client, endpoints.Spec.Tailnet, endpoints.Namespace)
	if err != nil {
		return fmt.Errorf("failed to get Tailscale client: %w", err)
	}

	// Handle proxy-based structure only
	if endpoints.Spec.Proxy != nil && len(endpoints.Spec.Ports) > 0 {
		return r.reconcileProxyStatefulSets(ctx, endpoints, tsClient)
	}

	logger.Info("No proxy configuration found, skipping StatefulSet creation", "endpoints", endpoints.Name)
	return nil
}

// reconcileProxyStatefulSets handles the new proxy-based structure
func (r *TailscaleEndpointsReconciler) reconcileProxyStatefulSets(ctx context.Context, endpoints *gatewayv1alpha1.TailscaleEndpoints, tsClient tailscale.Client) error {
	logger := log.FromContext(ctx)

	// Get proxy configuration
	proxyConfig := endpoints.Spec.Proxy
	if proxyConfig == nil {
		return fmt.Errorf("proxy configuration is nil")
	}

	// Set defaults
	replicas := int32(2)
	if proxyConfig.Replicas != nil {
		replicas = *proxyConfig.Replicas
	}

	connectionType := "bidirectional"
	if proxyConfig.ConnectionType != "" {
		connectionType = proxyConfig.ConnectionType
	}

	var statefulSetRefs []gatewayv1alpha1.StatefulSetReference

	// Create StatefulSets based on connection type
	switch connectionType {
	case "ingress":
		ingressRef, err := r.reconcileProxyIngress(ctx, endpoints, tsClient, replicas)
		if err != nil {
			return fmt.Errorf("failed to reconcile ingress proxy: %w", err)
		}
		statefulSetRefs = append(statefulSetRefs, *ingressRef)

	case "egress":
		egressRef, err := r.reconcileProxyEgress(ctx, endpoints, tsClient, replicas)
		if err != nil {
			return fmt.Errorf("failed to reconcile egress proxy: %w", err)
		}
		statefulSetRefs = append(statefulSetRefs, *egressRef)

	case "bidirectional":
		// Create both ingress and egress
		ingressRef, err := r.reconcileProxyIngress(ctx, endpoints, tsClient, replicas)
		if err != nil {
			return fmt.Errorf("failed to reconcile ingress proxy: %w", err)
		}
		statefulSetRefs = append(statefulSetRefs, *ingressRef)

		egressRef, err := r.reconcileProxyEgress(ctx, endpoints, tsClient, replicas)
		if err != nil {
			return fmt.Errorf("failed to reconcile egress proxy: %w", err)
		}
		statefulSetRefs = append(statefulSetRefs, *egressRef)

	default:
		return fmt.Errorf("invalid connection type: %s", connectionType)
	}

	// Update status with StatefulSet references
	endpoints.Status.StatefulSetRefs = statefulSetRefs

	logger.Info("Successfully reconciled proxy StatefulSets",
		"endpoints", endpoints.Name,
		"connectionType", connectionType,
		"replicas", replicas,
		"statefulSets", len(statefulSetRefs))

	return nil
}

// reconcileProxyIngress creates/updates ingress proxy StatefulSet
func (r *TailscaleEndpointsReconciler) reconcileProxyIngress(ctx context.Context, endpoints *gatewayv1alpha1.TailscaleEndpoints, tsClient tailscale.Client, replicas int32) (*gatewayv1alpha1.StatefulSetReference, error) {
	return r.reconcileProxyStatefulSet(ctx, endpoints, tsClient, "ingress", replicas)
}

// reconcileProxyEgress creates/updates egress proxy StatefulSet
func (r *TailscaleEndpointsReconciler) reconcileProxyEgress(ctx context.Context, endpoints *gatewayv1alpha1.TailscaleEndpoints, tsClient tailscale.Client, replicas int32) (*gatewayv1alpha1.StatefulSetReference, error) {
	return r.reconcileProxyStatefulSet(ctx, endpoints, tsClient, "egress", replicas)
}

// reconcileProxyStatefulSet creates/updates a proxy StatefulSet for the new structure
func (r *TailscaleEndpointsReconciler) reconcileProxyStatefulSet(ctx context.Context, endpoints *gatewayv1alpha1.TailscaleEndpoints, tsClient tailscale.Client, connectionType string, replicas int32) (*gatewayv1alpha1.StatefulSetReference, error) {
	logger := log.FromContext(ctx)

	// Generate names
	name := fmt.Sprintf("%s-%s", endpoints.Name, connectionType)

	// Create labels
	labels := map[string]string{
		"app.kubernetes.io/name":      "tailscale-proxy",
		"app.kubernetes.io/instance":  endpoints.Name,
		"app.kubernetes.io/component": connectionType,
		"tailscale.com/tailnet":       r.sanitizeTailnetName(endpoints.Spec.Tailnet),
		"tailscale.com/connection":    connectionType,
	}

	// Copy user labels
	for k, v := range endpoints.Labels {
		if !strings.HasPrefix(k, "app.kubernetes.io/") && !strings.HasPrefix(k, "tailscale.com/") {
			labels[k] = v
		}
	}

	// Create RBAC resources
	if err := r.createRBACResources(ctx, endpoints, name, labels); err != nil {
		return nil, fmt.Errorf("failed to create RBAC resources: %w", err)
	}

	// Create config secrets per replica (following ProxyGroup pattern)
	if err := r.ensureConfigSecretsCreated(ctx, endpoints, name, connectionType, replicas, tsClient); err != nil {
		return nil, fmt.Errorf("failed to create config secrets: %w", err)
	}

	// Create state secrets per replica (following ProxyGroup pattern)
	if err := r.ensureStateSecretsCreated(ctx, endpoints, name, replicas, labels); err != nil {
		return nil, fmt.Errorf("failed to create state secrets: %w", err)
	}

	// Create StatefulSet with per-replica config secrets
	if err := r.createEndpointStatefulSet(ctx, endpoints, nil, name, connectionType, replicas, labels); err != nil {
		return nil, fmt.Errorf("failed to create StatefulSet: %w", err)
	}

	// Create headless Service for StatefulSet
	if err := r.createProxyService(ctx, endpoints, name, connectionType, labels); err != nil {
		return nil, fmt.Errorf("failed to create Service: %w", err)
	}

	logger.Info("Successfully reconciled proxy StatefulSet",
		"name", name,
		"connectionType", connectionType,
		"replicas", replicas)

	return &gatewayv1alpha1.StatefulSetReference{
		Name:            name,
		Namespace:       endpoints.Namespace,
		ConnectionType:  connectionType,
		EndpointName:    endpoints.Name,
		Status:          "Ready", // Will be updated by status reconciliation
		DesiredReplicas: &replicas,
	}, nil
}

// createProxyAuthKey creates an auth key for proxy with proper tags
func (r *TailscaleEndpointsReconciler) createProxyAuthKey(ctx context.Context, tsClient tailscale.Client, endpoints *gatewayv1alpha1.TailscaleEndpoints) (string, error) {
	logger := log.FromContext(ctx)

	// Use tags from endpoints.Spec.Tags instead of individual endpoint tags
	tags := endpoints.Spec.Tags
	if len(tags) == 0 {
		// Fallback to default tags if none specified
		tags = []string{"tag:k8s-proxy"}
	}

	// Create persistent auth key for StatefulSet connections
	capabilities := tailscaleclient.KeyCapabilities{}
	capabilities.Devices.Create.Reusable = true
	capabilities.Devices.Create.Ephemeral = false
	capabilities.Devices.Create.Preauthorized = true
	capabilities.Devices.Create.Tags = tags

	logger.Info("Creating auth key for proxy", "endpoints", endpoints.Name, "tags", tags)
	key, err := tsClient.CreateKey(ctx, capabilities)
	if err != nil {
		return "", fmt.Errorf("failed to create auth key with tags %v: %w", tags, err)
	}

	return key.Key, nil
}

// createProxyStatefulSet creates a StatefulSet for the new proxy structure
func (r *TailscaleEndpointsReconciler) createProxyStatefulSet(ctx context.Context, endpoints *gatewayv1alpha1.TailscaleEndpoints, name, connectionType, configSecretName, stateSecretName string, labels map[string]string, replicas int32) error {
	// Get proxy configuration
	proxyConfig := endpoints.Spec.Proxy
	if proxyConfig == nil {
		return fmt.Errorf("proxy configuration is nil")
	}

	// Convert PortMapping to container ports
	var containerPorts []corev1.ContainerPort
	for _, portMapping := range endpoints.Spec.Ports {
		protocol := corev1.ProtocolTCP
		if portMapping.Protocol == "UDP" {
			protocol = corev1.ProtocolUDP
		}

		containerPorts = append(containerPorts, corev1.ContainerPort{
			Name:          portMapping.Name,
			ContainerPort: portMapping.Port,
			Protocol:      protocol,
		})
	}

	// Get container image
	image := "tailscale/tailscale:latest"
	if proxyConfig.Image != "" {
		image = proxyConfig.Image
	}

	// Create StatefulSet similar to existing pattern but using proxy configuration
	statefulSet := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: endpoints.Namespace,
			Labels:    labels,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: endpoints.APIVersion,
					Kind:       endpoints.Kind,
					Name:       endpoints.Name,
					UID:        endpoints.UID,
					Controller: &[]bool{true}[0],
				},
			},
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas:    &replicas,
			ServiceName: name, // Headless service name
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: name,
					Containers: []corev1.Container{
						{
							Name:         "tailscale",
							Image:        image,
							Ports:        containerPorts,
							Env:          r.getProxyEnvironmentVariables(endpoints, name, connectionType, configSecretName, stateSecretName),
							VolumeMounts: r.getProxyVolumeMounts(endpoints, connectionType),
							Resources:    r.getProxyResources(proxyConfig),
						},
					},
					Volumes:      r.getProxyVolumes(endpoints, name, configSecretName, stateSecretName),
					NodeSelector: proxyConfig.NodeSelector,
					Tolerations:  r.convertTolerationsToK8s(proxyConfig.Tolerations),
					Affinity:     r.convertAffinityToK8s(proxyConfig.Affinity),
				},
			},
		},
	}

	return r.createOrUpdate(ctx, statefulSet, func() {
		// Update logic for StatefulSet
		statefulSet.Spec.Replicas = &replicas
	})
}

// createProxyService creates a headless Service for the proxy StatefulSet
func (r *TailscaleEndpointsReconciler) createProxyService(ctx context.Context, endpoints *gatewayv1alpha1.TailscaleEndpoints, name, connectionType string, labels map[string]string) error {
	// Convert PortMapping to service ports
	var servicePorts []corev1.ServicePort
	for _, portMapping := range endpoints.Spec.Ports {
		protocol := corev1.ProtocolTCP
		if portMapping.Protocol == "UDP" {
			protocol = corev1.ProtocolUDP
		}

		targetPort := portMapping.Port
		if portMapping.TargetPort != nil {
			targetPort = *portMapping.TargetPort
		}

		servicePorts = append(servicePorts, corev1.ServicePort{
			Name:       portMapping.Name,
			Port:       portMapping.Port,
			TargetPort: intstr.FromInt(int(targetPort)),
			Protocol:   protocol,
		})
	}

	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: endpoints.Namespace,
			Labels:    labels,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: endpoints.APIVersion,
					Kind:       endpoints.Kind,
					Name:       endpoints.Name,
					UID:        endpoints.UID,
					Controller: &[]bool{true}[0],
				},
			},
		},
		Spec: corev1.ServiceSpec{
			Type:      corev1.ServiceTypeClusterIP,
			ClusterIP: "None", // Headless service
			Selector:  labels,
			Ports:     servicePorts,
		},
	}

	return r.createOrUpdate(ctx, service, func() {
		// Update logic for Service
		service.Spec.Ports = servicePorts
	})
}

// Helper functions for proxy StatefulSet configuration

func (r *TailscaleEndpointsReconciler) getProxyEnvironmentVariables(endpoints *gatewayv1alpha1.TailscaleEndpoints, name, connectionType, configSecretName, stateSecretName string) []corev1.EnvVar {
	return []corev1.EnvVar{
		{Name: "TS_KUBE_SECRET", Value: stateSecretName},
		{Name: "TS_STATE", Value: "kube:" + stateSecretName},
		{Name: "TS_EXPERIMENTAL_VERSIONED_CONFIG_DIR", Value: fmt.Sprintf("/etc/tsconfig/%s", r.sanitizeTailnetName(endpoints.Spec.Tailnet))},
		{Name: "TS_USERSPACE", Value: "true"},
		{Name: "POD_NAME", ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.name"}}},
		{Name: "POD_UID", ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.uid"}}},
	}
}

func (r *TailscaleEndpointsReconciler) getProxyVolumeMounts(endpoints *gatewayv1alpha1.TailscaleEndpoints, connectionType string) []corev1.VolumeMount {
	return []corev1.VolumeMount{
		{
			Name:      fmt.Sprintf("tailscaledconfig-%s", r.sanitizeTailnetName(endpoints.Spec.Tailnet)),
			ReadOnly:  true,
			MountPath: fmt.Sprintf("/etc/tsconfig/%s", r.sanitizeTailnetName(endpoints.Spec.Tailnet)),
		},
	}
}

func (r *TailscaleEndpointsReconciler) getProxyVolumes(endpoints *gatewayv1alpha1.TailscaleEndpoints, name, configSecretName, stateSecretName string) []corev1.Volume {
	return []corev1.Volume{
		{
			Name: fmt.Sprintf("tailscaledconfig-%s", r.sanitizeTailnetName(endpoints.Spec.Tailnet)),
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: configSecretName,
				},
			},
		},
	}
}

func (r *TailscaleEndpointsReconciler) getProxyResources(proxyConfig *gatewayv1alpha1.ProxyConfig) corev1.ResourceRequirements {
	if proxyConfig.Resources == nil {
		return corev1.ResourceRequirements{}
	}

	resources := corev1.ResourceRequirements{}

	if proxyConfig.Resources.Limits != nil {
		resources.Limits = make(corev1.ResourceList)
		for k, v := range proxyConfig.Resources.Limits {
			resources.Limits[corev1.ResourceName(k)] = resource.MustParse(v)
		}
	}

	if proxyConfig.Resources.Requests != nil {
		resources.Requests = make(corev1.ResourceList)
		for k, v := range proxyConfig.Resources.Requests {
			resources.Requests[corev1.ResourceName(k)] = resource.MustParse(v)
		}
	}

	return resources
}

func (r *TailscaleEndpointsReconciler) convertTolerationsToK8s(tolerations []gatewayv1alpha1.Toleration) []corev1.Toleration {
	var k8sTolerations []corev1.Toleration
	for _, t := range tolerations {
		k8sToleration := corev1.Toleration{
			Key:      t.Key,
			Operator: corev1.TolerationOperator(t.Operator),
			Value:    t.Value,
			Effect:   corev1.TaintEffect(t.Effect),
		}
		if t.TolerationSeconds != nil {
			k8sToleration.TolerationSeconds = t.TolerationSeconds
		}
		k8sTolerations = append(k8sTolerations, k8sToleration)
	}
	return k8sTolerations
}

func (r *TailscaleEndpointsReconciler) convertAffinityToK8s(affinity *gatewayv1alpha1.Affinity) *corev1.Affinity {
	if affinity == nil {
		return nil
	}

	k8sAffinity := &corev1.Affinity{}

	if affinity.NodeAffinity != nil {
		k8sAffinity.NodeAffinity = &corev1.NodeAffinity{}
		if affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution != nil {
			k8sAffinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution = &corev1.NodeSelector{
				NodeSelectorTerms: r.convertNodeSelectorTermsToK8s(affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms),
			}
		}
	}

	return k8sAffinity
}

func (r *TailscaleEndpointsReconciler) convertNodeSelectorTermsToK8s(terms []gatewayv1alpha1.NodeSelectorTerm) []corev1.NodeSelectorTerm {
	var k8sTerms []corev1.NodeSelectorTerm
	for _, term := range terms {
		k8sTerm := corev1.NodeSelectorTerm{}
		for _, expr := range term.MatchExpressions {
			k8sTerm.MatchExpressions = append(k8sTerm.MatchExpressions, corev1.NodeSelectorRequirement{
				Key:      expr.Key,
				Operator: corev1.NodeSelectorOperator(expr.Operator),
				Values:   expr.Values,
			})
		}
		k8sTerms = append(k8sTerms, k8sTerm)
	}
	return k8sTerms
}

// reconcileEndpointStatefulSet creates/updates a StatefulSet for an endpoint connection
// Follows k8s-operator ProxyGroup patterns for resource creation
func (r *TailscaleEndpointsReconciler) reconcileEndpointStatefulSet(ctx context.Context, endpoints *gatewayv1alpha1.TailscaleEndpoints, endpoint *gatewayv1alpha1.TailscaleEndpoint, connectionType string, tsClient tailscale.Client) (*gatewayv1alpha1.StatefulSetReference, error) {
	logger := log.FromContext(ctx)

	// Generate short StatefulSet name to avoid 63-character limit
	// Use hash-based approach for consistent short names including endpoint name
	hashSource := fmt.Sprintf("%s-%s-%s", endpoints.Name, endpoint.Name, connectionType)
	hasher := sha256.Sum256([]byte(hashSource))
	hash := hex.EncodeToString(hasher[:])[:8] // Use 8-char hash for uniqueness

	// Create short, predictable name that includes endpoint info
	ssName := fmt.Sprintf("ts-%s", hash)

	// Ensure it's under 63 characters (should be ~15-20 chars)
	if len(ssName) > 63 {
		ssName = fmt.Sprintf("ts-%s-%s", hash[:6], connectionType)
	}
	labels := map[string]string{
		"app.kubernetes.io/name":         "tailscale-endpoint",
		"app.kubernetes.io/instance":     endpoints.Name,
		"app.kubernetes.io/component":    connectionType,
		"app.kubernetes.io/managed-by":   "tailscale-gateway-operator",
		"gateway.tailscale.com/endpoint": endpoint.Name,
		"gateway.tailscale.com/type":     connectionType,
	}

	// Create RBAC resources
	if err := r.createRBACResources(ctx, endpoints, ssName, labels); err != nil {
		return nil, fmt.Errorf("failed to create RBAC resources: %w", err)
	}

	// Create config secrets per replica (following ProxyGroup pattern)
	replicas := int32(3) // Default replicas for now
	if err := r.ensureConfigSecretsCreated(ctx, endpoints, ssName, connectionType, replicas, tsClient); err != nil {
		return nil, fmt.Errorf("failed to create config secrets: %w", err)
	}

	// Create state secrets per replica (following ProxyGroup pattern)
	if err := r.ensureStateSecretsCreated(ctx, endpoints, ssName, replicas, labels); err != nil {
		return nil, fmt.Errorf("failed to create state secrets: %w", err)
	}

	// Create StatefulSet with per-replica config secrets
	if err := r.createEndpointStatefulSet(ctx, endpoints, endpoint, ssName, connectionType, replicas, labels); err != nil {
		return nil, fmt.Errorf("failed to create StatefulSet: %w", err)
	}

	logger.Info("Successfully reconciled endpoint StatefulSet", "name", ssName, "type", connectionType)

	return &gatewayv1alpha1.StatefulSetReference{
		Name:           ssName,
		Namespace:      endpoints.Namespace,
		ConnectionType: connectionType,
		EndpointName:   endpoint.Name,
	}, nil
}

// createRBACResources creates ServiceAccount, Role, and RoleBinding
// Following k8s-operator ProxyGroup RBAC patterns with per-replica secrets
func (r *TailscaleEndpointsReconciler) createRBACResources(ctx context.Context, endpoints *gatewayv1alpha1.TailscaleEndpoints, name string, labels map[string]string) error {
	// Get replicas count
	replicas := int32(2)
	if endpoints.Spec.Proxy != nil && endpoints.Spec.Proxy.Replicas != nil {
		replicas = *endpoints.Spec.Proxy.Replicas
	}

	// ServiceAccount
	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: endpoints.Namespace,
			Labels:    labels,
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(endpoints, gatewayv1alpha1.GroupVersion.WithKind("TailscaleEndpoints")),
			},
		},
	}
	if err := r.createOrUpdate(ctx, sa, func() {
		sa.Labels = labels
	}); err != nil {
		return fmt.Errorf("failed to create ServiceAccount: %w", err)
	}

	// Build per-replica secret names following ProxyGroup pattern
	var secretNames []string
	for i := int32(0); i < replicas; i++ {
		secretNames = append(secretNames,
			fmt.Sprintf("%s-%d-config", name, i), // Config with auth key
			fmt.Sprintf("%s-%d", name, i),        // State
		)
	}

	// Role
	role := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: endpoints.Namespace,
			Labels:    labels,
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(endpoints, gatewayv1alpha1.GroupVersion.WithKind("TailscaleEndpoints")),
			},
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{""},
				Resources: []string{"secrets"},
				Verbs:     []string{"list"},
			},
			{
				APIGroups:     []string{""},
				Resources:     []string{"secrets"},
				Verbs:         []string{"get", "patch", "update"},
				ResourceNames: secretNames,
			},
			{
				APIGroups: []string{""},
				Resources: []string{"events"},
				Verbs:     []string{"create", "patch", "get"},
			},
		},
	}
	if err := r.createOrUpdate(ctx, role, func() {
		// Update rules to match current replica count
		var secretNames []string
		for i := int32(0); i < replicas; i++ {
			secretNames = append(secretNames,
				fmt.Sprintf("%s-%d-config", name, i),
				fmt.Sprintf("%s-%d", name, i),
			)
		}
		role.Rules = []rbacv1.PolicyRule{
			{
				APIGroups: []string{""},
				Resources: []string{"secrets"},
				Verbs:     []string{"list"},
			},
			{
				APIGroups:     []string{""},
				Resources:     []string{"secrets"},
				Verbs:         []string{"get", "patch", "update"},
				ResourceNames: secretNames,
			},
			{
				APIGroups: []string{""},
				Resources: []string{"events"},
				Verbs:     []string{"create", "patch", "get"},
			},
		}
	}); err != nil {
		return fmt.Errorf("failed to create Role: %w", err)
	}

	// RoleBinding
	roleBinding := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: endpoints.Namespace,
			Labels:    labels,
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(endpoints, gatewayv1alpha1.GroupVersion.WithKind("TailscaleEndpoints")),
			},
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      name,
				Namespace: endpoints.Namespace,
			},
		},
		RoleRef: rbacv1.RoleRef{
			Kind:     "Role",
			Name:     name,
			APIGroup: "rbac.authorization.k8s.io",
		},
	}
	if err := r.createOrUpdate(ctx, roleBinding, func() {
		// RoleBinding subjects and roleRef are already set
	}); err != nil {
		return fmt.Errorf("failed to create RoleBinding: %w", err)
	}

	return nil
}

// createAuthKey creates a Tailscale auth key for the endpoint connection
// Following k8s-operator auth key creation patterns
func (r *TailscaleEndpointsReconciler) createAuthKey(ctx context.Context, tsClient tailscale.Client, endpoint *gatewayv1alpha1.TailscaleEndpoint) (string, error) {
	logger := log.FromContext(ctx)

	// Use endpoint tags or default to tag:web (which is valid in the current tailnet)
	tags := endpoint.Tags
	if len(tags) == 0 {
		tags = []string{"tag:web"}
	}

	// Create persistent auth key for StatefulSet connections
	// StatefulSets need persistent connections, not ephemeral ones
	capabilities := tailscaleclient.KeyCapabilities{}
	capabilities.Devices.Create.Reusable = true
	capabilities.Devices.Create.Ephemeral = false
	capabilities.Devices.Create.Preauthorized = true
	capabilities.Devices.Create.Tags = tags

	logger.Info("Creating auth key for endpoint", "endpoint", endpoint.Name, "tags", tags)
	authKey, err := tsClient.CreateKey(ctx, capabilities)
	if err != nil {
		return "", fmt.Errorf("failed to create auth key for endpoint %s: %w", endpoint.Name, err)
	}

	logger.Info("Successfully created auth key", "endpoint", endpoint.Name, "keyID", authKey.ID)
	return authKey.Key, nil
}

// createConfigSecret creates the Tailscale configuration secret
// Following k8s-operator config secret patterns with proper ipn.ConfigVAlpha files
func (r *TailscaleEndpointsReconciler) createConfigSecret(ctx context.Context, endpoints *gatewayv1alpha1.TailscaleEndpoints, secretName, hostname, authKey, connectionType string, labels map[string]string) error {
	// Create tailscaled configuration files for different capability versions
	configData, err := r.createTailscaledConfigs(hostname, authKey, connectionType, endpoints)
	if err != nil {
		return fmt.Errorf("failed to create tailscaled configs: %w", err)
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: endpoints.Namespace,
			Labels:    labels,
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(endpoints, gatewayv1alpha1.GroupVersion.WithKind("TailscaleEndpoints")),
			},
		},
		Data: configData,
	}

	return r.createOrUpdate(ctx, secret, func() {
		secret.Data = configData
	})
}

// createTailscaledConfigs creates tailscaled configuration files for different capability versions
// Following the exact pattern from Tailscale k8s-operator ProxyGroup implementation using ipn.ConfigVAlpha
func (r *TailscaleEndpointsReconciler) createTailscaledConfigs(hostname, authKey, connectionType string, endpoints *gatewayv1alpha1.TailscaleEndpoints) (map[string][]byte, error) {
	// Set AcceptRoutes based on connection type (use actual booleans)
	acceptRoutes := false
	if connectionType == "egress" {
		acceptRoutes = true
	}

	// Base configuration structure - use proper types for JSON
	baseConfig := map[string]interface{}{
		"Version":             "alpha0",
		"AcceptDNS":           false,        // boolean, not string
		"AcceptRoutes":        acceptRoutes, // boolean, not string
		"Locked":              false,        // boolean, not string
		"Hostname":            hostname,     // string
		"NoStatefulFiltering": true,         // boolean, not string
		"AuthKey":             authKey,      // string
	}

	// Create configs for different capability versions
	configs := make(map[string][]byte)

	// Capability version 106 - official k8s-operator version for AdvertiseServices
	config106JSON, err := json.Marshal(baseConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal cap-106 config: %w", err)
	}
	configs["cap-106.hujson"] = config106JSON

	return configs, nil
}

// ensureConfigSecretsCreated creates config secrets per replica following ProxyGroup pattern
func (r *TailscaleEndpointsReconciler) ensureConfigSecretsCreated(ctx context.Context, endpoints *gatewayv1alpha1.TailscaleEndpoints, baseName, connectionType string, replicas int32, tsClient tailscale.Client) error {
	logger := log.FromContext(ctx)

	for i := int32(0); i < replicas; i++ {
		cfgSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("%s-%d-config", baseName, i),
				Namespace: endpoints.Namespace,
				Labels: map[string]string{
					"app.kubernetes.io/name":       "tailscale-proxy",
					"app.kubernetes.io/instance":   endpoints.Name,
					"app.kubernetes.io/component":  "config",
					"gateway.tailscale.com/type":   "secret",
					"gateway.tailscale.com/secret": "config",
				},
				OwnerReferences: []metav1.OwnerReference{
					*metav1.NewControllerRef(endpoints, gatewayv1alpha1.GroupVersion.WithKind("TailscaleEndpoints")),
				},
			},
		}

		var existingCfgSecret *corev1.Secret
		if err := r.Get(ctx, client.ObjectKeyFromObject(cfgSecret), cfgSecret); err == nil {
			logger.Info("Config secret already exists", "name", cfgSecret.Name)
			existingCfgSecret = cfgSecret.DeepCopy()
		} else if !errors.IsNotFound(err) {
			return err
		}

		var authKey string
		if existingCfgSecret == nil {
			logger.Info("Creating new auth key for replica", "replica", i)
			var err error
			authKey, err = r.createAuthKeyForReplica(ctx, tsClient, endpoints, connectionType)
			if err != nil {
				return fmt.Errorf("failed to create auth key for replica %d: %w", i, err)
			}
		} else {
			// Extract auth key from existing config (for updates)
			// For now, skip auth key extraction and create a new one for existing secrets too
			logger.Info("Secret exists, creating new auth key for replica", "replica", i)
			var err error
			authKey, err = r.createAuthKeyForReplica(ctx, tsClient, endpoints, connectionType)
			if err != nil {
				return fmt.Errorf("failed to create auth key for replica %d: %w", i, err)
			}
		}

		// Create hostname for this replica
		hostname := fmt.Sprintf("%s-%d", baseName, i)

		configs, err := r.createTailscaledConfigs(hostname, authKey, connectionType, endpoints)
		if err != nil {
			return fmt.Errorf("error creating tailscaled config for replica %d: %w", i, err)
		}

		if cfgSecret.Data == nil {
			cfgSecret.Data = make(map[string][]byte)
		}

		for filename, configJSON := range configs {
			cfgSecret.Data[filename] = configJSON
		}

		if existingCfgSecret != nil {
			if !controllerutil.ContainsFinalizer(existingCfgSecret, cfgSecret.Name) {
				logger.Info("Updating existing config secret", "name", cfgSecret.Name)
				if err := r.Update(ctx, cfgSecret); err != nil {
					return err
				}
			}
		} else {
			logger.Info("Creating new config secret", "name", cfgSecret.Name)
			if err := r.Create(ctx, cfgSecret); err != nil {
				return err
			}
		}
	}

	return nil
}

// createAuthKeyForReplica creates a new auth key for a specific replica
func (r *TailscaleEndpointsReconciler) createAuthKeyForReplica(ctx context.Context, tsClient tailscale.Client, endpoints *gatewayv1alpha1.TailscaleEndpoints, connectionType string) (string, error) {
	logger := log.FromContext(ctx)

	// Use tags from endpoints.Spec.Tags or default
	tags := endpoints.Spec.Tags
	if len(tags) == 0 {
		// Default tags including connection type
		tags = []string{"tag:k8s", fmt.Sprintf("tag:%s", connectionType)}
	}

	// Create auth key with the same pattern as existing methods
	capabilities := tailscaleclient.KeyCapabilities{}
	capabilities.Devices.Create.Reusable = false     // Each replica gets unique key
	capabilities.Devices.Create.Ephemeral = false    // Persistent for StatefulSet
	capabilities.Devices.Create.Preauthorized = true // Pre-authorize for automation
	capabilities.Devices.Create.Tags = tags

	logger.Info("Creating auth key for replica", "connectionType", connectionType, "tags", tags)
	keyResult, err := tsClient.CreateKey(ctx, capabilities)
	if err != nil {
		return "", fmt.Errorf("failed to create auth key: %w", err)
	}

	return keyResult.Key, nil
}

// ensureStateSecretsCreated creates state secrets per replica following ProxyGroup pattern
func (r *TailscaleEndpointsReconciler) ensureStateSecretsCreated(ctx context.Context, endpoints *gatewayv1alpha1.TailscaleEndpoints, baseName string, replicas int32, labels map[string]string) error {
	logger := log.FromContext(ctx)

	for i := int32(0); i < replicas; i++ {
		stateSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("%s-%d", baseName, i), // e.g., "auto-web-service-proxies-egress-0"
				Namespace: endpoints.Namespace,
				Labels: map[string]string{
					"app.kubernetes.io/name":       "tailscale-proxy",
					"app.kubernetes.io/instance":   endpoints.Name,
					"app.kubernetes.io/component":  "state",
					"gateway.tailscale.com/type":   "secret",
					"gateway.tailscale.com/secret": "state",
				},
				OwnerReferences: []metav1.OwnerReference{
					*metav1.NewControllerRef(endpoints, gatewayv1alpha1.GroupVersion.WithKind("TailscaleEndpoints")),
				},
			},
			Data: map[string][]byte{},
		}

		if err := r.createOrUpdate(ctx, stateSecret, func() {
			// State secret is just a placeholder for Tailscale to store state
		}); err != nil {
			return fmt.Errorf("failed to create state secret for replica %d: %w", i, err)
		}

		logger.Info("Created state secret for replica", "name", stateSecret.Name, "replica", i)
	}

	return nil
}

// createStateSecret creates the Tailscale state secret
// Following k8s-operator state secret patterns
func (r *TailscaleEndpointsReconciler) createStateSecret(ctx context.Context, endpoints *gatewayv1alpha1.TailscaleEndpoints, secretName string, labels map[string]string) error {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: endpoints.Namespace,
			Labels:    labels,
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(endpoints, gatewayv1alpha1.GroupVersion.WithKind("TailscaleEndpoints")),
			},
		},
		Data: map[string][]byte{},
	}

	return r.createOrUpdate(ctx, secret, func() {
		// State secret is just a placeholder for Tailscale to store state
	})
}

// createEndpointStatefulSet creates the StatefulSet for an endpoint connection
// Following k8s-operator ProxyGroup patterns with per-replica config secrets
func (r *TailscaleEndpointsReconciler) createEndpointStatefulSet(ctx context.Context, endpoints *gatewayv1alpha1.TailscaleEndpoints, endpoint *gatewayv1alpha1.TailscaleEndpoint, name, connectionType string, replicas int32, labels map[string]string) error {
	// Use official Tailscale image
	image := "tailscale/tailscale:v1.78.3"

	// Build environment variables following EXACT ProxyGroup patterns
	envVars := []corev1.EnvVar{
		// Standard Pod metadata (required FIRST for variable expansion)
		{
			Name: "POD_NAME",
			ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{
					FieldPath: "metadata.name",
				},
			},
		},
		// CRITICAL: ProxyGroup state management pattern
		{
			Name:  "TS_KUBE_SECRET",
			Value: "$(POD_NAME)", // Points to pod-specific state secret
		},
		{
			Name:  "TS_STATE",
			Value: "kube:$(POD_NAME)", // Each pod uses its own state secret
		},
		{
			Name:  "TS_EXPERIMENTAL_VERSIONED_CONFIG_DIR",
			Value: "/etc/tsconfig/$(POD_NAME)", // Each pod reads its own config
		},
		{
			Name: "POD_UID",
			ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{
					FieldPath: "metadata.uid",
				},
			},
		},
		// Userspace mode for Kubernetes (following ProxyGroup pattern)
		{
			Name:  "TS_USERSPACE",
			Value: "true",
		},
	}

	// Note: When using TS_EXPERIMENTAL_VERSIONED_CONFIG_DIR, all other config
	// comes from the config files, not environment variables

	// Build volumes for each replica config (following ProxyGroup pattern)
	volumes := []corev1.Volume{}
	volumeMounts := []corev1.VolumeMount{}

	for i := int32(0); i < replicas; i++ {
		volumes = append(volumes, corev1.Volume{
			Name: fmt.Sprintf("tailscaledconfig-%d", i),
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: fmt.Sprintf("%s-%d-config", name, i),
				},
			},
		})

		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name:      fmt.Sprintf("tailscaledconfig-%d", i),
			ReadOnly:  true,
			MountPath: fmt.Sprintf("/etc/tsconfig/%s-%d", name, i),
		})
	}

	ss := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: endpoints.Namespace,
			Labels:    labels,
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(endpoints, gatewayv1alpha1.GroupVersion.WithKind("TailscaleEndpoints")),
			},
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: name,
					Containers: []corev1.Container{
						{
							Name:         "tailscale",
							Image:        image,
							Env:          envVars,
							VolumeMounts: volumeMounts,
							SecurityContext: &corev1.SecurityContext{
								Capabilities: &corev1.Capabilities{
									Add: []corev1.Capability{"NET_ADMIN"},
								},
							},
						},
					},
					Volumes: volumes,
				},
			},
		},
	}

	return r.createOrUpdate(ctx, ss, func() {
		// Update the StatefulSet spec
		ss.Spec.Replicas = &replicas
		ss.Spec.Template.Spec.Containers[0].Env = envVars
		ss.Spec.Template.Spec.Containers[0].VolumeMounts = volumeMounts
		ss.Spec.Template.Spec.Volumes = volumes
	})
}

// shouldPublishVIPService determines if an endpoint should be published as a VIP service
func (r *TailscaleEndpointsReconciler) shouldPublishVIPService(endpoint *gatewayv1alpha1.TailscaleEndpoint) bool {
	// Check if endpoint has VIP-related tags or is configured as a service
	for _, tag := range endpoint.Tags {
		if strings.HasPrefix(tag, "svc:") || strings.Contains(tag, "vip") || strings.Contains(tag, "service") {
			return true
		}
	}

	// Check if endpoint has an external target (indicating it's a backend service)
	return endpoint.ExternalTarget != ""
}

// getVolumeMounts returns volume mounts for the StatefulSet container
// Following EXACT ProxyGroup patterns from Tailscale k8s-operator
func (r *TailscaleEndpointsReconciler) getVolumeMounts(connectionType string, endpoint *gatewayv1alpha1.TailscaleEndpoint, endpoints *gatewayv1alpha1.TailscaleEndpoints) []corev1.VolumeMount {
	var mounts []corev1.VolumeMount

	// CRITICAL: ProxyGroup config mount pattern - mount config secret to tailnet-specific path
	mounts = append(mounts, corev1.VolumeMount{
		Name:      fmt.Sprintf("tailscaledconfig-%s", r.sanitizeTailnetName(endpoints.Spec.Tailnet)),
		MountPath: fmt.Sprintf("/etc/tsconfig/%s", endpoints.Spec.Tailnet),
		ReadOnly:  true,
	})

	// Add serve config mount for VIP services
	if connectionType == "ingress" && r.shouldPublishVIPService(endpoint) {
		mounts = append(mounts, corev1.VolumeMount{
			Name:      "serve-config",
			MountPath: "/etc/tailscale",
			ReadOnly:  true,
		})
	}

	return mounts
}

// getVolumes returns volumes for the StatefulSet Pod
// Following EXACT ProxyGroup patterns from Tailscale k8s-operator
func (r *TailscaleEndpointsReconciler) getVolumes(connectionType string, endpoint *gatewayv1alpha1.TailscaleEndpoint, name string, endpoints *gatewayv1alpha1.TailscaleEndpoints) []corev1.Volume {
	var volumes []corev1.Volume

	// CRITICAL: ProxyGroup config volume pattern - secret mount for tailnet config
	volumes = append(volumes, corev1.Volume{
		Name: fmt.Sprintf("tailscaledconfig-%s", r.sanitizeTailnetName(endpoints.Spec.Tailnet)),
		VolumeSource: corev1.VolumeSource{
			Secret: &corev1.SecretVolumeSource{
				SecretName: name + "-config",
			},
		},
	})

	// Add serve config volume for VIP services
	if connectionType == "ingress" && r.shouldPublishVIPService(endpoint) {
		volumes = append(volumes, corev1.Volume{
			Name: "serve-config",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: name + "-serve-config",
					},
				},
			},
		})
	}

	return volumes
}

// sanitizeTailnetName sanitizes tailnet names for use in Kubernetes resource names
// Following ProxyGroup patterns for consistent naming
func (r *TailscaleEndpointsReconciler) sanitizeTailnetName(tailnet string) string {
	// Replace invalid characters with hyphens for Kubernetes naming
	sanitized := strings.ReplaceAll(tailnet, ".", "-")
	sanitized = strings.ReplaceAll(sanitized, "_", "-")
	sanitized = strings.ReplaceAll(sanitized, "@", "-")
	return strings.ToLower(sanitized)
}

// createServeConfigMap creates a ConfigMap with Tailscale serve configuration for VIP service publishing
func (r *TailscaleEndpointsReconciler) createServeConfigMap(ctx context.Context, endpoints *gatewayv1alpha1.TailscaleEndpoints, endpoint *gatewayv1alpha1.TailscaleEndpoint, name string, labels map[string]string) error {
	// Generate VIP service name from endpoint
	serviceName := endpoint.Name
	if endpoint.ExternalTarget != "" {
		// Use clean service name for VIP
		serviceName = strings.Split(endpoint.ExternalTarget, ".")[0]
	}

	// Create Tailscale serve config for VIP service publishing
	serveConfig := map[string]interface{}{
		"TCP": map[string]interface{}{
			fmt.Sprintf("%d", endpoint.Port): map[string]interface{}{
				"HTTP":  endpoint.Protocol == "HTTP",
				"HTTPS": endpoint.Protocol == "HTTPS",
			},
		},
	}

	// Add web handlers for HTTP/HTTPS
	if endpoint.Protocol == "HTTP" || endpoint.Protocol == "HTTPS" {
		hostPort := fmt.Sprintf("%s.%s:%d", serviceName, endpoints.Spec.Tailnet, endpoint.Port)
		if endpoint.Protocol == "HTTPS" {
			hostPort = fmt.Sprintf("${TS_CERT_DOMAIN}:%d", endpoint.Port)
		}

		target := endpoint.ExternalTarget
		if target == "" {
			target = fmt.Sprintf("http://localhost:%d", endpoint.Port)
		} else if !strings.HasPrefix(target, "http") {
			target = fmt.Sprintf("http://%s", target)
		}

		serveConfig["Web"] = map[string]interface{}{
			hostPort: map[string]interface{}{
				"Handlers": map[string]interface{}{
					"/": map[string]interface{}{
						"Proxy": target,
					},
				},
			},
		}
	}

	// Marshal to JSON
	configJSON, err := json.Marshal(serveConfig)
	if err != nil {
		return fmt.Errorf("failed to marshal serve config: %w", err)
	}

	// Create ConfigMap
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name + "-serve-config",
			Namespace: endpoints.Namespace,
			Labels:    labels,
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(endpoints, gatewayv1alpha1.GroupVersion.WithKind("TailscaleEndpoints")),
			},
		},
		Data: map[string]string{
			"serve-config.json": string(configJSON),
		},
	}

	return r.createOrUpdate(ctx, configMap, func() {
		configMap.Data = map[string]string{
			"serve-config.json": string(configJSON),
		}
	})
}

// createOrUpdate creates or updates a Kubernetes resource
func (r *TailscaleEndpointsReconciler) createOrUpdate(ctx context.Context, obj client.Object, updateFn func()) error {
	key := client.ObjectKeyFromObject(obj)
	existing := obj.DeepCopyObject().(client.Object)

	err := r.Get(ctx, key, existing)
	if errors.IsNotFound(err) {
		return r.Create(ctx, obj)
	} else if err != nil {
		return err
	}

	// Resource exists, create a copy and apply updates
	updated := existing.DeepCopyObject().(client.Object)

	// Apply the update function to the copy
	originalObj := obj
	defer func() {
		// Restore original object
		obj = originalObj
	}()
	obj = updated
	updateFn()

	// Only update if something actually changed
	if !r.objectsEqual(existing, updated) {
		return r.Update(ctx, updated)
	}

	return nil
}

// objectsEqual compares two Kubernetes objects for meaningful differences
func (r *TailscaleEndpointsReconciler) objectsEqual(existing, updated client.Object) bool {
	// For StatefulSets, compare the important spec fields
	if existingSS, ok := existing.(*appsv1.StatefulSet); ok {
		if updatedSS, ok := updated.(*appsv1.StatefulSet); ok {
			return r.statefulSetsEqual(existingSS, updatedSS)
		}
	}

	// For other objects, use a simple approach comparing resource versions
	return existing.GetResourceVersion() == updated.GetResourceVersion()
}

// statefulSetsEqual compares StatefulSets for meaningful spec differences
func (r *TailscaleEndpointsReconciler) statefulSetsEqual(existing, updated *appsv1.StatefulSet) bool {
	// Compare replicas
	if *existing.Spec.Replicas != *updated.Spec.Replicas {
		return false
	}

	// Compare environment variables (main source of changes)
	existingEnv := existing.Spec.Template.Spec.Containers[0].Env
	updatedEnv := updated.Spec.Template.Spec.Containers[0].Env

	if len(existingEnv) != len(updatedEnv) {
		return false
	}

	for i, env := range existingEnv {
		if env.Name != updatedEnv[i].Name || env.Value != updatedEnv[i].Value {
			return false
		}
		// Also compare ValueFrom if present
		if (env.ValueFrom == nil) != (updatedEnv[i].ValueFrom == nil) {
			return false
		}
	}

	// Compare volumes
	existingVolumes := existing.Spec.Template.Spec.Volumes
	updatedVolumes := updated.Spec.Template.Spec.Volumes

	if len(existingVolumes) != len(updatedVolumes) {
		return false
	}

	for i, vol := range existingVolumes {
		if vol.Name != updatedVolumes[i].Name {
			return false
		}
	}

	// Compare volume mounts
	existingMounts := existing.Spec.Template.Spec.Containers[0].VolumeMounts
	updatedMounts := updated.Spec.Template.Spec.Containers[0].VolumeMounts

	if len(existingMounts) != len(updatedMounts) {
		return false
	}

	for i, mount := range existingMounts {
		if mount.Name != updatedMounts[i].Name || mount.MountPath != updatedMounts[i].MountPath {
			return false
		}
	}

	return true
}

// handleDeletion handles cleanup when TailscaleEndpoints is being deleted
func (r *TailscaleEndpointsReconciler) handleDeletion(ctx context.Context, endpoints *gatewayv1alpha1.TailscaleEndpoints) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	if controllerutil.ContainsFinalizer(endpoints, TailscaleEndpointsFinalizer) {
		// Perform cleanup of StatefulSets and associated resources
		logger.Info("Cleaning up TailscaleEndpoints resources", "endpoints", endpoints.Name)

		// Cleanup will be handled by owner references, but could add explicit cleanup here
		// for Tailscale devices if needed

		// Remove finalizer
		controllerutil.RemoveFinalizer(endpoints, TailscaleEndpointsFinalizer)
		if err := r.Update(ctx, endpoints); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

// updateCondition updates or adds a condition to the TailscaleEndpoints status
// ReconcilerMetrics tracks reconciliation performance
type ReconcilerMetrics struct {
	ReconcileCount       int64
	SuccessfulReconciles int64
	FailedReconciles     int64
	APICallCount         int64
	LastReconcileTime    time.Time
	TotalReconcileTime   time.Duration
}

// recordError records a detailed error in the status
func (r *TailscaleEndpointsReconciler) recordError(endpoints *gatewayv1alpha1.TailscaleEndpoints, code, message, component string, err error) {
	detailedError := gatewayv1alpha1.DetailedError{
		Code:      code,
		Message:   message,
		Component: component,
		Timestamp: metav1.Now(),
		Severity:  gatewayv1alpha1.SeverityHigh,
	}

	if err != nil {
		detailedError.Context = map[string]string{
			"error": err.Error(),
		}
	}

	gatewayv1alpha1.AddError(&endpoints.Status.RecentErrors, detailedError, 10)
}

// updateOperationalMetrics updates the operational metrics in the status
func (r *TailscaleEndpointsReconciler) updateOperationalMetrics(endpoints *gatewayv1alpha1.TailscaleEndpoints, duration time.Duration, success bool) {
	if endpoints.Status.OperationalMetrics == nil {
		endpoints.Status.OperationalMetrics = &gatewayv1alpha1.OperationalMetrics{}
	}

	metrics := endpoints.Status.OperationalMetrics
	metrics.LastReconcileTime = metav1.Now()
	metrics.ReconcileDuration = &metav1.Duration{Duration: duration}
	metrics.ReconcileCount++

	if success {
		metrics.SuccessfulReconciles++
	} else {
		metrics.FailedReconciles++
	}

	// Update average reconcile time
	r.metrics.TotalReconcileTime += duration
	avgDuration := r.metrics.TotalReconcileTime / time.Duration(r.metrics.ReconcileCount)
	metrics.AverageReconcileTime = &metav1.Duration{Duration: avgDuration}

	// Calculate error rate
	if metrics.ReconcileCount > 0 {
		errorRate := float64(metrics.FailedReconciles) / float64(metrics.ReconcileCount) * 100
		errorRateStr := fmt.Sprintf("%.1f%%", errorRate)
		metrics.ErrorRate = &errorRateStr
	}

	// Update metrics
	if r.metrics != nil {
		metrics.APICallCount = r.metrics.APICallCount
	}
}

// updateAPIStatus updates the Tailscale API status in the endpoints
func (r *TailscaleEndpointsReconciler) updateAPIStatus(endpoints *gatewayv1alpha1.TailscaleEndpoints, connected bool, err error) {
	if endpoints.Status.AutoDiscoveryStatus == nil {
		endpoints.Status.AutoDiscoveryStatus = &gatewayv1alpha1.AutoDiscoveryStatus{}
	}

	if endpoints.Status.AutoDiscoveryStatus.APIStatus == nil {
		endpoints.Status.AutoDiscoveryStatus.APIStatus = &gatewayv1alpha1.TailscaleAPIStatus{}
	}

	apiStatus := endpoints.Status.AutoDiscoveryStatus.APIStatus
	apiStatus.Connected = connected

	if connected {
		apiStatus.LastSuccessfulCall = &metav1.Time{Time: time.Now()}
		// Clear any previous error
		apiStatus.LastError = nil
	} else if err != nil {
		// Record the error
		detailedError := &gatewayv1alpha1.DetailedError{
			Code:      gatewayv1alpha1.ErrorCodeAPIConnection,
			Message:   err.Error(),
			Component: gatewayv1alpha1.ComponentTailscaleAPI,
			Timestamp: metav1.Now(),
			Severity:  gatewayv1alpha1.SeverityHigh,
		}
		apiStatus.LastError = detailedError
	}

	// Update API metrics
	if r.metrics != nil {
		apiStatus.RequestCount = r.metrics.APICallCount
	}
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

// reconcileEndpointServices creates ClusterIP Services for ingress and egress StatefulSets
// Following k8s-operator service creation patterns for backend connectivity
func (r *TailscaleEndpointsReconciler) reconcileEndpointServices(ctx context.Context, endpoints *gatewayv1alpha1.TailscaleEndpoints, endpoint *gatewayv1alpha1.TailscaleEndpoint, ingressRef, egressRef *gatewayv1alpha1.StatefulSetReference) error {
	logger := log.FromContext(ctx)

	// Create service for ingress StatefulSet (external → tailscale)
	if err := r.createEndpointService(ctx, endpoints, endpoint, ingressRef, "ingress"); err != nil {
		return fmt.Errorf("failed to create ingress service: %w", err)
	}

	// Create service for egress StatefulSet (tailscale → external)
	if err := r.createEndpointService(ctx, endpoints, endpoint, egressRef, "egress"); err != nil {
		return fmt.Errorf("failed to create egress service: %w", err)
	}

	logger.Info("Successfully reconciled endpoint services", "endpoint", endpoint.Name)
	return nil
}

// createEndpointService creates a ClusterIP Service for a StatefulSet
// Following k8s-operator service patterns with proper labeling for discovery
func (r *TailscaleEndpointsReconciler) createEndpointService(ctx context.Context, endpoints *gatewayv1alpha1.TailscaleEndpoints, endpoint *gatewayv1alpha1.TailscaleEndpoint, ssRef *gatewayv1alpha1.StatefulSetReference, connectionType string) error {
	serviceName := ssRef.Name + "-service"

	// Labels for service discovery by extension server
	labels := map[string]string{
		"app.kubernetes.io/name":         "tailscale-endpoint-service",
		"app.kubernetes.io/instance":     endpoints.Name,
		"app.kubernetes.io/component":    connectionType,
		"app.kubernetes.io/managed-by":   "tailscale-gateway-operator",
		"gateway.tailscale.com/endpoint": endpoint.Name,
		"gateway.tailscale.com/type":     connectionType,
		"gateway.tailscale.com/tailnet":  endpoints.Spec.Tailnet,
	}

	// Annotations for additional metadata
	annotations := map[string]string{
		"gateway.tailscale.com/statefulset":    ssRef.Name,
		"gateway.tailscale.com/endpoint-name":  endpoint.Name,
		"gateway.tailscale.com/protocol":       endpoint.Protocol,
		"gateway.tailscale.com/tailscale-ip":   endpoint.TailscaleIP,
		"gateway.tailscale.com/tailscale-fqdn": endpoint.TailscaleFQDN,
	}

	// Add external target annotation for egress services
	if connectionType == "egress" && endpoint.ExternalTarget != "" {
		annotations["gateway.tailscale.com/external-target"] = endpoint.ExternalTarget
	}

	// Determine service port - use endpoint port or default based on protocol
	servicePort := endpoint.Port
	if servicePort == 0 {
		if endpoint.Protocol == "HTTPS" {
			servicePort = 443
		} else {
			servicePort = 80
		}
	}

	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:        serviceName,
			Namespace:   endpoints.Namespace,
			Labels:      labels,
			Annotations: annotations,
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(endpoints, gatewayv1alpha1.GroupVersion.WithKind("TailscaleEndpoints")),
			},
		},
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeClusterIP,
			Ports: []corev1.ServicePort{
				{
					Name:     strings.ToLower(endpoint.Protocol),
					Port:     servicePort,
					Protocol: corev1.ProtocolTCP,
				},
			},
			Selector: map[string]string{
				"app.kubernetes.io/name":         "tailscale-endpoint",
				"app.kubernetes.io/instance":     endpoints.Name,
				"app.kubernetes.io/component":    connectionType,
				"gateway.tailscale.com/endpoint": endpoint.Name,
				"gateway.tailscale.com/type":     connectionType,
			},
		},
	}

	return r.createOrUpdate(ctx, service, func() {
		service.Labels = labels
		service.Annotations = annotations
		service.Spec.Ports = []corev1.ServicePort{
			{
				Name:     strings.ToLower(endpoint.Protocol),
				Port:     servicePort,
				Protocol: corev1.ProtocolTCP,
			},
		}
	})
}

// performLocalServiceDiscovery discovers local Kubernetes services for bidirectional exposure
func (r *TailscaleEndpointsReconciler) performLocalServiceDiscovery(ctx context.Context, endpoints *gatewayv1alpha1.TailscaleEndpoints) error {
	logger := log.FromContext(ctx)

	// List all services in the same namespace
	serviceList := &corev1.ServiceList{}
	if err := r.List(ctx, serviceList, client.InNamespace(endpoints.Namespace)); err != nil {
		return fmt.Errorf("failed to list services for local discovery: %w", err)
	}

	var localEndpoints []gatewayv1alpha1.TailscaleEndpoint
	for _, svc := range serviceList.Items {
		// Skip system services and services that are already referenced
		if r.shouldSkipService(&svc) {
			continue
		}

		// Check if this service is already configured in endpoints
		if r.isServiceAlreadyConfigured(&svc, endpoints) {
			continue
		}

		// Create TailscaleEndpoint for this local service
		for _, port := range svc.Spec.Ports {
			endpoint := gatewayv1alpha1.TailscaleEndpoint{
				Name:           fmt.Sprintf("local-%s-%s", svc.Name, port.Name),
				TailscaleIP:    "", // Will be assigned when VIP service is created
				TailscaleFQDN:  fmt.Sprintf("%s.%s.svc.cluster.local", svc.Name, svc.Namespace),
				Port:           port.Port,
				Protocol:       strings.ToUpper(string(port.Protocol)),
				Tags:           []string{"tag:local-service", "tag:k8s-service"},
				ExternalTarget: "", // This is for external targets, local services don't need this
				Weight:         &[]int32{1}[0],
			}

			// Add health check configuration for local services
			endpoint.HealthCheck = &gatewayv1alpha1.EndpointHealthCheck{
				Enabled:            true,
				Path:               "/health",
				Interval:           &metav1.Duration{Duration: 30 * time.Second},
				Timeout:            &metav1.Duration{Duration: 5 * time.Second},
				HealthyThreshold:   &[]int32{2}[0],
				UnhealthyThreshold: &[]int32{3}[0],
			}

			localEndpoints = append(localEndpoints, endpoint)
		}
	}

	// Add discovered local services to the endpoints spec
	if len(localEndpoints) > 0 {
		// Merge with existing endpoints
		existingEndpoints := endpoints.Spec.Endpoints
		mergedEndpoints := r.mergeEndpoints(existingEndpoints, localEndpoints)
		endpoints.Spec.Endpoints = mergedEndpoints

		logger.Info("Discovered local services for bidirectional exposure",
			"namespace", endpoints.Namespace,
			"discoveredServices", len(localEndpoints),
			"totalEndpoints", len(mergedEndpoints))
	}

	return nil
}

// shouldSkipService determines if a service should be skipped during local discovery
func (r *TailscaleEndpointsReconciler) shouldSkipService(svc *corev1.Service) bool {
	// Skip system services
	if strings.HasPrefix(svc.Namespace, "kube-") || svc.Namespace == "default" {
		if svc.Name == "kubernetes" || strings.HasPrefix(svc.Name, "kube-") {
			return true
		}
	}

	// Skip headless services
	if svc.Spec.ClusterIP == "None" {
		return true
	}

	// Skip services with no ports
	if len(svc.Spec.Ports) == 0 {
		return true
	}

	// Skip services that are marked to be excluded from discovery
	if excludeAnnotation, exists := svc.Annotations["gateway.tailscale.com/exclude-from-discovery"]; exists {
		if excludeAnnotation == "true" {
			return true
		}
	}

	return false
}

// isServiceAlreadyConfigured checks if a service is already configured in the endpoints
func (r *TailscaleEndpointsReconciler) isServiceAlreadyConfigured(svc *corev1.Service, endpoints *gatewayv1alpha1.TailscaleEndpoints) bool {
	for _, endpoint := range endpoints.Spec.Endpoints {
		// Check if the endpoint name matches or if the FQDN matches
		if strings.Contains(endpoint.Name, svc.Name) {
			return true
		}
		if strings.Contains(endpoint.TailscaleFQDN, svc.Name) {
			return true
		}
		// Check if ExternalTarget points to this service
		if strings.Contains(endpoint.ExternalTarget, svc.Name) {
			return true
		}
	}
	return false
}

// updateDeviceStatus updates the device status fields following ProxyGroup patterns from official Tailscale k8s-operator
func (r *TailscaleEndpointsReconciler) updateDeviceStatus(ctx context.Context, endpoints *gatewayv1alpha1.TailscaleEndpoints) error {
	logger := log.FromContext(ctx)

	// Skip device status update if TailscaleClientManager is not available (e.g., in tests)
	if r.TailscaleClientManager == nil {
		logger.Info("TailscaleClientManager not available, skipping device status update")
		return nil
	}

	// Get Tailscale client for this tailnet
	tsClient, err := r.TailscaleClientManager.GetClient(ctx, r.Client, endpoints.Spec.Tailnet, endpoints.Namespace)
	if err != nil {
		return fmt.Errorf("failed to get Tailscale client for device status update: %w", err)
	}

	var devices []gatewayv1alpha1.TailscaleDevice
	var statefulSets []gatewayv1alpha1.StatefulSetInfo

	// Update StatefulSet status
	if err := r.updateStatefulSetStatus(ctx, endpoints, &statefulSets); err != nil {
		logger.Error(err, "Failed to update StatefulSet status")
		// Continue with device status update even if StatefulSet status fails
	}

	// Extract NodeIDs from state secrets and get device information
	if err := r.extractDevicesFromStateSecrets(ctx, endpoints, tsClient, &devices); err != nil {
		logger.Error(err, "Failed to extract devices from state secrets")
		// Continue with partial status update
	}

	// Update the status
	endpoints.Status.Devices = devices
	endpoints.Status.StatefulSets = statefulSets

	logger.Info("Updated device status",
		"endpoints", endpoints.Name,
		"devices", len(devices),
		"statefulSets", len(statefulSets))

	return nil
}

// updateStatefulSetStatus collects information about created StatefulSets
func (r *TailscaleEndpointsReconciler) updateStatefulSetStatus(ctx context.Context, endpoints *gatewayv1alpha1.TailscaleEndpoints, statefulSets *[]gatewayv1alpha1.StatefulSetInfo) error {
	// List all StatefulSets with labels matching this resource
	statefulSetList := &appsv1.StatefulSetList{}
	labelSelector := client.MatchingLabels{
		"app.kubernetes.io/managed-by":  "tailscale-gateway-operator",
		"tailscale.com/parent-resource": endpoints.Name,
	}

	if err := r.List(ctx, statefulSetList, client.InNamespace(endpoints.Namespace), labelSelector); err != nil {
		return fmt.Errorf("failed to list StatefulSets: %w", err)
	}

	for _, sts := range statefulSetList.Items {
		// Determine StatefulSet type from labels
		connectionType := "unknown"
		if sts.Labels["tailscale.com/connection-type"] != "" {
			connectionType = sts.Labels["tailscale.com/connection-type"]
		}

		// Find associated Service
		serviceName := ""
		serviceList := &corev1.ServiceList{}
		serviceSelector := client.MatchingLabels{
			"app.kubernetes.io/managed-by": "tailscale-gateway-operator",
			"tailscale.com/statefulset":    sts.Name,
		}
		if err := r.List(ctx, serviceList, client.InNamespace(endpoints.Namespace), serviceSelector); err == nil && len(serviceList.Items) > 0 {
			serviceName = serviceList.Items[0].Name
		}

		info := gatewayv1alpha1.StatefulSetInfo{
			Name:          sts.Name,
			Namespace:     sts.Namespace,
			Type:          connectionType,
			Replicas:      *sts.Spec.Replicas,
			ReadyReplicas: sts.Status.ReadyReplicas,
			Service:       serviceName,
			CreatedAt:     &sts.CreationTimestamp,
		}

		*statefulSets = append(*statefulSets, info)
	}

	return nil
}

// extractDevicesFromStateSecrets extracts NodeIDs from state secrets and fetches device info from Tailscale API
func (r *TailscaleEndpointsReconciler) extractDevicesFromStateSecrets(ctx context.Context, endpoints *gatewayv1alpha1.TailscaleEndpoints, tsClient tailscale.Client, devices *[]gatewayv1alpha1.TailscaleDevice) error {
	logger := log.FromContext(ctx)

	// List all secrets with labels matching this resource
	secretList := &corev1.SecretList{}
	labelSelector := client.MatchingLabels{
		"app.kubernetes.io/managed-by":  "tailscale-gateway-operator",
		"tailscale.com/parent-resource": endpoints.Name,
		"tailscale.com/secret-type":     "state",
	}

	if err := r.List(ctx, secretList, client.InNamespace(endpoints.Namespace), labelSelector); err != nil {
		return fmt.Errorf("failed to list state secrets: %w", err)
	}

	for _, secret := range secretList.Items {
		// Extract NodeID from state secret data (following ProxyGroup pattern)
		nodeID, err := r.extractNodeIDFromStateSecret(&secret)
		if err != nil {
			logger.Error(err, "Failed to extract NodeID from state secret", "secret", secret.Name)
			continue
		}

		if nodeID == "" {
			// Secret exists but no NodeID yet (device not connected)
			logger.Info("State secret has no NodeID yet", "secret", secret.Name)
			continue
		}

		// Get device information from Tailscale API
		device, err := r.getDeviceFromTailscaleAPI(ctx, tsClient, nodeID)
		if err != nil {
			logger.Error(err, "Failed to get device from Tailscale API", "nodeID", nodeID)
			continue
		}

		// Convert to TailscaleDevice with StatefulSet pod mapping
		tailscaleDevice := r.convertToTailscaleDevice(device, &secret)
		*devices = append(*devices, tailscaleDevice)
	}

	return nil
}

// extractNodeIDFromStateSecret extracts the NodeID from a Tailscale state secret
// Following the same pattern as ProxyGroup in official Tailscale k8s-operator
func (r *TailscaleEndpointsReconciler) extractNodeIDFromStateSecret(secret *corev1.Secret) (string, error) {
	// The state secret contains various keys, we need to find the NodeID
	// This follows the same pattern as in ProxyGroup status updates

	// Look for the NodeKey or NodeID in the secret data
	if data, exists := secret.Data["_daemon.json"]; exists {
		// Parse the daemon state to extract NodeID
		var daemonState map[string]interface{}
		if err := json.Unmarshal(data, &daemonState); err == nil {
			if nodeData, ok := daemonState["NetMap"].(map[string]interface{}); ok {
				if nodeKey, ok := nodeData["NodeKey"].(string); ok && nodeKey != "" {
					// Convert NodeKey to NodeID format if needed
					return nodeKey, nil
				}
			}
		}
	}

	// Alternative: look for other state keys that might contain node information
	for key, data := range secret.Data {
		if strings.Contains(key, "node") || strings.Contains(key, "NodeKey") {
			// This might contain node information
			if len(data) > 0 {
				return string(data), nil
			}
		}
	}

	// No NodeID found (device may not be connected yet)
	return "", nil
}

// getDeviceFromTailscaleAPI fetches device information from the Tailscale API
func (r *TailscaleEndpointsReconciler) getDeviceFromTailscaleAPI(ctx context.Context, tsClient tailscale.Client, nodeID string) (*tailscaleclient.Device, error) {
	// Use the Tailscale client to get all devices, then find the one we want
	devices, err := tsClient.Devices(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get devices from Tailscale API: %w", err)
	}

	// Find the device with matching NodeID
	for i := range devices {
		if devices[i].NodeID == nodeID {
			return &devices[i], nil
		}
	}

	return nil, fmt.Errorf("device with NodeID %s not found", nodeID)
}

// convertToTailscaleDevice converts a Tailscale API device to our TailscaleDevice status type
func (r *TailscaleEndpointsReconciler) convertToTailscaleDevice(device *tailscaleclient.Device, secret *corev1.Secret) gatewayv1alpha1.TailscaleDevice {
	// Extract StatefulSet pod name from secret labels
	podName := ""
	if secret.Labels["tailscale.com/statefulset-pod"] != "" {
		podName = secret.Labels["tailscale.com/statefulset-pod"]
	}

	var tailscaleIP string
	var tailscaleFQDN string

	// Extract primary IP address
	if len(device.Addresses) > 0 {
		tailscaleIP = device.Addresses[0]
	}

	// Build FQDN from hostname and tailnet
	if device.Name != "" {
		// Extract tailnet from device's FQDN if available
		if strings.Contains(device.Name, ".") {
			tailscaleFQDN = device.Name
		} else {
			// Construct FQDN if we have enough information
			tailscaleFQDN = device.Name
		}
	}

	var lastSeen *metav1.Time
	if !device.LastSeen.Time.IsZero() {
		lastSeen = &metav1.Time{Time: device.LastSeen.Time}
	}

	// Determine if device is online based on connection status
	// A device is considered online if it was authorized and recently seen
	isOnline := device.Authorized && lastSeen != nil

	return gatewayv1alpha1.TailscaleDevice{
		Hostname:       device.Name,
		TailscaleIP:    tailscaleIP,
		TailscaleFQDN:  tailscaleFQDN,
		NodeID:         device.NodeID,
		StatefulSetPod: podName,
		Connected:      isOnline,
		Online:         isOnline,
		LastSeen:       lastSeen,
		Tags:           device.Tags,
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *TailscaleEndpointsReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&gatewayv1alpha1.TailscaleEndpoints{}).
		Complete(r)
}
