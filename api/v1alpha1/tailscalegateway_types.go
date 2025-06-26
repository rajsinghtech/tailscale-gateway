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

// ExtensionServerConfig configures the gRPC extension server (integrated into main operator)
type ExtensionServerConfig struct {
	// Image defines the container image for the Extension Server (deprecated - now integrated into main operator)
	// +kubebuilder:default="ghcr.io/rajsinghtech/tailscale-gateway-operator:latest"
	Image string `json:"image"`

	// Replicas defines the number of Extension Server replicas (deprecated - extension server now integrated)
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default=1
	Replicas int32 `json:"replicas"`

	// Resources defines resource requirements for Extension Server pods
	// +optional
	Resources *corev1.ResourceRequirements `json:"resources,omitempty"`

	// ServiceAccountName defines the ServiceAccount for Extension Server pods
	// +optional
	ServiceAccountName string `json:"serviceAccountName,omitempty"`

	// Port is the port number for the gRPC extension server.
	// Defaults to 5005.
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

	// RecentErrors tracks recent operational errors with context
	// +optional
	// +listType=atomic
	RecentErrors []DetailedError `json:"recentErrors,omitempty"`

	// OperationalMetrics provides performance and operational insights
	// +optional
	OperationalMetrics *OperationalMetrics `json:"operationalMetrics,omitempty"`

	// DependencyStatus tracks the status of dependent resources
	// +optional
	DependencyStatus []DependencyStatus `json:"dependencyStatus,omitempty"`

	// HTTPRouteRefs tracks which HTTPRoutes are being processed
	// +optional
	HTTPRouteRefs []LocalPolicyTargetReference `json:"httpRouteRefs,omitempty"`
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

	// APIStatus provides details about Tailscale API communication
	// +optional
	APIStatus *TailscaleAPIStatus `json:"apiStatus,omitempty"`

	// ServiceDiscoveryDetails provides detailed service discovery information
	// +optional
	ServiceDiscoveryDetails *ServiceDiscoveryDetails `json:"serviceDiscoveryDetails,omitempty"`

	// LastError provides context about the most recent error
	// +optional
	LastError *DetailedError `json:"lastError,omitempty"`
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

	// HookCallMetrics provides metrics about hook call performance
	// +optional
	HookCallMetrics *HookCallMetrics `json:"hookCallMetrics,omitempty"`

	// ReplicaStatus provides detailed status for each replica
	// +optional
	ReplicaStatus []ReplicaStatus `json:"replicaStatus,omitempty"`

	// DeploymentErrors tracks deployment-related errors
	// +optional
	DeploymentErrors []DetailedError `json:"deploymentErrors,omitempty"`
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

	// Status indicates the current state of this service
	// +kubebuilder:validation:Enum=Ready;Pending;Failed;Unknown
	Status string `json:"status"`

	// CreatedAt indicates when this service was created
	// +optional
	CreatedAt *metav1.Time `json:"createdAt,omitempty"`

	// LastUpdated indicates when this service was last updated
	// +optional
	LastUpdated *metav1.Time `json:"lastUpdated,omitempty"`

	// ErrorMessage provides context if the service is in an error state
	// +optional
	ErrorMessage string `json:"errorMessage,omitempty"`

	// ConsumerClusters lists which clusters are consuming this service
	// +optional
	ConsumerClusters []string `json:"consumerClusters,omitempty"`
}

// ServiceDiscoveryDetails provides detailed service discovery information
type ServiceDiscoveryDetails struct {
	// TotalServices total number of services in the tailnet
	TotalServices int `json:"totalServices"`

	// FilteredServices services after applying filters
	FilteredServices int `json:"filteredServices"`

	// LastDiscoveryDuration how long service discovery took
	// +optional
	LastDiscoveryDuration *metav1.Duration `json:"lastDiscoveryDuration,omitempty"`

	// DiscoveryErrors any errors during service discovery
	// +optional
	DiscoveryErrors []DetailedError `json:"discoveryErrors,omitempty"`
}

// HookCallMetrics provides metrics about extension server hook calls
type HookCallMetrics struct {
	// TotalCalls total number of hook calls
	TotalCalls int64 `json:"totalCalls"`

	// SuccessfulCalls number of successful hook calls
	SuccessfulCalls int64 `json:"successfulCalls"`

	// FailedCalls number of failed hook calls
	FailedCalls int64 `json:"failedCalls"`

	// AverageResponseTime average hook call response time
	// +optional
	AverageResponseTime *metav1.Duration `json:"averageResponseTime,omitempty"`

	// LastCallDuration duration of the last hook call
	// +optional
	LastCallDuration *metav1.Duration `json:"lastCallDuration,omitempty"`

	// HookTypeMetrics per-hook-type metrics
	// +optional
	HookTypeMetrics map[string]HookTypeMetric `json:"hookTypeMetrics,omitempty"`
}

// HookTypeMetric provides metrics for a specific hook type
type HookTypeMetric struct {
	// Calls number of calls for this hook type
	Calls int64 `json:"calls"`

	// Successes number of successful calls
	Successes int64 `json:"successes"`

	// Failures number of failed calls
	Failures int64 `json:"failures"`

	// AverageResponseTime average response time for this hook type
	// +optional
	AverageResponseTime *metav1.Duration `json:"averageResponseTime,omitempty"`
}

// ReplicaStatus provides detailed status for an extension server replica
type ReplicaStatus struct {
	// Name of the replica pod
	Name string `json:"name"`

	// Ready indicates if the replica is ready
	Ready bool `json:"ready"`

	// Phase current phase of the replica
	// +kubebuilder:validation:Enum=Pending;Running;Succeeded;Failed;Unknown
	Phase string `json:"phase"`

	// LastHealthCheck timestamp of last health check
	// +optional
	LastHealthCheck *metav1.Time `json:"lastHealthCheck,omitempty"`

	// Errors any errors from this replica
	// +optional
	Errors []DetailedError `json:"errors,omitempty"`

	// Version the image version running in this replica
	// +optional
	Version string `json:"version,omitempty"`
}

func init() {
	SchemeBuilder.Register(&TailscaleGateway{}, &TailscaleGatewayList{})
}
