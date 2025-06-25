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
// TailscaleEndpoints now focuses on StatefulSet/proxy infrastructure management
type TailscaleEndpointsSpec struct {
	// Tailnet identifies which tailnet these endpoints belong to
	// +kubebuilder:validation:Required
	Tailnet string `json:"tailnet"`

	// Tags for the Tailscale machines (used in ACL policies)
	// +optional
	Tags []string `json:"tags,omitempty"`

	// Proxy configuration for StatefulSet creation
	// +optional
	Proxy *ProxyConfig `json:"proxy,omitempty"`

	// Ports that the proxy StatefulSets should serve
	// +optional
	Ports []PortMapping `json:"ports,omitempty"`

	// Endpoints defines the mapping between Tailscale services and their network details
	// +optional
	// Deprecated: Use TailscaleServices for VIP service management
	Endpoints []TailscaleEndpoint `json:"endpoints,omitempty"`

	// AutoDiscovery configures automatic endpoint discovery
	// +optional
	// Deprecated: Use TailscaleServices for VIP service management
	AutoDiscovery *EndpointAutoDiscovery `json:"autoDiscovery,omitempty"`
}

// ProxyConfig defines configuration for Tailscale proxy StatefulSets
type ProxyConfig struct {
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

	// Image for the Tailscale proxy containers
	// +optional
	Image string `json:"image,omitempty"`

	// Resources for the proxy containers
	// +optional
	Resources *ResourceRequirements `json:"resources,omitempty"`

	// NodeSelector for StatefulSet pods
	// +optional
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`

	// Tolerations for StatefulSet pods
	// +optional
	Tolerations []Toleration `json:"tolerations,omitempty"`

	// Affinity for StatefulSet pods
	// +optional
	Affinity *Affinity `json:"affinity,omitempty"`
}

// PortMapping defines port configuration for proxy StatefulSets
type PortMapping struct {
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

	// Name for the port (used in Service creation)
	// +optional
	Name string `json:"name,omitempty"`

	// TargetPort on the backend service
	// If not specified, defaults to Port
	// +optional
	TargetPort *int32 `json:"targetPort,omitempty"`
}

// ResourceRequirements defines resource requirements for proxy containers
type ResourceRequirements struct {
	// Limits describes the maximum amount of resources required
	// +optional
	Limits map[string]string `json:"limits,omitempty"`

	// Requests describes the minimum amount of resources required
	// +optional
	Requests map[string]string `json:"requests,omitempty"`
}

// Toleration defines toleration for StatefulSet pods
type Toleration struct {
	// Key is the taint key that the toleration applies to
	// +optional
	Key string `json:"key,omitempty"`

	// Operator represents a key's relationship to the value
	// +kubebuilder:validation:Enum=Exists;Equal
	// +optional
	Operator string `json:"operator,omitempty"`

	// Value is the taint value the toleration matches to
	// +optional
	Value string `json:"value,omitempty"`

	// Effect indicates the taint effect to match
	// +kubebuilder:validation:Enum=NoSchedule;PreferNoSchedule;NoExecute
	// +optional
	Effect string `json:"effect,omitempty"`

	// TolerationSeconds represents the period of time the toleration
	// +optional
	TolerationSeconds *int64 `json:"tolerationSeconds,omitempty"`
}

// Affinity defines affinity settings for StatefulSet pods
type Affinity struct {
	// NodeAffinity describes node affinity scheduling rules
	// +optional
	NodeAffinity *NodeAffinity `json:"nodeAffinity,omitempty"`

	// PodAffinity describes pod affinity scheduling rules
	// +optional
	PodAffinity *PodAffinity `json:"podAffinity,omitempty"`

	// PodAntiAffinity describes pod anti-affinity scheduling rules
	// +optional
	PodAntiAffinity *PodAntiAffinity `json:"podAntiAffinity,omitempty"`
}

// NodeAffinity is a simplified version of corev1.NodeAffinity
type NodeAffinity struct {
	// RequiredDuringSchedulingIgnoredDuringExecution specifies hard node constraints
	// +optional
	RequiredDuringSchedulingIgnoredDuringExecution *NodeSelector `json:"requiredDuringSchedulingIgnoredDuringExecution,omitempty"`
}

// NodeSelector is a simplified version of corev1.NodeSelector
type NodeSelector struct {
	// NodeSelectorTerms is a list of node selector terms
	NodeSelectorTerms []NodeSelectorTerm `json:"nodeSelectorTerms"`
}

// NodeSelectorTerm is a simplified version of corev1.NodeSelectorTerm
type NodeSelectorTerm struct {
	// MatchExpressions is a list of node selector requirements by node's labels
	// +optional
	MatchExpressions []NodeSelectorRequirement `json:"matchExpressions,omitempty"`
}

// NodeSelectorRequirement is a simplified version of corev1.NodeSelectorRequirement
type NodeSelectorRequirement struct {
	// Key is the label key that the selector applies to
	Key string `json:"key"`

	// Operator represents a key's relationship to a set of values
	// +kubebuilder:validation:Enum=In;NotIn;Exists;DoesNotExist;Gt;Lt
	Operator string `json:"operator"`

	// Values is an array of string values
	// +optional
	Values []string `json:"values,omitempty"`
}

// PodAffinity is a simplified version of corev1.PodAffinity
type PodAffinity struct {
	// RequiredDuringSchedulingIgnoredDuringExecution specifies hard pod constraints
	// +optional
	RequiredDuringSchedulingIgnoredDuringExecution []PodAffinityTerm `json:"requiredDuringSchedulingIgnoredDuringExecution,omitempty"`
}

// PodAntiAffinity is a simplified version of corev1.PodAntiAffinity
type PodAntiAffinity struct {
	// RequiredDuringSchedulingIgnoredDuringExecution specifies hard pod anti-constraints
	// +optional
	RequiredDuringSchedulingIgnoredDuringExecution []PodAffinityTerm `json:"requiredDuringSchedulingIgnoredDuringExecution,omitempty"`
}

// PodAffinityTerm is a simplified version of corev1.PodAffinityTerm
type PodAffinityTerm struct {
	// LabelSelector is a label query over a set of resources
	// +optional
	LabelSelector *metav1.LabelSelector `json:"labelSelector,omitempty"`

	// TopologyKey is the key of node labels
	TopologyKey string `json:"topologyKey"`
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

	// SyncInterval defines how often to sync with Tailscale API
	// +kubebuilder:default="30s"
	// +optional
	SyncInterval *metav1.Duration `json:"syncInterval,omitempty"`

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

	// Status indicates the current state of the StatefulSet
	// +kubebuilder:validation:Enum=Ready;Pending;Failed;Scaling;Unknown
	// +optional
	Status string `json:"status,omitempty"`

	// ReadyReplicas number of ready replicas
	// +optional
	ReadyReplicas *int32 `json:"readyReplicas,omitempty"`

	// DesiredReplicas desired number of replicas
	// +optional
	DesiredReplicas *int32 `json:"desiredReplicas,omitempty"`

	// LastStatusChange when the status last changed
	// +optional
	LastStatusChange *metav1.Time `json:"lastStatusChange,omitempty"`

	// ErrorMessage provides context if the StatefulSet is in an error state
	// +optional
	ErrorMessage string `json:"errorMessage,omitempty"`
}

// TailscaleEndpointsStatus defines the observed state of TailscaleEndpoints
type TailscaleEndpointsStatus struct {
	// Conditions represent the current state of the TailscaleEndpoints
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// Devices contains information about Tailscale machines created by this resource
	// Similar to ProxyGroup devices in the official Tailscale k8s-operator
	// +optional
	Devices []TailscaleDevice `json:"devices,omitempty"`

	// StatefulSets contains information about the created StatefulSets
	// +optional
	StatefulSets []StatefulSetInfo `json:"statefulSets,omitempty"`

	// DiscoveredEndpoints is the count of endpoints discovered via auto-discovery
	DiscoveredEndpoints int `json:"discoveredEndpoints"`

	// TotalEndpoints is the total count of endpoints (discovered + manually configured)
	TotalEndpoints int `json:"totalEndpoints"`

	// HealthyEndpoints is the count of endpoints passing health checks
	HealthyEndpoints int `json:"healthyEndpoints"`

	// UnhealthyEndpoints is the count of endpoints failing health checks
	UnhealthyEndpoints int `json:"unhealthyEndpoints"`

	// LastSync indicates when endpoint discovery last succeeded
	// +optional
	LastSync *metav1.Time `json:"lastSync,omitempty"`

	// EndpointStatus provides detailed status for each endpoint
	// +optional
	EndpointStatus []EndpointStatus `json:"endpointStatus,omitempty"`

	// StatefulSetRefs tracks the StatefulSets created for endpoint connections
	// +optional
	StatefulSetRefs []StatefulSetReference `json:"statefulSetRefs,omitempty"`

	// AutoDiscoveryStatus provides details about the auto-discovery process
	// +optional
	AutoDiscoveryStatus *AutoDiscoveryStatus `json:"autoDiscoveryStatus,omitempty"`

	// TagSelectorResults provides results of tag selector matching
	// +optional
	TagSelectorResults []TagSelectorResult `json:"tagSelectorResults,omitempty"`

	// RecentErrors tracks recent operational errors with context
	// +optional
	// +listType=atomic
	RecentErrors []DetailedError `json:"recentErrors,omitempty"`

	// OperationalMetrics provides performance and operational insights
	// +optional
	OperationalMetrics *OperationalMetrics `json:"operationalMetrics,omitempty"`
}

// EndpointStatus provides status information for a specific endpoint
type EndpointStatus struct {
	// Name identifies the endpoint
	Name string `json:"name"`

	// TailscaleIP is the endpoint's Tailscale IP
	TailscaleIP string `json:"tailscaleIP"`

	// HealthStatus indicates the health check status
	// +kubebuilder:validation:Enum=Healthy;Unhealthy;Unknown;Disabled
	HealthStatus string `json:"healthStatus"`

	// LastHealthCheck is when the endpoint was last health checked
	// +optional
	LastHealthCheck *metav1.Time `json:"lastHealthCheck,omitempty"`

	// DiscoverySource indicates how this endpoint was discovered
	// +kubebuilder:validation:Enum=Manual;AutoDiscovery;VIPService;TagSelector
	DiscoverySource string `json:"discoverySource"`

	// Tags are the Tailscale tags associated with this endpoint
	// +optional
	Tags []string `json:"tags,omitempty"`

	// HealthCheckDetails provides detailed health check information
	// +optional
	HealthCheckDetails *HealthCheckDetails `json:"healthCheckDetails,omitempty"`

	// ConnectionStatus tracks the status of StatefulSet connections
	// +optional
	ConnectionStatus *ConnectionStatus `json:"connectionStatus,omitempty"`

	// LastError provides context about the most recent error
	// +optional
	LastError *DetailedError `json:"lastError,omitempty"`

	// Weight is the current load balancing weight
	// +optional
	Weight *int32 `json:"weight,omitempty"`

	// ExternalTarget shows the configured external target
	// +optional
	ExternalTarget string `json:"externalTarget,omitempty"`
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

// HealthCheckDetails provides comprehensive health check information
type HealthCheckDetails struct {
	// Enabled indicates if health checking is enabled for this endpoint
	Enabled bool `json:"enabled"`

	// SuccessiveSuccesses number of consecutive successful health checks
	SuccessiveSuccesses int32 `json:"successiveSuccesses"`

	// SuccessiveFailures number of consecutive failed health checks
	SuccessiveFailures int32 `json:"successiveFailures"`

	// TotalChecks total number of health checks performed
	TotalChecks int64 `json:"totalChecks"`

	// SuccessfulChecks total number of successful health checks
	SuccessfulChecks int64 `json:"successfulChecks"`

	// LastCheckDuration duration of the last health check
	// +optional
	LastCheckDuration *metav1.Duration `json:"lastCheckDuration,omitempty"`

	// LastCheckResponse details about the last health check response
	// +optional
	LastCheckResponse *HealthCheckResponse `json:"lastCheckResponse,omitempty"`

	// AverageResponseTime average health check response time
	// +optional
	AverageResponseTime *metav1.Duration `json:"averageResponseTime,omitempty"`

	// RecentFailures details about recent health check failures
	// +optional
	RecentFailures []HealthCheckFailure `json:"recentFailures,omitempty"`

	// Configuration shows the current health check configuration
	// +optional
	Configuration *EndpointHealthCheck `json:"configuration,omitempty"`
}

// HealthCheckResponse contains details about a health check response
type HealthCheckResponse struct {
	// StatusCode HTTP status code (for HTTP/HTTPS checks)
	// +optional
	StatusCode *int32 `json:"statusCode,omitempty"`

	// Success indicates if the health check succeeded
	Success bool `json:"success"`

	// ResponseTime how long the check took
	ResponseTime metav1.Duration `json:"responseTime"`

	// ErrorMessage error message if the check failed
	// +optional
	ErrorMessage string `json:"errorMessage,omitempty"`

	// ResponseHeaders key response headers (for HTTP/HTTPS checks)
	// +optional
	ResponseHeaders map[string]string `json:"responseHeaders,omitempty"`

	// Timestamp when the check was performed
	Timestamp metav1.Time `json:"timestamp"`
}

// HealthCheckFailure provides details about a health check failure
type HealthCheckFailure struct {
	// Timestamp when the failure occurred
	Timestamp metav1.Time `json:"timestamp"`

	// ErrorType categorizes the type of failure
	// +kubebuilder:validation:Enum=Timeout;ConnectionRefused;DNSResolution;HTTPError;TCPError;UDPError;TLSError;Unknown
	ErrorType string `json:"errorType"`

	// ErrorMessage detailed error message
	ErrorMessage string `json:"errorMessage"`

	// StatusCode HTTP status code if applicable
	// +optional
	StatusCode *int32 `json:"statusCode,omitempty"`

	// Duration how long the failed check took
	// +optional
	Duration *metav1.Duration `json:"duration,omitempty"`

	// RetryAttempt which retry attempt this was
	// +optional
	RetryAttempt *int32 `json:"retryAttempt,omitempty"`
}

// ConnectionStatus provides details about StatefulSet connection status
type ConnectionStatus struct {
	// IngressReady indicates if ingress connection is ready
	// +optional
	IngressReady *bool `json:"ingressReady,omitempty"`

	// EgressReady indicates if egress connection is ready
	// +optional
	EgressReady *bool `json:"egressReady,omitempty"`

	// TailscaleConnected indicates if Tailscale connection is established
	// +optional
	TailscaleConnected *bool `json:"tailscaleConnected,omitempty"`

	// LastConnectionAttempt timestamp of last connection attempt
	// +optional
	LastConnectionAttempt *metav1.Time `json:"lastConnectionAttempt,omitempty"`

	// ConnectionErrors recent connection errors
	// +optional
	ConnectionErrors []DetailedError `json:"connectionErrors,omitempty"`
}

// AutoDiscoveryStatus provides details about the auto-discovery process
type AutoDiscoveryStatus struct {
	// Enabled indicates if auto-discovery is enabled
	Enabled bool `json:"enabled"`

	// LastDiscoveryTime when auto-discovery was last performed
	// +optional
	LastDiscoveryTime *metav1.Time `json:"lastDiscoveryTime,omitempty"`

	// DiscoveryDuration how long the last discovery took
	// +optional
	DiscoveryDuration *metav1.Duration `json:"discoveryDuration,omitempty"`

	// TotalDevicesScanned total devices found in tailnet
	TotalDevicesScanned int `json:"totalDevicesScanned"`

	// DevicesMatchingCriteria devices that matched discovery criteria
	DevicesMatchingCriteria int `json:"devicesMatchingCriteria"`

	// VIPServicesDiscovered VIP services found
	// +optional
	VIPServicesDiscovered int `json:"vipServicesDiscovered,omitempty"`

	// TagSelectorsApplied number of tag selectors applied
	// +optional
	TagSelectorsApplied int `json:"tagSelectorsApplied,omitempty"`

	// DiscoveryErrors errors encountered during discovery
	// +optional
	DiscoveryErrors []DetailedError `json:"discoveryErrors,omitempty"`

	// APIStatus status of Tailscale API communication
	// +optional
	APIStatus *TailscaleAPIStatus `json:"apiStatus,omitempty"`
}

// TagSelectorResult provides results of tag selector matching
type TagSelectorResult struct {
	// Selector the tag selector that was applied
	Selector TagSelector `json:"selector"`

	// MatchedDevices number of devices that matched this selector
	MatchedDevices int `json:"matchedDevices"`

	// ExcludedDevices number of devices that were excluded
	// +optional
	ExcludedDevices int `json:"excludedDevices,omitempty"`

	// LastApplied when this selector was last applied
	LastApplied metav1.Time `json:"lastApplied"`

	// Success indicates if the selector was applied successfully
	Success bool `json:"success"`

	// ErrorMessage error message if selector application failed
	// +optional
	ErrorMessage string `json:"errorMessage,omitempty"`

	// MatchedDeviceNames names of devices that matched (for debugging)
	// +optional
	MatchedDeviceNames []string `json:"matchedDeviceNames,omitempty"`
}

// TailscaleDevice represents a Tailscale machine/device created by the operator
// Similar to the ProxyGroup device status in the official Tailscale k8s-operator
type TailscaleDevice struct {
	// Hostname is the Tailscale hostname of the device
	// +optional
	Hostname string `json:"hostname,omitempty"`

	// TailscaleIP is the Tailscale IP address assigned to the device
	// +optional
	TailscaleIP string `json:"tailscaleIP,omitempty"`

	// TailscaleFQDN is the fully qualified domain name in the tailnet
	// +optional
	TailscaleFQDN string `json:"tailscaleFQDN,omitempty"`

	// NodeID is the Tailscale node ID
	// +optional
	NodeID string `json:"nodeID,omitempty"`

	// StatefulSetPod is the name of the pod backing this device
	// +optional
	StatefulSetPod string `json:"statefulSetPod,omitempty"`

	// Connected indicates if the device is currently connected to the tailnet
	// +optional
	Connected bool `json:"connected,omitempty"`

	// Online indicates if the device is currently online
	// +optional
	Online bool `json:"online,omitempty"`

	// LastSeen is when the device was last seen online
	// +optional
	LastSeen *metav1.Time `json:"lastSeen,omitempty"`

	// Tags are the Tailscale tags applied to this device
	// +optional
	Tags []string `json:"tags,omitempty"`
}

// StatefulSetInfo provides information about created StatefulSets
type StatefulSetInfo struct {
	// Name of the StatefulSet
	Name string `json:"name"`

	// Namespace of the StatefulSet
	Namespace string `json:"namespace"`

	// Type indicates the StatefulSet type (ingress, egress, bidirectional)
	Type string `json:"type"`

	// Replicas is the desired number of replicas
	Replicas int32 `json:"replicas"`

	// ReadyReplicas is the number of ready replicas
	ReadyReplicas int32 `json:"readyReplicas"`

	// Service is the name of the associated Service
	// +optional
	Service string `json:"service,omitempty"`

	// CreatedAt is when the StatefulSet was created
	// +optional
	CreatedAt *metav1.Time `json:"createdAt,omitempty"`
}

func init() {
	SchemeBuilder.Register(&TailscaleEndpoints{}, &TailscaleEndpointsList{})
}
