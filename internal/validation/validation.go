// Copyright (c) Tailscale Gateway Authors
// SPDX-License-Identifier: BSD-3-Clause

package validation

import (
	"fmt"
	"net"
	"regexp"
	"strings"
	"time"

	gatewayv1alpha1 "github.com/rajsinghtech/tailscale-gateway/api/v1alpha1"
	"github.com/rajsinghtech/tailscale-gateway/internal/errors"
	"github.com/rajsinghtech/tailscale-gateway/internal/tailscale"
)

var (
	// validDNSNameRegex validates DNS names and FQDNs
	validDNSNameRegex = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$`)

	// validExternalTargetRegex validates external target format (hostname:port or service.namespace.svc.cluster.local:port)
	validExternalTargetRegex = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]*[a-z0-9])?:[0-9]+$`)
)

// ValidateTailscaleEndpoints validates a TailscaleEndpoints resource
func ValidateTailscaleEndpoints(endpoints *gatewayv1alpha1.TailscaleEndpoints) error {
	if endpoints == nil {
		return errors.NewValidationError("endpoints", "nil", "TailscaleEndpoints cannot be nil").BuildError()
	}

	// Validate basic spec requirements
	if err := validateTailscaleEndpointsSpec(&endpoints.Spec); err != nil {
		return err
	}

	// Validate individual endpoints
	for i, endpoint := range endpoints.Spec.Endpoints {
		if err := validateTailscaleEndpoint(&endpoint, i); err != nil {
			return err
		}
	}

	// Validate auto-discovery configuration if present
	if endpoints.Spec.AutoDiscovery != nil {
		if err := validateEndpointAutoDiscovery(endpoints.Spec.AutoDiscovery); err != nil {
			return err
		}
	}

	return nil
}

// validateTailscaleEndpointsSpec validates the spec section
func validateTailscaleEndpointsSpec(spec *gatewayv1alpha1.TailscaleEndpointsSpec) error {
	if spec == nil {
		return errors.NewValidationError("spec", "nil", "spec cannot be nil").BuildError()
	}

	// Validate tailnet name
	if spec.Tailnet == "" {
		return errors.NewValidationError("tailnet", "empty", "tailnet name cannot be empty").BuildError()
	}
	if !validDNSNameRegex.MatchString(spec.Tailnet) {
		return errors.NewValidationError("tailnet", spec.Tailnet, "tailnet name must be a valid DNS name").BuildError()
	}

	// Must have either static endpoints or auto-discovery
	if len(spec.Endpoints) == 0 && (spec.AutoDiscovery == nil || !spec.AutoDiscovery.Enabled) {
		return errors.NewValidationError("configuration", "empty", "must specify either static endpoints or enable auto-discovery").BuildError()
	}

	return nil
}

// validateTailscaleEndpoint validates an individual endpoint
func validateTailscaleEndpoint(endpoint *gatewayv1alpha1.TailscaleEndpoint, index int) error {
	if endpoint == nil {
		return errors.NewValidationError("endpoint", "nil", fmt.Sprintf("endpoint at index %d cannot be nil", index)).BuildError()
	}

	// Validate name
	if endpoint.Name == "" {
		return errors.NewValidationError("endpoint.name", "empty", fmt.Sprintf("endpoint name at index %d cannot be empty", index)).BuildError()
	}

	// Validate IP address
	if endpoint.TailscaleIP != "" {
		if net.ParseIP(endpoint.TailscaleIP) == nil {
			return errors.NewValidationError("endpoint.tailscaleIP", endpoint.TailscaleIP, fmt.Sprintf("invalid IP address at index %d", index)).BuildError()
		}
	}

	// Validate FQDN
	if endpoint.TailscaleFQDN != "" {
		if !validDNSNameRegex.MatchString(endpoint.TailscaleFQDN) {
			return errors.NewValidationError("endpoint.tailscaleFQDN", endpoint.TailscaleFQDN, fmt.Sprintf("invalid FQDN at index %d", index)).BuildError()
		}
	}

	// Must have either IP or FQDN
	if endpoint.TailscaleIP == "" && endpoint.TailscaleFQDN == "" {
		return errors.NewValidationError("endpoint.address", "missing", fmt.Sprintf("endpoint at index %d must have either TailscaleIP or TailscaleFQDN", index)).BuildError()
	}

	// Validate port
	if endpoint.Port <= 0 || endpoint.Port > 65535 {
		return errors.NewValidationError("endpoint.port", fmt.Sprintf("%d", endpoint.Port), fmt.Sprintf("port at index %d must be between 1 and 65535", index)).BuildError()
	}

	// Validate protocol
	validProtocols := map[string]bool{"HTTP": true, "HTTPS": true, "TCP": true, "UDP": true}
	if endpoint.Protocol != "" && !validProtocols[endpoint.Protocol] {
		return errors.NewValidationError("endpoint.protocol", endpoint.Protocol, fmt.Sprintf("invalid protocol at index %d, must be one of: HTTP, HTTPS, TCP, UDP", index)).BuildError()
	}

	// Validate external target format if present
	if endpoint.ExternalTarget != "" {
		if !validExternalTargetRegex.MatchString(endpoint.ExternalTarget) {
			return errors.NewValidationError("endpoint.externalTarget", endpoint.ExternalTarget, fmt.Sprintf("invalid external target format at index %d, must be hostname:port", index)).BuildError()
		}
	}

	// Validate weight
	if endpoint.Weight != nil && (*endpoint.Weight < 1 || *endpoint.Weight > 100) {
		return errors.NewValidationError("endpoint.weight", fmt.Sprintf("%d", *endpoint.Weight), fmt.Sprintf("weight at index %d must be between 1 and 100", index)).BuildError()
	}

	// Validate health check if present
	if endpoint.HealthCheck != nil {
		if err := validateEndpointHealthCheck(endpoint.HealthCheck, index); err != nil {
			return err
		}
	}

	return nil
}

// validateEndpointHealthCheck validates health check configuration
func validateEndpointHealthCheck(hc *gatewayv1alpha1.EndpointHealthCheck, endpointIndex int) error {
	if hc == nil {
		return nil
	}

	// Validate timeout if specified
	if hc.Timeout != nil {
		if hc.Timeout.Duration <= 0 {
			return errors.NewValidationError("healthCheck.timeout", hc.Timeout.Duration.String(), fmt.Sprintf("timeout must be positive at endpoint index %d", endpointIndex)).BuildError()
		}
		if hc.Timeout.Duration > 60*time.Second {
			return errors.NewValidationError("healthCheck.timeout", hc.Timeout.Duration.String(), fmt.Sprintf("timeout too long at endpoint index %d, maximum is 60s", endpointIndex)).BuildError()
		}
	}

	// Validate interval if specified
	if hc.Interval != nil {
		if hc.Interval.Duration <= 0 {
			return errors.NewValidationError("healthCheck.interval", hc.Interval.Duration.String(), fmt.Sprintf("interval must be positive at endpoint index %d", endpointIndex)).BuildError()
		}
		if hc.Interval.Duration < 5*time.Second {
			return errors.NewValidationError("healthCheck.interval", hc.Interval.Duration.String(), fmt.Sprintf("interval too short at endpoint index %d, minimum is 5s", endpointIndex)).BuildError()
		}
	}

	// Validate healthy threshold
	if hc.HealthyThreshold != nil && *hc.HealthyThreshold > 10 {
		return errors.NewValidationError("healthCheck.healthyThreshold", fmt.Sprintf("%d", *hc.HealthyThreshold), fmt.Sprintf("healthy threshold too high at endpoint index %d, maximum is 10", endpointIndex)).BuildError()
	}

	// Validate unhealthy threshold
	if hc.UnhealthyThreshold != nil && *hc.UnhealthyThreshold > 10 {
		return errors.NewValidationError("healthCheck.unhealthyThreshold", fmt.Sprintf("%d", *hc.UnhealthyThreshold), fmt.Sprintf("unhealthy threshold too high at endpoint index %d, maximum is 10", endpointIndex)).BuildError()
	}

	// Validate path for HTTP checks
	if hc.Path != "" && !strings.HasPrefix(hc.Path, "/") {
		return errors.NewValidationError("healthCheck.path", hc.Path, fmt.Sprintf("health check path must start with '/' at endpoint index %d", endpointIndex)).BuildError()
	}

	return nil
}

// validateEndpointAutoDiscovery validates auto-discovery configuration
func validateEndpointAutoDiscovery(ad *gatewayv1alpha1.EndpointAutoDiscovery) error {
	if ad == nil {
		return nil
	}

	// Validate sync interval
	if ad.SyncInterval != nil {
		if ad.SyncInterval.Duration <= 0 {
			return errors.NewValidationError("autoDiscovery.syncInterval", ad.SyncInterval.Duration.String(), "sync interval must be positive").BuildError()
		}
		if ad.SyncInterval.Duration < 10*time.Second {
			return errors.NewValidationError("autoDiscovery.syncInterval", ad.SyncInterval.Duration.String(), "sync interval too short, minimum is 10s").BuildError()
		}
	}

	// Validate service discovery configuration
	if ad.ServiceDiscovery != nil {
		if err := validateVIPServiceDiscoveryConfig(ad.ServiceDiscovery); err != nil {
			return err
		}
	}

	return nil
}

// validateVIPServiceDiscoveryConfig validates VIP service discovery configuration
func validateVIPServiceDiscoveryConfig(sd *gatewayv1alpha1.VIPServiceDiscoveryConfig) error {
	if sd == nil {
		return nil
	}

	// Validate service names if specified
	for i, serviceName := range sd.ServiceNames {
		if serviceName == "" {
			return errors.NewValidationError("serviceDiscovery.serviceNames", "empty", fmt.Sprintf("service name at index %d cannot be empty", i)).BuildError()
		}

		// Validate as a service name (may or may not have svc: prefix)
		var nameToValidate string
		if strings.HasPrefix(serviceName, "svc:") {
			nameToValidate = serviceName
		} else {
			nameToValidate = "svc:" + serviceName
		}

		svcName := tailscale.ServiceName(nameToValidate)
		if err := svcName.Validate(); err != nil {
			return errors.NewValidationError("serviceDiscovery.serviceNames", serviceName, fmt.Sprintf("invalid service name at index %d: %v", i, err)).BuildError()
		}
	}

	// Validate tags
	for i, tag := range sd.ServiceTags {
		if tag == "" {
			return errors.NewValidationError("serviceDiscovery.serviceTags", "empty", fmt.Sprintf("tag at index %d cannot be empty", i)).BuildError()
		}
	}

	return nil
}

// ValidateTailscaleGateway validates a TailscaleGateway resource
func ValidateTailscaleGateway(gateway *gatewayv1alpha1.TailscaleGateway) error {
	if gateway == nil {
		return errors.NewValidationError("gateway", "nil", "TailscaleGateway cannot be nil").BuildError()
	}

	// Validate gateway reference
	if gateway.Spec.GatewayRef.Name == "" {
		return errors.NewValidationError("gatewayRef.name", "empty", "gateway reference name cannot be empty").BuildError()
	}

	// Validate tailnet configurations
	if len(gateway.Spec.Tailnets) == 0 {
		return errors.NewValidationError("tailnets", "empty", "at least one tailnet configuration is required").BuildError()
	}

	for i, tailnet := range gateway.Spec.Tailnets {
		if err := validateTailnetConfig(&tailnet, i); err != nil {
			return err
		}
	}

	// Validate extension server configuration if present
	if gateway.Spec.ExtensionServer != nil {
		if err := validateExtensionServerConfig(gateway.Spec.ExtensionServer); err != nil {
			return err
		}
	}

	return nil
}

// validateTailnetConfig validates a tailnet configuration
func validateTailnetConfig(config *gatewayv1alpha1.TailnetConfig, index int) error {
	if config == nil {
		return errors.NewValidationError("tailnetConfig", "nil", fmt.Sprintf("tailnet config at index %d cannot be nil", index)).BuildError()
	}

	// Validate name
	if config.Name == "" {
		return errors.NewValidationError("tailnetConfig.name", "empty", fmt.Sprintf("tailnet name at index %d cannot be empty", index)).BuildError()
	}

	// Validate tailnet reference
	if config.TailscaleTailnetRef.Name == "" {
		return errors.NewValidationError("tailnetConfig.tailscaleTailnetRef.name", "empty", fmt.Sprintf("tailnet reference name at index %d cannot be empty", index)).BuildError()
	}

	return nil
}

// validateExtensionServerConfig validates extension server configuration
func validateExtensionServerConfig(config *gatewayv1alpha1.ExtensionServerConfig) error {
	if config == nil {
		return nil
	}

	// Validate replicas
	if config.Replicas <= 0 {
		return errors.NewValidationError("extensionServer.replicas", fmt.Sprintf("%d", config.Replicas), "replicas must be positive").BuildError()
	}

	// Validate port if specified
	if config.Port != nil && (*config.Port <= 0 || *config.Port > 65535) {
		return errors.NewValidationError("extensionServer.port", fmt.Sprintf("%d", *config.Port), "port must be between 1 and 65535").BuildError()
	}

	return nil
}

// ValidateServiceName is a convenience function to validate service names
func ValidateServiceName(serviceName string) error {
	if serviceName == "" {
		return errors.NewValidationError("serviceName", "empty", "service name cannot be empty").BuildError()
	}

	// Ensure it has the svc: prefix
	var nameToValidate string
	if strings.HasPrefix(serviceName, "svc:") {
		nameToValidate = serviceName
	} else {
		nameToValidate = "svc:" + serviceName
	}

	svcName := tailscale.ServiceName(nameToValidate)
	return svcName.Validate()
}

// ValidateExternalTarget validates external target format
func ValidateExternalTarget(target string) error {
	if target == "" {
		return errors.NewValidationError("externalTarget", "empty", "external target cannot be empty").BuildError()
	}

	if !validExternalTargetRegex.MatchString(target) {
		return errors.NewValidationError("externalTarget", target, "external target must be in format hostname:port").BuildError()
	}

	return nil
}
