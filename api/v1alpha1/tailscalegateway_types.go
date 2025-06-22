// Copyright (c) Tailscale Gateway Authors
// SPDX-License-Identifier: BSD-3-Clause

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Status",type="string",JSONPath=`.status.conditions[?(@.type == "Ready")].reason`,description="Status of the Tailscale Gateway."
// +kubebuilder:printcolumn:name="Gateway",type="string",JSONPath=`.spec.gatewayRef.name`,description="Associated Envoy Gateway."
// +kubebuilder:printcolumn:name="Tailnet",type="string",JSONPath=`.spec.tailnetRef.name`,description="Associated Tailnet."

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
	GatewayRef gwapiv1.ParentReference `json:"gatewayRef"`

	// TailnetRef is a reference to the TailscaleTailnet that provides
	// the tailnet connection and credentials.
	TailnetRef TailnetReference `json:"tailnetRef"`

	// RouteDiscovery configures how routes from the tailnet should be
	// discovered and injected into the gateway.
	// +optional
	RouteDiscovery *RouteDiscoveryConfig `json:"routeDiscovery,omitempty"`

	// ExtensionServer configures the gRPC extension server that implements
	// Envoy Gateway xDS hooks for dynamic route injection.
	// +optional
	ExtensionServer *ExtensionServerConfig `json:"extensionServer,omitempty"`
}

// TailnetReference refers to a TailscaleTailnet resource
type TailnetReference struct {
	// Name is the name of the TailscaleTailnet resource.
	Name string `json:"name"`

	// Namespace is the namespace of the TailscaleTailnet resource.
	// If not specified, defaults to the same namespace as the TailscaleGateway.
	// +optional
	Namespace *string `json:"namespace,omitempty"`
}

// RouteDiscoveryConfig configures automatic route discovery from tailnet devices
type RouteDiscoveryConfig struct {
	// Enabled determines if route discovery is active.
	// +optional
	Enabled *bool `json:"enabled,omitempty"`

	// DeviceSelector selects which tailnet devices should have their
	// routes discovered and injected.
	// +optional
	DeviceSelector *DeviceSelector `json:"deviceSelector,omitempty"`

	// RouteFilters define which discovered routes should be included/excluded.
	// +optional
	RouteFilters []RouteFilter `json:"routeFilters,omitempty"`

	// SyncInterval is how often to refresh route discovery.
	// Defaults to 30s.
	// +optional
	SyncInterval *metav1.Duration `json:"syncInterval,omitempty"`
}

// DeviceSelector selects tailnet devices for route discovery
type DeviceSelector struct {
	// Tags selects devices that have any of the specified tags.
	// +optional
	Tags []string `json:"tags,omitempty"`

	// Hostnames selects devices by hostname patterns.
	// Supports wildcards (*) for pattern matching.
	// +optional
	Hostnames []string `json:"hostnames,omitempty"`

	// ExcludeTags excludes devices that have any of the specified tags.
	// +optional
	ExcludeTags []string `json:"excludeTags,omitempty"`
}

// RouteFilter defines include/exclude rules for discovered routes
type RouteFilter struct {
	// Type specifies whether this is an include or exclude filter.
	// +kubebuilder:validation:Enum=include;exclude
	Type FilterType `json:"type"`

	// CIDRs are network ranges to include or exclude.
	// +optional
	CIDRs []string `json:"cidrs,omitempty"`

	// Hostnames are hostname patterns to include or exclude.
	// Supports wildcards (*) for pattern matching.
	// +optional
	Hostnames []string `json:"hostnames,omitempty"`
}

// FilterType represents the type of route filter
type FilterType string

const (
	// FilterTypeInclude means the filter includes matching routes
	FilterTypeInclude FilterType = "include"

	// FilterTypeExclude means the filter excludes matching routes
	FilterTypeExclude FilterType = "exclude"
)

// ExtensionServerConfig configures the gRPC extension server
type ExtensionServerConfig struct {
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
	// Conditions represent the current state of the TailscaleGateway.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// DiscoveredRoutes contains information about routes discovered
	// from the tailnet and injected into the gateway.
	// +optional
	DiscoveredRoutes *DiscoveredRoutesStatus `json:"discoveredRoutes,omitempty"`

	// ExtensionServerStatus contains information about the extension server.
	// +optional
	ExtensionServerStatus *ExtensionServerStatus `json:"extensionServerStatus,omitempty"`
}

// DiscoveredRoutesStatus contains information about discovered routes
type DiscoveredRoutesStatus struct {
	// Count is the number of routes currently discovered and active.
	Count int32 `json:"count"`

	// LastDiscoveryTime is when routes were last discovered.
	// +optional
	LastDiscoveryTime *metav1.Time `json:"lastDiscoveryTime,omitempty"`

	// Routes contains details about discovered routes.
	// +optional
	Routes []DiscoveredRoute `json:"routes,omitempty"`
}

// DiscoveredRoute represents a route discovered from a tailnet device
type DiscoveredRoute struct {
	// DeviceHostname is the hostname of the tailnet device.
	DeviceHostname string `json:"deviceHostname"`

	// DeviceIP is the tailnet IP of the device.
	DeviceIP string `json:"deviceIP"`

	// Routes are the network routes advertised by this device.
	Routes []string `json:"routes"`

	// Tags are the tags associated with the device.
	// +optional
	Tags []string `json:"tags,omitempty"`
}

// ExtensionServerStatus contains information about the extension server
type ExtensionServerStatus struct {
	// Address is the address the extension server is listening on.
	// +optional
	Address *string `json:"address,omitempty"`

	// Ready indicates if the extension server is ready to accept connections.
	Ready bool `json:"ready"`

	// LastHookCall is the timestamp of the last successful hook call.
	// +optional
	LastHookCall *metav1.Time `json:"lastHookCall,omitempty"`
}

func init() {
	SchemeBuilder.Register(&TailscaleGateway{}, &TailscaleGatewayList{})
}
