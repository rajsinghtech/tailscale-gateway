package controller

import (
	"context"
	"strings"
	"testing"

	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	gatewayv1alpha1 "github.com/rajsinghtech/tailscale-gateway/api/v1alpha1"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func TestTailscaleGatewayController(t *testing.T) {
	testScheme := runtime.NewScheme()
	scheme.AddToScheme(testScheme) // Add core Kubernetes types
	gatewayv1alpha1.AddToScheme(testScheme)
	gwapiv1.AddToScheme(testScheme)

	// Create test resources that all tests will reference
	testEnvoyGateway := &gwapiv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-envoy-gateway",
			Namespace: "default",
		},
		Spec: gwapiv1.GatewaySpec{
			GatewayClassName: "envoy-gateway",
		},
	}

	testTailnet := &gatewayv1alpha1.TailscaleTailnet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-tailnet",
			Namespace: "default",
		},
		Spec: gatewayv1alpha1.TailscaleTailnetSpec{
			Tailnet:              "test.tailnet.ts.net",
			OAuthSecretName:      "test-oauth-secret",
			OAuthSecretNamespace: "default",
		},
		Status: gatewayv1alpha1.TailscaleTailnetStatus{
			Conditions: []metav1.Condition{
				{
					Type:               "Ready",
					Status:             metav1.ConditionTrue,
					LastTransitionTime: metav1.Now(),
					Reason:             "TailnetReady",
					Message:            "Tailnet is ready for testing",
				},
			},
		},
	}

	// Create OAuth secret that the TailscaleTailnet references
	testOAuthSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-oauth-secret",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"client_id":     []byte("test-client-id"),
			"client_secret": []byte("test-client-secret"),
		},
	}

	t.Run("basic_gateway_creation", func(t *testing.T) {
		fc := fake.NewClientBuilder().
			WithScheme(testScheme).
			WithStatusSubresource(&gatewayv1alpha1.TailscaleGateway{}, &gatewayv1alpha1.TailscaleTailnet{}).
			WithObjects(testEnvoyGateway, testTailnet, testOAuthSecret).
			Build()

		recorder := record.NewFakeRecorder(100)
		logger := zap.NewNop().Sugar() // No-op logger for tests
		reconciler := &TailscaleGatewayReconciler{
			Client:   fc,
			Scheme:   testScheme,
			Recorder: recorder,
			Logger:   logger,
		}

		gateway := &gatewayv1alpha1.TailscaleGateway{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-gateway",
				Namespace: "default",
				UID:       "12345678-1234-1234-1234-123456789012",
			},
			Spec: gatewayv1alpha1.TailscaleGatewaySpec{
				GatewayRef: gatewayv1alpha1.LocalPolicyTargetReference{
					Group: "gateway.networking.k8s.io",
					Kind:  "Gateway",
					Name:  "test-envoy-gateway",
				},
				Tailnets: []gatewayv1alpha1.TailnetConfig{
					{
						Name: "test-tailnet",
						TailscaleTailnetRef: gatewayv1alpha1.LocalPolicyTargetReference{
							Group: "gateway.tailscale.com",
							Kind:  "TailscaleTailnet",
							Name:  "test-tailnet",
						},
						ServiceDiscovery: &gatewayv1alpha1.ServiceDiscoveryConfig{
							Enabled: true,
						},
					},
				},
				ExtensionServer: &gatewayv1alpha1.ExtensionServerConfig{
					Image:    "ghcr.io/rajsinghtech/tailscale-gateway-extension-server:latest",
					Replicas: 2,
				},
			},
		}

		mustCreate(t, fc, gateway)
		expectReconciled(t, reconciler, "default", "test-gateway")

		// Verify gateway status is updated
		got := &gatewayv1alpha1.TailscaleGateway{}
		mustGet(t, fc, "default", "test-gateway", got)

		// Should have Ready condition set (True if resources are ready)
		if len(got.Status.Conditions) == 0 {
			t.Fatal("expected conditions to be set")
		}

		readyCondition := findCondition(got.Status.Conditions, "Ready")
		if readyCondition == nil {
			t.Fatal("expected Ready condition")
		}
		// Since we provide all necessary resources in test, expect True
		if readyCondition.Status != metav1.ConditionTrue {
			t.Errorf("expected Ready condition to be True, got %s", readyCondition.Status)
		}
	})

	t.Run("gateway_with_extension_server", func(t *testing.T) {
		fc := fake.NewClientBuilder().
			WithScheme(testScheme).
			WithStatusSubresource(&gatewayv1alpha1.TailscaleGateway{}, &gatewayv1alpha1.TailscaleTailnet{}).
			WithObjects(testEnvoyGateway, testTailnet, testOAuthSecret).
			Build()

		recorder := record.NewFakeRecorder(100)
		logger := zap.NewNop().Sugar() // No-op logger for tests
		reconciler := &TailscaleGatewayReconciler{
			Client:   fc,
			Scheme:   testScheme,
			Recorder: recorder,
			Logger:   logger,
		}

		gateway := &gatewayv1alpha1.TailscaleGateway{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-gateway",
				Namespace: "default",
				UID:       "12345678-1234-1234-1234-123456789012",
			},
			Spec: gatewayv1alpha1.TailscaleGatewaySpec{
				GatewayRef: gatewayv1alpha1.LocalPolicyTargetReference{
					Group: "gateway.networking.k8s.io",
					Kind:  "Gateway",
					Name:  "test-envoy-gateway",
				},
				Tailnets: []gatewayv1alpha1.TailnetConfig{
					{
						Name: "test-tailnet",
						TailscaleTailnetRef: gatewayv1alpha1.LocalPolicyTargetReference{
							Group: "gateway.tailscale.com",
							Kind:  "TailscaleTailnet",
							Name:  "test-tailnet",
						},
					},
				},
				ExtensionServer: &gatewayv1alpha1.ExtensionServerConfig{
					Image:    "ghcr.io/rajsinghtech/tailscale-gateway-extension-server:latest",
					Replicas: 2,
					Port:     int32Ptr(5005),
				},
			},
		}

		mustCreate(t, fc, gateway)
		expectReconciled(t, reconciler, "default", "test-gateway")

		// Verify extension server status is tracked
		got := &gatewayv1alpha1.TailscaleGateway{}
		mustGet(t, fc, "default", "test-gateway", got)

		if got.Status.ExtensionServerStatus == nil {
			t.Fatal("expected extension server status to be set")
		}

		if got.Status.ExtensionServerStatus.DesiredReplicas != 2 {
			t.Errorf("expected 2 desired replicas, got %d", got.Status.ExtensionServerStatus.DesiredReplicas)
		}
	})

	t.Run("gateway_deletion_cleanup", func(t *testing.T) {
		fc := fake.NewClientBuilder().
			WithScheme(testScheme).
			WithStatusSubresource(&gatewayv1alpha1.TailscaleGateway{}, &gatewayv1alpha1.TailscaleTailnet{}).
			WithObjects(testEnvoyGateway, testTailnet, testOAuthSecret).
			Build()

		recorder := record.NewFakeRecorder(100)
		logger := zap.NewNop().Sugar() // No-op logger for tests
		reconciler := &TailscaleGatewayReconciler{
			Client:   fc,
			Scheme:   testScheme,
			Recorder: recorder,
			Logger:   logger,
		}

		gateway := &gatewayv1alpha1.TailscaleGateway{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-gateway",
				Namespace: "default",
				UID:       "12345678-1234-1234-1234-123456789012",
			},
			Spec: gatewayv1alpha1.TailscaleGatewaySpec{
				GatewayRef: gatewayv1alpha1.LocalPolicyTargetReference{
					Group: "gateway.networking.k8s.io",
					Kind:  "Gateway",
					Name:  "test-envoy-gateway",
				},
				Tailnets: []gatewayv1alpha1.TailnetConfig{
					{
						Name: "test-tailnet",
						TailscaleTailnetRef: gatewayv1alpha1.LocalPolicyTargetReference{
							Group: "gateway.tailscale.com",
							Kind:  "TailscaleTailnet",
							Name:  "test-tailnet",
						},
					},
				},
				ExtensionServer: &gatewayv1alpha1.ExtensionServerConfig{
					Image:    "ghcr.io/rajsinghtech/tailscale-gateway-extension-server:latest",
					Replicas: 1,
				},
			},
		}

		mustCreate(t, fc, gateway)
		expectReconciled(t, reconciler, "default", "test-gateway")

		// Delete the gateway
		mustDelete(t, fc, gateway)
		expectReconciled(t, reconciler, "default", "test-gateway")

		// Verify the gateway is actually deleted
		got := &gatewayv1alpha1.TailscaleGateway{}
		err := fc.Get(context.Background(), types.NamespacedName{Name: "test-gateway", Namespace: "default"}, got)
		if !errors.IsNotFound(err) {
			t.Errorf("expected gateway to be deleted, but got error: %v", err)
		}
	})
}

func TestTailscaleGatewayValidation(t *testing.T) {
	tests := []struct {
		name    string
		gateway *gatewayv1alpha1.TailscaleGateway
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid_gateway",
			gateway: &gatewayv1alpha1.TailscaleGateway{
				Spec: gatewayv1alpha1.TailscaleGatewaySpec{
					GatewayRef: gatewayv1alpha1.LocalPolicyTargetReference{
						Group: "gateway.networking.k8s.io",
						Kind:  "Gateway",
						Name:  "test-gateway",
					},
					Tailnets: []gatewayv1alpha1.TailnetConfig{
						{
							Name: "test-tailnet",
							TailscaleTailnetRef: gatewayv1alpha1.LocalPolicyTargetReference{
								Group: "gateway.tailscale.com",
								Kind:  "TailscaleTailnet",
								Name:  "test-tailnet",
							},
						},
					},
					ExtensionServer: &gatewayv1alpha1.ExtensionServerConfig{
						Image:    "test:latest",
						Replicas: 1,
					},
				},
			},
			wantErr: false,
		},
		{
			name: "missing_tailnet_ref",
			gateway: &gatewayv1alpha1.TailscaleGateway{
				Spec: gatewayv1alpha1.TailscaleGatewaySpec{
					GatewayRef: gatewayv1alpha1.LocalPolicyTargetReference{
						Group: "gateway.networking.k8s.io",
						Kind:  "Gateway",
						Name:  "test-gateway",
					},
					Tailnets: []gatewayv1alpha1.TailnetConfig{
						{
							Name: "test-tailnet",
							// Missing TailscaleTailnetRef
						},
					},
					ExtensionServer: &gatewayv1alpha1.ExtensionServerConfig{
						Image:    "test:latest",
						Replicas: 1,
					},
				},
			},
			wantErr: true,
			errMsg:  "TailscaleTailnetRef",
		},
		{
			name: "invalid_extension_server_replicas",
			gateway: &gatewayv1alpha1.TailscaleGateway{
				Spec: gatewayv1alpha1.TailscaleGatewaySpec{
					GatewayRef: gatewayv1alpha1.LocalPolicyTargetReference{
						Group: "gateway.networking.k8s.io",
						Kind:  "Gateway",
						Name:  "test-gateway",
					},
					Tailnets: []gatewayv1alpha1.TailnetConfig{
						{
							Name: "test-tailnet",
							TailscaleTailnetRef: gatewayv1alpha1.LocalPolicyTargetReference{
								Group: "gateway.tailscale.com",
								Kind:  "TailscaleTailnet",
								Name:  "test-tailnet",
							},
						},
					},
					ExtensionServer: &gatewayv1alpha1.ExtensionServerConfig{
						Image:    "test:latest",
						Replicas: 0, // Invalid - must be >= 1
					},
				},
			},
			wantErr: true,
			errMsg:  "replicas",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateTailscaleGateway(tt.gateway)
			if tt.wantErr {
				if err == nil {
					t.Error("expected validation error but got none")
				} else if tt.errMsg != "" && !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("expected error message to contain %q, got %q", tt.errMsg, err.Error())
				}
			} else if err != nil {
				t.Errorf("unexpected validation error: %v", err)
			}
		})
	}
}

// Test helpers following Tailscale k8s-operator patterns

func mustCreate(t *testing.T, client client.Client, obj client.Object) {
	t.Helper()
	if err := client.Create(context.Background(), obj); err != nil {
		t.Fatalf("creating %q: %v", obj.GetName(), err)
	}
}

func mustGet(t *testing.T, client client.Client, namespace, name string, obj client.Object) {
	t.Helper()
	if err := client.Get(context.Background(), types.NamespacedName{Name: name, Namespace: namespace}, obj); err != nil {
		t.Fatalf("getting %q: %v", name, err)
	}
}

func mustDelete(t *testing.T, client client.Client, obj client.Object) {
	t.Helper()
	if err := client.Delete(context.Background(), obj); err != nil {
		t.Fatalf("deleting %q: %v", obj.GetName(), err)
	}
}

func expectReconciled(t *testing.T, sr reconcile.Reconciler, ns, name string) {
	t.Helper()
	req := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: ns, Name: name}}
	res, err := sr.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("Reconcile: unexpected error: %v", err)
	}
	// Allow requeue behavior as this is normal for controllers waiting for resources
	if res.Requeue {
		t.Logf("Note: Controller requested immediate requeue")
	}
	if res.RequeueAfter != 0 {
		t.Logf("Note: Controller requested requeue after %v", res.RequeueAfter)
	}
}

func findCondition(conditions []metav1.Condition, conditionType string) *metav1.Condition {
	for i := range conditions {
		if conditions[i].Type == conditionType {
			return &conditions[i]
		}
	}
	return nil
}

func int32Ptr(i int32) *int32 {
	return &i
}

// Validation function that would be implemented in the actual controller
func validateTailscaleGateway(gateway *gatewayv1alpha1.TailscaleGateway) error {
	// This is a placeholder - implement actual validation logic
	if gateway.Spec.ExtensionServer.Replicas <= 0 {
		return errors.NewBadRequest("extension server replicas must be greater than 0")
	}

	for _, tailnet := range gateway.Spec.Tailnets {
		if tailnet.TailscaleTailnetRef.Name == "" {
			return errors.NewBadRequest("TailscaleTailnetRef name is required")
		}
	}

	return nil
}
