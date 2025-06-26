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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TailscaleServicesSpec defines the desired state of TailscaleServices
type TailscaleServicesSpec struct {
	// Selector for choosing TailscaleEndpoints to include in this service
	// +kubebuilder:validation:Required
	Selector *metav1.LabelSelector `json:"selector"`

	// VIPService configuration for the Tailscale VIP service
	// +optional
	VIPService *VIPServiceConfig `json:"vipService,omitempty"`

	// LoadBalancing configuration for service-level load balancing
	// +optional
	LoadBalancing *LoadBalancingConfig `json:"loadBalancing,omitempty"`

	// ServiceDiscovery configuration for discovering external services
	// +optional
	ServiceDiscovery *TailscaleServiceDiscoveryConfig `json:"serviceDiscovery,omitempty"`

	// EndpointTemplate for auto-creating TailscaleEndpoints when none match the selector
	// +optional
	EndpointTemplate *TailscaleEndpointTemplate `json:"endpointTemplate,omitempty"`
}

// VIPServiceConfig defines configuration for Tailscale VIP services
type VIPServiceConfig struct {
	// Name of the VIP service (e.g., "svc:web-service")
	// If not specified, defaults to "svc:<TailscaleServices-name>"
	// +kubebuilder:validation:Pattern="^svc:[a-z0-9]([-a-z0-9]*[a-z0-9])?$"
	// +optional
	Name string `json:"name,omitempty"`

	// Ports the service should expose
	// Format: ["tcp:80", "udp:53", "tcp:443"]
	// +optional
	Ports []string `json:"ports,omitempty"`

	// Tags for the VIP service (used in ACL policies)
	// +optional
	Tags []string `json:"tags,omitempty"`

	// Comment for service documentation
	// +optional
	Comment string `json:"comment,omitempty"`

	// Annotations are key-value pairs for additional metadata
	// +optional
	Annotations map[string]string `json:"annotations,omitempty"`

	// AutoApprove indicates whether endpoints should be auto-approved for hosting this service
	// +kubebuilder:default=true
	// +optional
	AutoApprove *bool `json:"autoApprove,omitempty"`
}

// LoadBalancingConfig defines service-level load balancing configuration
type LoadBalancingConfig struct {
	// Strategy for load balancing (round-robin, least-connections, weighted)
	// +kubebuilder:validation:Enum=round-robin;least-connections;weighted;failover
	// +kubebuilder:default="round-robin"
	// +optional
	Strategy string `json:"strategy,omitempty"`

	// HealthCheck configuration for service-level health checking
	// +optional
	HealthCheck *ServiceHealthCheckConfig `json:"healthCheck,omitempty"`

	// Weights for weighted load balancing (maps endpoint names to weights)
	// +optional
	Weights map[string]int32 `json:"weights,omitempty"`

	// FailoverConfig for failover load balancing
	// +optional
	FailoverConfig *FailoverConfig `json:"failoverConfig,omitempty"`
}

// ServiceHealthCheckConfig defines service-level health checking
type ServiceHealthCheckConfig struct {
	// Enabled controls whether service-level health checking is active
	// +kubebuilder:default=true
	Enabled bool `json:"enabled"`

	// Path is the HTTP path for health checks (HTTP/HTTPS protocols only)
	// +kubebuilder:default="/health"
	// +optional
	Path string `json:"path,omitempty"`

	// Interval defines how often to perform health checks
	// +kubebuilder:default="30s"
	// +optional
	Interval *metav1.Duration `json:"interval,omitempty"`

	// Timeout defines the timeout for health check requests
	// +kubebuilder:default="5s"
	// +optional
	Timeout *metav1.Duration `json:"timeout,omitempty"`

	// HealthyThreshold is the number of consecutive successful checks for healthy status
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default=2
	// +optional
	HealthyThreshold *int32 `json:"healthyThreshold,omitempty"`

	// UnhealthyThreshold is the number of consecutive failed checks for unhealthy status
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default=3
	// +optional
	UnhealthyThreshold *int32 `json:"unhealthyThreshold,omitempty"`
}

// FailoverConfig defines failover behavior for load balancing
type FailoverConfig struct {
	// PrimaryEndpoints are the preferred endpoints
	// +optional
	PrimaryEndpoints []string `json:"primaryEndpoints,omitempty"`

	// SecondaryEndpoints are used when primary endpoints are unhealthy
	// +optional
	SecondaryEndpoints []string `json:"secondaryEndpoints,omitempty"`

	// FailbackDelay is how long to wait before failing back to primary endpoints
	// +kubebuilder:default="60s"
	// +optional
	FailbackDelay *metav1.Duration `json:"failbackDelay,omitempty"`
}

// TailscaleServiceDiscoveryConfig defines service discovery configuration for TailscaleServices
type TailscaleServiceDiscoveryConfig struct {
	// ExternalServices are external (non-Kubernetes) services to include
	// +optional
	ExternalServices []ExternalServiceConfig `json:"externalServices,omitempty"`

	// DiscoverVIPServices indicates whether to discover existing VIP services
	// +kubebuilder:default=false
	// +optional
	DiscoverVIPServices bool `json:"discoverVIPServices,omitempty"`

	// VIPServiceSelector selects existing VIP services to include
	// +optional
	VIPServiceSelector *VIPServiceSelector `json:"vipServiceSelector,omitempty"`
}

// ExternalServiceConfig defines configuration for external services
type ExternalServiceConfig struct {
	// Name of the external service
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// Address of the external service (IP or hostname)
	// +kubebuilder:validation:Required
	Address string `json:"address"`

	// Port of the external service
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	Port int32 `json:"port"`

	// Protocol of the external service
	// +kubebuilder:validation:Enum=HTTP;HTTPS;TCP;UDP
	// +kubebuilder:default="HTTP"
	// +optional
	Protocol string `json:"protocol,omitempty"`

	// Weight for load balancing
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=100
	// +kubebuilder:default=1
	// +optional
	Weight *int32 `json:"weight,omitempty"`
}

// VIPServiceSelector defines selector for existing VIP services
type VIPServiceSelector struct {
	// Tags to match VIP services
	// +optional
	Tags []string `json:"tags,omitempty"`

	// ServiceNames are specific VIP service names to include
	// +optional
	ServiceNames []string `json:"serviceNames,omitempty"`
}

// TailscaleEndpointTemplate defines a template for auto-creating TailscaleEndpoints
type TailscaleEndpointTemplate struct {
	// Tailnet to connect to
	// +kubebuilder:validation:Required
	Tailnet string `json:"tailnet"`

	// Tags for the auto-created tailscale machines
	// +optional
	Tags []string `json:"tags,omitempty"`

	// Proxy configuration for auto-created endpoints
	// +optional
	Proxy *ProxyTemplate `json:"proxy,omitempty"`

	// Ports that auto-created endpoints should serve
	// +optional
	Ports []PortTemplate `json:"ports,omitempty"`

	// Labels to apply to auto-created TailscaleEndpoints
	// These labels should match the selector for this TailscaleServices
	// +optional
	Labels map[string]string `json:"labels,omitempty"`

	// NameSuffix to append to auto-created endpoint names
	// Default: "-endpoints"
	// +optional
	NameSuffix string `json:"nameSuffix,omitempty"`
}

// ProxyTemplate defines proxy configuration for auto-created endpoints
type ProxyTemplate struct {
	// Number of proxy replicas
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default=2
	// +optional
	Replicas *int32 `json:"replicas,omitempty"`

	// Connection type: ingress, egress, or bidirectional
	// +kubebuilder:validation:Enum=ingress;egress;bidirectional
	// +kubebuilder:default="bidirectional"
	// +optional
	ConnectionType string `json:"connectionType,omitempty"`
}

// PortTemplate defines port configuration for auto-created endpoints
type PortTemplate struct {
	// Port number
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	Port int32 `json:"port"`

	// Protocol (TCP or UDP)
	// +kubebuilder:validation:Enum=TCP;UDP
	// +kubebuilder:default="TCP"
	// +optional
	Protocol string `json:"protocol,omitempty"`

	// ExternalTarget defines the backend service this port should route to
	// Format: "service-name.namespace.svc.cluster.local:port" or "hostname:port"
	// +optional
	ExternalTarget string `json:"externalTarget,omitempty"`
}

// TailscaleServicesStatus defines the observed state of TailscaleServices
type TailscaleServicesStatus struct {
	// Conditions represent the current state of the TailscaleServices
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// SelectedEndpoints are the TailscaleEndpoints selected by the selector
	// +optional
	SelectedEndpoints []SelectedEndpoint `json:"selectedEndpoints,omitempty"`

	// VIPServiceStatus provides status of the created VIP service
	// +optional
	VIPServiceStatus *VIPServiceStatus `json:"vipServiceStatus,omitempty"`

	// LoadBalancingStatus provides load balancing statistics
	// +optional
	LoadBalancingStatus *LoadBalancingStatus `json:"loadBalancingStatus,omitempty"`

	// DiscoveredServices are services discovered through service discovery
	// +optional
	DiscoveredServices []DiscoveredService `json:"discoveredServices,omitempty"`

	// TotalBackends is the total number of backends in this service
	TotalBackends int `json:"totalBackends"`

	// HealthyBackends is the number of healthy backends
	HealthyBackends int `json:"healthyBackends"`

	// UnhealthyBackends is the number of unhealthy backends
	UnhealthyBackends int `json:"unhealthyBackends"`

	// LastUpdate indicates when the service status was last updated
	// +optional
	LastUpdate *metav1.Time `json:"lastUpdate,omitempty"`

	// ServiceInfo tracks information about the managed service
	// +optional
	ServiceInfo *TailscaleServiceInfo `json:"serviceInfo,omitempty"`
}

// SelectedEndpoint represents a selected TailscaleEndpoints resource
type SelectedEndpoint struct {
	// Name of the TailscaleEndpoints resource
	Name string `json:"name"`

	// Namespace of the TailscaleEndpoints resource
	Namespace string `json:"namespace"`

	// Machines are the individual machines from this endpoint
	// +optional
	Machines []string `json:"machines,omitempty"`

	// Labels are the labels that matched the selector
	// +optional
	Labels map[string]string `json:"labels,omitempty"`

	// Status indicates the status of this endpoint selection
	// +kubebuilder:validation:Enum=Ready;Pending;Failed;NotFound
	Status string `json:"status"`

	// LastSelected when this endpoint was last selected
	// +optional
	LastSelected *metav1.Time `json:"lastSelected,omitempty"`

	// HealthStatus indicates the health of backends from this endpoint
	// +optional
	HealthStatus *EndpointHealthStatus `json:"healthStatus,omitempty"`
}

// EndpointHealthStatus provides health information for an endpoint
type EndpointHealthStatus struct {
	// HealthyMachines number of healthy machines from this endpoint
	HealthyMachines int `json:"healthyMachines"`

	// UnhealthyMachines number of unhealthy machines from this endpoint
	UnhealthyMachines int `json:"unhealthyMachines"`

	// LastHealthCheck when health was last checked
	// +optional
	LastHealthCheck *metav1.Time `json:"lastHealthCheck,omitempty"`
}

// VIPServiceStatus provides status of the VIP service
type VIPServiceStatus struct {
	// Created indicates whether the VIP service was successfully created
	Created bool `json:"created"`

	// ServiceName is the actual name of the created VIP service
	ServiceName string `json:"serviceName"`

	// Addresses are the allocated VIP addresses (IPv4 and IPv6)
	// +optional
	Addresses []string `json:"addresses,omitempty"`

	// DNSName is the DNS name for the service
	// +optional
	DNSName string `json:"dnsName,omitempty"`

	// BackendCount is the number of registered backends
	BackendCount int `json:"backendCount"`

	// HealthyBackendCount is the number of healthy backends
	HealthyBackendCount int `json:"healthyBackendCount"`

	// RegistrationErrors tracks errors in backend registration
	// +optional
	RegistrationErrors []string `json:"registrationErrors,omitempty"`

	// LastRegistration when backends were last registered
	// +optional
	LastRegistration *metav1.Time `json:"lastRegistration,omitempty"`

	// ServiceTags are the actual tags applied to the VIP service
	// +optional
	ServiceTags []string `json:"serviceTags,omitempty"`
}

// LoadBalancingStatus provides load balancing statistics
type LoadBalancingStatus struct {
	// Strategy is the active load balancing strategy
	Strategy string `json:"strategy"`

	// EndpointWeights shows current weights for weighted load balancing
	// +optional
	EndpointWeights map[string]int32 `json:"endpointWeights,omitempty"`

	// RequestCounts tracks request distribution (if available)
	// +optional
	RequestCounts map[string]int64 `json:"requestCounts,omitempty"`

	// FailoverStatus tracks failover state
	// +optional
	FailoverStatus *FailoverStatus `json:"failoverStatus,omitempty"`
}

// FailoverStatus tracks the current failover state
type FailoverStatus struct {
	// ActiveTier indicates which tier is currently active (primary/secondary)
	ActiveTier string `json:"activeTier"`

	// LastFailover when the last failover occurred
	// +optional
	LastFailover *metav1.Time `json:"lastFailover,omitempty"`

	// LastFailback when the last failback occurred
	// +optional
	LastFailback *metav1.Time `json:"lastFailback,omitempty"`

	// FailoverReason explains why failover occurred
	// +optional
	FailoverReason string `json:"failoverReason,omitempty"`
}

// DiscoveredService represents a service discovered through service discovery
type DiscoveredService struct {
	// Name of the discovered service
	Name string `json:"name"`

	// Type of service (vip-service, external-service)
	// +kubebuilder:validation:Enum=vip-service;external-service
	Type string `json:"type"`

	// Address of the discovered service
	Address string `json:"address"`

	// Port of the discovered service
	Port int32 `json:"port"`

	// Protocol of the discovered service
	Protocol string `json:"protocol"`

	// Tags associated with the discovered service
	// +optional
	Tags []string `json:"tags,omitempty"`

	// LastDiscovered when this service was last discovered
	// +optional
	LastDiscovered *metav1.Time `json:"lastDiscovered,omitempty"`

	// DiscoverySource indicates how this service was discovered
	DiscoverySource string `json:"discoverySource"`
}

// TailscaleServiceInfo tracks service-level information for TailscaleServices
type TailscaleServiceInfo struct {
	// MultiOperatorService indicates if this service is shared across operators
	MultiOperatorService bool `json:"multiOperatorService"`

	// OwnerOperator is the operator that created the VIP service
	// +optional
	OwnerOperator string `json:"ownerOperator,omitempty"`

	// ConsumerOperators are other operators using this service
	// +optional
	ConsumerOperators []string `json:"consumerOperators,omitempty"`

	// ServiceRegistryKey is the key used in service coordination
	// +optional
	ServiceRegistryKey string `json:"serviceRegistryKey,omitempty"`

	// LastCoordination when multi-operator coordination last occurred
	// +optional
	LastCoordination *metav1.Time `json:"lastCoordination,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:resource:categories=tailscale-gateway
//+kubebuilder:printcolumn:name="VIP Service",type="string",JSONPath=".status.vipServiceStatus.serviceName",description="VIP service name"
//+kubebuilder:printcolumn:name="Backends",type="integer",JSONPath=".status.totalBackends",description="Total backends"
//+kubebuilder:printcolumn:name="Healthy",type="integer",JSONPath=".status.healthyBackends",description="Healthy backends"
//+kubebuilder:printcolumn:name="DNS Name",type="string",JSONPath=".status.vipServiceStatus.dnsName",description="Service DNS name"
//+kubebuilder:printcolumn:name="Strategy",type="string",JSONPath=".status.loadBalancingStatus.strategy",description="Load balancing strategy"
//+kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// TailscaleServices is the Schema for the tailscaleservices API
type TailscaleServices struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   TailscaleServicesSpec   `json:"spec,omitempty"`
	Status TailscaleServicesStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// TailscaleServicesList contains a list of TailscaleServices
type TailscaleServicesList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []TailscaleServices `json:"items"`
}

func init() {
	SchemeBuilder.Register(&TailscaleServices{}, &TailscaleServicesList{})
}
