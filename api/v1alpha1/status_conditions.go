// Copyright (c) Tailscale Gateway Authors
// SPDX-License-Identifier: BSD-3-Clause

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// StatusConditionHelper provides utility functions for managing status conditions
// consistently across all CRDs in the Tailscale Gateway Operator

// SetCondition sets or updates a condition in the conditions slice
func SetCondition(conditions *[]metav1.Condition, conditionType string, status metav1.ConditionStatus, reason, message string) {
	newCondition := metav1.Condition{
		Type:               conditionType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: metav1.Now(),
	}

	for i, condition := range *conditions {
		if condition.Type == conditionType {
			// Update existing condition if status or reason changed
			if condition.Status != status || condition.Reason != reason {
				(*conditions)[i] = newCondition
			} else {
				// Only update message and keep existing LastTransitionTime
				(*conditions)[i].Message = message
			}
			return
		}
	}

	// Add new condition
	*conditions = append(*conditions, newCondition)
}

// GetCondition returns a condition from the conditions slice
func GetCondition(conditions []metav1.Condition, conditionType string) *metav1.Condition {
	for _, condition := range conditions {
		if condition.Type == conditionType {
			return &condition
		}
	}
	return nil
}

// IsConditionTrue returns true if the condition is present and has status True
func IsConditionTrue(conditions []metav1.Condition, conditionType string) bool {
	condition := GetCondition(conditions, conditionType)
	return condition != nil && condition.Status == metav1.ConditionTrue
}

// IsConditionFalse returns true if the condition is present and has status False
func IsConditionFalse(conditions []metav1.Condition, conditionType string) bool {
	condition := GetCondition(conditions, conditionType)
	return condition != nil && condition.Status == metav1.ConditionFalse
}

// IsConditionUnknown returns true if the condition is present and has status Unknown
func IsConditionUnknown(conditions []metav1.Condition, conditionType string) bool {
	condition := GetCondition(conditions, conditionType)
	return condition != nil && condition.Status == metav1.ConditionUnknown
}

// SetReadyCondition sets the Ready condition based on overall resource status
func SetReadyCondition(conditions *[]metav1.Condition, ready bool, reason, message string) {
	status := metav1.ConditionTrue
	if !ready {
		status = metav1.ConditionFalse
	}
	SetCondition(conditions, ConditionReady, status, reason, message)
}

// SetProgressingCondition sets the Progressing condition
func SetProgressingCondition(conditions *[]metav1.Condition, progressing bool, reason, message string) {
	status := metav1.ConditionTrue
	if !progressing {
		status = metav1.ConditionFalse
	}
	SetCondition(conditions, ConditionProgressing, status, reason, message)
}

// SetDegradedCondition sets the Degraded condition
func SetDegradedCondition(conditions *[]metav1.Condition, degraded bool, reason, message string) {
	status := metav1.ConditionTrue
	if !degraded {
		status = metav1.ConditionFalse
	}
	SetCondition(conditions, ConditionDegraded, status, reason, message)
}

// SetAPIConnectionCondition sets the APIConnectionReady condition
func SetAPIConnectionCondition(conditions *[]metav1.Condition, connected bool, reason, message string) {
	status := metav1.ConditionTrue
	if !connected {
		status = metav1.ConditionFalse
	}
	SetCondition(conditions, ConditionAPIConnectionReady, status, reason, message)
}

// SetValidatedCondition sets the Validated condition
func SetValidatedCondition(conditions *[]metav1.Condition, valid bool, reason, message string) {
	status := metav1.ConditionTrue
	if !valid {
		status = metav1.ConditionFalse
	}
	SetCondition(conditions, ConditionValidated, status, reason, message)
}

// SetAuthenticatedCondition sets the Authenticated condition
func SetAuthenticatedCondition(conditions *[]metav1.Condition, authenticated bool, reason, message string) {
	status := metav1.ConditionTrue
	if !authenticated {
		status = metav1.ConditionFalse
	}
	SetCondition(conditions, ConditionAuthenticated, status, reason, message)
}

// ConditionSummary provides a summary of all conditions for debugging
type ConditionSummary struct {
	Ready              bool   `json:"ready"`
	Progressing        bool   `json:"progressing"`
	Degraded           bool   `json:"degraded"`
	APIConnectionReady bool   `json:"apiConnectionReady"`
	Validated          bool   `json:"validated"`
	Authenticated      bool   `json:"authenticated"`
	ResourcesReady     bool   `json:"resourcesReady"`
	DependenciesReady  bool   `json:"dependenciesReady"`
	LastTransitionTime string `json:"lastTransitionTime"`
	ErrorConditions    int    `json:"errorConditions"`
	UnknownConditions  int    `json:"unknownConditions"`
	TotalConditions    int    `json:"totalConditions"`
}

// GetConditionSummary returns a summary of all conditions
func GetConditionSummary(conditions []metav1.Condition) ConditionSummary {
	summary := ConditionSummary{
		TotalConditions: len(conditions),
	}

	var latestTransition metav1.Time

	for _, condition := range conditions {
		if condition.LastTransitionTime.After(latestTransition.Time) {
			latestTransition = condition.LastTransitionTime
		}

		switch condition.Status {
		case metav1.ConditionFalse:
			summary.ErrorConditions++
		case metav1.ConditionUnknown:
			summary.UnknownConditions++
		}

		switch condition.Type {
		case ConditionReady:
			summary.Ready = condition.Status == metav1.ConditionTrue
		case ConditionProgressing:
			summary.Progressing = condition.Status == metav1.ConditionTrue
		case ConditionDegraded:
			summary.Degraded = condition.Status == metav1.ConditionTrue
		case ConditionAPIConnectionReady:
			summary.APIConnectionReady = condition.Status == metav1.ConditionTrue
		case ConditionValidated:
			summary.Validated = condition.Status == metav1.ConditionTrue
		case ConditionAuthenticated:
			summary.Authenticated = condition.Status == metav1.ConditionTrue
		case ConditionResourcesReady:
			summary.ResourcesReady = condition.Status == metav1.ConditionTrue
		case ConditionDependenciesReady:
			summary.DependenciesReady = condition.Status == metav1.ConditionTrue
		}
	}

	if !latestTransition.IsZero() {
		summary.LastTransitionTime = latestTransition.Format("2006-01-02T15:04:05Z")
	}

	return summary
}

// ErrorHelper provides utility functions for managing DetailedError slices

// AddError adds a new DetailedError to the slice, maintaining a maximum number of errors
func AddError(errors *[]DetailedError, err DetailedError, maxErrors int) {
	// Set default severity if not provided
	if err.Severity == "" {
		err.Severity = SeverityMedium
	}

	// Add timestamp if not set
	if err.Timestamp.IsZero() {
		err.Timestamp = metav1.Now()
	}

	// Add new error at the beginning
	*errors = append([]DetailedError{err}, *errors...)

	// Trim to max errors, keeping the most recent
	if len(*errors) > maxErrors {
		*errors = (*errors)[:maxErrors]
	}
}

// GetErrorsByComponent returns errors for a specific component
func GetErrorsByComponent(errors []DetailedError, component string) []DetailedError {
	var componentErrors []DetailedError
	for _, err := range errors {
		if err.Component == component {
			componentErrors = append(componentErrors, err)
		}
	}
	return componentErrors
}

// GetErrorsBySeverity returns errors of a specific severity
func GetErrorsBySeverity(errors []DetailedError, severity string) []DetailedError {
	var severityErrors []DetailedError
	for _, err := range errors {
		if err.Severity == severity {
			severityErrors = append(severityErrors, err)
		}
	}
	return severityErrors
}

// HasCriticalErrors returns true if there are any critical errors
func HasCriticalErrors(errors []DetailedError) bool {
	for _, err := range errors {
		if err.Severity == SeverityCritical {
			return true
		}
	}
	return false
}

// GetErrorSummary returns a summary of errors by component and severity
func GetErrorSummary(errors []DetailedError) map[string]map[string]int {
	summary := make(map[string]map[string]int)

	for _, err := range errors {
		if summary[err.Component] == nil {
			summary[err.Component] = make(map[string]int)
		}
		summary[err.Component][err.Severity]++
	}

	return summary
}
