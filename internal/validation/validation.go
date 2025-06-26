// Copyright (c) Tailscale Gateway Authors
// SPDX-License-Identifier: BSD-3-Clause

package validation

import (
	"fmt"
	"net"
	"regexp"
	"strings"

	gatewayv1alpha1 "github.com/rajsinghtech/tailscale-gateway/api/v1alpha1"
	"github.com/rajsinghtech/tailscale-gateway/internal/errors"
)

var (
	// validDNSNameRegex validates DNS names and FQDNs
	validDNSNameRegex = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$`)

	// validServiceNameRegex validates VIP service names (svc:name format)
	validServiceNameRegex = regexp.MustCompile(`^svc:[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

	// validAddressRegex validates backend addresses (hostname:port format)
	validAddressRegex = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]*[a-z0-9])?:[0-9]+$`)
)

// ValidateTailscaleService validates a TailscaleService resource
func ValidateTailscaleService(service *gatewayv1alpha1.TailscaleService) error {
	if service == nil {
		return errors.NewValidationError("service", "nil", "TailscaleService cannot be nil").BuildError()
	}

	// Validate VIP service configuration
	if err := validateVIPServiceSpec(&service.Spec.VIPService); err != nil {
		return fmt.Errorf("invalid VIP service spec: %w", err)
	}

	// Validate backends
	for i, backend := range service.Spec.Backends {
		if err := validateBackendSpec(&backend); err != nil {
			return fmt.Errorf("invalid backend %d: %w", i, err)
		}
	}

	// Validate proxy configuration if present
	if service.Spec.Proxy != nil {
		if err := validateProxySpec(service.Spec.Proxy); err != nil {
			return fmt.Errorf("invalid proxy spec: %w", err)
		}

		// Require tailnet when proxy is specified
		if service.Spec.Tailnet == "" {
			return errors.NewValidationError("tailnet", "", "tailnet is required when proxy is specified").BuildError()
		}
	}

	return nil
}

// validateVIPServiceSpec validates VIP service configuration
func validateVIPServiceSpec(spec *gatewayv1alpha1.VIPServiceSpec) error {
	// Validate service name format if specified
	if spec.Name != "" && !validServiceNameRegex.MatchString(spec.Name) {
		return errors.NewValidationError("vipService.name", spec.Name, "must match format 'svc:name'").BuildError()
	}

	// Validate ports
	if len(spec.Ports) == 0 {
		return errors.NewValidationError("vipService.ports", "", "at least one port must be specified").BuildError()
	}

	for _, port := range spec.Ports {
		if !isValidPortFormat(port) {
			return errors.NewValidationError("vipService.ports", port, "must be in format 'protocol:port' (e.g., 'tcp:80')").BuildError()
		}
	}

	// Validate tags
	if len(spec.Tags) == 0 {
		return errors.NewValidationError("vipService.tags", "", "at least one tag must be specified").BuildError()
	}

	for _, tag := range spec.Tags {
		if !isValidTagFormat(tag) {
			return errors.NewValidationError("vipService.tags", tag, "must start with 'tag:' and contain valid characters").BuildError()
		}
	}

	return nil
}

// validateBackendSpec validates backend configuration
func validateBackendSpec(spec *gatewayv1alpha1.BackendSpec) error {
	switch spec.Type {
	case "kubernetes":
		if spec.Service == "" {
			return errors.NewValidationError("backend.service", "", "service is required for kubernetes backend").BuildError()
		}
		if !isValidKubernetesService(spec.Service) {
			return errors.NewValidationError("backend.service", spec.Service, "invalid kubernetes service format").BuildError()
		}

	case "external":
		if spec.Address == "" {
			return errors.NewValidationError("backend.address", "", "address is required for external backend").BuildError()
		}
		if !validAddressRegex.MatchString(spec.Address) {
			return errors.NewValidationError("backend.address", spec.Address, "must be in format 'host:port'").BuildError()
		}

	case "tailscale":
		if spec.TailscaleService == "" {
			return errors.NewValidationError("backend.tailscaleService", "", "tailscaleService is required for tailscale backend").BuildError()
		}

	default:
		return errors.NewValidationError("backend.type", spec.Type, "must be 'kubernetes', 'external', or 'tailscale'").BuildError()
	}

	// Validate weight
	if spec.Weight != nil && (*spec.Weight < 1 || *spec.Weight > 100) {
		return errors.NewValidationError("backend.weight", fmt.Sprintf("%d", *spec.Weight), "must be between 1 and 100").BuildError()
	}

	return nil
}

// validateProxySpec validates proxy configuration
func validateProxySpec(spec *gatewayv1alpha1.ProxySpec) error {
	// Validate replicas
	if spec.Replicas != nil && *spec.Replicas < 1 {
		return errors.NewValidationError("proxy.replicas", fmt.Sprintf("%d", *spec.Replicas), "must be at least 1").BuildError()
	}

	// Validate connection type
	if spec.ConnectionType != "" {
		validTypes := []string{"ingress", "egress", "bidirectional"}
		if !contains(validTypes, spec.ConnectionType) {
			return errors.NewValidationError("proxy.connectionType", spec.ConnectionType, "must be 'ingress', 'egress', or 'bidirectional'").BuildError()
		}
	}

	return nil
}

// Helper functions

func isValidPortFormat(port string) bool {
	parts := strings.Split(port, ":")
	if len(parts) != 2 {
		return false
	}

	protocol := strings.ToLower(parts[0])
	validProtocols := []string{"tcp", "udp"}
	if !contains(validProtocols, protocol) {
		return false
	}

	// Validate port number
	_, err := net.LookupPort(protocol, parts[1])
	return err == nil
}

func isValidTagFormat(tag string) bool {
	return strings.HasPrefix(tag, "tag:") && len(tag) > 4
}

func isValidKubernetesService(service string) bool {
	// Format: service-name.namespace.svc.cluster.local:port
	parts := strings.Split(service, ":")
	if len(parts) != 2 {
		return false
	}

	// Validate port
	_, err := net.LookupPort("tcp", parts[1])
	if err != nil {
		return false
	}

	// Validate service FQDN or simple name
	fqdn := parts[0]
	return validDNSNameRegex.MatchString(fqdn)
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}