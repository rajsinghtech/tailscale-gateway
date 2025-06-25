// Copyright (c) Tailscale Gateway Authors
// SPDX-License-Identifier: BSD-3-Clause

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// LocalPolicyTargetReference identifies a target for policy attachment
// This type is shared across multiple CRDs to ensure consistency and avoid circular dependencies
type LocalPolicyTargetReference struct {
	// Group is the group of the target resource.
	Group gwapiv1.Group `json:"group"`

	// Kind is the kind of the target resource.
	Kind gwapiv1.Kind `json:"kind"`

	// Name is the name of the target resource.
	Name gwapiv1.ObjectName `json:"name"`

	// Namespace is the namespace of the target resource.
	// +optional
	Namespace *gwapiv1.Namespace `json:"namespace,omitempty"`

	// SectionName is the name of a section within the target resource.
	// +optional
	SectionName *gwapiv1.SectionName `json:"sectionName,omitempty"`
}

// DetailedError provides comprehensive error information with context
type DetailedError struct {
	// Code is a machine-readable error code
	Code string `json:"code"`

	// Message is a human-readable error message
	Message string `json:"message"`

	// Component identifies which component generated the error
	// +kubebuilder:validation:Enum=Controller;ExtensionServer;TailscaleAPI;ServiceDiscovery;RouteGeneration;Validation;StatefulSet;HealthCheck;OAuth;PolicyEngine
	Component string `json:"component"`

	// Timestamp when the error occurred
	Timestamp metav1.Time `json:"timestamp"`

	// Retries indicates how many times this operation has been retried
	Retries int32 `json:"retries"`

	// LastRetry timestamp of the last retry attempt
	// +optional
	LastRetry *metav1.Time `json:"lastRetry,omitempty"`

	// Context provides additional context about the error
	// +optional
	Context map[string]string `json:"context,omitempty"`

	// HTTPStatusCode provides HTTP status code for API-related errors
	// +optional
	HTTPStatusCode *int32 `json:"httpStatusCode,omitempty"`

	// ResourceRef identifies the specific resource that caused the error
	// +optional
	ResourceRef *LocalPolicyTargetReference `json:"resourceRef,omitempty"`

	// Severity indicates the severity of the error
	// +kubebuilder:validation:Enum=Critical;High;Medium;Low;Info
	// +optional
	Severity string `json:"severity,omitempty"`

	// ResolutionHint provides guidance on how to resolve the error
	// +optional
	ResolutionHint string `json:"resolutionHint,omitempty"`
}

// OperationalMetrics provides performance and operational insights
type OperationalMetrics struct {
	// LastReconcileTime when the last reconciliation occurred
	LastReconcileTime metav1.Time `json:"lastReconcileTime"`

	// ReconcileDuration how long the last reconciliation took
	// +optional
	ReconcileDuration *metav1.Duration `json:"reconcileDuration,omitempty"`

	// ReconcileCount total number of reconciliations
	ReconcileCount int64 `json:"reconcileCount"`

	// SuccessfulReconciles number of successful reconciliations
	SuccessfulReconciles int64 `json:"successfulReconciles"`

	// FailedReconciles number of failed reconciliations
	FailedReconciles int64 `json:"failedReconciles"`

	// APICallCount total API calls made to Tailscale
	// +optional
	APICallCount int64 `json:"apiCallCount,omitempty"`

	// APIRateLimitHits number of times rate limits were hit
	// +optional
	APIRateLimitHits int64 `json:"apiRateLimitHits,omitempty"`

	// AverageReconcileTime average reconciliation duration
	// +optional
	AverageReconcileTime *metav1.Duration `json:"averageReconcileTime,omitempty"`

	// ErrorRate percentage of failed operations as a string (e.g., "15.5%")
	// +optional
	ErrorRate *string `json:"errorRate,omitempty"`

	// ThroughputPerHour operations completed per hour
	// +optional
	ThroughputPerHour *int64 `json:"throughputPerHour,omitempty"`
}

// TailscaleAPIStatus provides detailed information about Tailscale API communication
type TailscaleAPIStatus struct {
	// Connected indicates if API communication is working
	Connected bool `json:"connected"`

	// LastSuccessfulCall timestamp of last successful API call
	// +optional
	LastSuccessfulCall *metav1.Time `json:"lastSuccessfulCall,omitempty"`

	// RateLimitRemaining API rate limit remaining
	// +optional
	RateLimitRemaining *int32 `json:"rateLimitRemaining,omitempty"`

	// RateLimitReset when the rate limit resets
	// +optional
	RateLimitReset *metav1.Time `json:"rateLimitReset,omitempty"`

	// LastError provides details about the most recent API error
	// +optional
	LastError *DetailedError `json:"lastError,omitempty"`

	// Capabilities lists available API capabilities
	// +optional
	Capabilities []string `json:"capabilities,omitempty"`

	// BaseURL the API base URL being used
	// +optional
	BaseURL string `json:"baseURL,omitempty"`

	// UserAgent the user agent string being used
	// +optional
	UserAgent string `json:"userAgent,omitempty"`

	// RequestCount total number of API requests made
	// +optional
	RequestCount int64 `json:"requestCount,omitempty"`

	// AverageResponseTime average API response time
	// +optional
	AverageResponseTime *metav1.Duration `json:"averageResponseTime,omitempty"`
}

// StandardConditionTypes defines common condition types used across all CRDs
const (
	// ConditionReady indicates that the resource is ready and functional
	ConditionReady = "Ready"

	// ConditionProgressing indicates that the resource is making progress toward readiness
	ConditionProgressing = "Progressing"

	// ConditionDegraded indicates that the resource is functioning but with reduced capability
	ConditionDegraded = "Degraded"

	// ConditionResourcesReady indicates that dependent resources are ready
	ConditionResourcesReady = "ResourcesReady"

	// ConditionDependenciesReady indicates that dependencies are satisfied
	ConditionDependenciesReady = "DependenciesReady"

	// ConditionAPIConnectionReady indicates that API connections are functional
	ConditionAPIConnectionReady = "APIConnectionReady"

	// ConditionValidated indicates that the resource configuration is valid
	ConditionValidated = "Validated"

	// ConditionAuthenticated indicates that authentication is working
	ConditionAuthenticated = "Authenticated"
)

// StandardConditionReasons defines common condition reasons
const (
	// ReasonReady indicates the resource is ready
	ReasonReady = "Ready"

	// ReasonProgressing indicates the resource is progressing
	ReasonProgressing = "Progressing"

	// ReasonFailed indicates the resource has failed
	ReasonFailed = "Failed"

	// ReasonPending indicates the resource is pending
	ReasonPending = "Pending"

	// ReasonDependencyNotReady indicates a dependency is not ready
	ReasonDependencyNotReady = "DependencyNotReady"

	// ReasonAPIError indicates an API error occurred
	ReasonAPIError = "APIError"

	// ReasonAuthenticationFailed indicates authentication failed
	ReasonAuthenticationFailed = "AuthenticationFailed"

	// ReasonValidationFailed indicates validation failed
	ReasonValidationFailed = "ValidationFailed"

	// ReasonResourceNotFound indicates a required resource was not found
	ReasonResourceNotFound = "ResourceNotFound"

	// ReasonConfigurationError indicates a configuration error
	ReasonConfigurationError = "ConfigurationError"
)

// ErrorCodes defines standard error codes for consistent error reporting
const (
	// ErrorCodeAPIConnection indicates API connection errors
	ErrorCodeAPIConnection = "API_CONNECTION_ERROR"

	// ErrorCodeAuthentication indicates authentication errors
	ErrorCodeAuthentication = "AUTHENTICATION_ERROR"

	// ErrorCodeValidation indicates validation errors
	ErrorCodeValidation = "VALIDATION_ERROR"

	// ErrorCodeResourceNotFound indicates resource not found errors
	ErrorCodeResourceNotFound = "RESOURCE_NOT_FOUND"

	// ErrorCodeRateLimit indicates rate limiting errors
	ErrorCodeRateLimit = "RATE_LIMIT_ERROR"

	// ErrorCodeTimeout indicates timeout errors
	ErrorCodeTimeout = "TIMEOUT_ERROR"

	// ErrorCodeConfiguration indicates configuration errors
	ErrorCodeConfiguration = "CONFIGURATION_ERROR"

	// ErrorCodeDependency indicates dependency errors
	ErrorCodeDependency = "DEPENDENCY_ERROR"

	// ErrorCodeHealthCheck indicates health check errors
	ErrorCodeHealthCheck = "HEALTH_CHECK_ERROR"

	// ErrorCodeNetworking indicates networking errors
	ErrorCodeNetworking = "NETWORKING_ERROR"

	// ErrorCodeServiceDiscovery indicates service discovery errors
	ErrorCodeServiceDiscovery = "SERVICE_DISCOVERY_ERROR"

	// ErrorCodeReconciliation indicates reconciliation errors
	ErrorCodeReconciliation = "RECONCILIATION_ERROR"
)

// ComponentTypes defines standard component identifiers
const (
	// ComponentController identifies controller components
	ComponentController = "Controller"

	// ComponentExtensionServer identifies extension server components
	ComponentExtensionServer = "ExtensionServer"

	// ComponentTailscaleAPI identifies Tailscale API components
	ComponentTailscaleAPI = "TailscaleAPI"

	// ComponentServiceDiscovery identifies service discovery components
	ComponentServiceDiscovery = "ServiceDiscovery"

	// ComponentRouteGeneration identifies route generation components
	ComponentRouteGeneration = "RouteGeneration"

	// ComponentValidation identifies validation components
	ComponentValidation = "Validation"

	// ComponentStatefulSet identifies StatefulSet management components
	ComponentStatefulSet = "StatefulSet"

	// ComponentHealthCheck identifies health check components
	ComponentHealthCheck = "HealthCheck"

	// ComponentOAuth identifies OAuth authentication components
	ComponentOAuth = "OAuth"

	// ComponentPolicyEngine identifies policy engine components
	ComponentPolicyEngine = "PolicyEngine"
)

// SeverityTypes defines standard severity levels
const (
	// SeverityCritical indicates critical errors that require immediate attention
	SeverityCritical = "Critical"

	// SeverityHigh indicates high priority errors
	SeverityHigh = "High"

	// SeverityMedium indicates medium priority errors
	SeverityMedium = "Medium"

	// SeverityLow indicates low priority errors
	SeverityLow = "Low"

	// SeverityInfo indicates informational messages
	SeverityInfo = "Info"
)

// DependencyStatus tracks the status of dependent resources
type DependencyStatus struct {
	// ResourceRef references the dependent resource
	ResourceRef LocalPolicyTargetReference `json:"resourceRef"`

	// Status indicates the current state of the dependency
	// +kubebuilder:validation:Enum=Ready;Pending;Failed;NotFound;Unknown;Conflicted
	Status string `json:"status"`

	// LastChecked when the dependency was last checked
	LastChecked metav1.Time `json:"lastChecked"`

	// ErrorMessage provides context if the dependency is in an error state
	// +optional
	ErrorMessage string `json:"errorMessage,omitempty"`

	// BlockingReason explains why this dependency is blocking operations
	// +optional
	BlockingReason string `json:"blockingReason,omitempty"`

	// Version tracks the resource version of the dependency
	// +optional
	Version string `json:"version,omitempty"`

	// ReadyConditions tracks which conditions are ready on the dependency
	// +optional
	ReadyConditions []string `json:"readyConditions,omitempty"`

	// FailedConditions tracks which conditions have failed on the dependency
	// +optional
	FailedConditions []string `json:"failedConditions,omitempty"`

	// DependencyType categorizes the type of dependency
	// +kubebuilder:validation:Enum=Required;Optional;Soft;Hard
	// +optional
	DependencyType string `json:"dependencyType,omitempty"`

	// Impact describes the impact if this dependency is not ready
	// +optional
	Impact string `json:"impact,omitempty"`
}

// DependencyStatusTypes defines standard dependency status values
const (
	// DependencyStatusReady indicates the dependency is ready
	DependencyStatusReady = "Ready"

	// DependencyStatusPending indicates the dependency is pending
	DependencyStatusPending = "Pending"

	// DependencyStatusFailed indicates the dependency has failed
	DependencyStatusFailed = "Failed"

	// DependencyStatusNotFound indicates the dependency was not found
	DependencyStatusNotFound = "NotFound"

	// DependencyStatusUnknown indicates the dependency status is unknown
	DependencyStatusUnknown = "Unknown"

	// DependencyStatusConflicted indicates the dependency has conflicts
	DependencyStatusConflicted = "Conflicted"
)

// DependencyTypes defines standard dependency types
const (
	// DependencyTypeRequired indicates a required dependency
	DependencyTypeRequired = "Required"

	// DependencyTypeOptional indicates an optional dependency
	DependencyTypeOptional = "Optional"

	// DependencyTypeSoft indicates a soft dependency (best effort)
	DependencyTypeSoft = "Soft"

	// DependencyTypeHard indicates a hard dependency (must be satisfied)
	DependencyTypeHard = "Hard"
)
