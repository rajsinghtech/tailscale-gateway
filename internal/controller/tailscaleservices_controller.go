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
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	gatewayv1alpha1 "github.com/rajsinghtech/tailscale-gateway/api/v1alpha1"
	tsservice "github.com/rajsinghtech/tailscale-gateway/internal/service"
	"github.com/rajsinghtech/tailscale-gateway/internal/tailscale"
	"tailscale.com/ipn"
)

const (
	// TailscaleServicesFinalizer is the finalizer for TailscaleServices resources
	TailscaleServicesFinalizer = "tailscaleservices.gateway.tailscale.com/finalizer"

	// RequeueAfterError is the duration to wait before re-queuing on errors
	RequeueAfterError = 30 * time.Second

	// RequeueAfterSuccess is the duration to wait for normal re-queuing
	RequeueAfterSuccess = 5 * time.Minute
)

// TailscaleServicesReconciler reconciles a TailscaleServices object
type TailscaleServicesReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Logger *zap.SugaredLogger

	// ServiceCoordinator manages VIP service coordination
	ServiceCoordinator *tsservice.ServiceCoordinator

	// TailscaleClientManager provides Tailscale API clients
	TailscaleClientManager *tailscale.MultiTailnetManager

	// EventChan for notifying other controllers
	EventChan chan<- ControllerEvent
}

// ControllerEvent represents an event from the TailscaleServices controller
type ControllerEvent struct {
	Type        string
	Object      client.Object
	Reason      string
	Message     string
	RelatedName string
}

//+kubebuilder:rbac:groups=gateway.tailscale.com,resources=tailscaleservices,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=gateway.tailscale.com,resources=tailscaleservices/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=gateway.tailscale.com,resources=tailscaleservices/finalizers,verbs=update
//+kubebuilder:rbac:groups=gateway.tailscale.com,resources=tailscaleendpoints,verbs=get;list;watch;create;update;patch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *TailscaleServicesReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling TailscaleServices", "name", req.Name, "namespace", req.Namespace)

	// Fetch the TailscaleServices instance
	var service gatewayv1alpha1.TailscaleServices
	if err := r.Get(ctx, req.NamespacedName, &service); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("TailscaleServices resource not found, may have been deleted")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get TailscaleServices")
		return ctrl.Result{RequeueAfter: RequeueAfterError}, err
	}

	// Handle deletion
	if service.DeletionTimestamp != nil {
		return r.reconcileDelete(ctx, &service)
	}

	// Add finalizer if not present
	if !controllerutil.ContainsFinalizer(&service, TailscaleServicesFinalizer) {
		controllerutil.AddFinalizer(&service, TailscaleServicesFinalizer)
		if err := r.Update(ctx, &service); err != nil {
			logger.Error(err, "Failed to add finalizer")
			return ctrl.Result{RequeueAfter: RequeueAfterError}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// Initialize status if needed
	if service.Status.Conditions == nil {
		service.Status.Conditions = []metav1.Condition{}
	}

	// Reconcile the service
	result, err := r.reconcileService(ctx, &service)

	// Update status
	if statusErr := r.Status().Update(ctx, &service); statusErr != nil {
		logger.Error(statusErr, "Failed to update status")
		if err == nil {
			err = statusErr
		}
	}

	// Emit event if needed
	if r.EventChan != nil {
		select {
		case r.EventChan <- ControllerEvent{
			Type:        "TailscaleServicesUpdate",
			Object:      &service,
			Reason:      "Reconciled",
			Message:     fmt.Sprintf("TailscaleServices %s reconciled", service.Name),
			RelatedName: service.Name,
		}:
		default:
			// Non-blocking send
		}
	}

	return result, err
}

// reconcileService handles the main reconciliation logic
func (r *TailscaleServicesReconciler) reconcileService(ctx context.Context, service *gatewayv1alpha1.TailscaleServices) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Step 1: Select TailscaleEndpoints
	if err := r.reconcileEndpointSelection(ctx, service); err != nil {
		logger.Error(err, "Failed to reconcile endpoint selection")
		gatewayv1alpha1.SetCondition(&service.Status.Conditions, "EndpointsSelected", metav1.ConditionFalse, "SelectionFailed", err.Error())
		return ctrl.Result{RequeueAfter: RequeueAfterError}, err
	}
	gatewayv1alpha1.SetCondition(&service.Status.Conditions, "EndpointsSelected", metav1.ConditionTrue, "SelectionSuccessful", "Endpoints selected successfully")

	// Step 2: Ensure VIP service
	if err := r.reconcileVIPService(ctx, service); err != nil {
		logger.Error(err, "Failed to reconcile VIP service")
		gatewayv1alpha1.SetCondition(&service.Status.Conditions, "VIPServiceReady", metav1.ConditionFalse, "VIPServiceFailed", err.Error())
		return ctrl.Result{RequeueAfter: RequeueAfterError}, err
	}
	gatewayv1alpha1.SetCondition(&service.Status.Conditions, "VIPServiceReady", metav1.ConditionTrue, "VIPServiceCreated", "VIP service created successfully")

	// Step 3: Update proxy configurations to advertise the VIP service
	if err := r.reconcileProxyAdvertiseServices(ctx, service); err != nil {
		logger.Error(err, "Failed to update proxy AdvertiseServices configuration")
		gatewayv1alpha1.SetCondition(&service.Status.Conditions, "ProxyConfigReady", metav1.ConditionFalse, "ProxyConfigFailed", err.Error())
		return ctrl.Result{RequeueAfter: RequeueAfterError}, err
	}
	gatewayv1alpha1.SetCondition(&service.Status.Conditions, "ProxyConfigReady", metav1.ConditionTrue, "ProxyConfigUpdated", "Proxy configurations updated successfully")

	// Step 4: Register backends
	if err := r.reconcileBackendRegistration(ctx, service); err != nil {
		logger.Error(err, "Failed to reconcile backend registration")
		gatewayv1alpha1.SetCondition(&service.Status.Conditions, "BackendsRegistered", metav1.ConditionFalse, "RegistrationFailed", err.Error())
		return ctrl.Result{RequeueAfter: RequeueAfterError}, err
	}
	gatewayv1alpha1.SetCondition(&service.Status.Conditions, "BackendsRegistered", metav1.ConditionTrue, "RegistrationSuccessful", "Backends registered successfully")

	// Step 4: Update service health status
	if err := r.reconcileServiceHealth(ctx, service); err != nil {
		logger.Error(err, "Failed to reconcile service health")
		// Don't fail reconciliation on health check errors
	}

	// Update overall status
	service.Status.LastUpdate = &metav1.Time{Time: time.Now()}
	gatewayv1alpha1.SetCondition(&service.Status.Conditions, "Ready", metav1.ConditionTrue, "ServiceReady", "TailscaleServices is ready")

	logger.Info("Successfully reconciled TailscaleServices",
		"service", service.Name,
		"selectedEndpoints", len(service.Status.SelectedEndpoints),
		"totalBackends", service.Status.TotalBackends,
		"healthyBackends", service.Status.HealthyBackends)

	return ctrl.Result{RequeueAfter: RequeueAfterSuccess}, nil
}

// reconcileEndpointSelection selects TailscaleEndpoints based on the selector
func (r *TailscaleServicesReconciler) reconcileEndpointSelection(ctx context.Context, service *gatewayv1alpha1.TailscaleServices) error {
	logger := log.FromContext(ctx)

	if service.Spec.Selector == nil {
		return fmt.Errorf("selector is required")
	}

	// Convert label selector
	selector, err := metav1.LabelSelectorAsSelector(service.Spec.Selector)
	if err != nil {
		return fmt.Errorf("invalid selector: %w", err)
	}

	// List TailscaleEndpoints in the same namespace
	var endpointsList gatewayv1alpha1.TailscaleEndpointsList
	if err := r.List(ctx, &endpointsList, &client.ListOptions{
		Namespace:     service.Namespace,
		LabelSelector: selector,
	}); err != nil {
		return fmt.Errorf("failed to list TailscaleEndpoints: %w", err)
	}

	// Auto-provision TailscaleEndpoints if none found and template is provided
	if len(endpointsList.Items) == 0 && service.Spec.EndpointTemplate != nil {
		logger.Info("No endpoints found matching selector, auto-provisioning from template",
			"service", service.Name,
			"selector", service.Spec.Selector.String())

		if err := r.autoProvisionTailscaleEndpoint(ctx, service); err != nil {
			return fmt.Errorf("failed to auto-provision TailscaleEndpoint: %w", err)
		}

		// Re-list after creating the endpoint
		if err := r.List(ctx, &endpointsList, &client.ListOptions{
			Namespace:     service.Namespace,
			LabelSelector: selector,
		}); err != nil {
			return fmt.Errorf("failed to re-list TailscaleEndpoints after auto-provisioning: %w", err)
		}
	}

	// Build selected endpoints status
	var selectedEndpoints []gatewayv1alpha1.SelectedEndpoint
	now := metav1.NewTime(time.Now())

	for _, endpoint := range endpointsList.Items {
		// Extract machine names from status
		var machines []string
		for _, statefulSetRef := range endpoint.Status.StatefulSetRefs {
			machines = append(machines, statefulSetRef.Name)
		}

		// Determine health status
		healthStatus := &gatewayv1alpha1.EndpointHealthStatus{
			HealthyMachines:   endpoint.Status.HealthyEndpoints,
			UnhealthyMachines: endpoint.Status.UnhealthyEndpoints,
			LastHealthCheck:   endpoint.Status.LastSync,
		}

		selectedEndpoint := gatewayv1alpha1.SelectedEndpoint{
			Name:         endpoint.Name,
			Namespace:    endpoint.Namespace,
			Machines:     machines,
			Labels:       endpoint.Labels,
			Status:       r.determineEndpointStatus(&endpoint),
			LastSelected: &now,
			HealthStatus: healthStatus,
		}

		selectedEndpoints = append(selectedEndpoints, selectedEndpoint)
	}

	// Update status
	service.Status.SelectedEndpoints = selectedEndpoints

	logger.Info("Selected endpoints",
		"service", service.Name,
		"selector", service.Spec.Selector.String(),
		"selectedCount", len(selectedEndpoints))

	// Clean up unused auto-provisioned endpoints
	if err := r.cleanupUnusedAutoProvisionedEndpoints(ctx, service); err != nil {
		logger.Info("Failed to clean up unused auto-provisioned endpoints", "error", err)
		// Don't fail reconciliation on cleanup errors
	}

	return nil
}

// determineEndpointStatus determines the status of a selected endpoint
func (r *TailscaleServicesReconciler) determineEndpointStatus(endpoint *gatewayv1alpha1.TailscaleEndpoints) string {
	// Check if StatefulSets are ready
	for _, condition := range endpoint.Status.Conditions {
		if condition.Type == "StatefulSetsReady" {
			if condition.Status == metav1.ConditionTrue {
				return "Ready"
			} else if condition.Status == metav1.ConditionFalse {
				return "Failed"
			}
		}
	}
	return "Pending"
}

// reconcileVIPService ensures the VIP service exists using discovery-first approach
func (r *TailscaleServicesReconciler) reconcileVIPService(ctx context.Context, service *gatewayv1alpha1.TailscaleServices) error {
	logger := log.FromContext(ctx)

	// Get tailnet name from the service
	tailnetName := r.getTailnetNameFromService(ctx, service)
	if tailnetName == "" {
		return fmt.Errorf("could not determine tailnet for service")
	}

	// Get Tailscale client for this tailnet
	tsClient, err := r.TailscaleClientManager.GetClient(ctx, r.Client, tailnetName, service.Namespace)
	if err != nil {
		return fmt.Errorf("failed to get Tailscale client for tailnet %s: %w", tailnetName, err)
	}

	// Create ServiceCoordinator if not already created
	if r.ServiceCoordinator == nil {
		// Generate operator and cluster IDs
		operatorID := fmt.Sprintf("tailscale-gateway-operator-%s", service.Namespace)

		// Use service UID as cluster ID basis for consistency
		uidStr := string(service.UID)
		if len(uidStr) < 8 {
			uidStr = "00000000" // Fallback for tests or missing UID
		}
		clusterID := fmt.Sprintf("cluster-%s", uidStr[:8])

		r.ServiceCoordinator = tsservice.NewServiceCoordinator(
			tsClient,
			r.Client,
			operatorID,
			clusterID,
			r.Logger.Named("service-coordinator"),
		)
	}

	// Determine service name
	serviceName := service.Spec.VIPService.Name
	if serviceName == "" {
		serviceName = service.Name
	}

	// First, try discovery-first approach: check if service already exists
	if existingService, err := r.discoverExistingVIPService(ctx, service, serviceName); err != nil {
		logger.Info("Failed to discover existing VIP services", "error", err)
	} else if existingService != nil {
		// Found existing service - attach to it
		logger.Info("Found existing VIP service, attaching", "serviceName", *existingService)
		serviceName = *existingService
	}

	tsServiceName := tailscale.ServiceName(serviceName)
	if err := tsServiceName.Validate(); err != nil {
		return fmt.Errorf("invalid service name %s: %w", serviceName, err)
	}

	// Build service metadata from TailscaleServices
	metadata := r.buildServiceMetadata(ctx, service)

	// Use ServiceCoordinator to ensure VIP service
	// routeName identifies this consumer, serviceName is the target backend
	registration, err := r.ServiceCoordinator.EnsureServiceWithMetadata(
		ctx,
		fmt.Sprintf("tailscaleservices-%s", service.Name), // routeName
		serviceName, // targetBackend (the VIP service name itself)
		metadata,
	)
	if err != nil {
		return fmt.Errorf("failed to ensure VIP service: %w", err)
	}

	// Update VIP service status
	if service.Status.VIPServiceStatus == nil {
		service.Status.VIPServiceStatus = &gatewayv1alpha1.VIPServiceStatus{}
	}

	service.Status.VIPServiceStatus.Created = true
	service.Status.VIPServiceStatus.ServiceName = string(registration.ServiceName)
	service.Status.VIPServiceStatus.Addresses = registration.VIPAddresses
	if len(registration.VIPAddresses) > 0 {
		// Build DNS name using Tailscale VIP service DNS patterns
		service.Status.VIPServiceStatus.DNSName = r.buildVIPServiceDNSName(ctx, service, string(registration.ServiceName))
	}

	logger.Info("VIP service ensured",
		"service", service.Name,
		"vipServiceName", registration.ServiceName,
		"addresses", registration.VIPAddresses)

	return nil
}

// buildServiceMetadata builds service metadata for the ServiceCoordinator
func (r *TailscaleServicesReconciler) buildServiceMetadata(ctx context.Context, tailscaleService *gatewayv1alpha1.TailscaleServices) *tsservice.ServiceMetadata {
	metadata := &tsservice.ServiceMetadata{
		TailscaleServices: tailscaleService,
	}

	// Try to get a related TailscaleEndpoints for additional metadata
	if len(tailscaleService.Status.SelectedEndpoints) > 0 {
		selectedEndpoint := tailscaleService.Status.SelectedEndpoints[0]
		var endpoint gatewayv1alpha1.TailscaleEndpoints
		if err := r.Get(ctx, types.NamespacedName{
			Name:      selectedEndpoint.Name,
			Namespace: selectedEndpoint.Namespace,
		}, &endpoint); err == nil {
			metadata.TailscaleEndpoints = &endpoint
		}
	}

	// Get TailscaleTailnet resource for domain information
	tailnetName := r.getTailnetNameFromService(ctx, tailscaleService)
	if tailnetName != "" {
		var tailnetList gatewayv1alpha1.TailscaleTailnetList
		if err := r.List(ctx, &tailnetList); err == nil {
			// Find matching TailscaleTailnet
			for _, tailnet := range tailnetList.Items {
				if (tailnet.Spec.Tailnet == tailnetName) ||
					(tailnet.Spec.Tailnet == "" && tailnetName == "-") ||
					(tailnet.Spec.Tailnet == "-" && tailnetName == "") {
					metadata.TailscaleTailnet = &tailnet
					break
				}
			}
		}
	}

	return metadata
}

// reconcileBackendRegistration registers selected endpoints as backends
func (r *TailscaleServicesReconciler) reconcileBackendRegistration(ctx context.Context, service *gatewayv1alpha1.TailscaleServices) error {
	logger := log.FromContext(ctx)

	// Count backends
	totalBackends := 0
	healthyBackends := 0

	for _, selectedEndpoint := range service.Status.SelectedEndpoints {
		totalBackends += len(selectedEndpoint.Machines)
		if selectedEndpoint.HealthStatus != nil {
			healthyBackends += selectedEndpoint.HealthStatus.HealthyMachines
		}
	}

	// Update backend counts
	service.Status.TotalBackends = totalBackends
	service.Status.HealthyBackends = healthyBackends
	service.Status.UnhealthyBackends = totalBackends - healthyBackends

	// Update VIP service backend status
	if service.Status.VIPServiceStatus != nil {
		service.Status.VIPServiceStatus.BackendCount = totalBackends
		service.Status.VIPServiceStatus.HealthyBackendCount = healthyBackends
		service.Status.VIPServiceStatus.LastRegistration = &metav1.Time{Time: time.Now()}
	}

	logger.Info("Backend registration updated",
		"service", service.Name,
		"totalBackends", totalBackends,
		"healthyBackends", healthyBackends)

	return nil
}

// reconcileServiceHealth updates service-level health status
func (r *TailscaleServicesReconciler) reconcileServiceHealth(ctx context.Context, service *gatewayv1alpha1.TailscaleServices) error {
	// This would implement service-level health checking
	// For now, we derive health from endpoint health status

	// Update load balancing status if configured
	if service.Spec.LoadBalancing != nil && service.Status.LoadBalancingStatus == nil {
		service.Status.LoadBalancingStatus = &gatewayv1alpha1.LoadBalancingStatus{
			Strategy: service.Spec.LoadBalancing.Strategy,
		}
		if service.Spec.LoadBalancing.Strategy == "" {
			service.Status.LoadBalancingStatus.Strategy = "round-robin"
		}
	}

	return nil
}

// reconcileDelete handles deletion of TailscaleServices
func (r *TailscaleServicesReconciler) reconcileDelete(ctx context.Context, service *gatewayv1alpha1.TailscaleServices) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Deleting TailscaleServices", "name", service.Name)

	// Clean up VIP service if we created it
	if service.Status.VIPServiceStatus != nil && service.Status.VIPServiceStatus.Created {
		serviceName := tailscale.ServiceName(service.Status.VIPServiceStatus.ServiceName)
		if r.ServiceCoordinator != nil {
			if err := r.ServiceCoordinator.DetachFromService(
				ctx,
				serviceName,
				fmt.Sprintf("tailscaleservices-%s", service.Name),
			); err != nil {
				logger.Error(err, "Failed to detach from VIP service")
				return ctrl.Result{RequeueAfter: RequeueAfterError}, err
			}
		}
	}

	// Remove finalizer
	controllerutil.RemoveFinalizer(service, TailscaleServicesFinalizer)
	if err := r.Update(ctx, service); err != nil {
		logger.Error(err, "Failed to remove finalizer")
		return ctrl.Result{RequeueAfter: RequeueAfterError}, err
	}

	logger.Info("Successfully deleted TailscaleServices", "name", service.Name)
	return ctrl.Result{}, nil
}

// mapTailscaleEndpointsToTailscaleServices maps TailscaleEndpoints changes to TailscaleServices
func (r *TailscaleServicesReconciler) mapTailscaleEndpointsToTailscaleServices(ctx context.Context, obj client.Object) []reconcile.Request {
	endpoint, ok := obj.(*gatewayv1alpha1.TailscaleEndpoints)
	if !ok {
		return nil
	}

	// Find TailscaleServices that might select this endpoint
	var servicesList gatewayv1alpha1.TailscaleServicesList
	if err := r.List(ctx, &servicesList, &client.ListOptions{
		Namespace: endpoint.Namespace,
	}); err != nil {
		r.Logger.Errorw("Failed to list TailscaleServices for mapping", "error", err)
		return nil
	}

	var requests []reconcile.Request
	endpointLabels := labels.Set(endpoint.Labels)

	for _, service := range servicesList.Items {
		if service.Spec.Selector == nil {
			continue
		}

		selector, err := metav1.LabelSelectorAsSelector(service.Spec.Selector)
		if err != nil {
			continue
		}

		if selector.Matches(endpointLabels) {
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      service.Name,
					Namespace: service.Namespace,
				},
			})
		}
	}

	return requests
}

// autoProvisionTailscaleEndpoint creates a TailscaleEndpoints resource from the template
func (r *TailscaleServicesReconciler) autoProvisionTailscaleEndpoint(ctx context.Context, service *gatewayv1alpha1.TailscaleServices) error {
	logger := log.FromContext(ctx)

	template := service.Spec.EndpointTemplate
	if template == nil {
		return fmt.Errorf("no endpoint template provided")
	}

	// Generate endpoint name
	endpointName := service.Name
	if template.NameSuffix != "" {
		endpointName = service.Name + template.NameSuffix
	} else {
		endpointName = service.Name + "-endpoints"
	}

	// Create TailscaleEndpoints spec with new structure focusing on proxy/infrastructure
	endpointSpec := gatewayv1alpha1.TailscaleEndpointsSpec{
		Tailnet: template.Tailnet,
		Tags:    template.Tags,
	}

	// Set proxy configuration from template
	if template.Proxy != nil {
		endpointSpec.Proxy = &gatewayv1alpha1.ProxyConfig{
			Replicas:       template.Proxy.Replicas,
			ConnectionType: template.Proxy.ConnectionType,
		}

		// Set defaults if not specified
		if endpointSpec.Proxy.Replicas == nil {
			defaultReplicas := int32(2)
			endpointSpec.Proxy.Replicas = &defaultReplicas
		}
		if endpointSpec.Proxy.ConnectionType == "" {
			endpointSpec.Proxy.ConnectionType = "bidirectional"
		}
	} else {
		// Default proxy configuration
		defaultReplicas := int32(2)
		endpointSpec.Proxy = &gatewayv1alpha1.ProxyConfig{
			Replicas:       &defaultReplicas,
			ConnectionType: "bidirectional",
		}
	}

	// Convert PortTemplate to PortMapping
	for _, portTemplate := range template.Ports {
		protocol := portTemplate.Protocol
		if protocol == "" {
			protocol = "TCP"
		}

		endpointSpec.Ports = append(endpointSpec.Ports, gatewayv1alpha1.PortMapping{
			Port:     portTemplate.Port,
			Protocol: protocol,
			Name:     fmt.Sprintf("port-%d", portTemplate.Port),
		})
	}

	// Create labels that match the selector
	labels := make(map[string]string)
	if template.Labels != nil {
		for k, v := range template.Labels {
			labels[k] = v
		}
	}

	// Ensure selector labels are present
	if service.Spec.Selector != nil && service.Spec.Selector.MatchLabels != nil {
		for k, v := range service.Spec.Selector.MatchLabels {
			labels[k] = v
		}
	}

	// Add metadata annotations
	annotations := map[string]string{
		"tailscale-gateway.com/auto-provisioned": "true",
		"tailscale-gateway.com/provisioned-by":   service.Name,
	}

	// Create the TailscaleEndpoints resource
	endpoint := &gatewayv1alpha1.TailscaleEndpoints{
		ObjectMeta: metav1.ObjectMeta{
			Name:        endpointName,
			Namespace:   service.Namespace,
			Labels:      labels,
			Annotations: annotations,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion:         service.APIVersion,
					Kind:               service.Kind,
					Name:               service.Name,
					UID:                service.UID,
					Controller:         &[]bool{true}[0],
					BlockOwnerDeletion: &[]bool{true}[0],
				},
			},
		},
		Spec: endpointSpec,
	}

	// Create the resource
	if err := r.Create(ctx, endpoint); err != nil {
		return fmt.Errorf("failed to create TailscaleEndpoints %s: %w", endpointName, err)
	}

	logger.Info("Auto-provisioned TailscaleEndpoints",
		"service", service.Name,
		"endpoint", endpointName,
		"tailnet", template.Tailnet,
		"tags", template.Tags,
		"ports", len(template.Ports),
		"replicas", *endpointSpec.Proxy.Replicas,
		"connectionType", endpointSpec.Proxy.ConnectionType)

	return nil
}

// buildVIPServiceDNSName builds the correct DNS name for Tailscale VIP services
func (r *TailscaleServicesReconciler) buildVIPServiceDNSName(ctx context.Context, service *gatewayv1alpha1.TailscaleServices, serviceName string) string {
	logger := log.FromContext(ctx)

	// For Tailscale VIP services, the DNS resolution pattern depends on service naming:
	// 1. If service name has "svc:" prefix, it resolves directly as serviceName.tailnet-domain
	// 2. If service name doesn't have prefix, it might resolve differently
	// 3. VIP services may have their own DNS resolution that bypasses MagicDNS

	tailnetDomain := r.getTailnetDomain(ctx, service)

	// Strip "svc:" prefix if present for DNS resolution
	dnsServiceName := serviceName
	if strings.HasPrefix(serviceName, "svc:") {
		dnsServiceName = strings.TrimPrefix(serviceName, "svc:")
	}

	// Check if this is a VIP service that should resolve using VIP addresses directly
	if len(service.Status.VIPServiceStatus.Addresses) > 0 {
		// For VIP services with addresses, the DNS name pattern is typically:
		// service-name.tailnet-domain (without svc: prefix)
		dnsName := fmt.Sprintf("%s.%s", dnsServiceName, tailnetDomain)

		logger.Info("Built VIP service DNS name",
			"originalServiceName", serviceName,
			"dnsServiceName", dnsServiceName,
			"tailnetDomain", tailnetDomain,
			"finalDNSName", dnsName)

		return dnsName
	}

	// Fallback to simple pattern
	return fmt.Sprintf("%s.%s", dnsServiceName, tailnetDomain)
}

// getTailnetDomain gets the actual tailnet domain from TailscaleTailnet resource
func (r *TailscaleServicesReconciler) getTailnetDomain(ctx context.Context, service *gatewayv1alpha1.TailscaleServices) string {
	logger := log.FromContext(ctx)

	// Default fallback
	defaultDomain := "ts.net"

	// Get tailnet name from selected endpoints
	tailnetName := r.getTailnetNameFromService(ctx, service)
	if tailnetName == "" {
		logger.Info("No tailnet name found, using default domain", "domain", defaultDomain)
		return defaultDomain
	}

	// Look up TailscaleTailnet resource
	var tailnetList gatewayv1alpha1.TailscaleTailnetList
	if err := r.List(ctx, &tailnetList); err != nil {
		logger.Error(err, "Failed to list TailscaleTailnet resources, using default domain")
		return defaultDomain
	}

	// Find matching TailscaleTailnet by tailnet name
	for _, tailnet := range tailnetList.Items {
		// Match by spec.tailnet field or use default tailnet ("-")
		if (tailnet.Spec.Tailnet == tailnetName) ||
			(tailnet.Spec.Tailnet == "" && tailnetName == "-") ||
			(tailnet.Spec.Tailnet == "-" && tailnetName == "") {

			// Extract domain from TailnetInfo
			if tailnet.Status.TailnetInfo != nil && tailnet.Status.TailnetInfo.Name != "" {
				// TailnetInfo.Name contains the full domain (e.g., "tail123abc.ts.net")
				logger.Info("Found tailnet domain from TailscaleTailnet resource",
					"tailnet", tailnetName,
					"domain", tailnet.Status.TailnetInfo.Name)
				return tailnet.Status.TailnetInfo.Name
			}

			// Try MagicDNSSuffix as fallback
			if tailnet.Status.TailnetInfo != nil &&
				tailnet.Status.TailnetInfo.MagicDNSSuffix != nil &&
				*tailnet.Status.TailnetInfo.MagicDNSSuffix != "" {
				logger.Info("Found tailnet domain from MagicDNSSuffix",
					"tailnet", tailnetName,
					"domain", *tailnet.Status.TailnetInfo.MagicDNSSuffix)
				return *tailnet.Status.TailnetInfo.MagicDNSSuffix
			}
		}
	}

	logger.Info("No matching TailscaleTailnet found, using default domain",
		"tailnet", tailnetName,
		"domain", defaultDomain)
	return defaultDomain
}

// discoverExistingVIPService tries to discover existing VIP services that match our requirements
func (r *TailscaleServicesReconciler) discoverExistingVIPService(ctx context.Context, service *gatewayv1alpha1.TailscaleServices, targetServiceName string) (*string, error) {
	logger := log.FromContext(ctx)

	// Get tailnet client from ServiceCoordinator
	if r.TailscaleClientManager == nil {
		return nil, fmt.Errorf("TailscaleClientManager not available")
	}

	// Get tailnet name for client selection
	tailnetName := r.getTailnetNameFromService(ctx, service)
	tsClient, err := r.TailscaleClientManager.GetClient(ctx, r.Client, tailnetName, service.Namespace)
	if err != nil {
		return nil, fmt.Errorf("failed to get Tailscale client for tailnet %s: %w", tailnetName, err)
	}

	// If service discovery is enabled in spec, try to discover services
	if service.Spec.ServiceDiscovery != nil && service.Spec.ServiceDiscovery.DiscoverVIPServices {
		logger.Info("Service discovery enabled, looking for existing VIP services")

		// Get all VIP services
		allServices, err := tsClient.GetVIPServices(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list VIP services: %w", err)
		}

		// Look for services that match our criteria
		for _, vipService := range allServices {
			serviceName := string(vipService.Name)

			// Check if service name matches what we're looking for
			if serviceName == targetServiceName {
				logger.Info("Found exact match for target service", "serviceName", serviceName)
				return &serviceName, nil
			}

			// Check if service matches our selector criteria
			if service.Spec.ServiceDiscovery.VIPServiceSelector != nil {
				if r.matchesVIPServiceSelector(vipService, service.Spec.ServiceDiscovery.VIPServiceSelector) {
					logger.Info("Found VIP service matching selector", "serviceName", serviceName)
					return &serviceName, nil
				}
			}
		}

		logger.Info("No matching existing VIP services found")
	}

	return nil, nil
}

// matchesVIPServiceSelector checks if a VIP service matches the selector criteria
func (r *TailscaleServicesReconciler) matchesVIPServiceSelector(vipService *tailscale.VIPService, selector *gatewayv1alpha1.VIPServiceSelector) bool {
	// Check service names
	if len(selector.ServiceNames) > 0 {
		serviceName := string(vipService.Name)
		for _, name := range selector.ServiceNames {
			if serviceName == name {
				return true
			}
		}
		return false
	}

	// Check tags
	if len(selector.Tags) > 0 {
		for _, requiredTag := range selector.Tags {
			found := false
			for _, serviceTag := range vipService.Tags {
				if serviceTag == requiredTag {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
		return true
	}

	return false
}

// getTailnetNameFromService extracts tailnet name from selected TailscaleEndpoints
func (r *TailscaleServicesReconciler) getTailnetNameFromService(ctx context.Context, service *gatewayv1alpha1.TailscaleServices) string {
	// Check EndpointTemplate first (for auto-provisioned endpoints)
	if service.Spec.EndpointTemplate != nil && service.Spec.EndpointTemplate.Tailnet != "" {
		return service.Spec.EndpointTemplate.Tailnet
	}

	// Get from selected endpoints
	if len(service.Status.SelectedEndpoints) > 0 {
		// Get the first selected endpoint and look up its tailnet
		selectedEndpoint := service.Status.SelectedEndpoints[0]
		var endpoint gatewayv1alpha1.TailscaleEndpoints
		if err := r.Get(ctx, types.NamespacedName{
			Name:      selectedEndpoint.Name,
			Namespace: selectedEndpoint.Namespace,
		}, &endpoint); err == nil {
			return endpoint.Spec.Tailnet
		}
	}

	return ""
}

// cleanupUnusedAutoProvisionedEndpoints removes auto-provisioned endpoints that are no longer selected by any service
func (r *TailscaleServicesReconciler) cleanupUnusedAutoProvisionedEndpoints(ctx context.Context, service *gatewayv1alpha1.TailscaleServices) error {
	logger := log.FromContext(ctx)

	// List all TailscaleEndpoints in the namespace
	var endpointsList gatewayv1alpha1.TailscaleEndpointsList
	if err := r.List(ctx, &endpointsList, &client.ListOptions{
		Namespace: service.Namespace,
	}); err != nil {
		return fmt.Errorf("failed to list TailscaleEndpoints for cleanup: %w", err)
	}

	// List all TailscaleServices in the namespace to check usage
	var servicesList gatewayv1alpha1.TailscaleServicesList
	if err := r.List(ctx, &servicesList, &client.ListOptions{
		Namespace: service.Namespace,
	}); err != nil {
		return fmt.Errorf("failed to list TailscaleServices for cleanup: %w", err)
	}

	var endpointsToDelete []gatewayv1alpha1.TailscaleEndpoints

	// Check each endpoint to see if it's auto-provisioned and unused
	for _, endpoint := range endpointsList.Items {
		if r.isAutoProvisionedEndpoint(&endpoint) {
			if !r.isEndpointSelectedByAnyService(ctx, &endpoint, servicesList.Items) {
				endpointsToDelete = append(endpointsToDelete, endpoint)
			}
		}
	}

	// Delete unused auto-provisioned endpoints
	for _, endpoint := range endpointsToDelete {
		logger.Info("Deleting unused auto-provisioned endpoint",
			"endpoint", endpoint.Name,
			"namespace", endpoint.Namespace,
			"provisionedBy", endpoint.Annotations["tailscale-gateway.com/provisioned-by"])

		if err := r.Delete(ctx, &endpoint); err != nil {
			logger.Error(err, "Failed to delete unused auto-provisioned endpoint",
				"endpoint", endpoint.Name)
			// Continue with other deletions even if one fails
		}
	}

	if len(endpointsToDelete) > 0 {
		logger.Info("Cleaned up unused auto-provisioned endpoints",
			"deletedCount", len(endpointsToDelete))
	}

	return nil
}

// isAutoProvisionedEndpoint checks if an endpoint was auto-provisioned
func (r *TailscaleServicesReconciler) isAutoProvisionedEndpoint(endpoint *gatewayv1alpha1.TailscaleEndpoints) bool {
	if endpoint.Annotations == nil {
		return false
	}
	return endpoint.Annotations["tailscale-gateway.com/auto-provisioned"] == "true"
}

// isEndpointSelectedByAnyService checks if any TailscaleServices selects this endpoint
func (r *TailscaleServicesReconciler) isEndpointSelectedByAnyService(ctx context.Context, endpoint *gatewayv1alpha1.TailscaleEndpoints, services []gatewayv1alpha1.TailscaleServices) bool {
	endpointLabels := labels.Set(endpoint.Labels)

	for _, service := range services {
		if service.Spec.Selector == nil {
			continue
		}

		selector, err := metav1.LabelSelectorAsSelector(service.Spec.Selector)
		if err != nil {
			// Skip services with invalid selectors
			continue
		}

		if selector.Matches(endpointLabels) {
			return true
		}
	}

	return false
}

// reconcileProxyAdvertiseServices updates the ingress proxy configurations to advertise the VIP service
func (r *TailscaleServicesReconciler) reconcileProxyAdvertiseServices(ctx context.Context, service *gatewayv1alpha1.TailscaleServices) error {
	logger := log.FromContext(ctx)

	// Get the VIP service name that should be advertised
	serviceName := service.Spec.VIPService.Name
	if serviceName == "" {
		serviceName = service.Name
	}

	// Ensure the service name has the correct format
	if !strings.HasPrefix(serviceName, "svc:") {
		serviceName = "svc:" + serviceName
	}

	logger.Info("Updating proxy AdvertiseServices configuration", "serviceName", serviceName)

	// Update ingress proxy configurations for each selected endpoint
	for _, selectedEndpoint := range service.Status.SelectedEndpoints {
		if err := r.updateEndpointIngressConfigs(ctx, selectedEndpoint.Name, selectedEndpoint.Namespace, serviceName); err != nil {
			return fmt.Errorf("failed to update configs for endpoint %s/%s: %w", selectedEndpoint.Namespace, selectedEndpoint.Name, err)
		}
	}

	return nil
}

// updateEndpointIngressConfigs updates the ingress proxy configs for a specific TailscaleEndpoints
func (r *TailscaleServicesReconciler) updateEndpointIngressConfigs(ctx context.Context, endpointName, namespace, serviceName string) error {
	logger := log.FromContext(ctx)

	// List all config secrets for ingress proxies of this endpoint
	secretList := &corev1.SecretList{}
	selector := map[string]string{
		"app.kubernetes.io/instance":  endpointName,
		"app.kubernetes.io/component": "config",
	}

	if err := r.List(ctx, secretList, client.InNamespace(namespace), client.MatchingLabels(selector)); err != nil {
		return fmt.Errorf("failed to list ingress config secrets: %w", err)
	}

	// Update each config secret (only ingress proxies should advertise services)
	for _, secret := range secretList.Items {
		if !strings.HasSuffix(secret.Name, "-config") {
			continue // Skip non-config secrets
		}

		// Only update ingress proxy configs (not egress)
		if !strings.Contains(secret.Name, "-ingress-") {
			continue // Skip egress proxy configs
		}

		updated := false
		for fileName, configData := range secret.Data {
			if !strings.HasPrefix(fileName, "cap-") || !strings.HasSuffix(fileName, ".hujson") {
				continue // Skip non-capability config files
			}

			// Parse the existing config using the proper ipn.ConfigVAlpha struct
			var config ipn.ConfigVAlpha
			if err := json.Unmarshal(configData, &config); err != nil {
				logger.Error(err, "Failed to unmarshal config", "secret", secret.Name, "file", fileName)
				continue
			}

			// Check if service is already advertised
			serviceAlreadyAdvertised := false
			for _, advertised := range config.AdvertiseServices {
				if advertised == serviceName {
					serviceAlreadyAdvertised = true
					break
				}
			}

			// Add service to AdvertiseServices if not already present
			if !serviceAlreadyAdvertised {
				config.AdvertiseServices = append(config.AdvertiseServices, serviceName)

				// Marshal the updated config back
				updatedConfig, err := json.Marshal(config)
				if err != nil {
					return fmt.Errorf("failed to marshal updated config: %w", err)
				}

				secret.Data[fileName] = updatedConfig
				updated = true
				logger.Info("Added service to AdvertiseServices",
					"secret", secret.Name,
					"file", fileName,
					"service", serviceName)
			}
		}

		// Update the secret if any configs were modified
		if updated {
			if err := r.Update(ctx, &secret); err != nil {
				return fmt.Errorf("failed to update config secret %s: %w", secret.Name, err)
			}
			logger.Info("Updated ingress proxy config secret", "secret", secret.Name)
		}
	}

	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *TailscaleServicesReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&gatewayv1alpha1.TailscaleServices{}).
		Watches(
			&gatewayv1alpha1.TailscaleEndpoints{},
			handler.EnqueueRequestsFromMapFunc(r.mapTailscaleEndpointsToTailscaleServices),
		).
		Complete(r)
}
