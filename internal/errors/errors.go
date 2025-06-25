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

package errors

import (
	"fmt"
	"net/http"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	gatewayv1alpha1 "github.com/rajsinghtech/tailscale-gateway/api/v1alpha1"
)

// ErrorBuilder provides a fluent interface for building detailed errors
type ErrorBuilder struct {
	error *gatewayv1alpha1.DetailedError
}

// NewErrorBuilder creates a new error builder with required fields
func NewErrorBuilder(code, message, component string) *ErrorBuilder {
	return &ErrorBuilder{
		error: &gatewayv1alpha1.DetailedError{
			Code:      code,
			Message:   message,
			Component: component,
			Timestamp: metav1.Now(),
			Severity:  gatewayv1alpha1.SeverityMedium,
			Context:   make(map[string]string),
		},
	}
}

// WithSeverity sets the error severity
func (eb *ErrorBuilder) WithSeverity(severity string) *ErrorBuilder {
	eb.error.Severity = severity
	return eb
}

// WithHTTPStatus sets the HTTP status code
func (eb *ErrorBuilder) WithHTTPStatus(statusCode int) *ErrorBuilder {
	eb.error.HTTPStatusCode = &[]int32{int32(statusCode)}[0]
	return eb
}

// WithContext adds context key-value pairs
func (eb *ErrorBuilder) WithContext(key, value string) *ErrorBuilder {
	if eb.error.Context == nil {
		eb.error.Context = make(map[string]string)
	}
	eb.error.Context[key] = value
	return eb
}

// WithContextMap adds multiple context key-value pairs
func (eb *ErrorBuilder) WithContextMap(context map[string]string) *ErrorBuilder {
	if eb.error.Context == nil {
		eb.error.Context = make(map[string]string)
	}
	for k, v := range context {
		eb.error.Context[k] = v
	}
	return eb
}

// WithResourceRef sets the resource reference
func (eb *ErrorBuilder) WithResourceRef(name, namespace, kind string) *ErrorBuilder {
	eb.error.ResourceRef = &gatewayv1alpha1.LocalPolicyTargetReference{
		Name:      gwapiv1.ObjectName(name),
		Namespace: (*gwapiv1.Namespace)(&namespace),
		Kind:      gwapiv1.Kind(kind),
	}
	return eb
}

// WithResolutionHint sets a hint for resolving the error
func (eb *ErrorBuilder) WithResolutionHint(hint string) *ErrorBuilder {
	eb.error.ResolutionHint = hint
	return eb
}

// WithRetryInfo sets retry information
func (eb *ErrorBuilder) WithRetryInfo(retries int32, lastRetry *metav1.Time) *ErrorBuilder {
	eb.error.Retries = retries
	eb.error.LastRetry = lastRetry
	return eb
}

// Build returns the constructed DetailedError
func (eb *ErrorBuilder) Build() *gatewayv1alpha1.DetailedError {
	return eb.error
}

// BuildError returns the constructed DetailedError as a standard error interface
func (eb *ErrorBuilder) BuildError() error {
	return &DetailedErrorWrapper{eb.error}
}

// DetailedErrorWrapper wraps a DetailedError to implement the error interface
type DetailedErrorWrapper struct {
	*gatewayv1alpha1.DetailedError
}

// Error implements the error interface
func (dew *DetailedErrorWrapper) Error() string {
	var parts []string

	if dew.Component != "" {
		parts = append(parts, fmt.Sprintf("[%s]", dew.Component))
	}

	if dew.Code != "" {
		parts = append(parts, fmt.Sprintf("(%s)", dew.Code))
	}

	parts = append(parts, dew.Message)

	if len(dew.Context) > 0 {
		var contextParts []string
		for k, v := range dew.Context {
			contextParts = append(contextParts, fmt.Sprintf("%s=%s", k, v))
		}
		parts = append(parts, fmt.Sprintf("{%s}", strings.Join(contextParts, ", ")))
	}

	return strings.Join(parts, " ")
}

// GetDetailedError extracts the DetailedError from the wrapper
func (dew *DetailedErrorWrapper) GetDetailedError() *gatewayv1alpha1.DetailedError {
	return dew.DetailedError
}

// Predefined error builders for common scenarios

// NewValidationError creates a validation error
func NewValidationError(field, value, reason string) *ErrorBuilder {
	return NewErrorBuilder(
		gatewayv1alpha1.ErrorCodeValidation,
		fmt.Sprintf("validation failed for field '%s': %s", field, reason),
		gatewayv1alpha1.ComponentValidation,
	).WithContext("field", field).
		WithContext("value", value).
		WithSeverity(gatewayv1alpha1.SeverityHigh).
		WithResolutionHint(fmt.Sprintf("Please check the value for field '%s' and ensure it meets the validation requirements", field))
}

// NewAPIError creates an API-related error
func NewAPIError(endpoint string, statusCode int, message string) *ErrorBuilder {
	return NewErrorBuilder(
		getAPIErrorCode(statusCode),
		fmt.Sprintf("API request to %s failed: %s", endpoint, message),
		gatewayv1alpha1.ComponentTailscaleAPI,
	).WithHTTPStatus(statusCode).
		WithContext("endpoint", endpoint).
		WithSeverity(getAPISeverity(statusCode)).
		WithResolutionHint(getAPIResolutionHint(statusCode))
}

// NewResourceNotFoundError creates a resource not found error
func NewResourceNotFoundError(resourceType, name, namespace string) *ErrorBuilder {
	return NewErrorBuilder(
		gatewayv1alpha1.ErrorCodeResourceNotFound,
		fmt.Sprintf("%s '%s' not found in namespace '%s'", resourceType, name, namespace),
		gatewayv1alpha1.ComponentController,
	).WithResourceRef(name, namespace, resourceType).
		WithSeverity(gatewayv1alpha1.SeverityHigh).
		WithResolutionHint(fmt.Sprintf("Ensure the %s resource exists and is properly configured", resourceType))
}

// NewDependencyError creates a dependency-related error
func NewDependencyError(dependentResource, requiredResource, reason string) *ErrorBuilder {
	return NewErrorBuilder(
		gatewayv1alpha1.ErrorCodeDependency,
		fmt.Sprintf("dependency error: %s requires %s (%s)", dependentResource, requiredResource, reason),
		gatewayv1alpha1.ComponentController,
	).WithContext("dependent_resource", dependentResource).
		WithContext("required_resource", requiredResource).
		WithSeverity(gatewayv1alpha1.SeverityHigh).
		WithResolutionHint(fmt.Sprintf("Ensure %s is properly configured and accessible", requiredResource))
}

// NewTimeoutError creates a timeout error
func NewTimeoutError(operation string, timeout string) *ErrorBuilder {
	return NewErrorBuilder(
		gatewayv1alpha1.ErrorCodeTimeout,
		fmt.Sprintf("operation '%s' timed out after %s", operation, timeout),
		gatewayv1alpha1.ComponentController,
	).WithContext("operation", operation).
		WithContext("timeout", timeout).
		WithSeverity(gatewayv1alpha1.SeverityMedium).
		WithResolutionHint("Check network connectivity and consider increasing timeout values")
}

// NewConfigurationError creates a configuration error
func NewConfigurationError(configField, issue, suggestion string) *ErrorBuilder {
	return NewErrorBuilder(
		gatewayv1alpha1.ErrorCodeConfiguration,
		fmt.Sprintf("configuration error in '%s': %s", configField, issue),
		gatewayv1alpha1.ComponentController,
	).WithContext("config_field", configField).
		WithSeverity(gatewayv1alpha1.SeverityHigh).
		WithResolutionHint(suggestion)
}

// NewServiceDiscoveryError creates a service discovery error
func NewServiceDiscoveryError(serviceName, tailnet, reason string) *ErrorBuilder {
	return NewErrorBuilder(
		gatewayv1alpha1.ErrorCodeServiceDiscovery,
		fmt.Sprintf("service discovery failed for '%s' in tailnet '%s': %s", serviceName, tailnet, reason),
		gatewayv1alpha1.ComponentServiceDiscovery,
	).WithContext("service_name", serviceName).
		WithContext("tailnet", tailnet).
		WithSeverity(gatewayv1alpha1.SeverityMedium).
		WithResolutionHint("Check Tailscale network connectivity and service configuration")
}

// NewReconciliationError creates a reconciliation error
func NewReconciliationError(resourceName, operation, reason string) *ErrorBuilder {
	return NewErrorBuilder(
		gatewayv1alpha1.ErrorCodeReconciliation,
		fmt.Sprintf("reconciliation failed for '%s' during %s: %s", resourceName, operation, reason),
		gatewayv1alpha1.ComponentController,
	).WithContext("resource_name", resourceName).
		WithContext("operation", operation).
		WithSeverity(gatewayv1alpha1.SeverityHigh).
		WithResolutionHint("Check resource status and resolve any dependency issues")
}

// Wrap wraps an existing error with additional context
func Wrap(err error, code, component string) *ErrorBuilder {
	message := err.Error()
	if dew, ok := err.(*DetailedErrorWrapper); ok {
		// If it's already a detailed error, preserve some context
		existingError := dew.GetDetailedError()
		return NewErrorBuilder(code, message, component).
			WithContextMap(existingError.Context).
			WithSeverity(existingError.Severity)
	}

	return NewErrorBuilder(code, message, component)
}

// WrapWithContext wraps an error with additional context
func WrapWithContext(err error, code, component string, context map[string]string) error {
	return Wrap(err, code, component).WithContextMap(context).BuildError()
}

// Helper functions

func getAPIErrorCode(statusCode int) string {
	switch statusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return gatewayv1alpha1.ErrorCodeAuthentication
	case http.StatusNotFound:
		return gatewayv1alpha1.ErrorCodeResourceNotFound
	case http.StatusTooManyRequests:
		return gatewayv1alpha1.ErrorCodeRateLimit
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		return gatewayv1alpha1.ErrorCodeTimeout
	default:
		return gatewayv1alpha1.ErrorCodeAPIConnection
	}
}

func getAPISeverity(statusCode int) string {
	switch statusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return gatewayv1alpha1.SeverityCritical
	case http.StatusNotFound:
		return gatewayv1alpha1.SeverityHigh
	case http.StatusTooManyRequests:
		return gatewayv1alpha1.SeverityMedium
	default:
		return gatewayv1alpha1.SeverityHigh
	}
}

func getAPIResolutionHint(statusCode int) string {
	switch statusCode {
	case http.StatusUnauthorized:
		return "Check API credentials and ensure they are valid"
	case http.StatusForbidden:
		return "Ensure the API credentials have sufficient permissions"
	case http.StatusNotFound:
		return "Verify the API endpoint exists and the resource is available"
	case http.StatusTooManyRequests:
		return "Reduce API call frequency or implement exponential backoff"
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		return "Check network connectivity and API service availability"
	default:
		return "Check API service status and network connectivity"
	}
}

// IsErrorCode checks if an error has a specific error code
func IsErrorCode(err error, code string) bool {
	if dew, ok := err.(*DetailedErrorWrapper); ok {
		return dew.Code == code
	}
	return false
}

// IsComponent checks if an error originated from a specific component
func IsComponent(err error, component string) bool {
	if dew, ok := err.(*DetailedErrorWrapper); ok {
		return dew.Component == component
	}
	return false
}

// GetErrorContext extracts context from a detailed error
func GetErrorContext(err error) map[string]string {
	if dew, ok := err.(*DetailedErrorWrapper); ok {
		return dew.Context
	}
	return nil
}

// ResourceErrorRecorder provides a convenient way to record errors on resources
type ResourceErrorRecorder struct {
	maxErrors int
}

// NewResourceErrorRecorder creates a new error recorder
func NewResourceErrorRecorder(maxErrors int) *ResourceErrorRecorder {
	return &ResourceErrorRecorder{maxErrors: maxErrors}
}

// RecordError records an error on a resource status
func (rer *ResourceErrorRecorder) RecordError(status interface{}, err *gatewayv1alpha1.DetailedError) {
	// This would need to be implemented based on the specific status type
	// For now, we provide the interface for recording errors
	// Implementation would vary based on whether it's TailscaleGateway, TailscaleEndpoints, etc.
}

// AddResourceContext adds resource context to an error builder
func AddResourceContext(eb *ErrorBuilder, obj metav1.Object) *ErrorBuilder {
	return eb.WithContext("resource_name", obj.GetName()).
		WithContext("resource_namespace", obj.GetNamespace()).
		WithContext("resource_uid", string(obj.GetUID()))
}

// AddNamespacedNameContext adds NamespacedName context to an error builder
func AddNamespacedNameContext(eb *ErrorBuilder, nn types.NamespacedName) *ErrorBuilder {
	return eb.WithContext("name", nn.Name).WithContext("namespace", nn.Namespace)
}
