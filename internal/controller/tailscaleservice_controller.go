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
	"strconv"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	gatewayv1alpha1 "github.com/rajsinghtech/tailscale-gateway/api/v1alpha1"
	"github.com/rajsinghtech/tailscale-gateway/internal/proxy"
	"github.com/rajsinghtech/tailscale-gateway/internal/tailscale"
)

// TailscaleServiceReconciler reconciles a TailscaleService object
type TailscaleServiceReconciler struct {
	client.Client
	Scheme       *runtime.Scheme
	TsClient     tailscale.Client
	ManagerID    string
	ProxyBuilder *proxy.ProxyBuilder
}

//+kubebuilder:rbac:groups=gateway.tailscale.com,resources=tailscaleservice,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=gateway.tailscale.com,resources=tailscaleservice/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=gateway.tailscale.com,resources=tailscaleservice/finalizers,verbs=update
//+kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *TailscaleServiceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("tailscaleservice", req.NamespacedName)
	logger.Info("Starting reconciliation")

	// Fetch the TailscaleService instance
	tailscaleService := &gatewayv1alpha1.TailscaleService{}
	if err := r.Get(ctx, req.NamespacedName, tailscaleService); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("TailscaleService not found, likely deleted")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get TailscaleService")
		return ctrl.Result{}, err
	}

	// Handle deletion
	if !tailscaleService.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, tailscaleService)
	}

	// Add finalizer if not present
	if !controllerutil.ContainsFinalizer(tailscaleService, "tailscale.com/finalizer") {
		controllerutil.AddFinalizer(tailscaleService, "tailscale.com/finalizer")
		if err := r.Update(ctx, tailscaleService); err != nil {
			logger.Error(err, "Failed to add finalizer")
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// Initialize status if needed
	if tailscaleService.Status.VIPServiceStatus == nil {
		tailscaleService.Status.VIPServiceStatus = &gatewayv1alpha1.ServiceStatus{}
	}

	// Reconcile VIP service
	if err := r.reconcileVIPService(ctx, tailscaleService); err != nil {
		r.updateCondition(tailscaleService, "VIPServiceReady", metav1.ConditionFalse, "VIPServiceError", err.Error())
		logger.Error(err, "Failed to reconcile VIP service")
		return r.updateStatusAndRequeue(ctx, tailscaleService, time.Minute)
	}
	r.updateCondition(tailscaleService, "VIPServiceReady", metav1.ConditionTrue, "VIPServiceReady", "VIP service is ready")

	// Reconcile proxy infrastructure if specified
	if tailscaleService.Spec.Proxy != nil {
		if err := r.reconcileProxy(ctx, tailscaleService); err != nil {
			r.updateCondition(tailscaleService, "ProxyReady", metav1.ConditionFalse, "ProxyError", err.Error())
			logger.Error(err, "Failed to reconcile proxy")
			return r.updateStatusAndRequeue(ctx, tailscaleService, time.Minute)
		}
		r.updateCondition(tailscaleService, "ProxyReady", metav1.ConditionTrue, "ProxyReady", "Proxy infrastructure is ready")
	}

	// Update backend status
	r.updateBackendStatus(ctx, tailscaleService)

	// Set overall ready condition
	r.updateCondition(tailscaleService, "Ready", metav1.ConditionTrue, "Ready", "TailscaleService is ready")

	// Update status
	now := metav1.Now()
	tailscaleService.Status.LastUpdate = &now
	if err := r.Status().Update(ctx, tailscaleService); err != nil {
		logger.Error(err, "Failed to update status")
		return ctrl.Result{}, err
	}

	logger.Info("Reconciliation completed successfully")
	return ctrl.Result{RequeueAfter: time.Minute * 5}, nil
}

// reconcileVIPService creates or updates the Tailscale VIP service
func (r *TailscaleServiceReconciler) reconcileVIPService(ctx context.Context, ts *gatewayv1alpha1.TailscaleService) error {
	logger := log.FromContext(ctx)

	// Determine VIP service name
	vipServiceName := ts.Spec.VIPService.Name
	if vipServiceName == "" {
		vipServiceName = fmt.Sprintf("svc:%s", ts.Name)
	}

	// Skip VIP service creation if TsClient is not available
	if r.TsClient == nil {
		logger.Info("TsClient not configured, skipping VIP service creation", "serviceName", vipServiceName)
		// Update status to show service is configured (but not created)
		ts.Status.VIPServiceStatus.Created = false
		ts.Status.VIPServiceStatus.ServiceName = vipServiceName
		ts.Status.VIPServiceStatus.ServiceTags = ts.Spec.VIPService.Tags
		ts.Status.VIPServiceStatus.DNSName = fmt.Sprintf("%s.tailnet.ts.net", vipServiceName)
		return nil
	}

	// Check if VIP service already exists
	serviceName := tailscale.ServiceName(vipServiceName)
	existingService, err := r.TsClient.GetVIPService(ctx, serviceName)
	if err == nil && existingService != nil {
		logger.Info("VIP service already exists", "serviceName", vipServiceName)
		// Update status
		ts.Status.VIPServiceStatus.Created = true
		ts.Status.VIPServiceStatus.ServiceName = vipServiceName
		ts.Status.VIPServiceStatus.ServiceTags = ts.Spec.VIPService.Tags
		return nil
	}

	// Create VIP service
	vipService := &tailscale.VIPService{
		Name:    serviceName,
		Ports:   ts.Spec.VIPService.Ports,
		Tags:    ts.Spec.VIPService.Tags,
		Comment: ts.Spec.VIPService.Comment,
	}

	logger.Info("Creating VIP service", "serviceName", vipServiceName, "ports", vipService.Ports, "tags", vipService.Tags)
	
	if err := r.TsClient.CreateOrUpdateVIPService(ctx, vipService); err != nil {
		return fmt.Errorf("failed to create VIP service: %w", err)
	}

	// Update status
	ts.Status.VIPServiceStatus.Created = true
	ts.Status.VIPServiceStatus.ServiceName = vipServiceName
	ts.Status.VIPServiceStatus.ServiceTags = ts.Spec.VIPService.Tags
	
	// Try to get the service details for status
	if createdService, err := r.TsClient.GetVIPService(ctx, serviceName); err == nil && createdService != nil {
		ts.Status.VIPServiceStatus.Addresses = createdService.Addrs
		// Generate DNS name based on tailnet (would need tailnet info for full FQDN)
		ts.Status.VIPServiceStatus.DNSName = fmt.Sprintf("%s.tailnet.ts.net", vipServiceName)
	}

	logger.Info("VIP service created successfully", "serviceName", vipServiceName)
	return nil
}

// reconcileProxy creates or updates the proxy infrastructure using ProxyBuilder
func (r *TailscaleServiceReconciler) reconcileProxy(ctx context.Context, ts *gatewayv1alpha1.TailscaleService) error {
	logger := log.FromContext(ctx)

	if ts.Spec.Tailnet == "" {
		return fmt.Errorf("tailnet is required when proxy is specified")
	}

	// Ensure ProxyBuilder is available
	if r.ProxyBuilder == nil {
		r.ProxyBuilder = proxy.NewProxyBuilder(r.Client)
	}

	// Build proxy specification
	proxySpec := &proxy.ProxySpec{
		Name:           r.getProxyStatefulSetName(ts),
		Namespace:      ts.Namespace,
		Tailnet:        ts.Spec.Tailnet,
		ConnectionType: "bidirectional", // TailscaleServices default to bidirectional
		Tags:           ts.Spec.VIPService.Tags, // Use VIP service tags for proxy
		Ports:          r.convertVIPPortsToPortMappings(ts.Spec.VIPService.Ports),
		ProxyConfig:    ts.Spec.Proxy,
		OwnerRef: metav1.OwnerReference{
			APIVersion: ts.APIVersion,
			Kind:       ts.Kind,
			Name:       ts.Name,
			UID:        ts.UID,
			Controller: &[]bool{true}[0],
		},
		Labels: map[string]string{
			"app":                             "tailscale-proxy",
			"tailscale.com/managed":           "true",
			"tailscale.com/parent-resource":   ts.Name,
			"tailscale.com/parent-type":       "TailscaleService",
		},
		Replicas: r.getProxyReplicas(ts),
	}

	// Create proxy infrastructure using ProxyBuilder
	if err := r.ProxyBuilder.CreateProxyInfrastructure(ctx, proxySpec); err != nil {
		return fmt.Errorf("failed to create proxy infrastructure: %w", err)
	}

	// Update proxy status
	ts.Status.ProxyStatus = &gatewayv1alpha1.ProxyStatus{
		StatefulSetName: proxySpec.Name,
		Replicas:        proxySpec.Replicas,
		ServiceName:     proxySpec.Name, // Service has same name as StatefulSet
	}

	logger.Info("Proxy infrastructure reconciled using ProxyBuilder")
	return nil
}

// convertVIPPortsToPortMappings converts VIP service port strings to PortMapping structs
func (r *TailscaleServiceReconciler) convertVIPPortsToPortMappings(vipPorts []string) []proxy.PortMapping {
	var portMappings []proxy.PortMapping
	
	for i, portStr := range vipPorts {
		// Parse port strings like "tcp:80", "udp:53", or just "80"
		protocol := "TCP"
		portNum := "80"
		
		if strings.Contains(portStr, ":") {
			parts := strings.SplitN(portStr, ":", 2)
			if len(parts) == 2 {
				protocol = strings.ToUpper(parts[0])
				portNum = parts[1]
			}
		} else {
			portNum = portStr
		}
		
		// Convert port string to int32
		if port, err := strconv.ParseInt(portNum, 10, 32); err == nil {
			portMappings = append(portMappings, proxy.PortMapping{
				Name:     fmt.Sprintf("port-%d", i),
				Port:     int32(port),
				Protocol: protocol,
			})
		}
	}
	
	// Default to port 80 if no ports specified
	if len(portMappings) == 0 {
		portMappings = append(portMappings, proxy.PortMapping{
			Name:     "http",
			Port:     80,
			Protocol: "TCP",
		})
	}
	
	return portMappings
}


// updateBackendStatus updates the status of configured backends
func (r *TailscaleServiceReconciler) updateBackendStatus(ctx context.Context, ts *gatewayv1alpha1.TailscaleService) {
	backendStatus := make([]gatewayv1alpha1.BackendStatus, len(ts.Spec.Backends))
	totalBackends := len(ts.Spec.Backends)
	healthyBackends := 0

	for i, backend := range ts.Spec.Backends {
		weight := int32(1)
		if backend.Weight != nil {
			weight = *backend.Weight
		}
		
		priority := int32(0)
		if backend.Priority != nil {
			priority = *backend.Priority
		}

		target := ""
		switch backend.Type {
		case "kubernetes":
			target = backend.Service
		case "external":
			target = backend.Address
		case "tailscale":
			target = backend.TailscaleService
		}

		// For now, assume all backends are healthy (could add real health checking later)
		healthy := true
		if healthy {
			healthyBackends++
		}

		backendStatus[i] = gatewayv1alpha1.BackendStatus{
			Type:     backend.Type,
			Target:   target,
			Healthy:  healthy,
			Weight:   weight,
			Priority: priority,
		}
	}

	ts.Status.BackendStatus = backendStatus
	ts.Status.TotalBackends = totalBackends
	ts.Status.HealthyBackends = healthyBackends

	// Update VIP service backend counts
	if ts.Status.VIPServiceStatus != nil {
		ts.Status.VIPServiceStatus.BackendCount = totalBackends
		ts.Status.VIPServiceStatus.HealthyBackendCount = healthyBackends
	}
}

// handleDeletion handles TailscaleService deletion
func (r *TailscaleServiceReconciler) handleDeletion(ctx context.Context, ts *gatewayv1alpha1.TailscaleService) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Handling TailscaleService deletion")

	// Delete VIP service if it exists and TsClient is available
	if r.TsClient != nil && ts.Status.VIPServiceStatus != nil && ts.Status.VIPServiceStatus.Created {
		serviceName := tailscale.ServiceName(ts.Status.VIPServiceStatus.ServiceName)
		if err := r.TsClient.DeleteVIPService(ctx, serviceName); err != nil {
			logger.Error(err, "Failed to delete VIP service", "serviceName", ts.Status.VIPServiceStatus.ServiceName)
			// Don't block deletion on VIP service deletion failure
		} else {
			logger.Info("VIP service deleted", "serviceName", ts.Status.VIPServiceStatus.ServiceName)
		}
	}

	// Remove finalizer
	controllerutil.RemoveFinalizer(ts, "tailscale.com/finalizer")
	if err := r.Update(ctx, ts); err != nil {
		logger.Error(err, "Failed to remove finalizer")
		return ctrl.Result{}, err
	}

	logger.Info("TailscaleService deletion completed")
	return ctrl.Result{}, nil
}

// Helper methods
func (r *TailscaleServiceReconciler) getProxyStatefulSetName(ts *gatewayv1alpha1.TailscaleService) string {
	return fmt.Sprintf("%s-proxy", ts.Name)
}

func (r *TailscaleServiceReconciler) getProxyServiceName(ts *gatewayv1alpha1.TailscaleService) string {
	return fmt.Sprintf("%s-proxy-svc", ts.Name)
}

func (r *TailscaleServiceReconciler) getProxyReplicas(ts *gatewayv1alpha1.TailscaleService) int32 {
	if ts.Spec.Proxy != nil && ts.Spec.Proxy.Replicas != nil {
		return *ts.Spec.Proxy.Replicas
	}
	return 2 // default
}

func (r *TailscaleServiceReconciler) getProxyImage(ts *gatewayv1alpha1.TailscaleService) string {
	if ts.Spec.Proxy != nil && ts.Spec.Proxy.Image != "" {
		return ts.Spec.Proxy.Image
	}
	return "tailscale/tailscale:latest" // default
}

func (r *TailscaleServiceReconciler) updateCondition(ts *gatewayv1alpha1.TailscaleService, conditionType string, status metav1.ConditionStatus, reason, message string) {
	condition := metav1.Condition{
		Type:               conditionType,
		Status:             status,
		LastTransitionTime: metav1.Now(),
		Reason:             reason,
		Message:            message,
	}

	// Find existing condition
	for i, existingCondition := range ts.Status.Conditions {
		if existingCondition.Type == conditionType {
			ts.Status.Conditions[i] = condition
			return
		}
	}

	// Add new condition
	ts.Status.Conditions = append(ts.Status.Conditions, condition)
}

func (r *TailscaleServiceReconciler) updateStatusAndRequeue(ctx context.Context, ts *gatewayv1alpha1.TailscaleService, after time.Duration) (ctrl.Result, error) {
	now := metav1.Now()
	ts.Status.LastUpdate = &now
	
	if err := r.Status().Update(ctx, ts); err != nil {
		return ctrl.Result{}, err
	}
	
	return ctrl.Result{RequeueAfter: after}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *TailscaleServiceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&gatewayv1alpha1.TailscaleService{}).
		Complete(r)
}