// Copyright (c) Tailscale Gateway Authors
// SPDX-License-Identifier: BSD-3-Clause

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="Status",type="string",JSONPath=`.status.conditions[?(@.type == "Ready")].reason`,description="Status of the tailnet connection."
// +kubebuilder:printcolumn:name="Tailnet",type="string",JSONPath=`.status.tailnetInfo.name`,description="Discovered tailnet domain name."
// +kubebuilder:printcolumn:name="Organization",type="string",JSONPath=`.status.tailnetInfo.organization`,description="Organization type of the tailnet."

// TailscaleTailnet defines a connection to a specific Tailscale tailnet
// with associated credentials and configuration.
type TailscaleTailnet struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   TailscaleTailnetSpec   `json:"spec,omitempty"`
	Status TailscaleTailnetStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// TailscaleTailnetList contains a list of TailscaleTailnet
type TailscaleTailnetList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []TailscaleTailnet `json:"items"`
}

// TailscaleTailnetSpec defines the desired state of a tailnet connection
type TailscaleTailnetSpec struct {
	// Tailnet is the tailnet name or organization.
	// If not specified, uses the default tailnet ("-").
	// +optional
	Tailnet string `json:"tailnet,omitempty"`

	// OAuthSecretName is the name of the secret containing OAuth credentials.
	// The secret must contain keys `client_id` and `client_secret`.
	OAuthSecretName string `json:"oauthSecretName"`

	// OAuthSecretNamespace is the namespace of the secret containing OAuth credentials.
	// If not specified, it will look for the secret in the same namespace as the TailscaleTailnet.
	// +optional
	OAuthSecretNamespace string `json:"oauthSecretNamespace,omitempty"`

	// Tags to apply to auth keys created for this tailnet.
	// +optional
	Tags []string `json:"tags,omitempty"`
}

// TailscaleTailnetStatus defines the observed state of a tailnet connection
type TailscaleTailnetStatus struct {
	// Conditions represent the current state of the tailnet connection.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// TailnetInfo contains information about the connected tailnet.
	// +optional
	TailnetInfo *TailnetInfo `json:"tailnetInfo,omitempty"`

	// LastSyncTime is the last time the operator successfully
	// communicated with the Tailscale API for this tailnet.
	// +optional
	LastSyncTime *metav1.Time `json:"lastSyncTime,omitempty"`

	// APIStatus provides detailed information about Tailscale API communication
	// +optional
	APIStatus *TailscaleAPIStatus `json:"apiStatus,omitempty"`

	// CredentialStatus tracks OAuth credential validation
	// +optional
	CredentialStatus *CredentialStatus `json:"credentialStatus,omitempty"`

	// OperationalMetrics provides performance and operational insights
	// +optional
	OperationalMetrics *OperationalMetrics `json:"operationalMetrics,omitempty"`

	// RecentErrors tracks recent operational errors with context
	// +optional
	// +listType=atomic
	RecentErrors []DetailedError `json:"recentErrors,omitempty"`

	// DeviceCount total number of devices in this tailnet
	// +optional
	DeviceCount *int32 `json:"deviceCount,omitempty"`

	// ConnectedDevices number of currently connected devices
	// +optional
	ConnectedDevices *int32 `json:"connectedDevices,omitempty"`

	// FeatureFlags available features for this tailnet
	// +optional
	FeatureFlags []string `json:"featureFlags,omitempty"`
}

// TailnetInfo contains essential metadata about a connected tailnet
type TailnetInfo struct {
	// Name is the tailnet domain (e.g., "tail123abc.ts.net").
	Name string `json:"name"`

	// MagicDNSSuffix is the DNS suffix used for MagicDNS in this tailnet.
	// +optional
	MagicDNSSuffix *string `json:"magicDNSSuffix,omitempty"`

	// Organization is the organization name if applicable.
	// +optional
	Organization *string `json:"organization,omitempty"`

	// TailnetID is the unique identifier for this tailnet
	// +optional
	TailnetID *string `json:"tailnetID,omitempty"`

	// PlanType indicates the Tailscale plan (Personal, Business, Enterprise)
	// +optional
	PlanType *string `json:"planType,omitempty"`

	// Region indicates the primary region for this tailnet
	// +optional
	Region *string `json:"region,omitempty"`

	// CreatedAt when the tailnet was created
	// +optional
	CreatedAt *metav1.Time `json:"createdAt,omitempty"`

	// LastUpdated when the tailnet info was last updated
	// +optional
	LastUpdated *metav1.Time `json:"lastUpdated,omitempty"`
}

// ConditionType represents the type of condition for TailscaleTailnet
type ConditionType string

const (
	// TailnetReady indicates that the tailnet connection is ready and functional
	TailnetReady ConditionType = "Ready"

	// TailnetAuthenticated indicates that OAuth authentication is working
	TailnetAuthenticated ConditionType = "Authenticated"

	// TailnetValidated indicates that the tailnet configuration is valid
	TailnetValidated ConditionType = "Validated"
)

const (
	// ReasonInvalidConfiguration indicates invalid tailnet configuration
	ReasonInvalidConfiguration = "InvalidConfiguration"
)

// CredentialStatus tracks OAuth credential validation and status
type CredentialStatus struct {
	// Valid indicates if the credentials are currently valid
	Valid bool `json:"valid"`

	// LastValidated when credentials were last validated
	// +optional
	LastValidated *metav1.Time `json:"lastValidated,omitempty"`

	// ExpiresAt when the current token expires (if applicable)
	// +optional
	ExpiresAt *metav1.Time `json:"expiresAt,omitempty"`

	// Scopes available OAuth scopes for these credentials
	// +optional
	Scopes []string `json:"scopes,omitempty"`

	// TokenRefreshCount number of times the token has been refreshed
	// +optional
	TokenRefreshCount *int64 `json:"tokenRefreshCount,omitempty"`

	// LastRefresh when the token was last refreshed
	// +optional
	LastRefresh *metav1.Time `json:"lastRefresh,omitempty"`

	// ValidationErrors errors from credential validation
	// +optional
	ValidationErrors []DetailedError `json:"validationErrors,omitempty"`

	// SecretVersion tracks the version of the OAuth secret
	// +optional
	SecretVersion string `json:"secretVersion,omitempty"`
}

func init() {
	SchemeBuilder.Register(&TailscaleTailnet{}, &TailscaleTailnetList{})
}
