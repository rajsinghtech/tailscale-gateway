// Copyright (c) Tailscale Gateway Authors
// SPDX-License-Identifier: BSD-3-Clause

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=tailscale-gateway
// +kubebuilder:printcolumn:name="Gateway",type="string",JSONPath=".spec.gatewayRef.name",description="Referenced Envoy Gateway"
// +kubebuilder:printcolumn:name="Tailnets",type="integer",JSONPath=".status.tailnetStatus.length",description="Number of configured tailnets"
// +kubebuilder:printcolumn:name="Services",type="integer",JSONPath=".status.tailnetStatus[*].discoveredServices",description="Total discovered services"
// +kubebuilder:printcolumn:name="Extension",type="string",JSONPath=".status.conditions[?(@.type==\"ExtensionServerReady\")].status",description="Extension Server status"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// TailscaleGateway integrates Envoy Gateway with Tailscale networking,
// enabling dynamic route injection based on tailnet topology.
type TailscaleGateway struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   TailscaleGatewaySpec   `json:"spec,omitempty"`
	Status TailscaleGatewayStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// TailscaleGatewayList contains a list of TailscaleGateway
type TailscaleGatewayList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []TailscaleGateway `json:"items"`
}

// TailscaleGatewaySpec defines the desired state of TailscaleGateway
type TailscaleGatewaySpec struct {
	// GatewayRef is a reference to the Envoy Gateway that this
	// TailscaleGateway should integrate with.
	GatewayRef LocalPolicyTargetReference `json:"gatewayRef"`

	// Tailnets defines the Tailscale networks and their service discovery configuration
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	Tailnets []TailnetConfig `json:"tailnets"`

	// ExtensionServer configures the gRPC extension server that implements
	// Envoy Gateway xDS hooks for dynamic route injection.
	// +optional
	ExtensionServer *ExtensionServerConfig `json:"extensionServer,omitempty"`
}

// TailnetConfig defines configuration for a specific Tailscale network
type TailnetConfig struct {
	// Name is a unique identifier for this tailnet within the gateway
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=^[a-z0-9]([-a-z0-9]*[a-z0-9])?$
	Name string `json:"name"`

	// TailscaleTailnetRef references the TailscaleTailnet resource for this tailnet
	// +kubebuilder:validation:Required
	TailscaleTailnetRef LocalPolicyTargetReference `json:"tailscaleTailnetRef"`

	// ServiceDiscovery configures automatic service discovery for this tailnet
	// +optional
	ServiceDiscovery *ServiceDiscoveryConfig `json:"serviceDiscovery,omitempty"`

	// RouteGeneration configures how routes are generated for this tailnet
	// +optional
	RouteGeneration *RouteGenerationConfig `json:"routeGeneration,omitempty"`
}

// ServiceDiscoveryConfig defines service discovery settings
type ServiceDiscoveryConfig struct {
	// Enabled controls whether service discovery is active for this tailnet
	// +kubebuilder:default=true
	Enabled bool `json:"enabled"`

	// Patterns defines glob patterns for services to include in discovery
	// +optional
	Patterns []string `json:"patterns,omitempty"`

	// ExcludePatterns defines glob patterns for services to exclude from discovery
	// +optional
	ExcludePatterns []string `json:"excludePatterns,omitempty"`

	// SyncInterval defines how often to sync with Tailscale API
	// +kubebuilder:default="30s"
	// +optional
	SyncInterval *metav1.Duration `json:"syncInterval,omitempty"`
}

// RouteGenerationConfig defines route generation settings
type RouteGenerationConfig struct {
	// Ingress configures routes for traffic from tailnet to cluster
	// +optional
	Ingress *IngressRouteConfig `json:"ingress,omitempty"`

	// Egress configures routes for traffic from cluster to tailnet
	// +optional
	Egress *EgressRouteConfig `json:"egress,omitempty"`
}

// IngressRouteConfig defines ingress route generation
type IngressRouteConfig struct {
	// HostPattern defines the hostname pattern for generated routes
	// Variables: {service}, {tailnet}, {domain}
	// +kubebuilder:default="{service}.{tailnet}.gateway.local"
	HostPattern string `json:"hostPattern"`

	// PathPrefix defines the path prefix for generated routes
	// +kubebuilder:default="/"
	PathPrefix string `json:"pathPrefix"`

	// Protocol defines the protocol for generated routes
	// +kubebuilder:validation:Enum=HTTP;HTTPS
	// +kubebuilder:default="HTTPS"
	Protocol string `json:"protocol"`
}

// EgressRouteConfig defines egress route generation
type EgressRouteConfig struct {
	// HostPattern defines the hostname pattern for generated routes
	// Variables: {service}, {tailnet}, {domain}
	// +kubebuilder:default="{service}.tailscale.local"
	HostPattern string `json:"hostPattern"`

	// PathPrefix defines the path prefix for generated routes
	// Variables: {service}, {tailnet}
	// +kubebuilder:default="/tailscale/{service}/"
	PathPrefix string `json:"pathPrefix"`

	// Protocol defines the protocol for generated routes
	// +kubebuilder:validation:Enum=HTTP;HTTPS
	// +kubebuilder:default="HTTP"
	Protocol string `json:"protocol"`
}

// ExtensionServerConfig configures the gRPC extension server deployment
type ExtensionServerConfig struct {
	// Image defines the container image for the Extension Server
	// +kubebuilder:default="tailscale-gateway-extension:latest"
	Image string `json:"image"`

	// Replicas defines the number of Extension Server replicas
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default=2
	Replicas int32 `json:"replicas"`

	// Resources defines resource requirements for Extension Server pods
	// +optional
	Resources *corev1.ResourceRequirements `json:"resources,omitempty"`

	// ServiceAccountName defines the ServiceAccount for Extension Server pods
	// +optional
	ServiceAccountName string `json:"serviceAccountName,omitempty"`

	// Port is the port number for the gRPC extension server.
	// Defaults to 8443.
	// +optional
	Port *int32 `json:"port,omitempty"`

	// TLS configures TLS settings for the extension server.
	// +optional
	TLS *ExtensionServerTLS `json:"tls,omitempty"`
}

// ExtensionServerTLS configures TLS for the extension server
type ExtensionServerTLS struct {
	// CertificateRef is a reference to a Secret containing TLS certificates.
	// +optional
	CertificateRef *gwapiv1.SecretObjectReference `json:"certificateRef,omitempty"`

	// InsecureSkipVerify disables TLS certificate verification.
	// Should only be used for development/testing.
	// +optional
	InsecureSkipVerify *bool `json:"insecureSkipVerify,omitempty"`
}

// TailscaleGatewayStatus defines the observed state of TailscaleGateway
type TailscaleGatewayStatus struct {
	// Conditions represent the current state of the TailscaleGateway
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// TailnetStatus provides status for each configured tailnet
	// +optional
	TailnetStatus []TailnetStatus `json:"tailnetStatus,omitempty"`

	// ExtensionServerStatus provides status for the Extension Server
	// +optional
	ExtensionServerStatus *ExtensionServerStatus `json:"extensionServerStatus,omitempty"`

	// GeneratedRoutes tracks the number of routes generated by this gateway
	// +optional
	GeneratedRoutes *GeneratedRoutesStatus `json:"generatedRoutes,omitempty"`

	// Services tracks VIP services managed by this gateway
	// +optional
	Services []ServiceInfo `json:"services,omitempty"`
}

// TailnetStatus provides status information for a specific tailnet
type TailnetStatus struct {
	// Name identifies the tailnet
	Name string `json:"name"`

	// DiscoveredServices is the count of services discovered in this tailnet
	DiscoveredServices int `json:"discoveredServices"`

	// LastSync indicates when service discovery last succeeded
	// +optional
	LastSync *metav1.Time `json:"lastSync,omitempty"`

	// Conditions represent the current state of this tailnet
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// ExtensionServerStatus provides status for the Extension Server deployment
type ExtensionServerStatus struct {
	// ReadyReplicas indicates how many Extension Server replicas are ready
	ReadyReplicas int32 `json:"readyReplicas"`

	// DesiredReplicas indicates the desired number of Extension Server replicas
	DesiredReplicas int32 `json:"desiredReplicas"`

	// ServiceEndpoint provides the gRPC endpoint for the Extension Server
	// +optional
	ServiceEndpoint string `json:"serviceEndpoint,omitempty"`

	// Ready indicates if the extension server is ready to accept connections.
	Ready bool `json:"ready"`

	// LastHookCall is the timestamp of the last successful hook call.
	// +optional
	LastHookCall *metav1.Time `json:"lastHookCall,omitempty"`
}

// GeneratedRoutesStatus tracks generated route metrics
type GeneratedRoutesStatus struct {
	// IngressRoutes is the count of ingress routes generated
	IngressRoutes int `json:"ingressRoutes"`

	// EgressRoutes is the count of egress routes generated
	EgressRoutes int `json:"egressRoutes"`

	// LastGeneration indicates when routes were last generated
	// +optional
	LastGeneration *metav1.Time `json:"lastGeneration,omitempty"`
}

// ServiceInfo provides information about a VIP service managed by this gateway
type ServiceInfo struct {
	// Name is the service name
	Name string `json:"name"`

	// VIPAddresses are the VIP addresses allocated to this service
	// +optional
	VIPAddresses []string `json:"vipAddresses,omitempty"`

	// OwnerCluster indicates which cluster/operator owns this service
	OwnerCluster string `json:"ownerCluster"`

	// ConsumerCount is the number of clusters consuming this service
	ConsumerCount int `json:"consumerCount"`
}

func init() {
	SchemeBuilder.Register(&TailscaleGateway{}, &TailscaleGatewayList{})
}
