// Copyright (c) Tailscale Gateway Authors
// SPDX-License-Identifier: BSD-3-Clause

package controller

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	tailscaleclient "tailscale.com/client/tailscale/v2"

	gatewayv1alpha1 "github.com/rajsinghtech/tailscale-gateway/api/v1alpha1"
	"github.com/rajsinghtech/tailscale-gateway/internal/tailscale"
)

const (
	// Finalizer for TailscaleTailnet resources
	TailscaleTailnetFinalizer = "gateway.tailscale.com/tailnet-finalizer"

	// Default sync interval for tailnet validation
	DefaultSyncInterval = 5 * time.Minute

	// Event reasons
	ReasonTailnetValidated        = "TailnetValidated"
	ReasonTailnetValidationFailed = "TailnetValidationFailed"
	ReasonAuthenticationSucceeded = "AuthenticationSucceeded"
	ReasonAuthenticationFailed    = "AuthenticationFailed"
)

// TailscaleTailnetReconciler reconciles a TailscaleTailnet object
type TailscaleTailnetReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Logger   *zap.SugaredLogger
	Recorder record.EventRecorder

}

//+kubebuilder:rbac:groups=gateway.tailscale.com,resources=tailscaletailnets,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=gateway.tailscale.com,resources=tailscaletailnets/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=gateway.tailscale.com,resources=tailscaletailnets/finalizers,verbs=update
//+kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
//+kubebuilder:rbac:groups="",resources=events,verbs=create;get;patch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *TailscaleTailnetReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := r.Logger.With("tailnet", req.Name)
	logger.Debug("Starting reconcile")

	tailnet := &gatewayv1alpha1.TailscaleTailnet{}
	if err := r.Get(ctx, req.NamespacedName, tailnet); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Add finalizer if not present
	if !controllerutil.ContainsFinalizer(tailnet, TailscaleTailnetFinalizer) {
		controllerutil.AddFinalizer(tailnet, TailscaleTailnetFinalizer)
		return ctrl.Result{}, r.Update(ctx, tailnet)
	}

	// Handle deletion
	if !tailnet.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, tailnet)
	}

	// Track reconciliation metrics
	reconcileStart := time.Now()
	apiCallCount := int32(0)

	// Reconcile the tailnet
	result, err := r.reconcileTailnet(ctx, tailnet)
	if err != nil {
		r.recordError(tailnet, gatewayv1alpha1.ErrorCodeReconciliation, "Failed to reconcile TailscaleTailnet", "controller", err)
		return result, err
	}

	// Record successful reconciliation metrics
	r.recordMetrics(tailnet, reconcileStart, apiCallCount)
	return result, nil
}

// reconcileTailnet handles the main reconciliation logic
func (r *TailscaleTailnetReconciler) reconcileTailnet(ctx context.Context, tailnet *gatewayv1alpha1.TailscaleTailnet) (ctrl.Result, error) {
	logger := r.Logger.With("tailnet", tailnet.Name)

	// Get OAuth credentials
	tsClient, err := r.createTailscaleClient(ctx, tailnet)
	if err != nil {
		r.updateStatus(ctx, tailnet, gatewayv1alpha1.TailnetAuthenticated, metav1.ConditionFalse, gatewayv1alpha1.ReasonAuthenticationFailed, err.Error())
		r.Recorder.Event(tailnet, "Warning", ReasonAuthenticationFailed, fmt.Sprintf("failed to create Tailscale client: %v", err))
		return ctrl.Result{RequeueAfter: 1 * time.Minute}, err
	}

	// Validate OAuth credentials by creating an auth key
	if err := r.validateOAuthCredentials(ctx, tailnet, tsClient); err != nil {
		r.updateStatus(ctx, tailnet, gatewayv1alpha1.TailnetAuthenticated, metav1.ConditionFalse, gatewayv1alpha1.ReasonAuthenticationFailed, err.Error())
		r.Recorder.Event(tailnet, "Warning", ReasonAuthenticationFailed, fmt.Sprintf("OAuth validation failed: %v", err))
		return ctrl.Result{RequeueAfter: 5 * time.Minute}, err
	}

	// Update status to authenticated
	r.updateStatus(ctx, tailnet, gatewayv1alpha1.TailnetAuthenticated, metav1.ConditionTrue, gatewayv1alpha1.ReasonReady, "OAuth credentials validated successfully")

	// Discover comprehensive tailnet metadata
	if err := r.discoverAndUpdateTailnetInfo(ctx, tailnet, tsClient); err != nil {
		// Log error but don't fail reconciliation - this is supplementary information
		logger.Warnf("Failed to discover tailnet metadata: %v", err)
		r.updateStatus(ctx, tailnet, gatewayv1alpha1.TailnetValidated, metav1.ConditionFalse, gatewayv1alpha1.ReasonAPIError, fmt.Sprintf("Failed to discover tailnet metadata: %v", err))
	} else {
		r.updateStatus(ctx, tailnet, gatewayv1alpha1.TailnetValidated, metav1.ConditionTrue, gatewayv1alpha1.ReasonReady, "Tailnet metadata discovered successfully")
	}

	r.updateStatus(ctx, tailnet, gatewayv1alpha1.TailnetReady, metav1.ConditionTrue, gatewayv1alpha1.ReasonReady, "Tailnet connection is ready")

	// Set last sync time
	now := metav1.Now()
	tailnet.Status.LastSyncTime = &now

	if err := r.Status().Update(ctx, tailnet); err != nil {
		return ctrl.Result{}, err
	}

	r.Recorder.Event(tailnet, "Normal", ReasonAuthenticationSucceeded, "Successfully validated OAuth credentials and tailnet connection")
	logger.Info("Successfully reconciled TailscaleTailnet")

	return ctrl.Result{RequeueAfter: DefaultSyncInterval}, nil
}

// createTailscaleClient creates a Tailscale client from OAuth credentials
func (r *TailscaleTailnetReconciler) createTailscaleClient(ctx context.Context, tailnet *gatewayv1alpha1.TailscaleTailnet) (tailscale.Client, error) {
	secretNamespace := tailnet.Spec.OAuthSecretNamespace
	if secretNamespace == "" {
		secretNamespace = tailnet.Namespace
	}

	// Get the OAuth secret
	secret := &corev1.Secret{}
	err := r.Get(ctx, types.NamespacedName{Name: tailnet.Spec.OAuthSecretName, Namespace: secretNamespace}, secret)
	if err != nil {
		return nil, fmt.Errorf("failed to get OAuth secret: %w", err)
	}

	clientID := string(secret.Data["client_id"])
	clientSecret := string(secret.Data["client_secret"])
	if clientID == "" || clientSecret == "" {
		return nil, fmt.Errorf("secret must contain both 'client_id' and 'client_secret' keys")
	}

	// Always use default tailnet ("-") for OAuth client creation
	// The actual tailnet domain will be discovered after authentication
	config := tailscale.ClientConfig{
		Tailnet:      tailscale.DefaultTailnet,
		APIBaseURL:   tailscale.DefaultAPIBaseURL,
		ClientID:     clientID,
		ClientSecret: clientSecret,
	}

	return tailscale.NewClient(ctx, config)
}

// validateOAuthCredentials validates OAuth credentials by creating a test auth key
func (r *TailscaleTailnetReconciler) validateOAuthCredentials(ctx context.Context, tailnet *gatewayv1alpha1.TailscaleTailnet, tsClient tailscale.Client) error {
	// Build tags for auth key validation
	tags := tailnet.Spec.Tags
	if len(tags) == 0 {
		tags = []string{"tag:k8s-operator"}
	}

	// Create validation auth key capabilities
	caps := tailscaleclient.KeyCapabilities{}
	caps.Devices.Create.Reusable = false     // Single use for validation
	caps.Devices.Create.Ephemeral = true     // Auto-cleanup
	caps.Devices.Create.Preauthorized = true // Skip manual approval
	caps.Devices.Create.Tags = tags

	// Attempt to create auth key - this validates the OAuth credentials
	keyMeta, err := tsClient.CreateKey(ctx, caps)
	if err != nil {
		var apiErr tailscaleclient.APIError
		if errors.As(err, &apiErr) {
			// Note: APIError doesn't expose status code directly, so we check common error patterns
			if apiErr.Error() == "401" || strings.Contains(apiErr.Error(), "unauthorized") {
				return fmt.Errorf("invalid OAuth credentials: %w", err)
			}
			if apiErr.Error() == "403" || strings.Contains(apiErr.Error(), "forbidden") {
				return fmt.Errorf("OAuth credentials lack required permissions for auth key creation: %w", err)
			}
			return fmt.Errorf("failed to validate OAuth credentials: %w", err)
		}
		return fmt.Errorf("failed to create validation auth key: %w", err)
	}

	// Log success but don't expose the actual key secret
	r.Logger.Infof("Successfully created validation auth key with ID: %s", keyMeta.ID)

	// Cleanup the validation key immediately since it was ephemeral
	if keyMeta != nil && keyMeta.ID != "" {
		if err := tsClient.DeleteKey(ctx, keyMeta.ID); err != nil {
			// Log but don't fail - ephemeral keys auto-expire anyway
			r.Logger.Infof("Note: failed to cleanup validation auth key %s (will auto-expire): %v", keyMeta.ID, err)
		} else {
			r.Logger.Infof("Successfully cleaned up validation auth key: %s", keyMeta.ID)
		}
	}

	return nil
}

// discoverAndUpdateTailnetInfo discovers tailnet metadata and updates the status
func (r *TailscaleTailnetReconciler) discoverAndUpdateTailnetInfo(ctx context.Context, tailnet *gatewayv1alpha1.TailscaleTailnet, tsClient tailscale.Client) error {
	metadata, err := tsClient.DiscoverTailnetInfo(ctx)
	if err != nil {
		return fmt.Errorf("failed to discover tailnet info: %w", err)
	}

	// Update TailnetInfo in status
	tailnet.Status.TailnetInfo = &gatewayv1alpha1.TailnetInfo{
		Name:         metadata.Name,
		Organization: &metadata.Organization,
	}

	// Set MagicDNSSuffix if different from Name
	if metadata.MagicDNSSuffix != metadata.Name {
		tailnet.Status.TailnetInfo.MagicDNSSuffix = &metadata.MagicDNSSuffix
	}

	r.Logger.Infof("Discovered tailnet info: name=%s, org=%s", metadata.Name, metadata.Organization)
	return nil
}

// handleDeletion handles tailnet resource deletion
func (r *TailscaleTailnetReconciler) handleDeletion(ctx context.Context, tailnet *gatewayv1alpha1.TailscaleTailnet) (ctrl.Result, error) {
	logger := r.Logger.With("tailnet", tailnet.Name)
	logger.Info("Handling TailscaleTailnet deletion")

	// Perform any necessary cleanup here
	// For now, we just remove the finalizer
	controllerutil.RemoveFinalizer(tailnet, TailscaleTailnetFinalizer)
	return ctrl.Result{}, r.Update(ctx, tailnet)
}

// updateStatus updates the status condition for the tailnet
func (r *TailscaleTailnetReconciler) updateStatus(ctx context.Context, tailnet *gatewayv1alpha1.TailscaleTailnet, conditionType gatewayv1alpha1.ConditionType, status metav1.ConditionStatus, reason, message string) {
	gatewayv1alpha1.SetCondition(&tailnet.Status.Conditions, string(conditionType), status, reason, message)
}

func (r *TailscaleTailnetReconciler) recordError(tailnet *gatewayv1alpha1.TailscaleTailnet, code, message, component string, err error) {
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
	if tailnet.Name != "" {
		detailedError.Context = map[string]string{
			"tailnet":   tailnet.Name,
			"namespace": tailnet.Namespace,
		}
	}

	gatewayv1alpha1.AddError(&tailnet.Status.RecentErrors, detailedError, 10)
}

func (r *TailscaleTailnetReconciler) recordMetrics(tailnet *gatewayv1alpha1.TailscaleTailnet, reconcileStart time.Time, apiCallCount int32) {
	duration := time.Since(reconcileStart)

	if tailnet.Status.OperationalMetrics == nil {
		tailnet.Status.OperationalMetrics = &gatewayv1alpha1.OperationalMetrics{}
	}

	tailnet.Status.OperationalMetrics.LastReconcileTime = metav1.Time{Time: reconcileStart}
	tailnet.Status.OperationalMetrics.SuccessfulReconciles++
	tailnet.Status.OperationalMetrics.AverageReconcileTime = &metav1.Duration{Duration: duration}

	if tailnet.Status.APIStatus == nil {
		tailnet.Status.APIStatus = &gatewayv1alpha1.TailscaleAPIStatus{}
	}
	// Update API call tracking
	if tailnet.Status.APIStatus.LastSuccessfulCall == nil {
		tailnet.Status.APIStatus.LastSuccessfulCall = &metav1.Time{Time: time.Now()}
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *TailscaleTailnetReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&gatewayv1alpha1.TailscaleTailnet{}).
		Complete(r)
}
