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

// TailscaleEndpointsSpec defines the desired state of TailscaleEndpoints
type TailscaleEndpointsSpec struct {
	// Tailnet identifies which tailnet these endpoints belong to
	// +kubebuilder:validation:Required
	Tailnet string `json:"tailnet"`

	// Endpoints defines the mapping between Tailscale services and their network details
	// +optional
	Endpoints []TailscaleEndpoint `json:"endpoints,omitempty"`

	// AutoDiscovery configures automatic endpoint discovery
	// +optional
	AutoDiscovery *EndpointAutoDiscovery `json:"autoDiscovery,omitempty"`
}

// TailscaleEndpoint represents a service accessible via Tailscale
type TailscaleEndpoint struct {
	// Name is a unique identifier for this endpoint within the tailnet
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=^[a-z0-9]([-a-z0-9]*[a-z0-9])?$
	Name string `json:"name"`

	// TailscaleIP is the Tailscale IP address of the service
	// +kubebuilder:validation:Required
	TailscaleIP string `json:"tailscaleIP"`

	// TailscaleFQDN is the fully qualified domain name in the tailnet
	// +kubebuilder:validation:Required
	TailscaleFQDN string `json:"tailscaleFQDN"`

	// Port is the port number the service listens on
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	Port int32 `json:"port"`

	// Protocol defines the network protocol (HTTP, HTTPS, TCP, UDP)
	// +kubebuilder:validation:Enum=HTTP;HTTPS;TCP;UDP
	// +kubebuilder:default="HTTP"
	Protocol string `json:"protocol"`

	// Tags are Tailscale tags associated with this endpoint
	// +optional
	Tags []string `json:"tags,omitempty"`

	// HealthCheck defines health checking configuration for this endpoint
	// +optional
	HealthCheck *EndpointHealthCheck `json:"healthCheck,omitempty"`

	// Weight defines the load balancing weight for this endpoint
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=100
	// +kubebuilder:default=1
	// +optional
	Weight *int32 `json:"weight,omitempty"`

	// ExternalTarget defines the external service this endpoint should route to (for Envoy Gateway integration)
	// Format: "hostname:port" or "service-name.namespace.svc.cluster.local:port"
	// +optional
	ExternalTarget string `json:"externalTarget,omitempty"`
}

// EndpointAutoDiscovery configures automatic endpoint discovery
type EndpointAutoDiscovery struct {
	// Enabled controls whether auto-discovery is active
	// +kubebuilder:default=true
	Enabled bool `json:"enabled"`

	// IncludePatterns defines patterns for services to include in discovery
	// DEPRECATED: Use TagSelectors or ServiceDiscovery instead
	// +optional
	IncludePatterns []string `json:"includePatterns,omitempty"`

	// ExcludePatterns defines patterns for services to exclude from discovery
	// DEPRECATED: Use TagSelectors or ServiceDiscovery instead
	// +optional
	ExcludePatterns []string `json:"excludePatterns,omitempty"`

	// SyncInterval defines how often to sync with Tailscale API
	// +kubebuilder:default="30s"
	// +optional
	SyncInterval *metav1.Duration `json:"syncInterval,omitempty"`

	// RequiredTags filters discovery to devices with specific tags
	// DEPRECATED: Use TagSelectors instead
	// +optional
	RequiredTags []string `json:"requiredTags,omitempty"`

	// TagSelectors define advanced tag-based discovery rules
	// +optional
	TagSelectors []TagSelector `json:"tagSelectors,omitempty"`

	// ServiceDiscovery configures VIP service discovery
	// +optional
	ServiceDiscovery *VIPServiceDiscoveryConfig `json:"serviceDiscovery,omitempty"`
}

// TagSelector defines a tag-based selection rule
type TagSelector struct {
	// Tag is the full tag key (e.g., "tag:service", "tag:environment")
	// +kubebuilder:validation:Required
	Tag string `json:"tag"`

	// Operator defines the selection operator
	// +kubebuilder:validation:Enum=In;NotIn;Exists;DoesNotExist
	// +kubebuilder:default="Exists"
	// +optional
	Operator string `json:"operator,omitempty"`

	// Values are the tag values to match (for In/NotIn operators)
	// For "tag:environment", values might be ["production", "staging"]
	// +optional
	Values []string `json:"values,omitempty"`
}

// VIPServiceDiscoveryConfig configures Tailscale VIP service discovery
type VIPServiceDiscoveryConfig struct {
	// Enabled controls whether to discover Tailscale VIP services
	// +kubebuilder:default=false
	Enabled bool `json:"enabled"`

	// ServiceNames are specific VIP service names to discover
	// Format: ["svc:web-service", "svc:api-service"]
	// +optional
	ServiceNames []string `json:"serviceNames,omitempty"`

	// ServiceTags filter VIP services by ACL tags
	// +optional
	ServiceTags []string `json:"serviceTags,omitempty"`
}

// EndpointHealthCheck defines health checking for an endpoint
type EndpointHealthCheck struct {
	// Enabled controls whether health checking is active
	// +kubebuilder:default=true
	Enabled bool `json:"enabled"`

	// Path is the HTTP path for health checks (HTTP/HTTPS protocols only)
	// +kubebuilder:default="/health"
	// +optional
	Path string `json:"path,omitempty"`

	// Interval defines how often to perform health checks
	// +kubebuilder:default="10s"
	// +optional
	Interval *metav1.Duration `json:"interval,omitempty"`

	// Timeout defines the timeout for health check requests
	// +kubebuilder:default="3s"
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

// StatefulSetReference tracks a StatefulSet created for an endpoint connection
type StatefulSetReference struct {
	// Name of the StatefulSet
	Name string `json:"name"`

	// Namespace of the StatefulSet
	Namespace string `json:"namespace"`

	// ConnectionType indicates whether this is an ingress or egress connection
	// +kubebuilder:validation:Enum=ingress;egress
	ConnectionType string `json:"connectionType"`

	// EndpointName identifies which endpoint this StatefulSet serves
	EndpointName string `json:"endpointName"`
}

// TailscaleEndpointsStatus defines the observed state of TailscaleEndpoints
type TailscaleEndpointsStatus struct {
	// Conditions represent the current state of the TailscaleEndpoints
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// DiscoveredEndpoints is the count of endpoints discovered via auto-discovery
	DiscoveredEndpoints int `json:"discoveredEndpoints"`

	// TotalEndpoints is the total count of endpoints (discovered + manually configured)
	TotalEndpoints int `json:"totalEndpoints"`

	// HealthyEndpoints is the count of endpoints passing health checks
	HealthyEndpoints int `json:"healthyEndpoints"`

	// LastSync indicates when endpoint discovery last succeeded
	// +optional
	LastSync *metav1.Time `json:"lastSync,omitempty"`

	// EndpointStatus provides detailed status for each endpoint
	// +optional
	EndpointStatus []EndpointStatus `json:"endpointStatus,omitempty"`

	// StatefulSetRefs tracks the StatefulSets created for endpoint connections
	// +optional
	StatefulSetRefs []StatefulSetReference `json:"statefulSetRefs,omitempty"`
}

// EndpointStatus provides status information for a specific endpoint
type EndpointStatus struct {
	// Name identifies the endpoint
	Name string `json:"name"`

	// TailscaleIP is the endpoint's Tailscale IP
	TailscaleIP string `json:"tailscaleIP"`

	// HealthStatus indicates the health check status
	// +kubebuilder:validation:Enum=Healthy;Unhealthy;Unknown
	HealthStatus string `json:"healthStatus"`

	// LastHealthCheck is when the endpoint was last health checked
	// +optional
	LastHealthCheck *metav1.Time `json:"lastHealthCheck,omitempty"`

	// DiscoverySource indicates how this endpoint was discovered
	// +kubebuilder:validation:Enum=Manual;AutoDiscovery
	DiscoverySource string `json:"discoverySource"`

	// Tags are the Tailscale tags associated with this endpoint
	// +optional
	Tags []string `json:"tags,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:resource:categories=tailscale-gateway
//+kubebuilder:printcolumn:name="Tailnet",type="string",JSONPath=".spec.tailnet",description="Associated tailnet"
//+kubebuilder:printcolumn:name="Total",type="integer",JSONPath=".status.totalEndpoints",description="Total endpoints"
//+kubebuilder:printcolumn:name="Healthy",type="integer",JSONPath=".status.healthyEndpoints",description="Healthy endpoints"
//+kubebuilder:printcolumn:name="StatefulSets",type="integer",JSONPath=".status.statefulSetRefs[*].name",description="Created StatefulSets"
//+kubebuilder:printcolumn:name="Last Sync",type="date",JSONPath=".status.lastSync",description="Last discovery sync"
//+kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// TailscaleEndpoints is the Schema for the tailscaleendpoints API
type TailscaleEndpoints struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   TailscaleEndpointsSpec   `json:"spec,omitempty"`
	Status TailscaleEndpointsStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// TailscaleEndpointsList contains a list of TailscaleEndpoints
type TailscaleEndpointsList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []TailscaleEndpoints `json:"items"`
}

func init() {
	SchemeBuilder.Register(&TailscaleEndpoints{}, &TailscaleEndpointsList{})
}
