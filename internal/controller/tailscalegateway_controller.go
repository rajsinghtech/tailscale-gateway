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
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"
	"go.uber.org/zap"

	gatewayv1alpha1 "github.com/rajsinghtech/tailscale-gateway/api/v1alpha1"
)

// TailscaleGatewayReconciler reconciles a TailscaleGateway object (simplified)
type TailscaleGatewayReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Logger   *zap.SugaredLogger
	Recorder record.EventRecorder
}

//+kubebuilder:rbac:groups=gateway.tailscale.com,resources=tailscalegateways,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=gateway.tailscale.com,resources=tailscalegateways/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=gateway.tailscale.com,resources=tailscalegateways/finalizers,verbs=update
//+kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=gateways,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop
func (r *TailscaleGatewayReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Fetch the TailscaleGateway instance
	gateway := &gatewayv1alpha1.TailscaleGateway{}
	if err := r.Get(ctx, req.NamespacedName, gateway); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("TailscaleGateway not found, likely deleted")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get TailscaleGateway")
		return ctrl.Result{}, err
	}

	// Handle deletion
	if !gateway.DeletionTimestamp.IsZero() {
		logger.Info("TailscaleGateway is being deleted")
		return ctrl.Result{}, nil
	}

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

	// Set overall ready condition
	r.updateCondition(gateway, "Ready", metav1.ConditionTrue, "GatewayConfigured", "TailscaleGateway configuration is valid")

	// Update status
	if err := r.Status().Update(ctx, gateway); err != nil {
		logger.Error(err, "Failed to update TailscaleGateway status")
		return ctrl.Result{}, err
	}

	logger.Info("TailscaleGateway reconciled successfully")
	return ctrl.Result{RequeueAfter: time.Minute * 5}, nil
}

// validateGatewayRef validates that the referenced Gateway exists
func (r *TailscaleGatewayReconciler) validateGatewayRef(ctx context.Context, gateway *gatewayv1alpha1.TailscaleGateway) error {
	gatewayRef := gateway.Spec.GatewayRef
	if gatewayRef.Name == "" {
		return nil // No gateway reference to validate
	}

	gatewayObj := &gwapiv1.Gateway{}
	gatewayNamespace := gateway.Namespace // Default to same namespace
	if gatewayRef.Namespace != nil {
		gatewayNamespace = string(*gatewayRef.Namespace)
	}

	gatewayKey := types.NamespacedName{
		Name:      string(gatewayRef.Name),
		Namespace: gatewayNamespace,
	}

	return r.Get(ctx, gatewayKey, gatewayObj)
}

// validateTailnetRefs validates that all referenced tailnets exist
func (r *TailscaleGatewayReconciler) validateTailnetRefs(ctx context.Context, gateway *gatewayv1alpha1.TailscaleGateway) error {
	for _, tailnetConfig := range gateway.Spec.Tailnets {
		tailnet := &gatewayv1alpha1.TailscaleTailnet{}
		tailnetKey := types.NamespacedName{
			Name:      tailnetConfig.Name,
			Namespace: gateway.Namespace,
		}

		if err := r.Get(ctx, tailnetKey, tailnet); err != nil {
			return err
		}
	}
	return nil
}

// updateCondition updates or adds a condition to the TailscaleGateway status
func (r *TailscaleGatewayReconciler) updateCondition(gateway *gatewayv1alpha1.TailscaleGateway, conditionType string, status metav1.ConditionStatus, reason, message string) {
	condition := metav1.Condition{
		Type:               conditionType,
		Status:             status,
		LastTransitionTime: metav1.Now(),
		Reason:             reason,
		Message:            message,
	}

	// Find existing condition
	for i, existingCondition := range gateway.Status.Conditions {
		if existingCondition.Type == conditionType {
			gateway.Status.Conditions[i] = condition
			return
		}
	}

	// Add new condition
	gateway.Status.Conditions = append(gateway.Status.Conditions, condition)
}

// SetupWithManager sets up the controller with the Manager.
func (r *TailscaleGatewayReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&gatewayv1alpha1.TailscaleGateway{}).
		Complete(r)
}