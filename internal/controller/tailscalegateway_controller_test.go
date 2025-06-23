package controller

import (
	"context"
	"testing"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	gatewayv1alpha1 "github.com/rajsinghtech/tailscale-gateway/api/v1alpha1"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func TestTailscaleGatewayController(t *testing.T) {
	scheme := runtime.NewScheme()
	gatewayv1alpha1.AddToScheme(scheme)
	gwapiv1.AddToScheme(scheme)

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
	}

	t.Run("basic_gateway_creation", func(t *testing.T) {
		fc := fake.NewClientBuilder().
			WithScheme(scheme).
			WithStatusSubresource(&gatewayv1alpha1.TailscaleGateway{}).
			WithObjects(testEnvoyGateway, testTailnet).
			Build()

		recorder := record.NewFakeRecorder(100)
		reconciler := &TailscaleGatewayReconciler{
			Client:   fc,
			Scheme:   scheme,
			Recorder: recorder,
		}

		gateway := &gatewayv1alpha1.TailscaleGateway{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-gateway",
				Namespace: "default",
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
					Image:    "tailscale-gateway-extension:latest",
					Replicas: 2,
				},
			},
		}

		mustCreate(t, fc, gateway)
		expectReconciled(t, reconciler, "default", "test-gateway")

		// Verify gateway status is updated
		got := &gatewayv1alpha1.TailscaleGateway{}
		mustGet(t, fc, "default", "test-gateway", got)

		// Should have Ready condition set to False initially (no referenced resources)
		if len(got.Status.Conditions) == 0 {
			t.Fatal("expected conditions to be set")
		}

		readyCondition := findCondition(got.Status.Conditions, "Ready")
		if readyCondition == nil {
			t.Fatal("expected Ready condition")
		}
		if readyCondition.Status != metav1.ConditionFalse {
			t.Errorf("expected Ready condition to be False, got %s", readyCondition.Status)
		}
	})

	t.Run("gateway_with_extension_server", func(t *testing.T) {
		fc := fake.NewClientBuilder().
			WithScheme(scheme).
			WithStatusSubresource(&gatewayv1alpha1.TailscaleGateway{}).
			WithObjects(testEnvoyGateway, testTailnet).
			Build()

		recorder := record.NewFakeRecorder(100)
		reconciler := &TailscaleGatewayReconciler{
			Client:   fc,
			Scheme:   scheme,
			Recorder: recorder,
		}

		gateway := &gatewayv1alpha1.TailscaleGateway{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-gateway",
				Namespace: "default",
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
					Image:    "tailscale-gateway-extension:latest",
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
			WithScheme(scheme).
			WithStatusSubresource(&gatewayv1alpha1.TailscaleGateway{}).
			WithObjects(testEnvoyGateway, testTailnet).
			Build()

		recorder := record.NewFakeRecorder(100)
		reconciler := &TailscaleGatewayReconciler{
			Client:   fc,
			Scheme:   scheme,
			Recorder: recorder,
		}

		gateway := &gatewayv1alpha1.TailscaleGateway{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-gateway",
				Namespace: "default",
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
					Image:    "tailscale-gateway-extension:latest",
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
				} else if tt.errMsg != "" && err.Error() != tt.errMsg {
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
	if res.Requeue || res.RequeueAfter != 0 {
		t.Fatalf("unexpected requeue: %+v", res)
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
