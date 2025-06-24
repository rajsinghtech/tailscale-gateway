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

	"go.uber.org/zap"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"
	gwapiv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	gatewayv1alpha1 "github.com/rajsinghtech/tailscale-gateway/api/v1alpha1"
	"github.com/rajsinghtech/tailscale-gateway/internal/service"
	"github.com/rajsinghtech/tailscale-gateway/internal/tailscale"
)

const (
	// TailscaleGatewayFinalizer is the finalizer for TailscaleGateway resources
	TailscaleGatewayFinalizer = "gateway.tailscale.com/tailscalegateway"

	// Extension Server defaults
	defaultExtensionServerImage = "tailscale-gateway-extension:latest"
	defaultExtensionServerPort  = 8443
	defaultExtensionReplicas    = 2

	// Event reasons
	ReasonGatewayConfigured   = "GatewayConfigured"
	ReasonGatewayConfigFailed = "GatewayConfigFailed"
	ReasonServiceCreated      = "ServiceCreated"
	ReasonServiceAttached     = "ServiceAttached"
	ReasonServiceError        = "ServiceError"
)

// TailscaleGatewayReconciler reconciles a TailscaleGateway object
type TailscaleGatewayReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Logger   *zap.SugaredLogger
	Recorder record.EventRecorder

	// TailscaleClient provides access to Tailscale APIs
	TailscaleClient tailscale.Client

	// ServiceCoordinator manages multi-operator service coordination
	ServiceCoordinator *service.ServiceCoordinator

	// Event channels for controller coordination
	EndpointsEventChan chan event.GenericEvent
	HTTPRouteEventChan chan event.GenericEvent
	TCPRouteEventChan  chan event.GenericEvent
	UDPRouteEventChan  chan event.GenericEvent
	TLSRouteEventChan  chan event.GenericEvent
	GRPCRouteEventChan chan event.GenericEvent

	// Metrics for tracking reconciliation performance
	metrics *ReconcilerMetrics
}

//+kubebuilder:rbac:groups=gateway.tailscale.com,resources=tailscalegateways,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=gateway.tailscale.com,resources=tailscalegateways/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=gateway.tailscale.com,resources=tailscalegateways/finalizers,verbs=update
//+kubebuilder:rbac:groups=gateway.tailscale.com,resources=tailscaletailnets,verbs=get;list;watch
//+kubebuilder:rbac:groups=gateway.tailscale.com,resources=tailscaleendpoints,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=httproutes,verbs=get;list;watch
//+kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=gateways,verbs=get;list;watch
//+kubebuilder:rbac:groups=gateway.envoyproxy.io,resources=gateways,verbs=get;list;watch
//+kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=services,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=serviceaccounts,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=events,verbs=create;patch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *TailscaleGatewayReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Fetch the TailscaleGateway instance
	gateway := &gatewayv1alpha1.TailscaleGateway{}
	if err := r.Get(ctx, req.NamespacedName, gateway); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("TailscaleGateway resource not found, ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get TailscaleGateway")
		return ctrl.Result{}, err
	}

	// Handle deletion
	if gateway.DeletionTimestamp != nil {
		return r.handleDeletion(ctx, gateway)
	}

	// Add finalizer if not present
	if !controllerutil.ContainsFinalizer(gateway, TailscaleGatewayFinalizer) {
		controllerutil.AddFinalizer(gateway, TailscaleGatewayFinalizer)
		if err := r.Update(ctx, gateway); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Track reconciliation metrics
	reconcileStart := time.Now()
	apiCallCount := int32(0)

	// Reconcile the gateway
	result, err := r.reconcileGateway(ctx, gateway)
	if err != nil {
		logger.Error(err, "Failed to reconcile TailscaleGateway")
		r.recordError(gateway, gatewayv1alpha1.ErrorCodeReconciliation, "Failed to reconcile TailscaleGateway", "controller", err)
		r.updateCondition(gateway, "Ready", metav1.ConditionFalse, "ReconcileError", err.Error())
		if statusErr := r.Status().Update(ctx, gateway); statusErr != nil {
			logger.Error(statusErr, "Failed to update status")
		}
		return result, err
	}

	// Record successful reconciliation metrics
	r.recordMetrics(gateway, reconcileStart, apiCallCount)
	r.updateCondition(gateway, "Ready", metav1.ConditionTrue, "ReconcileSuccess", "TailscaleGateway reconciled successfully")

	// Update status
	if err := r.Status().Update(ctx, gateway); err != nil {
		logger.Error(err, "Failed to update TailscaleGateway status")
		return ctrl.Result{}, err
	}

	return result, nil
}

// reconcileGateway handles the main reconciliation logic
func (r *TailscaleGatewayReconciler) reconcileGateway(ctx context.Context, gateway *gatewayv1alpha1.TailscaleGateway) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Validate the referenced Gateway exists
	if err := r.validateGatewayRef(ctx, gateway); err != nil {
		r.updateCondition(gateway, "GatewayValid", metav1.ConditionFalse, "InvalidGateway", err.Error())
		return ctrl.Result{RequeueAfter: time.Minute}, err
	}
	r.updateCondition(gateway, "GatewayValid", metav1.ConditionTrue, "GatewayFound", "Referenced Gateway exists")

	// Validate all tailnet references
	if err := r.validateTailnetRefs(ctx, gateway); err != nil {
		r.updateCondition(gateway, "TailnetsValid", metav1.ConditionFalse, "InvalidTailnets", err.Error())
		return ctrl.Result{RequeueAfter: time.Minute}, err
	}
	r.updateCondition(gateway, "TailnetsValid", metav1.ConditionTrue, "TailnetsFound", "All referenced tailnets are valid")

	// Reconcile Extension Server if configured
	if gateway.Spec.ExtensionServer != nil {
		if err := r.reconcileExtensionServer(ctx, gateway); err != nil {
			r.updateCondition(gateway, "ExtensionServerReady", metav1.ConditionFalse, "ExtensionServerError", err.Error())
			return ctrl.Result{RequeueAfter: time.Minute}, err
		}
		r.updateCondition(gateway, "ExtensionServerReady", metav1.ConditionTrue, "ExtensionServerDeployed", "Extension Server is ready")
	}

	// Reconcile TailscaleEndpoints for each tailnet
	if err := r.reconcileTailnetEndpoints(ctx, gateway); err != nil {
		r.updateCondition(gateway, "EndpointsReady", metav1.ConditionFalse, "EndpointsError", err.Error())
		return ctrl.Result{RequeueAfter: time.Minute}, err
	}
	r.updateCondition(gateway, "EndpointsReady", metav1.ConditionTrue, "EndpointsManaged", "TailscaleEndpoints are managed")

	// Initialize ServiceCoordinator and TailscaleClient if needed
	if err := r.ensureServiceCoordinator(ctx, gateway); err != nil {
		r.updateCondition(gateway, "ServicesReady", metav1.ConditionFalse, "ServiceCoordinatorError", err.Error())
		return ctrl.Result{RequeueAfter: time.Minute}, err
	}

	// Process HTTPRoutes and ensure VIP services if ServiceCoordinator is available
	if r.ServiceCoordinator != nil {
		// Step 1: Create VIP services for Gateway listeners (inbound traffic)
		if err := r.reconcileGatewayVIPServices(ctx, gateway); err != nil {
			r.updateCondition(gateway, "GatewayVIPReady", metav1.ConditionFalse, "GatewayVIPError", err.Error())
			return ctrl.Result{RequeueAfter: time.Minute}, err
		}
		r.updateCondition(gateway, "GatewayVIPReady", metav1.ConditionTrue, "GatewayVIPConfigured", "Gateway VIP services are configured")

		// Step 2: Process HTTPRoutes and ensure VIP services for backends (egress traffic)
		if err := r.reconcileServiceCoordination(ctx, gateway); err != nil {
			r.updateCondition(gateway, "ServicesReady", metav1.ConditionFalse, "ServiceError", err.Error())
			return ctrl.Result{RequeueAfter: time.Minute}, err
		}
		r.updateCondition(gateway, "ServicesReady", metav1.ConditionTrue, "ServicesConfigured", "VIP services are configured")

		// Step 3: Expose local services bidirectionally
		if err := r.reconcileBidirectionalServiceExposure(ctx, gateway); err != nil {
			r.updateCondition(gateway, "BidirectionalServicesReady", metav1.ConditionFalse, "BidirectionalServiceError", err.Error())
			return ctrl.Result{RequeueAfter: time.Minute}, err
		}
		r.updateCondition(gateway, "BidirectionalServicesReady", metav1.ConditionTrue, "BidirectionalServicesConfigured", "Bidirectional service exposure is configured")
	}

	// Update overall ready condition
	r.updateCondition(gateway, "Ready", metav1.ConditionTrue, "GatewayReady", "TailscaleGateway is ready")

	logger.Info("Successfully reconciled TailscaleGateway", "gateway", gateway.Name)
	return ctrl.Result{RequeueAfter: time.Minute * 5}, nil
}

// validateGatewayRef validates that the referenced Gateway exists
func (r *TailscaleGatewayReconciler) validateGatewayRef(ctx context.Context, gateway *gatewayv1alpha1.TailscaleGateway) error {
	gatewayRef := gateway.Spec.GatewayRef

	// Default namespace to the same as TailscaleGateway if not specified
	namespace := gatewayRef.Namespace
	var namespaceStr string
	if namespace == nil {
		namespaceStr = gateway.Namespace
	} else {
		namespaceStr = string(*namespace)
	}

	// Fetch the referenced Gateway
	envoyGateway := &gwapiv1.Gateway{}
	key := client.ObjectKey{
		Name:      string(gatewayRef.Name),
		Namespace: namespaceStr,
	}

	if err := r.Get(ctx, key, envoyGateway); err != nil {
		return fmt.Errorf("referenced Gateway %s/%s not found: %w", namespaceStr, gatewayRef.Name, err)
	}

	return nil
}

// validateTailnetRefs validates that all referenced TailscaleTailnets exist and are ready
func (r *TailscaleGatewayReconciler) validateTailnetRefs(ctx context.Context, gateway *gatewayv1alpha1.TailscaleGateway) error {
	for _, tailnetConfig := range gateway.Spec.Tailnets {
		tailnetRef := tailnetConfig.TailscaleTailnetRef

		// Default namespace to the same as TailscaleGateway if not specified
		namespace := tailnetRef.Namespace
		var namespaceStr string
		if namespace == nil {
			namespaceStr = gateway.Namespace
		} else {
			namespaceStr = string(*namespace)
		}

		// Fetch the referenced TailscaleTailnet
		tailnet := &gatewayv1alpha1.TailscaleTailnet{}
		key := client.ObjectKey{
			Name:      string(tailnetRef.Name),
			Namespace: namespaceStr,
		}

		if err := r.Get(ctx, key, tailnet); err != nil {
			return fmt.Errorf("referenced TailscaleTailnet %s/%s not found: %w", namespaceStr, tailnetRef.Name, err)
		}

		// Check if tailnet is ready
		if !r.isTailnetReady(tailnet) {
			return fmt.Errorf("TailscaleTailnet %s/%s is not ready", namespaceStr, tailnetRef.Name)
		}
	}

	return nil
}

// isTailnetReady checks if a TailscaleTailnet is ready
func (r *TailscaleGatewayReconciler) isTailnetReady(tailnet *gatewayv1alpha1.TailscaleTailnet) bool {
	for _, condition := range tailnet.Status.Conditions {
		if condition.Type == "Ready" && condition.Status == metav1.ConditionTrue {
			return true
		}
	}
	return false
}

// reconcileExtensionServer ensures the Extension Server deployment exists and is ready
func (r *TailscaleGatewayReconciler) reconcileExtensionServer(ctx context.Context, gateway *gatewayv1alpha1.TailscaleGateway) error {
	// Create or update the Extension Server deployment
	deployment := r.buildExtensionServerDeployment(gateway)
	if err := ctrl.SetControllerReference(gateway, deployment, r.Scheme); err != nil {
		return err
	}

	if err := r.reconcileResource(ctx, deployment); err != nil {
		return fmt.Errorf("failed to reconcile Extension Server deployment: %w", err)
	}

	// Create or update the Extension Server service
	service := r.buildExtensionServerService(gateway)
	if err := ctrl.SetControllerReference(gateway, service, r.Scheme); err != nil {
		return err
	}

	if err := r.reconcileResource(ctx, service); err != nil {
		return fmt.Errorf("failed to reconcile Extension Server service: %w", err)
	}

	// Update Extension Server status
	deployment = &appsv1.Deployment{}
	if err := r.Get(ctx, client.ObjectKey{Name: r.extensionServerName(gateway), Namespace: gateway.Namespace}, deployment); err != nil {
		return err
	}

	gateway.Status.ExtensionServerStatus = &gatewayv1alpha1.ExtensionServerStatus{
		ReadyReplicas:   deployment.Status.ReadyReplicas,
		DesiredReplicas: *deployment.Spec.Replicas,
		Ready:           deployment.Status.ReadyReplicas == *deployment.Spec.Replicas,
		ServiceEndpoint: r.extensionServerServiceEndpoint(gateway),
	}

	return nil
}

// reconcileTailnetEndpoints ensures TailscaleEndpoints resources exist for each tailnet
func (r *TailscaleGatewayReconciler) reconcileTailnetEndpoints(ctx context.Context, gateway *gatewayv1alpha1.TailscaleGateway) error {
	gateway.Status.TailnetStatus = make([]gatewayv1alpha1.TailnetStatus, len(gateway.Spec.Tailnets))

	for i, tailnetConfig := range gateway.Spec.Tailnets {
		// Create or update TailscaleEndpoints for this tailnet
		endpoints := r.buildTailscaleEndpoints(gateway, tailnetConfig)
		if err := ctrl.SetControllerReference(gateway, endpoints, r.Scheme); err != nil {
			return err
		}

		if err := r.reconcileResource(ctx, endpoints); err != nil {
			return fmt.Errorf("failed to reconcile TailscaleEndpoints for tailnet %s: %w", tailnetConfig.Name, err)
		}

		// Update tailnet status
		endpoints = &gatewayv1alpha1.TailscaleEndpoints{}
		if err := r.Get(ctx, client.ObjectKey{Name: r.endpointsName(gateway, tailnetConfig.Name), Namespace: gateway.Namespace}, endpoints); err != nil {
			return err
		}

		gateway.Status.TailnetStatus[i] = gatewayv1alpha1.TailnetStatus{
			Name:               tailnetConfig.Name,
			DiscoveredServices: endpoints.Status.TotalEndpoints,
			LastSync:           endpoints.Status.LastSync,
		}
	}

	return nil
}

// reconcileGatewayVIPServices creates VIP services for Gateway listeners (inbound traffic)
func (r *TailscaleGatewayReconciler) reconcileGatewayVIPServices(ctx context.Context, gateway *gatewayv1alpha1.TailscaleGateway) error {
	logger := log.FromContext(ctx)

	// Get the referenced Gateway
	envoyGateway, err := r.getReferencedGateway(ctx, gateway)
	if err != nil {
		return fmt.Errorf("failed to get referenced Gateway: %w", err)
	}

	// Process each Gateway listener and create VIP services
	var gatewayServiceRegistrations []*service.ServiceRegistration
	for i, listener := range envoyGateway.Spec.Listeners {
		registration, err := r.processGatewayListener(ctx, gateway, envoyGateway, listener, i)
		if err != nil {
			logger.Error(err, "Failed to process Gateway listener", "listener", listener.Name, "port", listener.Port)
			r.Recorder.Event(gateway, "Warning", ReasonServiceError, fmt.Sprintf("Failed to process Gateway listener %s: %v", listener.Name, err))
			continue
		}
		if registration != nil {
			gatewayServiceRegistrations = append(gatewayServiceRegistrations, registration)
		}
	}

	// Update gateway status with inbound VIP service information
	if err := r.updateGatewayInboundServiceStatus(ctx, gateway, gatewayServiceRegistrations); err != nil {
		return fmt.Errorf("failed to update gateway inbound service status: %w", err)
	}

	if len(gatewayServiceRegistrations) > 0 {
		r.Recorder.Event(gateway, "Normal", ReasonGatewayConfigured, fmt.Sprintf("Successfully configured %d Gateway VIP services", len(gatewayServiceRegistrations)))
	}

	return nil
}

// processGatewayListener processes a Gateway listener and creates a VIP service for inbound traffic
func (r *TailscaleGatewayReconciler) processGatewayListener(ctx context.Context, gateway *gatewayv1alpha1.TailscaleGateway, envoyGateway *gwapiv1.Gateway, listener gwapiv1.Listener, index int) (*service.ServiceRegistration, error) {
	logger := log.FromContext(ctx)

	// Generate unique service name for this Gateway listener
	_ = fmt.Sprintf("gateway-%s-%s-%d", envoyGateway.Name, strings.ToLower(string(listener.Protocol)), listener.Port)

	// Generate route identifier for the Gateway listener
	routeName := fmt.Sprintf("gateway-%s/%s-listener-%d", envoyGateway.Namespace, envoyGateway.Name, index)

	// Create target backend pointing to the Envoy Gateway service
	// This assumes the Envoy Gateway service follows standard naming convention
	gatewayServiceTarget := fmt.Sprintf("%s-envoy.%s.svc.cluster.local:%d", envoyGateway.Name, envoyGateway.Namespace, listener.Port)

	// Gather metadata for dynamic service creation
	metadata, err := r.gatherGatewayServiceMetadata(ctx, gateway, envoyGateway, listener)
	if err != nil {
		logger.Info("Failed to gather complete metadata for Gateway listener, using basic service creation", "error", err)
		// Fallback to basic service creation
		registration, err := r.ServiceCoordinator.EnsureServiceForRoute(ctx, routeName, gatewayServiceTarget)
		if err != nil {
			return nil, fmt.Errorf("failed to ensure service for Gateway listener: %w", err)
		}
		return registration, nil
	}

	// Ensure service exists with dynamic configuration
	registration, err := r.ServiceCoordinator.EnsureServiceWithMetadata(ctx, routeName, gatewayServiceTarget, metadata)
	if err != nil {
		return nil, fmt.Errorf("failed to ensure service for Gateway listener: %w", err)
	}

	logger.Info("Successfully processed Gateway listener", "listener", listener.Name, "route", routeName, "service", registration.ServiceName, "vips", registration.VIPAddresses)

	return registration, nil
}

// gatherGatewayServiceMetadata collects metadata for Gateway VIP service creation
func (r *TailscaleGatewayReconciler) gatherGatewayServiceMetadata(
	ctx context.Context,
	gateway *gatewayv1alpha1.TailscaleGateway,
	envoyGateway *gwapiv1.Gateway,
	listener gwapiv1.Listener,
) (*service.ServiceMetadata, error) {
	metadata := &service.ServiceMetadata{
		Gateway: gateway,
	}

	// Try to get the Envoy Gateway service
	gatewayServiceName := fmt.Sprintf("%s-envoy", envoyGateway.Name)
	kubeService := &corev1.Service{}
	err := r.Get(ctx, types.NamespacedName{
		Name:      gatewayServiceName,
		Namespace: envoyGateway.Namespace,
	}, kubeService)
	if err != nil {
		// Try alternative naming patterns
		alternativeNames := []string{
			envoyGateway.Name,
			fmt.Sprintf("%s-gateway", envoyGateway.Name),
			fmt.Sprintf("envoy-%s", envoyGateway.Name),
		}

		for _, altName := range alternativeNames {
			err = r.Get(ctx, types.NamespacedName{
				Name:      altName,
				Namespace: envoyGateway.Namespace,
			}, kubeService)
			if err == nil {
				break
			}
		}

		if err != nil {
			return nil, fmt.Errorf("failed to find Envoy Gateway service in namespace %s: %w", envoyGateway.Namespace, err)
		}
	}
	metadata.Service = kubeService

	// Get TailscaleTailnet for the first tailnet (simplified)
	if len(gateway.Spec.Tailnets) > 0 {
		tailnetConfig := gateway.Spec.Tailnets[0]
		tailnetRef := tailnetConfig.TailscaleTailnetRef

		tailnet := &gatewayv1alpha1.TailscaleTailnet{}
		tailnetNamespace := envoyGateway.Namespace
		if tailnetRef.Namespace != nil {
			tailnetNamespace = string(*tailnetRef.Namespace)
		}

		err := r.Get(ctx, types.NamespacedName{
			Name:      string(tailnetRef.Name),
			Namespace: tailnetNamespace,
		}, tailnet)
		if err != nil {
			return nil, fmt.Errorf("failed to get TailscaleTailnet %s/%s: %w", tailnetNamespace, tailnetRef.Name, err)
		}
		metadata.TailscaleTailnet = tailnet
	}

	return metadata, nil
}

// getReferencedGateway gets the Gateway referenced by the TailscaleGateway
func (r *TailscaleGatewayReconciler) getReferencedGateway(ctx context.Context, gateway *gatewayv1alpha1.TailscaleGateway) (*gwapiv1.Gateway, error) {
	gatewayRef := gateway.Spec.GatewayRef

	// Default namespace to the same as TailscaleGateway if not specified
	namespace := gatewayRef.Namespace
	var namespaceStr string
	if namespace == nil {
		namespaceStr = gateway.Namespace
	} else {
		namespaceStr = string(*namespace)
	}

	// Fetch the referenced Gateway
	envoyGateway := &gwapiv1.Gateway{}
	key := client.ObjectKey{
		Name:      string(gatewayRef.Name),
		Namespace: namespaceStr,
	}

	if err := r.Get(ctx, key, envoyGateway); err != nil {
		return nil, fmt.Errorf("referenced Gateway %s/%s not found: %w", namespaceStr, gatewayRef.Name, err)
	}

	return envoyGateway, nil
}

// updateGatewayInboundServiceStatus updates the gateway status with inbound VIP service information
func (r *TailscaleGatewayReconciler) updateGatewayInboundServiceStatus(ctx context.Context, gateway *gatewayv1alpha1.TailscaleGateway, registrations []*service.ServiceRegistration) error {
	// Add inbound services to existing services list
	for _, reg := range registrations {
		serviceInfo := gatewayv1alpha1.ServiceInfo{
			Name:          fmt.Sprintf("inbound-%s", string(reg.ServiceName)),
			VIPAddresses:  reg.VIPAddresses,
			OwnerCluster:  reg.OwnerOperator,
			ConsumerCount: len(reg.ConsumerClusters),
			Status:        "Ready",
			CreatedAt:     &metav1.Time{Time: reg.LastUpdated},
			LastUpdated:   &metav1.Time{Time: reg.LastUpdated},
		}
		gateway.Status.Services = append(gateway.Status.Services, serviceInfo)
	}

	return nil
}

// reconcileBidirectionalServiceExposure exposes local Kubernetes services as VIP services
func (r *TailscaleGatewayReconciler) reconcileBidirectionalServiceExposure(ctx context.Context, gateway *gatewayv1alpha1.TailscaleGateway) error {
	logger := log.FromContext(ctx)

	// Get all TailscaleEndpoints resources managed by this gateway
	var bidirectionalRegistrations []*service.ServiceRegistration
	for _, tailnetConfig := range gateway.Spec.Tailnets {
		endpointsName := r.endpointsName(gateway, tailnetConfig.Name)
		endpoints := &gatewayv1alpha1.TailscaleEndpoints{}
		err := r.Get(ctx, types.NamespacedName{
			Name:      endpointsName,
			Namespace: gateway.Namespace,
		}, endpoints)
		if err != nil {
			logger.Error(err, "Failed to get TailscaleEndpoints", "name", endpointsName)
			continue
		}

		// Process each endpoint for bidirectional exposure
		for _, endpoint := range endpoints.Spec.Endpoints {
			if endpoint.ExternalTarget != " " {
				// Skip endpoints that already point to external targets
				continue
			}

			// Create VIP service for local Kubernetes service exposure
			registration, err := r.processLocalServiceExposure(ctx, gateway, &endpoint, endpoints)
			if err != nil {
				logger.Error(err, "Failed to process local service exposure", "endpoint", endpoint.Name)
				continue
			}
			if registration != nil {
				bidirectionalRegistrations = append(bidirectionalRegistrations, registration)
			}
		}
	}

	// Update gateway status with bidirectional service information
	if err := r.updateGatewayBidirectionalServiceStatus(ctx, gateway, bidirectionalRegistrations); err != nil {
		return fmt.Errorf("failed to update gateway bidirectional service status: %w", err)
	}

	if len(bidirectionalRegistrations) > 0 {
		r.Recorder.Event(gateway, "Normal", ReasonServiceCreated, fmt.Sprintf("Successfully configured %d bidirectional VIP services", len(bidirectionalRegistrations)))
	}

	return nil
}

// processLocalServiceExposure creates VIP services for local Kubernetes services
func (r *TailscaleGatewayReconciler) processLocalServiceExposure(ctx context.Context, gateway *gatewayv1alpha1.TailscaleGateway, endpoint *gatewayv1alpha1.TailscaleEndpoint, endpoints *gatewayv1alpha1.TailscaleEndpoints) (*service.ServiceRegistration, error) {
	// For local services, we create VIP services that make them accessible from other tailnets
	// The endpoint should reference a local Kubernetes service

	// Generate service name for local exposure
	_ = fmt.Sprintf("local-%s-%s", endpoints.Spec.Tailnet, endpoint.Name)

	// Generate route identifier
	routeName := fmt.Sprintf("local-exposure/%s/%s", endpoints.Name, endpoint.Name)

	// Target is the local service that we want to expose
	// For now, we'll use the endpoint's FQDN as the target
	targetBackend := fmt.Sprintf("%s:%d", endpoint.TailscaleFQDN, endpoint.Port)

	// Try to find the corresponding Kubernetes service
	metadata, err := r.gatherLocalServiceMetadata(ctx, gateway, endpoint, endpoints)
	if err != nil {
		log.FromContext(ctx).Info("Failed to gather complete metadata for local service, using basic service creation", "error", err)
		// Fallback to basic service creation
		registration, err := r.ServiceCoordinator.EnsureServiceForRoute(ctx, routeName, targetBackend)
		if err != nil {
			return nil, fmt.Errorf("failed to ensure service for local service exposure: %w", err)
		}
		return registration, nil
	}

	// Ensure service exists with dynamic configuration
	registration, err := r.ServiceCoordinator.EnsureServiceWithMetadata(ctx, routeName, targetBackend, metadata)
	if err != nil {
		return nil, fmt.Errorf("failed to ensure service for local service exposure: %w", err)
	}

	log.FromContext(ctx).Info("Successfully processed local service exposure", "endpoint", endpoint.Name, "route", routeName, "service", registration.ServiceName, "vips", registration.VIPAddresses)

	return registration, nil
}

// gatherLocalServiceMetadata collects metadata for local service VIP creation
func (r *TailscaleGatewayReconciler) gatherLocalServiceMetadata(
	ctx context.Context,
	gateway *gatewayv1alpha1.TailscaleGateway,
	endpoint *gatewayv1alpha1.TailscaleEndpoint,
	endpoints *gatewayv1alpha1.TailscaleEndpoints,
) (*service.ServiceMetadata, error) {
	metadata := &service.ServiceMetadata{
		Gateway:            gateway,
		TailscaleEndpoints: endpoints,
	}

	// Try to find the corresponding Kubernetes service by looking for services
	// that might match this endpoint (by name or labels)
	serviceList := &corev1.ServiceList{}
	err := r.List(ctx, serviceList, client.InNamespace(endpoints.Namespace))
	if err != nil {
		return nil, fmt.Errorf("failed to list services in namespace %s: %w", endpoints.Namespace, err)
	}

	// Try to find a matching service
	var matchingService *corev1.Service
	for _, svc := range serviceList.Items {
		// Simple matching by name similarity
		if strings.Contains(endpoint.Name, svc.Name) || strings.Contains(svc.Name, endpoint.Name) {
			matchingService = &svc
			break
		}
	}

	if matchingService != nil {
		metadata.Service = matchingService
	}

	// Get TailscaleTailnet for the first tailnet (simplified)
	if len(gateway.Spec.Tailnets) > 0 {
		tailnetConfig := gateway.Spec.Tailnets[0]
		tailnetRef := tailnetConfig.TailscaleTailnetRef

		tailnet := &gatewayv1alpha1.TailscaleTailnet{}
		tailnetNamespace := endpoints.Namespace
		if tailnetRef.Namespace != nil {
			tailnetNamespace = string(*tailnetRef.Namespace)
		}

		err := r.Get(ctx, types.NamespacedName{
			Name:      string(tailnetRef.Name),
			Namespace: tailnetNamespace,
		}, tailnet)
		if err != nil {
			return nil, fmt.Errorf("failed to get TailscaleTailnet %s/%s: %w", tailnetNamespace, tailnetRef.Name, err)
		}
		metadata.TailscaleTailnet = tailnet
	}

	return metadata, nil
}

// updateGatewayBidirectionalServiceStatus updates the gateway status with bidirectional service information
func (r *TailscaleGatewayReconciler) updateGatewayBidirectionalServiceStatus(ctx context.Context, gateway *gatewayv1alpha1.TailscaleGateway, registrations []*service.ServiceRegistration) error {
	// Add bidirectional services to existing services list
	for _, reg := range registrations {
		serviceInfo := gatewayv1alpha1.ServiceInfo{
			Name:          fmt.Sprintf("bidirectional-%s", string(reg.ServiceName)),
			VIPAddresses:  reg.VIPAddresses,
			OwnerCluster:  reg.OwnerOperator,
			ConsumerCount: len(reg.ConsumerClusters),
			Status:        "Ready",
			CreatedAt:     &metav1.Time{Time: reg.LastUpdated},
			LastUpdated:   &metav1.Time{Time: reg.LastUpdated},
		}
		gateway.Status.Services = append(gateway.Status.Services, serviceInfo)
	}

	return nil
}

// reconcileServiceCoordination processes HTTPRoutes and ensures VIP services
func (r *TailscaleGatewayReconciler) reconcileServiceCoordination(ctx context.Context, gateway *gatewayv1alpha1.TailscaleGateway) error {
	logger := log.FromContext(ctx)

	// Get all HTTPRoutes that reference this TailscaleGateway
	httpRoutes, err := r.getRelatedHTTPRoutes(ctx, gateway)
	if err != nil {
		return fmt.Errorf("failed to get related HTTPRoutes: %w", err)
	}

	// Process each HTTPRoute and ensure services
	var serviceRegistrations []*service.ServiceRegistration
	for _, httpRoute := range httpRoutes {
		registration, err := r.processHTTPRoute(ctx, gateway, &httpRoute)
		if err != nil {
			logger.Error(err, "Failed to process HTTPRoute", "httpRoute", httpRoute.Name, "namespace", httpRoute.Namespace)
			r.Recorder.Event(gateway, "Warning", ReasonServiceError, fmt.Sprintf("Failed to process HTTPRoute %s: %v", httpRoute.Name, err))
			continue
		}
		if registration != nil {
			serviceRegistrations = append(serviceRegistrations, registration)
		}
	}

	// Update gateway status with service information
	if err := r.updateGatewayServiceStatus(ctx, gateway, serviceRegistrations); err != nil {
		return fmt.Errorf("failed to update gateway service status: %w", err)
	}

	if len(serviceRegistrations) > 0 {
		r.Recorder.Event(gateway, "Normal", ReasonGatewayConfigured, fmt.Sprintf("Successfully configured %d VIP services", len(serviceRegistrations)))
	}

	return nil
}

// processHTTPRoute processes an HTTPRoute and ensures the corresponding VIP service
func (r *TailscaleGatewayReconciler) processHTTPRoute(ctx context.Context, gateway *gatewayv1alpha1.TailscaleGateway, httpRoute *gwapiv1.HTTPRoute) (*service.ServiceRegistration, error) {
	logger := log.FromContext(ctx)

	// Extract backend service from HTTPRoute
	targetBackend, err := r.extractTargetBackend(httpRoute)
	if err != nil {
		return nil, fmt.Errorf("failed to extract target backend: %w", err)
	}

	// Generate route identifier
	routeName := fmt.Sprintf("%s/%s", httpRoute.Namespace, httpRoute.Name)

	// Gather metadata for dynamic service creation
	metadata, err := r.gatherServiceMetadata(ctx, gateway, httpRoute, targetBackend)
	if err != nil {
		logger.Info("Failed to gather complete metadata, using basic service creation", "error", err)
		// Fallback to basic service creation
		registration, err := r.ServiceCoordinator.EnsureServiceForRoute(ctx, routeName, targetBackend)
		if err != nil {
			return nil, fmt.Errorf("failed to ensure service for route: %w", err)
		}
		return registration, nil
	}

	// Ensure service exists with dynamic configuration
	registration, err := r.ServiceCoordinator.EnsureServiceWithMetadata(ctx, routeName, targetBackend, metadata)
	if err != nil {
		return nil, fmt.Errorf("failed to ensure service for route: %w", err)
	}

	logger.Info("Successfully processed HTTPRoute", "httpRoute", routeName, "service", registration.ServiceName, "vips", registration.VIPAddresses)

	return registration, nil
}

// extractTargetBackend extracts the target backend from an HTTPRoute
func (r *TailscaleGatewayReconciler) extractTargetBackend(httpRoute *gwapiv1.HTTPRoute) (string, error) {
	// For now, use a simple heuristic based on the HTTPRoute name
	// In a real implementation, this would analyze the HTTPRoute rules and backends
	if len(httpRoute.Spec.Rules) == 0 {
		return "", fmt.Errorf("HTTPRoute has no rules")
	}

	rule := httpRoute.Spec.Rules[0]
	if len(rule.BackendRefs) == 0 {
		return "", fmt.Errorf("HTTPRoute rule has no backend refs")
	}

	backend := rule.BackendRefs[0]
	serviceName := string(backend.Name)

	// Generate a normalized backend identifier
	targetBackend := fmt.Sprintf("%s.%s", serviceName, httpRoute.Namespace)

	return targetBackend, nil
}

// getRelatedHTTPRoutes finds all HTTPRoutes that use this TailscaleGateway as an extension
func (r *TailscaleGatewayReconciler) getRelatedHTTPRoutes(ctx context.Context, gateway *gatewayv1alpha1.TailscaleGateway) ([]gwapiv1.HTTPRoute, error) {
	// List all HTTPRoutes in the same namespace
	httpRouteList := &gwapiv1.HTTPRouteList{}
	if err := r.List(ctx, httpRouteList, client.InNamespace(gateway.Namespace)); err != nil {
		return nil, fmt.Errorf("failed to list HTTPRoutes: %w", err)
	}

	var relatedRoutes []gwapiv1.HTTPRoute
	for _, httpRoute := range httpRouteList.Items {
		// Check if this HTTPRoute references our TailscaleGateway
		if r.httpRouteReferencesGateway(&httpRoute, gateway) {
			relatedRoutes = append(relatedRoutes, httpRoute)
		}
	}

	return relatedRoutes, nil
}

// httpRouteReferencesGateway checks if an HTTPRoute references a TailscaleGateway
func (r *TailscaleGatewayReconciler) httpRouteReferencesGateway(httpRoute *gwapiv1.HTTPRoute, gateway *gatewayv1alpha1.TailscaleGateway) bool {
	// Check annotations for TailscaleGateway reference
	// This is a simplified check - in practice, this would be more sophisticated
	if gatewayRef, exists := httpRoute.Annotations["gateway.tailscale.com/gateway"]; exists {
		return gatewayRef == gateway.Name
	}
	return false
}

// updateGatewayServiceStatus updates the gateway status with service information
func (r *TailscaleGatewayReconciler) updateGatewayServiceStatus(ctx context.Context, gateway *gatewayv1alpha1.TailscaleGateway, registrations []*service.ServiceRegistration) error {
	// Update service information in status
	gateway.Status.Services = make([]gatewayv1alpha1.ServiceInfo, len(registrations))
	for i, reg := range registrations {
		gateway.Status.Services[i] = gatewayv1alpha1.ServiceInfo{
			Name:          string(reg.ServiceName),
			VIPAddresses:  reg.VIPAddresses,
			OwnerCluster:  reg.OwnerOperator,
			ConsumerCount: len(reg.ConsumerClusters),
		}
	}

	return nil
}

// handleDeletion handles cleanup when TailscaleGateway is being deleted
func (r *TailscaleGatewayReconciler) handleDeletion(ctx context.Context, gateway *gatewayv1alpha1.TailscaleGateway) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	if controllerutil.ContainsFinalizer(gateway, TailscaleGatewayFinalizer) {
		// Perform cleanup
		logger.Info("Cleaning up TailscaleGateway resources", "gateway", gateway.Name)

		// Cleanup VIP services if ServiceCoordinator is available
		if r.ServiceCoordinator != nil {
			if err := r.cleanupServices(ctx, gateway); err != nil {
				logger.Error(err, "Failed to cleanup services during deletion")
				// Continue with deletion anyway
			}
		}

		// Remove finalizer
		controllerutil.RemoveFinalizer(gateway, TailscaleGatewayFinalizer)
		if err := r.Update(ctx, gateway); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

// cleanupServices detaches from all VIP services during gateway deletion
// ensureServiceCoordinator initializes ServiceCoordinator and TailscaleClient for the first tailnet
func (r *TailscaleGatewayReconciler) ensureServiceCoordinator(ctx context.Context, gateway *gatewayv1alpha1.TailscaleGateway) error {
	if r.ServiceCoordinator != nil && r.TailscaleClient != nil {
		return nil // Already initialized
	}

	if len(gateway.Spec.Tailnets) == 0 {
		return fmt.Errorf("no tailnets configured")
	}

	// Use the first tailnet for ServiceCoordinator initialization
	firstTailnet := gateway.Spec.Tailnets[0]

	// Get the TailscaleTailnet resource
	tailnet := &gatewayv1alpha1.TailscaleTailnet{}
	tailnetNamespace := gateway.Namespace
	if firstTailnet.TailscaleTailnetRef.Namespace != nil {
		tailnetNamespace = string(*firstTailnet.TailscaleTailnetRef.Namespace)
	}
	tailnetKey := types.NamespacedName{
		Name:      string(firstTailnet.TailscaleTailnetRef.Name),
		Namespace: tailnetNamespace,
	}
	if err := r.Get(ctx, tailnetKey, tailnet); err != nil {
		return fmt.Errorf("failed to get TailscaleTailnet %s: %w", tailnetKey, err)
	}

	// Initialize TailscaleClient using the tailnet's OAuth credentials
	tsClient, err := r.createTailscaleClient(ctx, tailnet)
	if err != nil {
		return fmt.Errorf("failed to create Tailscale client: %w", err)
	}
	r.TailscaleClient = tsClient

	// Initialize ServiceCoordinator
	operatorID := fmt.Sprintf("%s-%s", gateway.Name, gateway.Namespace)
	uidStr := string(gateway.UID)
	if len(uidStr) < 8 {
		uidStr = "00000000" // Fallback for tests or missing UID
	}
	clusterID := fmt.Sprintf("cluster-%s", uidStr[:8]) // Use truncated UID as cluster identifier
	r.ServiceCoordinator = service.NewServiceCoordinator(
		tsClient,
		r.Client,
		operatorID,
		clusterID,
		r.Logger.Named("service-coordinator"),
	)

	tailnetName := "unknown"
	if tailnet.Status.TailnetInfo != nil {
		tailnetName = tailnet.Status.TailnetInfo.Name
	}
	r.Logger.Infow("ServiceCoordinator initialized",
		"operatorID", operatorID,
		"clusterID", clusterID,
		"tailnet", tailnetName,
	)

	return nil
}

// createTailscaleClient creates a Tailscale client from TailscaleTailnet credentials
func (r *TailscaleGatewayReconciler) createTailscaleClient(ctx context.Context, tailnet *gatewayv1alpha1.TailscaleTailnet) (tailscale.Client, error) {
	// Get the OAuth secret
	secret := &corev1.Secret{}
	secretKey := types.NamespacedName{
		Name:      tailnet.Spec.OAuthSecretName,
		Namespace: tailnet.Spec.OAuthSecretNamespace,
	}
	if err := r.Get(ctx, secretKey, secret); err != nil {
		return nil, fmt.Errorf("failed to get OAuth secret %s: %w", secretKey, err)
	}

	clientID, ok := secret.Data["client_id"]
	if !ok {
		return nil, fmt.Errorf("client_id not found in OAuth secret")
	}

	clientSecret, ok := secret.Data["client_secret"]
	if !ok {
		return nil, fmt.Errorf("client_secret not found in OAuth secret")
	}

	// Create Tailscale client config
	config := tailscale.ClientConfig{
		Tailnet:      tailnet.Spec.Tailnet,
		APIBaseURL:   "", // Use default
		ClientID:     string(clientID),
		ClientSecret: string(clientSecret),
	}

	client, err := tailscale.NewClient(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("failed to create Tailscale client: %w", err)
	}
	return client, nil
}

func (r *TailscaleGatewayReconciler) cleanupServices(ctx context.Context, gateway *gatewayv1alpha1.TailscaleGateway) error {
	// Get all HTTPRoutes that were managed by this gateway
	httpRoutes, err := r.getRelatedHTTPRoutes(ctx, gateway)
	if err != nil {
		return fmt.Errorf("failed to get related HTTPRoutes during cleanup: %w", err)
	}

	// Detach from services for each HTTPRoute
	for _, httpRoute := range httpRoutes {
		targetBackend, err := r.extractTargetBackend(&httpRoute)
		if err != nil {
			log.FromContext(ctx).Error(err, "Failed to extract target backend for HTTPRoute during cleanup", "httpRoute", httpRoute.Name)
			continue
		}

		serviceName := r.generateServiceName(targetBackend)
		routeName := fmt.Sprintf("%s/%s", httpRoute.Namespace, httpRoute.Name)

		if err := r.ServiceCoordinator.DetachFromService(ctx, serviceName, routeName); err != nil {
			log.FromContext(ctx).Error(err, "Failed to detach from service during cleanup", "service", serviceName, "route", routeName)
			// Continue with other cleanups
		}
	}

	return nil
}

// Helper functions for building Kubernetes resources

func (r *TailscaleGatewayReconciler) buildExtensionServerDeployment(gateway *gatewayv1alpha1.TailscaleGateway) *appsv1.Deployment {
	config := gateway.Spec.ExtensionServer
	replicas := int32(defaultExtensionReplicas)
	if config.Replicas != 0 {
		replicas = config.Replicas
	}

	image := defaultExtensionServerImage
	if config.Image != "" {
		image = config.Image
	}

	port := defaultExtensionServerPort
	if config.Port != nil {
		port = int(*config.Port)
	}

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      r.extensionServerName(gateway),
			Namespace: gateway.Namespace,
			Labels:    r.labelsForExtensionServer(gateway),
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: r.labelsForExtensionServer(gateway),
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: r.labelsForExtensionServer(gateway),
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "extension-server",
							Image: image,
							Ports: []corev1.ContainerPort{
								{
									ContainerPort: int32(port),
									Name:          "grpc",
									Protocol:      corev1.ProtocolTCP,
								},
							},
							Env: []corev1.EnvVar{
								{
									Name:  "GRPC_PORT",
									Value: fmt.Sprintf("%d", port),
								},
								{
									Name:  "GATEWAY_NAME",
									Value: gateway.Name,
								},
								{
									Name:  "GATEWAY_NAMESPACE",
									Value: gateway.Namespace,
								},
							},
						},
					},
				},
			},
		},
	}

	if config.Resources != nil {
		deployment.Spec.Template.Spec.Containers[0].Resources = *config.Resources
	}

	if config.ServiceAccountName != "" {
		deployment.Spec.Template.Spec.ServiceAccountName = config.ServiceAccountName
	}

	return deployment
}

func (r *TailscaleGatewayReconciler) buildExtensionServerService(gateway *gatewayv1alpha1.TailscaleGateway) *corev1.Service {
	config := gateway.Spec.ExtensionServer
	port := defaultExtensionServerPort
	if config.Port != nil {
		port = int(*config.Port)
	}

	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      r.extensionServerName(gateway),
			Namespace: gateway.Namespace,
			Labels:    r.labelsForExtensionServer(gateway),
		},
		Spec: corev1.ServiceSpec{
			Selector: r.labelsForExtensionServer(gateway),
			Ports: []corev1.ServicePort{
				{
					Port:       int32(port),
					TargetPort: intstr.FromString("grpc"),
					Protocol:   corev1.ProtocolTCP,
					Name:       "grpc",
				},
			},
		},
	}
}

func (r *TailscaleGatewayReconciler) buildTailscaleEndpoints(gateway *gatewayv1alpha1.TailscaleGateway, tailnetConfig gatewayv1alpha1.TailnetConfig) *gatewayv1alpha1.TailscaleEndpoints {
	endpoints := &gatewayv1alpha1.TailscaleEndpoints{
		ObjectMeta: metav1.ObjectMeta{
			Name:      r.endpointsName(gateway, tailnetConfig.Name),
			Namespace: gateway.Namespace,
			Labels: map[string]string{
				"gateway.tailscale.com/gateway": gateway.Name,
				"gateway.tailscale.com/tailnet": tailnetConfig.Name,
			},
		},
		Spec: gatewayv1alpha1.TailscaleEndpointsSpec{
			Tailnet: tailnetConfig.Name,
		},
	}

	// Configure auto-discovery if service discovery is enabled
	if tailnetConfig.ServiceDiscovery != nil && tailnetConfig.ServiceDiscovery.Enabled {
		endpoints.Spec.AutoDiscovery = &gatewayv1alpha1.EndpointAutoDiscovery{
			Enabled:      true,
			SyncInterval: tailnetConfig.ServiceDiscovery.SyncInterval,
			// Convert patterns to tag selectors if needed
			TagSelectors: r.convertPatternsToTagSelectors(tailnetConfig.ServiceDiscovery.Patterns, tailnetConfig.ServiceDiscovery.ExcludePatterns),
		}
	}

	return endpoints
}

// convertPatternsToTagSelectors converts legacy pattern-based discovery to tag selectors
func (r *TailscaleGatewayReconciler) convertPatternsToTagSelectors(includePatterns, excludePatterns []string) []gatewayv1alpha1.TagSelector {
	var selectors []gatewayv1alpha1.TagSelector

	// Convert include patterns to tag selectors that look for service tags
	for _, pattern := range includePatterns {
		if pattern != "" {
			selectors = append(selectors, gatewayv1alpha1.TagSelector{
				Tag:      "tag:service",
				Operator: "Exists",
			})
		}
	}

	// Note: Exclude patterns are more complex to convert and may require
	// application-specific logic. For now, we'll use basic service tag existence.

	return selectors
}

// Helper functions for resource management

func (r *TailscaleGatewayReconciler) reconcileResource(ctx context.Context, obj client.Object) error {
	key := client.ObjectKeyFromObject(obj)
	current := obj.DeepCopyObject().(client.Object)

	err := r.Get(ctx, key, current)
	if err != nil {
		if errors.IsNotFound(err) {
			return r.Create(ctx, obj)
		}
		return err
	}

	// Update existing resource
	obj.SetResourceVersion(current.GetResourceVersion())
	return r.Update(ctx, obj)
}

func (r *TailscaleGatewayReconciler) updateCondition(gateway *gatewayv1alpha1.TailscaleGateway, conditionType string, status metav1.ConditionStatus, reason, message string) {
	gatewayv1alpha1.SetCondition(&gateway.Status.Conditions, conditionType, status, reason, message)
}

func (r *TailscaleGatewayReconciler) recordError(gateway *gatewayv1alpha1.TailscaleGateway, code, message, component string, err error) {
	errorMessage := message
	if err != nil {
		errorMessage = fmt.Sprintf("%s: %v", message, err)
	}

	detailedError := gatewayv1alpha1.DetailedError{
		Code:      code,
		Message:   errorMessage,
		Component: component,
		Timestamp: metav1.Now(),
		Severity:  gatewayv1alpha1.SeverityHigh,
	}

	// Add context if available
	if gateway.Name != "" {
		detailedError.Context = map[string]string{
			"gateway":   gateway.Name,
			"namespace": gateway.Namespace,
		}
	}

	gatewayv1alpha1.AddError(&gateway.Status.RecentErrors, detailedError, 10)
}

func (r *TailscaleGatewayReconciler) recordMetrics(gateway *gatewayv1alpha1.TailscaleGateway, reconcileStart time.Time, apiCallCount int32) {
	duration := time.Since(reconcileStart)

	if gateway.Status.OperationalMetrics == nil {
		gateway.Status.OperationalMetrics = &gatewayv1alpha1.OperationalMetrics{}
	}

	gateway.Status.OperationalMetrics.LastReconcileTime = metav1.Time{Time: reconcileStart}
	gateway.Status.OperationalMetrics.SuccessfulReconciles++
	gateway.Status.OperationalMetrics.AverageReconcileTime = &metav1.Duration{Duration: duration}

	// Note: TailscaleAPIStatus is nested under TailnetStatus for per-tailnet tracking
}

// Helper functions for naming

func (r *TailscaleGatewayReconciler) extensionServerName(gateway *gatewayv1alpha1.TailscaleGateway) string {
	return fmt.Sprintf("%s-extension-server", gateway.Name)
}

func (r *TailscaleGatewayReconciler) extensionServerServiceEndpoint(gateway *gatewayv1alpha1.TailscaleGateway) string {
	config := gateway.Spec.ExtensionServer
	port := defaultExtensionServerPort
	if config.Port != nil {
		port = int(*config.Port)
	}
	return fmt.Sprintf("%s.%s.svc.cluster.local:%d", r.extensionServerName(gateway), gateway.Namespace, port)
}

func (r *TailscaleGatewayReconciler) endpointsName(gateway *gatewayv1alpha1.TailscaleGateway, tailnetName string) string {
	return fmt.Sprintf("%s-%s-endpoints", gateway.Name, tailnetName)
}

func (r *TailscaleGatewayReconciler) labelsForExtensionServer(gateway *gatewayv1alpha1.TailscaleGateway) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":        "tailscale-gateway-extension",
		"app.kubernetes.io/instance":    gateway.Name,
		"app.kubernetes.io/component":   "extension-server",
		"app.kubernetes.io/part-of":     "tailscale-gateway",
		"gateway.tailscale.com/gateway": gateway.Name,
	}
}

// generateServiceName generates a service name from target backend (matching ServiceCoordinator logic)
func (r *TailscaleGatewayReconciler) generateServiceName(targetBackend string) tailscale.ServiceName {
	return r.ServiceCoordinator.GenerateServiceName(targetBackend)
}

// SetupWithManager sets up the controller with the Manager.
func (r *TailscaleGatewayReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&gatewayv1alpha1.TailscaleGateway{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Owns(&gatewayv1alpha1.TailscaleEndpoints{}).
		Watches(
			&gwapiv1.HTTPRoute{},
			handler.EnqueueRequestsFromMapFunc(r.MapHTTPRouteToTailscaleGateway),
		).
		Watches(
			&gatewayv1alpha1.TailscaleTailnet{},
			handler.EnqueueRequestsFromMapFunc(r.MapTailnetToTailscaleGateway),
		).
		Complete(r)
}

// MapHTTPRouteToTailscaleGateway maps HTTPRoute changes to TailscaleGateway reconciliation requests
func (r *TailscaleGatewayReconciler) MapHTTPRouteToTailscaleGateway(ctx context.Context, obj client.Object) []ctrl.Request {
	httpRoute := obj.(*gwapiv1.HTTPRoute)

	// Check if this HTTPRoute references a TailscaleGateway
	if gatewayRef, exists := httpRoute.Annotations["gateway.tailscale.com/gateway"]; exists {
		return []ctrl.Request{
			{
				NamespacedName: types.NamespacedName{
					Name:      gatewayRef,
					Namespace: httpRoute.Namespace,
				},
			},
		}
	}

	return nil
}

// MapTailnetToTailscaleGateway maps TailscaleTailnet changes to TailscaleGateway reconciliation requests
func (r *TailscaleGatewayReconciler) MapTailnetToTailscaleGateway(ctx context.Context, obj client.Object) []ctrl.Request {
	tailnet := obj.(*gatewayv1alpha1.TailscaleTailnet)

	// Find all TailscaleGateways that reference this tailnet
	gatewayList := &gatewayv1alpha1.TailscaleGatewayList{}
	if err := r.List(context.Background(), gatewayList, client.InNamespace(tailnet.Namespace)); err != nil {
		if r.Logger != nil {
			r.Logger.Errorf("Failed to list TailscaleGateways for tailnet mapping: %v", err)
		}
		return nil
	}

	var requests []ctrl.Request
	for _, gateway := range gatewayList.Items {
		for _, tailnetConfig := range gateway.Spec.Tailnets {
			tailnetRef := tailnetConfig.TailscaleTailnetRef
			refNamespace := tailnet.Namespace
			if tailnetRef.Namespace != nil {
				refNamespace = string(*tailnetRef.Namespace)
			}

			if string(tailnetRef.Name) == tailnet.Name && refNamespace == tailnet.Namespace {
				requests = append(requests, ctrl.Request{
					NamespacedName: types.NamespacedName{
						Name:      gateway.Name,
						Namespace: gateway.Namespace,
					},
				})
				break
			}
		}
	}

	return requests
}

// MapTCPRouteToTailscaleGateway maps TCPRoute changes to TailscaleGateway reconciliation requests
func (r *TailscaleGatewayReconciler) MapTCPRouteToTailscaleGateway(ctx context.Context, obj client.Object) []ctrl.Request {
	tcpRoute := obj.(*gwapiv1alpha2.TCPRoute)

	// Check if this TCPRoute references a TailscaleGateway through annotations
	if gatewayRef, exists := tcpRoute.Annotations["gateway.tailscale.com/gateway"]; exists {
		return []ctrl.Request{{
			NamespacedName: types.NamespacedName{
				Name:      gatewayRef,
				Namespace: tcpRoute.Namespace,
			},
		}}
	}

	// Also check if this TCPRoute has TailscaleEndpoints as backends
	for _, rule := range tcpRoute.Spec.Rules {
		for _, backendRef := range rule.BackendRefs {
			if backendRef.Group != nil && *backendRef.Group == "gateway.tailscale.com" &&
				backendRef.Kind != nil && *backendRef.Kind == "TailscaleEndpoints" {
				// Find TailscaleGateways that might be managing this route
				return r.findGatewaysForNamespace(tcpRoute.Namespace)
			}
		}
	}

	return nil
}

// MapUDPRouteToTailscaleGateway maps UDPRoute changes to TailscaleGateway reconciliation requests
func (r *TailscaleGatewayReconciler) MapUDPRouteToTailscaleGateway(ctx context.Context, obj client.Object) []ctrl.Request {
	udpRoute := obj.(*gwapiv1alpha2.UDPRoute)

	// Check if this UDPRoute references a TailscaleGateway through annotations
	if gatewayRef, exists := udpRoute.Annotations["gateway.tailscale.com/gateway"]; exists {
		return []ctrl.Request{{
			NamespacedName: types.NamespacedName{
				Name:      gatewayRef,
				Namespace: udpRoute.Namespace,
			},
		}}
	}

	// Also check if this UDPRoute has TailscaleEndpoints as backends
	for _, rule := range udpRoute.Spec.Rules {
		for _, backendRef := range rule.BackendRefs {
			if backendRef.Group != nil && *backendRef.Group == "gateway.tailscale.com" &&
				backendRef.Kind != nil && *backendRef.Kind == "TailscaleEndpoints" {
				// Find TailscaleGateways that might be managing this route
				return r.findGatewaysForNamespace(udpRoute.Namespace)
			}
		}
	}

	return nil
}

// MapTLSRouteToTailscaleGateway maps TLSRoute changes to TailscaleGateway reconciliation requests
func (r *TailscaleGatewayReconciler) MapTLSRouteToTailscaleGateway(ctx context.Context, obj client.Object) []ctrl.Request {
	tlsRoute := obj.(*gwapiv1alpha2.TLSRoute)

	// Check if this TLSRoute references a TailscaleGateway through annotations
	if gatewayRef, exists := tlsRoute.Annotations["gateway.tailscale.com/gateway"]; exists {
		return []ctrl.Request{{
			NamespacedName: types.NamespacedName{
				Name:      gatewayRef,
				Namespace: tlsRoute.Namespace,
			},
		}}
	}

	// Also check if this TLSRoute has TailscaleEndpoints as backends
	for _, rule := range tlsRoute.Spec.Rules {
		for _, backendRef := range rule.BackendRefs {
			if backendRef.Group != nil && *backendRef.Group == "gateway.tailscale.com" &&
				backendRef.Kind != nil && *backendRef.Kind == "TailscaleEndpoints" {
				// Find TailscaleGateways that might be managing this route
				return r.findGatewaysForNamespace(tlsRoute.Namespace)
			}
		}
	}

	return nil
}

// MapGRPCRouteToTailscaleGateway maps GRPCRoute changes to TailscaleGateway reconciliation requests
func (r *TailscaleGatewayReconciler) MapGRPCRouteToTailscaleGateway(ctx context.Context, obj client.Object) []ctrl.Request {
	grpcRoute := obj.(*gwapiv1.GRPCRoute)

	// Check if this GRPCRoute references a TailscaleGateway through annotations
	if gatewayRef, exists := grpcRoute.Annotations["gateway.tailscale.com/gateway"]; exists {
		return []ctrl.Request{{
			NamespacedName: types.NamespacedName{
				Name:      gatewayRef,
				Namespace: grpcRoute.Namespace,
			},
		}}
	}

	// Also check if this GRPCRoute has TailscaleEndpoints as backends
	for _, rule := range grpcRoute.Spec.Rules {
		for _, backendRef := range rule.BackendRefs {
			if backendRef.Group != nil && *backendRef.Group == "gateway.tailscale.com" &&
				backendRef.Kind != nil && *backendRef.Kind == "TailscaleEndpoints" {
				// Find TailscaleGateways that might be managing this route
				return r.findGatewaysForNamespace(grpcRoute.Namespace)
			}
		}
	}

	return nil
}

// findGatewaysForNamespace finds all TailscaleGateways in a namespace
func (r *TailscaleGatewayReconciler) findGatewaysForNamespace(namespace string) []ctrl.Request {
	gatewayList := &gatewayv1alpha1.TailscaleGatewayList{}
	if err := r.List(context.Background(), gatewayList, client.InNamespace(namespace)); err != nil {
		if r.Logger != nil {
			r.Logger.Errorf("Failed to list TailscaleGateways for namespace %s: %v", namespace, err)
		}
		return nil
	}

	var requests []ctrl.Request
	for _, gateway := range gatewayList.Items {
		requests = append(requests, ctrl.Request{
			NamespacedName: types.NamespacedName{
				Name:      gateway.Name,
				Namespace: gateway.Namespace,
			},
		})
	}

	return requests
}

// gatherServiceMetadata collects metadata needed for dynamic VIP service creation
func (r *TailscaleGatewayReconciler) gatherServiceMetadata(
	ctx context.Context,
	gateway *gatewayv1alpha1.TailscaleGateway,
	httpRoute *gwapiv1.HTTPRoute,
	targetBackend string,
) (*service.ServiceMetadata, error) {
	metadata := &service.ServiceMetadata{
		Gateway:   gateway,
		HTTPRoute: httpRoute,
	}

	// Extract service name from target backend
	serviceName, serviceNamespace := r.parseTargetBackend(targetBackend)
	if serviceName == "" {
		return nil, fmt.Errorf("could not parse service name from target backend: %s", targetBackend)
	}

	// Get the Kubernetes Service
	kubeService := &corev1.Service{}
	err := r.Get(ctx, types.NamespacedName{
		Name:      serviceName,
		Namespace: serviceNamespace,
	}, kubeService)
	if err != nil {
		return nil, fmt.Errorf("failed to get Kubernetes service %s/%s: %w", serviceNamespace, serviceName, err)
	}
	metadata.Service = kubeService

	// Get TailscaleTailnet for the first tailnet (simplified)
	if len(gateway.Spec.Tailnets) > 0 {
		tailnetConfig := gateway.Spec.Tailnets[0]
		tailnetRef := tailnetConfig.TailscaleTailnetRef

		tailnet := &gatewayv1alpha1.TailscaleTailnet{}
		tailnetNamespace := httpRoute.Namespace
		if tailnetRef.Namespace != nil {
			tailnetNamespace = string(*tailnetRef.Namespace)
		}

		err := r.Get(ctx, types.NamespacedName{
			Name:      string(tailnetRef.Name),
			Namespace: tailnetNamespace,
		}, tailnet)
		if err != nil {
			return nil, fmt.Errorf("failed to get TailscaleTailnet %s/%s: %w", tailnetNamespace, tailnetRef.Name, err)
		}
		metadata.TailscaleTailnet = tailnet
	}

	// Try to find matching TailscaleEndpoints
	endpoints := &gatewayv1alpha1.TailscaleEndpointsList{}
	err = r.List(ctx, endpoints, client.InNamespace(httpRoute.Namespace))
	if err != nil {
		return nil, fmt.Errorf("failed to list TailscaleEndpoints: %w", err)
	}

	// Find endpoints that reference our service
	for _, ep := range endpoints.Items {
		for _, endpoint := range ep.Spec.Endpoints {
			if endpoint.ExternalTarget != "" {
				if strings.Contains(endpoint.ExternalTarget, serviceName) {
					metadata.TailscaleEndpoints = &ep
					break
				}
			}
		}
		if metadata.TailscaleEndpoints != nil {
			break
		}
	}

	return metadata, nil
}

// parseTargetBackend extracts service name and namespace from target backend string
func (r *TailscaleGatewayReconciler) parseTargetBackend(targetBackend string) (string, string) {
	// Handle different formats:
	// - "service-name"
	// - "service-name.namespace"
	// - "service-name.namespace.svc.cluster.local"

	parts := strings.Split(targetBackend, ".")
	if len(parts) == 0 {
		return "", ""
	}

	serviceName := parts[0]
	serviceNamespace := "default"

	if len(parts) >= 2 && parts[1] != "" {
		serviceNamespace = parts[1]
	}

	return serviceName, serviceNamespace
}
