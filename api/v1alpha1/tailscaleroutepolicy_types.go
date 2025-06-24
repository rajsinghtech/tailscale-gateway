// Copyright (c) Tailscale Gateway Authors
// SPDX-License-Identifier: BSD-3-Clause

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Status",type="string",JSONPath=`.status.conditions[?(@.type == "Ready")].reason`,description="Status of the route policy."
// +kubebuilder:printcolumn:name="Gateway",type="string",JSONPath=`.spec.targetRefs[0].name`,description="Target gateway."

// TailscaleRoutePolicy defines advanced routing policies for tailnet traffic
// through Envoy Gateway, supporting conditions, transformations, and actions.
type TailscaleRoutePolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   TailscaleRoutePolicySpec   `json:"spec,omitempty"`
	Status TailscaleRoutePolicyStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// TailscaleRoutePolicyList contains a list of TailscaleRoutePolicy
type TailscaleRoutePolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []TailscaleRoutePolicy `json:"items"`
}

// TailscaleRoutePolicySpec defines the desired routing policy
type TailscaleRoutePolicySpec struct {
	// TargetRefs identifies the targets that this policy applies to.
	// Can target Gateways, HTTPRoutes, or TailscaleGateways.
	TargetRefs []LocalPolicyTargetReference `json:"targetRefs"`

	// Rules define the routing policy rules to apply.
	Rules []PolicyRule `json:"rules"`
}

// LocalPolicyTargetReference identifies a target for policy attachment
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

// PolicyRule defines a single routing policy rule
type PolicyRule struct {
	// Name is an optional name for this rule for debugging purposes.
	// +optional
	Name *string `json:"name,omitempty"`

	// Matches define when this rule should be applied.
	// +optional
	Matches []PolicyMatch `json:"matches,omitempty"`

	// Actions define what should happen when this rule matches.
	Actions []PolicyAction `json:"actions"`
}

// PolicyMatch defines conditions for when a rule should apply
type PolicyMatch struct {
	// TailnetDevice matches based on characteristics of the source tailnet device.
	// +optional
	TailnetDevice *TailnetDeviceMatch `json:"tailnetDevice,omitempty"`

	// Path matches based on the request path.
	// +optional
	Path *PathMatch `json:"path,omitempty"`

	// Headers matches based on request headers.
	// +optional
	Headers []HeaderMatch `json:"headers,omitempty"`

	// Method matches based on HTTP method.
	// +optional
	Method *MethodMatch `json:"method,omitempty"`
}

// TailnetDeviceMatch matches based on tailnet device characteristics
type TailnetDeviceMatch struct {
	// Tags matches devices that have any of the specified tags.
	// +optional
	Tags []string `json:"tags,omitempty"`

	// Hostnames matches devices by hostname patterns.
	// Supports wildcards (*) for pattern matching.
	// +optional
	Hostnames []string `json:"hostnames,omitempty"`

	// IPs matches devices by their tailnet IP addresses.
	// Supports CIDR notation for network ranges.
	// +optional
	IPs []string `json:"ips,omitempty"`

	// Users matches devices owned by specific users.
	// +optional
	Users []string `json:"users,omitempty"`
}

// PathMatch matches based on request path
type PathMatch struct {
	// Type specifies how to match the path.
	// +kubebuilder:validation:Enum=Exact;PathPrefix;RegularExpression
	Type PathMatchType `json:"type"`

	// Value is the path value to match against.
	Value string `json:"value"`
}

// PathMatchType defines the type of path matching
type PathMatchType string

const (
	// PathMatchExact matches the exact path
	PathMatchExact PathMatchType = "Exact"

	// PathMatchPathPrefix matches a path prefix
	PathMatchPathPrefix PathMatchType = "PathPrefix"

	// PathMatchRegularExpression matches using regular expressions
	PathMatchRegularExpression PathMatchType = "RegularExpression"
)

// HeaderMatch matches based on request headers
type HeaderMatch struct {
	// Type specifies how to match the header.
	// +kubebuilder:validation:Enum=Exact;RegularExpression
	Type HeaderMatchType `json:"type"`

	// Name is the header name to match.
	Name string `json:"name"`

	// Value is the header value to match against.
	Value string `json:"value"`
}

// HeaderMatchType defines the type of header matching
type HeaderMatchType string

const (
	// HeaderMatchExact matches the exact header value
	HeaderMatchExact HeaderMatchType = "Exact"

	// HeaderMatchRegularExpression matches using regular expressions
	HeaderMatchRegularExpression HeaderMatchType = "RegularExpression"
)

// MethodMatch matches based on HTTP method
type MethodMatch struct {
	// Methods are the HTTP methods to match.
	// +kubebuilder:validation:Enum=GET;HEAD;POST;PUT;DELETE;CONNECT;OPTIONS;TRACE;PATCH
	Methods []HTTPMethod `json:"methods"`
}

// HTTPMethod represents an HTTP method
type HTTPMethod string

const (
	HTTPMethodGet     HTTPMethod = "GET"
	HTTPMethodHead    HTTPMethod = "HEAD"
	HTTPMethodPost    HTTPMethod = "POST"
	HTTPMethodPut     HTTPMethod = "PUT"
	HTTPMethodDelete  HTTPMethod = "DELETE"
	HTTPMethodConnect HTTPMethod = "CONNECT"
	HTTPMethodOptions HTTPMethod = "OPTIONS"
	HTTPMethodTrace   HTTPMethod = "TRACE"
	HTTPMethodPatch   HTTPMethod = "PATCH"
)

// PolicyAction defines an action to take when a rule matches
type PolicyAction struct {
	// Type specifies the type of action to take.
	// +kubebuilder:validation:Enum=Route;Redirect;Deny;RequestHeaderModifier;ResponseHeaderModifier
	Type PolicyActionType `json:"type"`

	// Route configures routing to a specific backend.
	// +optional
	Route *RouteAction `json:"route,omitempty"`

	// Redirect configures HTTP redirect responses.
	// +optional
	Redirect *RedirectAction `json:"redirect,omitempty"`

	// RequestHeaderModifier configures request header modifications.
	// +optional
	RequestHeaderModifier *HeaderModifier `json:"requestHeaderModifier,omitempty"`

	// ResponseHeaderModifier configures response header modifications.
	// +optional
	ResponseHeaderModifier *HeaderModifier `json:"responseHeaderModifier,omitempty"`
}

// PolicyActionType defines the type of policy action
type PolicyActionType string

const (
	// PolicyActionRoute routes traffic to a backend
	PolicyActionRoute PolicyActionType = "Route"

	// PolicyActionRedirect sends HTTP redirects
	PolicyActionRedirect PolicyActionType = "Redirect"

	// PolicyActionDeny denies the request
	PolicyActionDeny PolicyActionType = "Deny"

	// PolicyActionRequestHeaderModifier modifies request headers
	PolicyActionRequestHeaderModifier PolicyActionType = "RequestHeaderModifier"

	// PolicyActionResponseHeaderModifier modifies response headers
	PolicyActionResponseHeaderModifier PolicyActionType = "ResponseHeaderModifier"
)

// RouteAction configures routing to a backend
type RouteAction struct {
	// BackendRefs are the backends to route traffic to.
	BackendRefs []BackendRef `json:"backendRefs"`

	// Timeout configures timeouts for this route.
	// +optional
	Timeout *RouteTimeout `json:"timeout,omitempty"`
}

// BackendRef references a backend service
type BackendRef struct {
	// BackendObjectReference references a backend object.
	gwapiv1.BackendObjectReference `json:",inline"`

	// Weight configures the weight for load balancing.
	// +optional
	Weight *int32 `json:"weight,omitempty"`
}

// RouteTimeout configures route timeouts
type RouteTimeout struct {
	// Request is the timeout for the entire request.
	// +optional
	Request *metav1.Duration `json:"request,omitempty"`

	// BackendRequest is the timeout for backend requests.
	// +optional
	BackendRequest *metav1.Duration `json:"backendRequest,omitempty"`
}

// RedirectAction configures HTTP redirect responses
type RedirectAction struct {
	// Scheme is the scheme to redirect to.
	// +optional
	Scheme *string `json:"scheme,omitempty"`

	// Hostname is the hostname to redirect to.
	// +optional
	Hostname *string `json:"hostname,omitempty"`

	// Path configures path replacement for redirects.
	// +optional
	Path *PathReplace `json:"path,omitempty"`

	// Port is the port to redirect to.
	// +optional
	Port *gwapiv1.PortNumber `json:"port,omitempty"`

	// StatusCode is the HTTP status code for the redirect.
	// +kubebuilder:validation:Enum=301;302
	// +optional
	StatusCode *int32 `json:"statusCode,omitempty"`
}

// PathReplace configures path replacement
type PathReplace struct {
	// Type specifies how to replace the path.
	// +kubebuilder:validation:Enum=ReplaceFullPath;ReplacePrefixMatch
	Type PathReplaceType `json:"type"`

	// Value is the replacement value.
	Value string `json:"value"`
}

// PathReplaceType defines the type of path replacement
type PathReplaceType string

const (
	// PathReplaceFullPath replaces the entire path
	PathReplaceFullPath PathReplaceType = "ReplaceFullPath"

	// PathReplacePrefixMatch replaces the matching prefix
	PathReplacePrefixMatch PathReplaceType = "ReplacePrefixMatch"
)

// HeaderModifier configures header modifications
type HeaderModifier struct {
	// Set configures headers to set, overwriting existing values.
	// +optional
	Set []Header `json:"set,omitempty"`

	// Add configures headers to add, preserving existing values.
	// +optional
	Add []Header `json:"add,omitempty"`

	// Remove configures headers to remove.
	// +optional
	Remove []string `json:"remove,omitempty"`
}

// Header represents an HTTP header
type Header struct {
	// Name is the header name.
	Name string `json:"name"`

	// Value is the header value.
	Value string `json:"value"`
}

// TailscaleRoutePolicyStatus defines the observed state of TailscaleRoutePolicy
type TailscaleRoutePolicyStatus struct {
	// Conditions represent the current state of the policy.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// AppliedTo indicates which targets this policy has been successfully applied to.
	// +optional
	AppliedTo []PolicyTargetStatus `json:"appliedTo,omitempty"`

	// RuleStatus provides detailed status for each policy rule
	// +optional
	RuleStatus []RuleStatus `json:"ruleStatus,omitempty"`

	// ValidationErrors tracks policy validation errors
	// +optional
	ValidationErrors []DetailedError `json:"validationErrors,omitempty"`

	// OperationalMetrics provides performance and operational insights
	// +optional
	OperationalMetrics *OperationalMetrics `json:"operationalMetrics,omitempty"`

	// PolicyConflicts tracks conflicts with other policies
	// +optional
	PolicyConflicts []PolicyConflict `json:"policyConflicts,omitempty"`
}

// PolicyTargetStatus represents the status of policy application to a target
type PolicyTargetStatus struct {
	// TargetRef is the reference to the target.
	TargetRef LocalPolicyTargetReference `json:"targetRef"`

	// Applied indicates if the policy was successfully applied.
	Applied bool `json:"applied"`

	// Message contains additional information about the application status.
	// +optional
	Message *string `json:"message,omitempty"`

	// Status indicates the current application state
	// +kubebuilder:validation:Enum=Applied;Pending;Failed;Conflicted;NotApplicable
	// +optional
	Status string `json:"status,omitempty"`

	// LastApplied timestamp when the policy was last applied
	// +optional
	LastApplied *metav1.Time `json:"lastApplied,omitempty"`

	// AppliedRules number of rules successfully applied to this target
	// +optional
	AppliedRules *int32 `json:"appliedRules,omitempty"`

	// TotalRules total number of rules in the policy
	// +optional
	TotalRules *int32 `json:"totalRules,omitempty"`

	// Errors specific errors for this target
	// +optional
	Errors []DetailedError `json:"errors,omitempty"`

	// Version tracks the resource version of the target
	// +optional
	Version string `json:"version,omitempty"`
}

// RuleStatus provides detailed status for a policy rule
type RuleStatus struct {
	// RuleName identifies the rule (uses rule name or index)
	RuleName string `json:"ruleName"`

	// Applied indicates if the rule was successfully applied
	Applied bool `json:"applied"`

	// MatchCount number of times this rule has matched
	MatchCount int64 `json:"matchCount"`

	// LastMatch timestamp when this rule last matched
	// +optional
	LastMatch *metav1.Time `json:"lastMatch,omitempty"`

	// ValidationStatus indicates if the rule passed validation
	// +kubebuilder:validation:Enum=Valid;Invalid;Warning
	ValidationStatus string `json:"validationStatus"`

	// ValidationErrors specific validation errors for this rule
	// +optional
	ValidationErrors []DetailedError `json:"validationErrors,omitempty"`

	// ActionResults status of each action in this rule
	// +optional
	ActionResults []ActionResult `json:"actionResults,omitempty"`

	// PerformanceMetrics performance metrics for this rule
	// +optional
	PerformanceMetrics *RulePerformanceMetrics `json:"performanceMetrics,omitempty"`
}

// ActionResult provides status for a policy action
type ActionResult struct {
	// ActionType the type of action
	ActionType PolicyActionType `json:"actionType"`

	// Success indicates if the action was successful
	Success bool `json:"success"`

	// ExecutionCount number of times this action has executed
	ExecutionCount int64 `json:"executionCount"`

	// LastExecution timestamp when this action last executed
	// +optional
	LastExecution *metav1.Time `json:"lastExecution,omitempty"`

	// ErrorMessage error message if action failed
	// +optional
	ErrorMessage string `json:"errorMessage,omitempty"`

	// AverageExecutionTime average time to execute this action
	// +optional
	AverageExecutionTime *metav1.Duration `json:"averageExecutionTime,omitempty"`
}

// RulePerformanceMetrics provides performance metrics for a rule
type RulePerformanceMetrics struct {
	// AverageEvaluationTime average time to evaluate this rule
	// +optional
	AverageEvaluationTime *metav1.Duration `json:"averageEvaluationTime,omitempty"`

	// LastEvaluationTime time for the last evaluation
	// +optional
	LastEvaluationTime *metav1.Duration `json:"lastEvaluationTime,omitempty"`

	// EvaluationCount total number of rule evaluations
	EvaluationCount int64 `json:"evaluationCount"`

	// MatchRate percentage of evaluations that resulted in matches as a string (e.g., "75.5%")
	// +optional
	MatchRate *string `json:"matchRate,omitempty"`
}

// PolicyConflict represents a conflict with another policy
type PolicyConflict struct {
	// ConflictingPolicy reference to the conflicting policy
	ConflictingPolicy LocalPolicyTargetReference `json:"conflictingPolicy"`

	// ConflictType describes the type of conflict
	// +kubebuilder:validation:Enum=OverlappingRule;ConflictingAction;PriorityConflict;TargetConflict
	ConflictType string `json:"conflictType"`

	// ConflictDescription human-readable description of the conflict
	ConflictDescription string `json:"conflictDescription"`

	// Resolution describes how the conflict was resolved
	// +kubebuilder:validation:Enum=Ignored;Overridden;Merged;Failed
	// +optional
	Resolution string `json:"resolution,omitempty"`

	// DetectedAt timestamp when the conflict was detected
	DetectedAt metav1.Time `json:"detectedAt"`

	// Severity indicates the severity of the conflict
	// +kubebuilder:validation:Enum=Critical;Warning;Info
	Severity string `json:"severity"`
}

func init() {
	SchemeBuilder.Register(&TailscaleRoutePolicy{}, &TailscaleRoutePolicyList{})
}
