// Copyright (c) Tailscale Gateway Authors
// SPDX-License-Identifier: BSD-3-Clause

package validation

import (
	"testing"

	gatewayv1alpha1 "github.com/rajsinghtech/tailscale-gateway/api/v1alpha1"
)

func TestValidateTailscaleEndpoints(t *testing.T) {
	tests := []struct {
		name          string
		endpoints     *gatewayv1alpha1.TailscaleEndpoints
		expectError   bool
		errorContains string
	}{
		{
			name:          "nil endpoints",
			endpoints:     nil,
			expectError:   true,
			errorContains: "cannot be nil",
		},
		{
			name: "empty tailnet",
			endpoints: &gatewayv1alpha1.TailscaleEndpoints{
				Spec: gatewayv1alpha1.TailscaleEndpointsSpec{
					Tailnet: "",
				},
			},
			expectError:   true,
			errorContains: "tailnet name cannot be empty",
		},
		{
			name: "invalid tailnet name",
			endpoints: &gatewayv1alpha1.TailscaleEndpoints{
				Spec: gatewayv1alpha1.TailscaleEndpointsSpec{
					Tailnet: "INVALID-NAME",
				},
			},
			expectError:   true,
			errorContains: "tailnet name must be a valid DNS name",
		},
		{
			name: "no endpoints or auto-discovery",
			endpoints: &gatewayv1alpha1.TailscaleEndpoints{
				Spec: gatewayv1alpha1.TailscaleEndpointsSpec{
					Tailnet: "test-tailnet",
				},
			},
			expectError:   true,
			errorContains: "must specify either static endpoints or enable auto-discovery",
		},
		{
			name: "valid with auto-discovery",
			endpoints: &gatewayv1alpha1.TailscaleEndpoints{
				Spec: gatewayv1alpha1.TailscaleEndpointsSpec{
					Tailnet: "test-tailnet",
					AutoDiscovery: &gatewayv1alpha1.EndpointAutoDiscovery{
						Enabled: true,
					},
				},
			},
			expectError: false,
		},
		{
			name: "valid with static endpoint",
			endpoints: &gatewayv1alpha1.TailscaleEndpoints{
				Spec: gatewayv1alpha1.TailscaleEndpointsSpec{
					Tailnet: "test-tailnet",
					Endpoints: []gatewayv1alpha1.TailscaleEndpoint{
						{
							Name:        "test-endpoint",
							TailscaleIP: "100.64.0.1",
							Port:        80,
							Protocol:    "HTTP",
						},
					},
				},
			},
			expectError: false,
		},
		{
			name: "invalid endpoint IP",
			endpoints: &gatewayv1alpha1.TailscaleEndpoints{
				Spec: gatewayv1alpha1.TailscaleEndpointsSpec{
					Tailnet: "test-tailnet",
					Endpoints: []gatewayv1alpha1.TailscaleEndpoint{
						{
							Name:        "test-endpoint",
							TailscaleIP: "invalid-ip",
							Port:        80,
							Protocol:    "HTTP",
						},
					},
				},
			},
			expectError:   true,
			errorContains: "invalid IP address",
		},
		{
			name: "invalid port range",
			endpoints: &gatewayv1alpha1.TailscaleEndpoints{
				Spec: gatewayv1alpha1.TailscaleEndpointsSpec{
					Tailnet: "test-tailnet",
					Endpoints: []gatewayv1alpha1.TailscaleEndpoint{
						{
							Name:        "test-endpoint",
							TailscaleIP: "100.64.0.1",
							Port:        70000,
							Protocol:    "HTTP",
						},
					},
				},
			},
			expectError:   true,
			errorContains: "port at index 0 must be between 1 and 65535",
		},
		{
			name: "invalid protocol",
			endpoints: &gatewayv1alpha1.TailscaleEndpoints{
				Spec: gatewayv1alpha1.TailscaleEndpointsSpec{
					Tailnet: "test-tailnet",
					Endpoints: []gatewayv1alpha1.TailscaleEndpoint{
						{
							Name:        "test-endpoint",
							TailscaleIP: "100.64.0.1",
							Port:        80,
							Protocol:    "INVALID",
						},
					},
				},
			},
			expectError:   true,
			errorContains: "invalid protocol",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateTailscaleEndpoints(tt.endpoints)

			if tt.expectError {
				if err == nil {
					t.Errorf("expected error but got none")
					return
				}
				if tt.errorContains != "" && !containsString(err.Error(), tt.errorContains) {
					t.Errorf("expected error to contain %q but got: %v", tt.errorContains, err)
				}
			} else {
				if err != nil {
					t.Errorf("expected no error but got: %v", err)
				}
			}
		})
	}
}

func TestValidateServiceName(t *testing.T) {
	tests := []struct {
		name        string
		serviceName string
		expectError bool
	}{
		{
			name:        "empty service name",
			serviceName: "",
			expectError: true,
		},
		{
			name:        "valid service name with prefix",
			serviceName: "svc:test-service",
			expectError: false,
		},
		{
			name:        "valid service name without prefix",
			serviceName: "test-service",
			expectError: false,
		},
		{
			name:        "invalid service name",
			serviceName: "INVALID-SERVICE",
			expectError: true,
		},
		{
			name:        "reserved service name",
			serviceName: "localhost",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateServiceName(tt.serviceName)

			if tt.expectError {
				if err == nil {
					t.Errorf("expected error but got none")
				}
			} else {
				if err != nil {
					t.Errorf("expected no error but got: %v", err)
				}
			}
		})
	}
}

func TestValidateExternalTarget(t *testing.T) {
	tests := []struct {
		name        string
		target      string
		expectError bool
	}{
		{
			name:        "empty target",
			target:      "",
			expectError: true,
		},
		{
			name:        "valid hostname:port",
			target:      "example.com:80",
			expectError: false,
		},
		{
			name:        "valid service.namespace.svc.cluster.local:port",
			target:      "my-service.default.svc.cluster.local:8080",
			expectError: false,
		},
		{
			name:        "missing port",
			target:      "example.com",
			expectError: true,
		},
		{
			name:        "invalid format",
			target:      "not:a:valid:target:format",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateExternalTarget(tt.target)

			if tt.expectError {
				if err == nil {
					t.Errorf("expected error but got none")
				}
			} else {
				if err != nil {
					t.Errorf("expected no error but got: %v", err)
				}
			}
		})
	}
}

func TestValidateTailscaleGateway(t *testing.T) {
	tests := []struct {
		name          string
		gateway       *gatewayv1alpha1.TailscaleGateway
		expectError   bool
		errorContains string
	}{
		{
			name:          "nil gateway",
			gateway:       nil,
			expectError:   true,
			errorContains: "cannot be nil",
		},
		{
			name: "empty gateway reference",
			gateway: &gatewayv1alpha1.TailscaleGateway{
				Spec: gatewayv1alpha1.TailscaleGatewaySpec{
					GatewayRef: gatewayv1alpha1.LocalPolicyTargetReference{},
				},
			},
			expectError:   true,
			errorContains: "gateway reference name cannot be empty",
		},
		{
			name: "no tailnets",
			gateway: &gatewayv1alpha1.TailscaleGateway{
				Spec: gatewayv1alpha1.TailscaleGatewaySpec{
					GatewayRef: gatewayv1alpha1.LocalPolicyTargetReference{
						Name: "test-gateway",
					},
					Tailnets: []gatewayv1alpha1.TailnetConfig{},
				},
			},
			expectError:   true,
			errorContains: "at least one tailnet configuration is required",
		},
		{
			name: "valid gateway",
			gateway: &gatewayv1alpha1.TailscaleGateway{
				Spec: gatewayv1alpha1.TailscaleGatewaySpec{
					GatewayRef: gatewayv1alpha1.LocalPolicyTargetReference{
						Name: "test-gateway",
					},
					Tailnets: []gatewayv1alpha1.TailnetConfig{
						{
							Name: "test-tailnet",
							TailscaleTailnetRef: gatewayv1alpha1.LocalPolicyTargetReference{
								Name: "test-tailnet-resource",
							},
						},
					},
				},
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateTailscaleGateway(tt.gateway)

			if tt.expectError {
				if err == nil {
					t.Errorf("expected error but got none")
					return
				}
				if tt.errorContains != "" && !containsString(err.Error(), tt.errorContains) {
					t.Errorf("expected error to contain %q but got: %v", tt.errorContains, err)
				}
			} else {
				if err != nil {
					t.Errorf("expected no error but got: %v", err)
				}
			}
		})
	}
}

// Helper function to check if a string contains a substring
func containsString(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || containsSubstring(s, substr))
}

func containsSubstring(s, substr string) bool {
	if len(substr) == 0 {
		return true
	}
	if len(s) < len(substr) {
		return false
	}
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
