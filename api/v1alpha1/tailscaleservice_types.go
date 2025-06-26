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

// TailscaleServiceSpec defines the desired state of TailscaleService
type TailscaleServiceSpec struct {
	// VIPService configuration for the Tailscale VIP service
	// +kubebuilder:validation:Required
	VIPService VIPServiceSpec `json:"vipService"`

	// Backends defines the backend services for this VIP service
	// +optional
	Backends []BackendSpec `json:"backends,omitempty"`

	// Proxy configuration for creating Tailscale proxy infrastructure
	// +optional
	Proxy *ProxySpec `json:"proxy,omitempty"`

	// Tailnet to connect to (required if proxy is specified)
	// +optional
	Tailnet string `json:"tailnet,omitempty"`
}

// VIPServiceSpec defines configuration for Tailscale VIP services
type VIPServiceSpec struct {
	// Name of the VIP service (e.g., "svc:web-service")
	// If not specified, defaults to "svc:<TailscaleService-name>"
	// +kubebuilder:validation:Pattern="^svc:[a-z0-9]([-a-z0-9]*[a-z0-9])?$"
	// +optional
	Name string `json:"name,omitempty"`

	// Ports the service should expose
	// Format: ["tcp:80", "udp:53", "tcp:443"]
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	Ports []string `json:"ports"`

	// Tags for the VIP service (used in ACL policies)
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	Tags []string `json:"tags"`

	// Comment for service documentation
	// +optional
	Comment string `json:"comment,omitempty"`

	// AutoApprove indicates whether endpoints should be auto-approved for hosting this service
	// +kubebuilder:default=true
	// +optional
	AutoApprove *bool `json:"autoApprove,omitempty"`
}

// BackendSpec defines a backend service for the VIP service
type BackendSpec struct {
	// Type of backend: kubernetes, external, or tailscale
	// +kubebuilder:validation:Enum=kubernetes;external;tailscale
	// +kubebuilder:validation:Required
	Type string `json:"type"`

	// Service reference for kubernetes backends
	// Format: "service-name.namespace.svc.cluster.local:port"
	// +optional
	Service string `json:"service,omitempty"`

	// Address for external backends
	// Format: "hostname:port" or "ip:port"
	// +optional
	Address string `json:"address,omitempty"`

	// TailscaleService reference for tailscale backends
	// Format: "service-name" (in same namespace) or "service-name.namespace"
	// +optional
	TailscaleService string `json:"tailscaleService,omitempty"`

	// Weight for load balancing (1-100)
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=100
	// +kubebuilder:default=1
	// +optional
	Weight *int32 `json:"weight,omitempty"`

	// Priority for failover (0 = highest priority)
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:default=0
	// +optional
	Priority *int32 `json:"priority,omitempty"`
}

// ProxySpec defines configuration for Tailscale proxy infrastructure
type ProxySpec struct {
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

	// Tags for the proxy machines (in addition to VIP service tags)
	// +optional
	Tags []string `json:"tags,omitempty"`

	// Image for the Tailscale proxy container
	// +kubebuilder:default="tailscale/tailscale:latest"
	// +optional
	Image string `json:"image,omitempty"`
}

// TailscaleServiceStatus defines the observed state of TailscaleService
type TailscaleServiceStatus struct {
	// Conditions represent the current state of the TailscaleService
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// VIPServiceStatus provides status of the created VIP service
	// +optional
	VIPServiceStatus *ServiceStatus `json:"vipServiceStatus,omitempty"`

	// ProxyStatus provides status of the proxy infrastructure
	// +optional
	ProxyStatus *ProxyStatus `json:"proxyStatus,omitempty"`

	// BackendStatus provides status of configured backends
	// +optional
	BackendStatus []BackendStatus `json:"backendStatus,omitempty"`

	// TotalBackends is the total number of backends
	TotalBackends int `json:"totalBackends"`

	// HealthyBackends is the number of healthy backends
	HealthyBackends int `json:"healthyBackends"`

	// LastUpdate indicates when the service status was last updated
	// +optional
	LastUpdate *metav1.Time `json:"lastUpdate,omitempty"`
}

// ServiceStatus provides status of the VIP service
type ServiceStatus struct {
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

	// LastRegistration when backends were last registered
	// +optional
	LastRegistration *metav1.Time `json:"lastRegistration,omitempty"`

	// ServiceTags are the actual tags applied to the VIP service
	// +optional
	ServiceTags []string `json:"serviceTags,omitempty"`

	// RegistrationErrors tracks errors in VIP service management
	// +optional
	RegistrationErrors []string `json:"registrationErrors,omitempty"`
}

// ProxyStatus provides status of the proxy infrastructure
type ProxyStatus struct {
	// StatefulSetName is the name of the created StatefulSet
	StatefulSetName string `json:"statefulSetName"`

	// Replicas is the desired number of replicas
	Replicas int32 `json:"replicas"`

	// ReadyReplicas is the number of ready replicas
	ReadyReplicas int32 `json:"readyReplicas"`

	// Devices are the Tailscale devices created by the proxy
	// +optional
	Devices []ProxyDevice `json:"devices,omitempty"`

	// ServiceName is the name of the Kubernetes service for the proxy
	// +optional
	ServiceName string `json:"serviceName,omitempty"`

	// CreatedAt when the proxy infrastructure was created
	// +optional
	CreatedAt *metav1.Time `json:"createdAt,omitempty"`
}

// ProxyDevice represents a Tailscale device created by the proxy
type ProxyDevice struct {
	// Hostname is the Tailscale hostname assigned to device
	Hostname string `json:"hostname"`

	// TailscaleIP is the Tailscale IP address
	// +optional
	TailscaleIP string `json:"tailscaleIP,omitempty"`

	// NodeID is the Tailscale node ID
	// +optional
	NodeID string `json:"nodeID,omitempty"`

	// Connected indicates if the device is connected to the tailnet
	Connected bool `json:"connected"`

	// PodName is the Kubernetes pod backing this device
	// +optional
	PodName string `json:"podName,omitempty"`

	// LastSeen when the device was last seen online
	// +optional
	LastSeen *metav1.Time `json:"lastSeen,omitempty"`
}

// BackendStatus provides status of a configured backend
type BackendStatus struct {
	// Type of backend
	Type string `json:"type"`

	// Target is the backend target (service, address, or tailscaleService)
	Target string `json:"target"`

	// Healthy indicates if the backend is healthy
	Healthy bool `json:"healthy"`

	// Weight is the effective weight for load balancing
	Weight int32 `json:"weight"`

	// Priority is the effective priority for failover
	Priority int32 `json:"priority"`

	// LastHealthCheck when health was last checked
	// +optional
	LastHealthCheck *metav1.Time `json:"lastHealthCheck,omitempty"`

	// HealthCheckError contains the last health check error
	// +optional
	HealthCheckError string `json:"healthCheckError,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:resource:path=tailscaleservice,scope=Namespaced,categories=tailscale-gateway
//+kubebuilder:printcolumn:name="VIP Service",type="string",JSONPath=".status.vipServiceStatus.serviceName",description="VIP service name"
//+kubebuilder:printcolumn:name="Backends",type="integer",JSONPath=".status.totalBackends",description="Total backends"
//+kubebuilder:printcolumn:name="Healthy",type="integer",JSONPath=".status.healthyBackends",description="Healthy backends"
//+kubebuilder:printcolumn:name="DNS Name",type="string",JSONPath=".status.vipServiceStatus.dnsName",description="Service DNS name"
//+kubebuilder:printcolumn:name="Ready Replicas",type="string",JSONPath=".status.proxyStatus.readyReplicas",description="Ready proxy replicas"
//+kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// TailscaleService is the Schema for the tailscaleservice API
type TailscaleService struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   TailscaleServiceSpec   `json:"spec,omitempty"`
	Status TailscaleServiceStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// TailscaleServiceList contains a list of TailscaleService
type TailscaleServiceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []TailscaleService `json:"items"`
}

func init() {
	SchemeBuilder.Register(&TailscaleService{}, &TailscaleServiceList{})
}