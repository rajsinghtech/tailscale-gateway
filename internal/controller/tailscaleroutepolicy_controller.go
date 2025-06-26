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
	"time"

	"go.uber.org/zap"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	gatewayv1alpha1 "github.com/rajsinghtech/tailscale-gateway/api/v1alpha1"
)

const (
	// TailscaleRoutePolicyFinalizer is the finalizer for TailscaleRoutePolicy resources
	TailscaleRoutePolicyFinalizer = "gateway.tailscale.com/tailscaleroutepolicy"

	// Event reasons
	ReasonPolicyApplied    = "PolicyApplied"
	ReasonPolicyFailed     = "PolicyFailed"
	ReasonTargetNotFound   = "TargetNotFound"
	ReasonValidationFailed = "ValidationFailed"
)

// TailscaleRoutePolicyReconciler reconciles a TailscaleRoutePolicy object
type TailscaleRoutePolicyReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Logger   *zap.SugaredLogger
	Recorder record.EventRecorder

	// Event channels for controller coordination
	GatewayEventChan   chan event.GenericEvent
	HTTPRouteEventChan chan event.GenericEvent
	TCPRouteEventChan  chan event.GenericEvent
	UDPRouteEventChan  chan event.GenericEvent
	TLSRouteEventChan  chan event.GenericEvent
	GRPCRouteEventChan chan event.GenericEvent
}

//+kubebuilder:rbac:groups=gateway.tailscale.com,resources=tailscaleroutepolicies,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=gateway.tailscale.com,resources=tailscaleroutepolicies/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=gateway.tailscale.com,resources=tailscaleroutepolicies/finalizers,verbs=update
//+kubebuilder:rbac:groups=gateway.tailscale.com,resources=tailscalegateways,verbs=get;list;watch
//+kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=httproutes,verbs=get;list;watch;create;update;patch
//+kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=gateways,verbs=get;list;watch
//+kubebuilder:rbac:groups="",resources=events,verbs=create;get;patch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *TailscaleRoutePolicyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Fetch the TailscaleRoutePolicy instance
	policy := &gatewayv1alpha1.TailscaleRoutePolicy{}
	if err := r.Get(ctx, req.NamespacedName, policy); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("TailscaleRoutePolicy resource not found, ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get TailscaleRoutePolicy")
		return ctrl.Result{}, err
	}

	// Handle deletion
	if policy.DeletionTimestamp != nil {
		return r.handleDeletion(ctx, policy)
	}

	// Add finalizer if not present
	if !controllerutil.ContainsFinalizer(policy, TailscaleRoutePolicyFinalizer) {
		controllerutil.AddFinalizer(policy, TailscaleRoutePolicyFinalizer)
		if err := r.Update(ctx, policy); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Reconcile the route policy
	result, err := r.reconcileRoutePolicy(ctx, policy)

	// Update status
	if err != nil {
		r.updateCondition(policy, gatewayv1alpha1.ConditionReady, metav1.ConditionFalse, gatewayv1alpha1.ReasonFailed, err.Error())
		r.Recorder.Event(policy, "Warning", ReasonPolicyFailed, err.Error())
	} else {
		r.updateCondition(policy, gatewayv1alpha1.ConditionReady, metav1.ConditionTrue, gatewayv1alpha1.ReasonReady, "Policy applied successfully")
		r.Recorder.Event(policy, "Normal", ReasonPolicyApplied, "Route policy applied successfully")

		// Notify controllers about policy changes
		r.notifyAllControllers(ctx, policy)
	}

	if statusErr := r.Status().Update(ctx, policy); statusErr != nil {
		logger.Error(statusErr, "Failed to update TailscaleRoutePolicy status")
		return ctrl.Result{}, statusErr
	}

	return result, err
}

// reconcileRoutePolicy handles the main reconciliation logic for route policies
func (r *TailscaleRoutePolicyReconciler) reconcileRoutePolicy(ctx context.Context, policy *gatewayv1alpha1.TailscaleRoutePolicy) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Validate policy rules
	if err := r.validatePolicyRules(policy); err != nil {
		r.Recorder.Event(policy, "Warning", ReasonValidationFailed, err.Error())
		return ctrl.Result{}, fmt.Errorf("policy validation failed: %w", err)
	}

	// Process each target reference
	appliedTargets := 0
	for _, targetRef := range policy.Spec.TargetRefs {
		if err := r.processTargetRef(ctx, policy, targetRef); err != nil {
			logger.Error(err, "Failed to process target reference", "target", targetRef)
			r.Recorder.Event(policy, "Warning", ReasonTargetNotFound, fmt.Sprintf("Failed to process target %s/%s: %v", targetRef.Kind, targetRef.Name, err))
			continue
		}
		appliedTargets++
	}

	if appliedTargets == 0 {
		return ctrl.Result{RequeueAfter: time.Minute}, fmt.Errorf("no target references could be processed")
	}

	logger.Info("Successfully reconciled TailscaleRoutePolicy", "policy", policy.Name, "appliedTargets", appliedTargets)
	return ctrl.Result{RequeueAfter: 5 * time.Minute}, nil
}

// processTargetRef processes a single target reference based on its kind
func (r *TailscaleRoutePolicyReconciler) processTargetRef(ctx context.Context, policy *gatewayv1alpha1.TailscaleRoutePolicy, targetRef gatewayv1alpha1.LocalPolicyTargetReference) error {
	switch targetRef.Kind {
	case "Gateway":
		return r.processGatewayTarget(ctx, policy, targetRef)
	case "HTTPRoute":
		return r.processHTTPRouteTarget(ctx, policy, targetRef)
	case "TailscaleGateway":
		return r.processTailscaleGatewayTarget(ctx, policy, targetRef)
	default:
		return fmt.Errorf("unsupported target kind: %s", targetRef.Kind)
	}
}

// processGatewayTarget processes Gateway targets
func (r *TailscaleRoutePolicyReconciler) processGatewayTarget(ctx context.Context, policy *gatewayv1alpha1.TailscaleRoutePolicy, targetRef gatewayv1alpha1.LocalPolicyTargetReference) error {
	logger := log.FromContext(ctx)

	// Get the Gateway resource
	gateway := &gwapiv1.Gateway{}
	gatewayKey := types.NamespacedName{
		Name:      string(targetRef.Name),
		Namespace: policy.Namespace,
	}

	if err := r.Get(ctx, gatewayKey, gateway); err != nil {
		return fmt.Errorf("failed to get Gateway %s: %w", gatewayKey, err)
	}

	// Apply policy rules to the Gateway
	for _, rule := range policy.Spec.Rules {
		if err := r.applyRuleToGateway(ctx, rule, gateway, policy); err != nil {
			logger.Error(err, "Failed to apply rule to Gateway", "rule", rule.Name, "gateway", gateway.Name)
			continue
		}
	}

	logger.Info("Applied policy to Gateway", "gateway", gateway.Name, "policy", policy.Name)
	return nil
}

// processHTTPRouteTarget processes HTTPRoute targets
func (r *TailscaleRoutePolicyReconciler) processHTTPRouteTarget(ctx context.Context, policy *gatewayv1alpha1.TailscaleRoutePolicy, targetRef gatewayv1alpha1.LocalPolicyTargetReference) error {
	logger := log.FromContext(ctx)

	// Get the HTTPRoute resource
	httpRoute := &gwapiv1.HTTPRoute{}
	routeKey := types.NamespacedName{
		Name:      string(targetRef.Name),
		Namespace: policy.Namespace,
	}

	if err := r.Get(ctx, routeKey, httpRoute); err != nil {
		return fmt.Errorf("failed to get HTTPRoute %s: %w", routeKey, err)
	}

	// Apply policy rules to the HTTPRoute
	for _, rule := range policy.Spec.Rules {
		if err := r.applyRuleToHTTPRoute(ctx, rule, httpRoute, policy); err != nil {
			logger.Error(err, "Failed to apply rule to HTTPRoute", "rule", rule.Name, "route", httpRoute.Name)
			continue
		}
	}

	// Update the HTTPRoute with applied policies
	if err := r.Update(ctx, httpRoute); err != nil {
		return fmt.Errorf("failed to update HTTPRoute: %w", err)
	}

	logger.Info("Applied policy to HTTPRoute", "route", httpRoute.Name, "policy", policy.Name)
	return nil
}

// processTailscaleGatewayTarget processes TailscaleGateway targets
func (r *TailscaleRoutePolicyReconciler) processTailscaleGatewayTarget(ctx context.Context, policy *gatewayv1alpha1.TailscaleRoutePolicy, targetRef gatewayv1alpha1.LocalPolicyTargetReference) error {
	logger := log.FromContext(ctx)

	// Get the TailscaleGateway resource
	tsGateway := &gatewayv1alpha1.TailscaleGateway{}
	gatewayKey := types.NamespacedName{
		Name:      string(targetRef.Name),
		Namespace: policy.Namespace,
	}

	if err := r.Get(ctx, gatewayKey, tsGateway); err != nil {
		return fmt.Errorf("failed to get TailscaleGateway %s: %w", gatewayKey, err)
	}

	// Apply policy rules to the TailscaleGateway
	for _, rule := range policy.Spec.Rules {
		if err := r.applyRuleToTailscaleGateway(ctx, rule, tsGateway, policy); err != nil {
			logger.Error(err, "Failed to apply rule to TailscaleGateway", "rule", rule.Name, "gateway", tsGateway.Name)
			continue
		}
	}

	logger.Info("Applied policy to TailscaleGateway", "gateway", tsGateway.Name, "policy", policy.Name)
	return nil
}

// validatePolicyRules validates the policy rules for correctness
func (r *TailscaleRoutePolicyReconciler) validatePolicyRules(policy *gatewayv1alpha1.TailscaleRoutePolicy) error {
	if len(policy.Spec.Rules) == 0 {
		return fmt.Errorf("policy must have at least one rule")
	}

	for i, rule := range policy.Spec.Rules {
		if rule.Name == nil || *rule.Name == "" {
			return fmt.Errorf("rule %d must have a name", i)
		}

		// Validate that rule has at least one condition or action
		// Note: The exact structure of PolicyRule would need to be defined in the CRD
		// This is a placeholder for validation logic
	}

	return nil
}

// applyRuleToGateway applies a policy rule to a Gateway resource
func (r *TailscaleRoutePolicyReconciler) applyRuleToGateway(ctx context.Context, rule gatewayv1alpha1.PolicyRule, gateway *gwapiv1.Gateway, policy *gatewayv1alpha1.TailscaleRoutePolicy) error {
	// Add policy metadata to Gateway annotations
	if gateway.Annotations == nil {
		gateway.Annotations = make(map[string]string)
	}

	policyKey := fmt.Sprintf("gateway.tailscale.com/route-policy-%s", policy.Name)
	ruleName := "unnamed"
	if rule.Name != nil {
		ruleName = *rule.Name
	}
	gateway.Annotations[policyKey] = ruleName

	// Update the Gateway
	return r.Update(ctx, gateway)
}

// applyRuleToHTTPRoute applies a policy rule to an HTTPRoute resource
func (r *TailscaleRoutePolicyReconciler) applyRuleToHTTPRoute(ctx context.Context, rule gatewayv1alpha1.PolicyRule, httpRoute *gwapiv1.HTTPRoute, policy *gatewayv1alpha1.TailscaleRoutePolicy) error {
	// Add policy metadata to HTTPRoute annotations
	if httpRoute.Annotations == nil {
		httpRoute.Annotations = make(map[string]string)
	}

	policyKey := fmt.Sprintf("gateway.tailscale.com/route-policy-%s", policy.Name)
	ruleName := "unnamed"
	if rule.Name != nil {
		ruleName = *rule.Name
	}
	httpRoute.Annotations[policyKey] = ruleName

	// Apply Tailscale-specific routing transformations based on rule
	// This would include things like:
	// - Path rewriting for Tailscale services
	// - Header injection for service identification
	// - Backend selection based on Tailscale topology

	return nil
}

// applyRuleToTailscaleGateway applies a policy rule to a TailscaleGateway resource
func (r *TailscaleRoutePolicyReconciler) applyRuleToTailscaleGateway(ctx context.Context, rule gatewayv1alpha1.PolicyRule, tsGateway *gatewayv1alpha1.TailscaleGateway, policy *gatewayv1alpha1.TailscaleRoutePolicy) error {
	// Apply policy to TailscaleGateway configuration
	// This could modify:
	// - Service discovery patterns
	// - Route generation configurations
	// - Extension server settings

	// For now, just add metadata
	if tsGateway.Annotations == nil {
		tsGateway.Annotations = make(map[string]string)
	}

	policyKey := fmt.Sprintf("gateway.tailscale.com/route-policy-%s", policy.Name)
	ruleName := "unnamed"
	if rule.Name != nil {
		ruleName = *rule.Name
	}
	tsGateway.Annotations[policyKey] = ruleName

	return r.Update(ctx, tsGateway)
}

// handleDeletion handles cleanup when TailscaleRoutePolicy is being deleted
func (r *TailscaleRoutePolicyReconciler) handleDeletion(ctx context.Context, policy *gatewayv1alpha1.TailscaleRoutePolicy) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	if controllerutil.ContainsFinalizer(policy, TailscaleRoutePolicyFinalizer) {
		// Clean up policy applications from target resources
		logger.Info("Cleaning up TailscaleRoutePolicy applications", "policy", policy.Name)

		for _, targetRef := range policy.Spec.TargetRefs {
			if err := r.cleanupTargetRef(ctx, policy, targetRef); err != nil {
				logger.Error(err, "Failed to cleanup target reference", "target", targetRef)
				// Continue with other targets even if one fails
			}
		}

		// Remove finalizer
		controllerutil.RemoveFinalizer(policy, TailscaleRoutePolicyFinalizer)
		if err := r.Update(ctx, policy); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

// cleanupTargetRef removes policy applications from a target resource
func (r *TailscaleRoutePolicyReconciler) cleanupTargetRef(ctx context.Context, policy *gatewayv1alpha1.TailscaleRoutePolicy, targetRef gatewayv1alpha1.LocalPolicyTargetReference) error {
	policyKey := fmt.Sprintf("gateway.tailscale.com/route-policy-%s", policy.Name)

	switch targetRef.Kind {
	case "Gateway":
		gateway := &gwapiv1.Gateway{}
		key := types.NamespacedName{Name: string(targetRef.Name), Namespace: policy.Namespace}
		if err := r.Get(ctx, key, gateway); err != nil {
			if apierrors.IsNotFound(err) {
				return nil // Resource already deleted
			}
			return err
		}
		if gateway.Annotations != nil {
			delete(gateway.Annotations, policyKey)
			return r.Update(ctx, gateway)
		}

	case "HTTPRoute":
		httpRoute := &gwapiv1.HTTPRoute{}
		key := types.NamespacedName{Name: string(targetRef.Name), Namespace: policy.Namespace}
		if err := r.Get(ctx, key, httpRoute); err != nil {
			if apierrors.IsNotFound(err) {
				return nil // Resource already deleted
			}
			return err
		}
		if httpRoute.Annotations != nil {
			delete(httpRoute.Annotations, policyKey)
			return r.Update(ctx, httpRoute)
		}

	case "TailscaleGateway":
		tsGateway := &gatewayv1alpha1.TailscaleGateway{}
		key := types.NamespacedName{Name: string(targetRef.Name), Namespace: policy.Namespace}
		if err := r.Get(ctx, key, tsGateway); err != nil {
			if apierrors.IsNotFound(err) {
				return nil // Resource already deleted
			}
			return err
		}
		if tsGateway.Annotations != nil {
			delete(tsGateway.Annotations, policyKey)
			return r.Update(ctx, tsGateway)
		}
	}

	return nil
}

// updateCondition updates or adds a condition to the TailscaleRoutePolicy status
func (r *TailscaleRoutePolicyReconciler) updateCondition(policy *gatewayv1alpha1.TailscaleRoutePolicy, conditionType string, status metav1.ConditionStatus, reason, message string) {
	gatewayv1alpha1.SetCondition(&policy.Status.Conditions, conditionType, status, reason, message)
}

// Event notification helper functions for controller coordination

// notifyGatewayController sends an event to the Gateway controller when policies change
func (r *TailscaleRoutePolicyReconciler) notifyGatewayController(ctx context.Context, obj client.Object) {
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
		// Critical: Channel is full - this could lead to missed reconciliation events
		logger.Error(nil, "Gateway event channel full, dropping critical notification - this may cause inconsistent state",
			"object", obj.GetName(), "namespace", obj.GetNamespace(),
			"action", "investigate_channel_buffer_size_or_consumer_performance")
	}
}

// notifyRouteControllers sends events to all route controllers when policies are applied
func (r *TailscaleRoutePolicyReconciler) notifyRouteControllers(ctx context.Context, obj client.Object) {
	logger := log.FromContext(ctx)
	droppedNotifications := 0

	// Helper function to send notification with proper error handling
	sendNotification := func(ch chan event.GenericEvent, routeType string) {
		if ch == nil {
			return
		}
		select {
		case ch <- event.GenericEvent{Object: obj}:
			logger.V(1).Info("Notified route controller", "routeType", routeType, "object", obj.GetName(), "namespace", obj.GetNamespace())
		case <-ctx.Done():
			return
		default:
			logger.Error(nil, "Route controller event channel full, dropping critical notification",
				"routeType", routeType, "object", obj.GetName(), "namespace", obj.GetNamespace(),
				"action", "investigate_channel_buffer_size_or_consumer_performance")
			droppedNotifications++
		}
	}

	// Send notifications to all route controllers
	sendNotification(r.HTTPRouteEventChan, "HTTPRoute")
	sendNotification(r.TCPRouteEventChan, "TCPRoute")
	sendNotification(r.UDPRouteEventChan, "UDPRoute")
	sendNotification(r.TLSRouteEventChan, "TLSRoute")
	sendNotification(r.GRPCRouteEventChan, "GRPCRoute")

	// Log summary if any notifications were dropped
	if droppedNotifications > 0 {
		logger.Error(nil, "Multiple route controller notifications dropped",
			"droppedCount", droppedNotifications, "object", obj.GetName(), "namespace", obj.GetNamespace(),
			"action", "check_event_channel_buffer_sizes_and_consumer_performance")
	}
}

// notifyAllControllers sends events to all dependent controllers
func (r *TailscaleRoutePolicyReconciler) notifyAllControllers(ctx context.Context, obj client.Object) {
	r.notifyGatewayController(ctx, obj)
	r.notifyRouteControllers(ctx, obj)
}

// SetupWithManager sets up the controller with the Manager.
func (r *TailscaleRoutePolicyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&gatewayv1alpha1.TailscaleRoutePolicy{}).
		Watches(
			&gwapiv1.Gateway{},
			handler.EnqueueRequestsFromMapFunc(r.MapGatewayToRoutePolicies),
		).
		Watches(
			&gwapiv1.HTTPRoute{},
			handler.EnqueueRequestsFromMapFunc(r.MapHTTPRouteToRoutePolicies),
		).
		Watches(
			&gatewayv1alpha1.TailscaleGateway{},
			handler.EnqueueRequestsFromMapFunc(r.MapTailscaleGatewayToRoutePolicies),
		).
		Complete(r)
}

// MapGatewayToRoutePolicies maps Gateway events to TailscaleRoutePolicy reconcile requests
func (r *TailscaleRoutePolicyReconciler) MapGatewayToRoutePolicies(ctx context.Context, obj client.Object) []ctrl.Request {
	gateway := obj.(*gwapiv1.Gateway)

	// Find TailscaleRoutePolicies that target this Gateway
	var policies gatewayv1alpha1.TailscaleRoutePolicyList
	if err := r.List(ctx, &policies, client.InNamespace(gateway.Namespace)); err != nil {
		return nil
	}

	var requests []ctrl.Request
	for _, policy := range policies.Items {
		for _, targetRef := range policy.Spec.TargetRefs {
			if targetRef.Kind == "Gateway" && string(targetRef.Name) == gateway.Name {
				requests = append(requests, ctrl.Request{
					NamespacedName: types.NamespacedName{
						Name:      policy.Name,
						Namespace: policy.Namespace,
					},
				})
				break
			}
		}
	}

	return requests
}

// mapHTTPRouteToRoutePolicies maps HTTPRoute events to TailscaleRoutePolicy reconcile requests
func (r *TailscaleRoutePolicyReconciler) MapHTTPRouteToRoutePolicies(ctx context.Context, obj client.Object) []ctrl.Request {
	httpRoute := obj.(*gwapiv1.HTTPRoute)

	// Find TailscaleRoutePolicies that target this HTTPRoute
	var policies gatewayv1alpha1.TailscaleRoutePolicyList
	if err := r.List(ctx, &policies, client.InNamespace(httpRoute.Namespace)); err != nil {
		return nil
	}

	var requests []ctrl.Request
	for _, policy := range policies.Items {
		for _, targetRef := range policy.Spec.TargetRefs {
			if targetRef.Kind == "HTTPRoute" && string(targetRef.Name) == httpRoute.Name {
				requests = append(requests, ctrl.Request{
					NamespacedName: types.NamespacedName{
						Name:      policy.Name,
						Namespace: policy.Namespace,
					},
				})
				break
			}
		}
	}

	return requests
}

// mapTailscaleGatewayToRoutePolicies maps TailscaleGateway events to TailscaleRoutePolicy reconcile requests
func (r *TailscaleRoutePolicyReconciler) MapTailscaleGatewayToRoutePolicies(ctx context.Context, obj client.Object) []ctrl.Request {
	tsGateway := obj.(*gatewayv1alpha1.TailscaleGateway)

	// Find TailscaleRoutePolicies that target this TailscaleGateway
	var policies gatewayv1alpha1.TailscaleRoutePolicyList
	if err := r.List(ctx, &policies, client.InNamespace(tsGateway.Namespace)); err != nil {
		return nil
	}

	var requests []ctrl.Request
	for _, policy := range policies.Items {
		for _, targetRef := range policy.Spec.TargetRefs {
			if targetRef.Kind == "TailscaleGateway" && string(targetRef.Name) == tsGateway.Name {
				requests = append(requests, ctrl.Request{
					NamespacedName: types.NamespacedName{
						Name:      policy.Name,
						Namespace: policy.Namespace,
					},
				})
				break
			}
		}
	}

	return requests
}
