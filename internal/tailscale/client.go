// Copyright (c) Tailscale Gateway Authors
// SPDX-License-Identifier: BSD-3-Clause

package tailscale

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/netip"
	"os"
	"regexp"
	"strings"

	"golang.org/x/oauth2/clientcredentials"
	tailscaleclient "tailscale.com/client/tailscale/v2"
)

// VIP Service types based on corp codebase implementation
type (
	// ServiceName represents a Tailscale service name (must start with "svc:")
	ServiceName string

	// VIPService represents a Tailscale VIP service for internal use
	VIPService struct {
		Name        ServiceName       `json:"name"`
		Tags        []string          `json:"tags,omitempty"`
		Comment     string            `json:"comment,omitempty"`
		Annotations map[string]string `json:"annotations,omitempty"`
		Ports       []string          `json:"ports,omitempty"`
		Addrs       []string          `json:"addrs,omitempty"` // String representation for compatibility
	}

	// vipServiceAPIResponse matches the actual API response format
	vipServiceAPIResponse struct {
		Name        ServiceName       `json:"name"`
		Addrs       []netip.Addr      `json:"addrs"` // API returns netip.Addr
		Comment     string            `json:"comment"`
		Annotations map[string]string `json:"annotations"`
		Ports       []string          `json:"ports"`
		Tags        []string          `json:"tags"`
	}

	// getAllVIPServicesAPIResponse wraps the list response
	getAllVIPServicesAPIResponse struct {
		VIPServices []vipServiceAPIResponse `json:"vipServices"`
	}

	// putVIPServiceRequest matches the API request format
	putVIPServiceRequest struct {
		Name        ServiceName       `json:"name"`
		Comment     string            `json:"comment,omitempty"`
		Annotations map[string]string `json:"annotations,omitempty"`
		Ports       []string          `json:"ports,omitempty"`
		Tags        []string          `json:"tags,omitempty"`
		Addrs       []string          `json:"addrs,omitempty"` // Required for updates
	}
)

const (
	// DefaultTailnet is used in API calls to indicate the default tailnet
	DefaultTailnet = "-"
	// DefaultAPIBaseURL is the default Tailscale API base URL
	DefaultAPIBaseURL = "https://api.tailscale.com"
)

var (
	// validServiceNameRegex validates the service name part after "svc:"
	validServiceNameRegex = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

	// reservedServiceNames that cannot be used
	reservedServiceNames = map[string]bool{
		"localhost":  true,
		"wpad":       true,
		"autoconfig": true,
		"isatap":     true,
		"teredo":     true,
		"autorouter": true,
	}
)

// Validate checks if the service name follows required format and rules
func (name ServiceName) Validate() error {
	nameStr := string(name)

	// Must start with "svc:" prefix
	if !strings.HasPrefix(nameStr, "svc:") {
		return fmt.Errorf("service name must start with 'svc:' prefix, got: %s", nameStr)
	}

	// Extract the actual service name
	baseName := strings.TrimPrefix(nameStr, "svc:")
	if baseName == "" {
		return fmt.Errorf("service name cannot be empty after 'svc:' prefix")
	}

	// Check length (DNS label limit)
	if len(baseName) > 63 {
		return fmt.Errorf("service name too long (max 63 characters): %s", baseName)
	}

	// Check for reserved names
	if reservedServiceNames[strings.ToLower(baseName)] {
		return fmt.Errorf("service name '%s' is reserved", baseName)
	}

	// Validate as DNS label
	if !validServiceNameRegex.MatchString(baseName) {
		return fmt.Errorf("service name must be a valid DNS label (lowercase letters, numbers, hyphens): %s", baseName)
	}

	return nil
}

// String returns the string representation
func (name ServiceName) String() string {
	return string(name)
}

// convertAPIResponseToVIPService converts API response to internal VIPService
func convertAPIResponseToVIPService(apiResp vipServiceAPIResponse) *VIPService {
	// Convert netip.Addr to strings for compatibility
	addrs := make([]string, len(apiResp.Addrs))
	for i, addr := range apiResp.Addrs {
		addrs[i] = addr.String()
	}

	return &VIPService{
		Name:        apiResp.Name,
		Tags:        apiResp.Tags,
		Comment:     apiResp.Comment,
		Annotations: apiResp.Annotations,
		Ports:       apiResp.Ports,
		Addrs:       addrs,
	}
}

// parseAPIError extracts error messages from API responses
func parseAPIError(resp *http.Response) error {
	var errBody map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&errBody); err != nil {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	if message, ok := errBody["message"].(string); ok {
		return fmt.Errorf("API error: %s", message)
	}

	if detail, ok := errBody["detail"].(string); ok {
		return fmt.Errorf("API error: %s", detail)
	}

	return fmt.Errorf("HTTP %d", resp.StatusCode)
}

// Client interface for Tailscale API operations
type Client interface {
	// Device operations
	Devices(ctx context.Context) ([]tailscaleclient.Device, error)
	// Auth key operations
	CreateKey(ctx context.Context, caps tailscaleclient.KeyCapabilities) (*tailscaleclient.Key, error)
	DeleteKey(ctx context.Context, id string) error
	// Tailnet metadata operations
	DiscoverTailnetInfo(ctx context.Context) (*TailnetMetadata, error)
	// VIP service operations
	GetVIPService(ctx context.Context, serviceName ServiceName) (*VIPService, error)
	GetVIPServices(ctx context.Context) ([]*VIPService, error)
	GetVIPServicesByTags(ctx context.Context, tags []string) ([]*VIPService, error)
	CreateOrUpdateVIPService(ctx context.Context, service *VIPService) error
	DeleteVIPService(ctx context.Context, serviceName ServiceName) error
	// Service validation
	ValidateVIPService(service *VIPService) error
}

// TailnetMetadata contains essential information about a tailnet
type TailnetMetadata struct {
	Name           string
	MagicDNSSuffix string
	Organization   string
}

// ClientConfig holds configuration for creating a Tailscale client
type ClientConfig struct {
	// Tailnet is the tailnet name or organization
	Tailnet string
	// APIBaseURL is the base URL for the Tailscale API
	APIBaseURL string
	// ClientID is the OAuth client ID
	ClientID string
	// ClientSecret is the OAuth client secret
	ClientSecret string
}

// clientImpl implements the Client interface
type clientImpl struct {
	*tailscaleclient.Client
	tailnet    string
	baseURL    string
	httpClient *http.Client
}

// NewClient creates a new Tailscale API client with OAuth authentication
func NewClient(ctx context.Context, config ClientConfig) (Client, error) {
	if config.Tailnet == "" {
		config.Tailnet = DefaultTailnet
	}
	if config.APIBaseURL == "" {
		config.APIBaseURL = DefaultAPIBaseURL
	}

	if config.ClientID == "" || config.ClientSecret == "" {
		return nil, fmt.Errorf("client ID and client secret are required")
	}

	// Set up OAuth2 client credentials flow
	credentials := clientcredentials.Config{
		ClientID:     config.ClientID,
		ClientSecret: config.ClientSecret,
		TokenURL:     config.APIBaseURL + "/api/v2/oauth/token",
	}

	// Create the Tailscale v2 client
	c := &tailscaleclient.Client{
		Tailnet:   config.Tailnet,
		UserAgent: "tailscale-gateway-operator",
		HTTP:      credentials.Client(ctx),
	}

	return &clientImpl{
		Client:     c,
		tailnet:    config.Tailnet,
		baseURL:    config.APIBaseURL,
		httpClient: credentials.Client(ctx),
	}, nil
}

// NewClientFromSecretFiles creates a client by reading credentials from files
// This follows the pattern used by the Tailscale k8s-operator
func NewClientFromSecretFiles(ctx context.Context, tailnet, apiBaseURL, clientIDPath, clientSecretPath string) (Client, error) {
	clientID, err := os.ReadFile(clientIDPath)
	if err != nil {
		return nil, fmt.Errorf("error reading client ID from %q: %w", clientIDPath, err)
	}
	clientSecret, err := os.ReadFile(clientSecretPath)
	if err != nil {
		return nil, fmt.Errorf("error reading client secret from %q: %w", clientSecretPath, err)
	}

	config := ClientConfig{
		Tailnet:      tailnet,
		APIBaseURL:   apiBaseURL,
		ClientID:     string(clientID),
		ClientSecret: string(clientSecret),
	}

	return NewClient(ctx, config)
}

// Devices returns all devices in the tailnet
func (c *clientImpl) Devices(ctx context.Context) ([]tailscaleclient.Device, error) {
	return c.Client.Devices().List(ctx)
}

// CreateKey creates a new auth key with the specified capabilities
func (c *clientImpl) CreateKey(ctx context.Context, caps tailscaleclient.KeyCapabilities) (*tailscaleclient.Key, error) {
	req := tailscaleclient.CreateKeyRequest{
		Capabilities: caps,
		Description:  "tailscale-gateway-operator",
	}
	return c.Client.Keys().CreateAuthKey(ctx, req)
}

// DeleteKey deletes an auth key by ID
func (c *clientImpl) DeleteKey(ctx context.Context, id string) error {
	return c.Client.Keys().Delete(ctx, id)
}

// DiscoverTailnetInfo gathers essential metadata about the tailnet
func (c *clientImpl) DiscoverTailnetInfo(ctx context.Context) (*TailnetMetadata, error) {
	// Get devices to extract the tailnet domain
	devices, err := c.Client.Devices().List(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list devices: %w", err)
	}

	if len(devices) == 0 {
		return nil, fmt.Errorf("no devices found in tailnet")
	}

	metadata := &TailnetMetadata{}

	// Extract MagicDNS domain from first device name
	if devices[0].Name != "" {
		parts := strings.SplitN(devices[0].Name, ".", 2)
		if len(parts) == 2 {
			metadata.Name = parts[1]           // e.g., "tail123abc.ts.net"
			metadata.MagicDNSSuffix = parts[1] // Same as name for most tailnets
		}
	}

	// Infer organization type from domain structure
	if metadata.Name != "" && strings.HasSuffix(metadata.Name, ".ts.net") {
		orgPart := strings.TrimSuffix(metadata.Name, ".ts.net")
		if strings.HasPrefix(orgPart, "tail") {
			metadata.Organization = "Personal" // Personal tailnets start with "tail"
		} else {
			metadata.Organization = "Organization" // Custom org domains
		}
	}

	return metadata, nil
}

// GetVIPService gets a VIP service by name
func (c *clientImpl) GetVIPService(ctx context.Context, serviceName ServiceName) (*VIPService, error) {
	// Validate service name
	if err := serviceName.Validate(); err != nil {
		return nil, fmt.Errorf("invalid service name: %w", err)
	}

	// Use real Tailscale VIP service API
	url := fmt.Sprintf("%s/api/v2/tailnet/%s/vip-services/%s", c.baseURL, c.tailnet, serviceName)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("error creating request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("error making request: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		// Parse the API response
		var apiResp vipServiceAPIResponse
		if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
			return nil, fmt.Errorf("error decoding response: %w", err)
		}

		return convertAPIResponseToVIPService(apiResp), nil

	case http.StatusNotFound:
		return nil, fmt.Errorf("VIP service %s not found", serviceName)

	case http.StatusForbidden:
		return nil, fmt.Errorf("VIP services feature not enabled or insufficient permissions")

	default:
		return nil, parseAPIError(resp)
	}
}

// GetVIPServices gets all VIP services in the tailnet
func (c *clientImpl) GetVIPServices(ctx context.Context) ([]*VIPService, error) {
	// Use real Tailscale VIP service API
	url := fmt.Sprintf("%s/api/v2/tailnet/%s/vip-services/", c.baseURL, c.tailnet)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("error creating request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("error making request: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		// Parse the API response
		var apiResp getAllVIPServicesAPIResponse
		if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
			return nil, fmt.Errorf("error decoding response: %w", err)
		}

		// Convert to internal format
		services := make([]*VIPService, len(apiResp.VIPServices))
		for i, svc := range apiResp.VIPServices {
			services[i] = convertAPIResponseToVIPService(svc)
		}

		return services, nil

	case http.StatusForbidden:
		return nil, fmt.Errorf("VIP services feature not enabled or insufficient permissions")

	default:
		return nil, parseAPIError(resp)
	}
}

// GetVIPServicesByTags gets VIP services filtered by tags
func (c *clientImpl) GetVIPServicesByTags(ctx context.Context, tags []string) ([]*VIPService, error) {
	// Get all services first, then filter by tags
	allServices, err := c.GetVIPServices(ctx)
	if err != nil {
		return nil, fmt.Errorf("error getting VIP services: %w", err)
	}

	if len(tags) == 0 {
		return allServices, nil
	}

	var filteredServices []*VIPService
	for _, service := range allServices {
		if hasAnyTag(service.Tags, tags) {
			filteredServices = append(filteredServices, service)
		}
	}

	return filteredServices, nil
}

// hasAnyTag checks if any of the service tags match the filter tags
func hasAnyTag(serviceTags []string, filterTags []string) bool {
	for _, serviceTag := range serviceTags {
		for _, filterTag := range filterTags {
			if serviceTag == filterTag {
				return true
			}
		}
	}
	return false
}

// CreateOrUpdateVIPService creates or updates a VIP service
func (c *clientImpl) CreateOrUpdateVIPService(ctx context.Context, service *VIPService) error {
	// Validate the service first
	if err := c.ValidateVIPService(service); err != nil {
		return fmt.Errorf("invalid VIP service: %w", err)
	}

	// Use real Tailscale VIP service API
	url := fmt.Sprintf("%s/api/v2/tailnet/%s/vip-services/%s", c.baseURL, c.tailnet, service.Name)

	// Use proper request format (include addrs for updates)
	request := putVIPServiceRequest{
		Name:        service.Name,
		Comment:     service.Comment,
		Annotations: service.Annotations,
		Ports:       service.Ports,
		Tags:        service.Tags,
		Addrs:       service.Addrs,
	}

	data, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("error marshaling request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "PUT", url, bytes.NewBuffer(data))
	if err != nil {
		return fmt.Errorf("error creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("error making request: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated:
		return nil

	case http.StatusBadRequest:
		return parseAPIError(resp)

	case http.StatusForbidden:
		return fmt.Errorf("VIP services feature not enabled or insufficient permissions")

	case http.StatusConflict:
		return fmt.Errorf("service name already in use by another service")

	default:
		return parseAPIError(resp)
	}
}

// DeleteVIPService deletes a VIP service by name
func (c *clientImpl) DeleteVIPService(ctx context.Context, serviceName ServiceName) error {
	// Validate service name
	if err := serviceName.Validate(); err != nil {
		return fmt.Errorf("invalid service name: %w", err)
	}

	// Use real Tailscale VIP service API
	url := fmt.Sprintf("%s/api/v2/tailnet/%s/vip-services/%s", c.baseURL, c.tailnet, serviceName)

	req, err := http.NewRequestWithContext(ctx, "DELETE", url, nil)
	if err != nil {
		return fmt.Errorf("error creating request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("error making request: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK, http.StatusNoContent:
		return nil

	case http.StatusNotFound:
		// Service already doesn't exist, consider it success
		return nil

	case http.StatusForbidden:
		return fmt.Errorf("VIP services feature not enabled or insufficient permissions")

	default:
		return parseAPIError(resp)
	}
}

// ValidateVIPService validates a VIP service before API operations
func (c *clientImpl) ValidateVIPService(service *VIPService) error {
	if service == nil {
		return fmt.Errorf("service cannot be nil")
	}

	// Validate service name
	if err := service.Name.Validate(); err != nil {
		return fmt.Errorf("invalid service name: %w", err)
	}

	// Validate ports format
	for _, port := range service.Ports {
		if err := validatePortSpec(port); err != nil {
			return fmt.Errorf("invalid port specification '%s': %w", port, err)
		}
	}

	// Validate tags format (basic validation)
	for _, tag := range service.Tags {
		if strings.TrimSpace(tag) == "" {
			return fmt.Errorf("empty tag not allowed")
		}
	}

	return nil
}

// validatePortSpec validates port specifications
func validatePortSpec(portSpec string) error {
	if portSpec == "do-not-validate" {
		return nil
	}

	// Basic validation for tcp:port or udp:port format
	if !strings.Contains(portSpec, ":") {
		return fmt.Errorf("port must be in format 'protocol:port' or 'protocol:startport-endport'")
	}

	parts := strings.SplitN(portSpec, ":", 2)
	protocol := strings.ToLower(parts[0])

	if protocol != "tcp" && protocol != "udp" {
		return fmt.Errorf("protocol must be 'tcp' or 'udp'")
	}

	// Port validation could be more detailed here
	// For now, just check it's not empty
	if strings.TrimSpace(parts[1]) == "" {
		return fmt.Errorf("port cannot be empty")
	}

	return nil
}

// MockClient provides a mock implementation for testing
type MockClient struct {
	DevicesFunc                  func(ctx context.Context) ([]tailscaleclient.Device, error)
	CreateKeyFunc                func(ctx context.Context, caps tailscaleclient.KeyCapabilities) (*tailscaleclient.Key, error)
	DeleteKeyFunc                func(ctx context.Context, id string) error
	DiscoverTailnetFunc          func(ctx context.Context) (*TailnetMetadata, error)
	GetVIPServiceFunc            func(ctx context.Context, serviceName ServiceName) (*VIPService, error)
	GetVIPServicesFunc           func(ctx context.Context) ([]*VIPService, error)
	GetVIPServicesByTagsFunc     func(ctx context.Context, tags []string) ([]*VIPService, error)
	CreateOrUpdateVIPServiceFunc func(ctx context.Context, service *VIPService) error
	DeleteVIPServiceFunc         func(ctx context.Context, serviceName ServiceName) error
	ValidateVIPServiceFunc       func(service *VIPService) error
}

func (m *MockClient) Devices(ctx context.Context) ([]tailscaleclient.Device, error) {
	if m.DevicesFunc != nil {
		return m.DevicesFunc(ctx)
	}
	return nil, fmt.Errorf("not implemented")
}

func (m *MockClient) CreateKey(ctx context.Context, caps tailscaleclient.KeyCapabilities) (*tailscaleclient.Key, error) {
	if m.CreateKeyFunc != nil {
		return m.CreateKeyFunc(ctx, caps)
	}
	return nil, fmt.Errorf("not implemented")
}

func (m *MockClient) DeleteKey(ctx context.Context, id string) error {
	if m.DeleteKeyFunc != nil {
		return m.DeleteKeyFunc(ctx, id)
	}
	return fmt.Errorf("not implemented")
}

func (m *MockClient) DiscoverTailnetInfo(ctx context.Context) (*TailnetMetadata, error) {
	if m.DiscoverTailnetFunc != nil {
		return m.DiscoverTailnetFunc(ctx)
	}
	return nil, fmt.Errorf("not implemented")
}

func (m *MockClient) GetVIPService(ctx context.Context, serviceName ServiceName) (*VIPService, error) {
	if m.GetVIPServiceFunc != nil {
		return m.GetVIPServiceFunc(ctx, serviceName)
	}
	return nil, fmt.Errorf("not implemented")
}

func (m *MockClient) GetVIPServices(ctx context.Context) ([]*VIPService, error) {
	if m.GetVIPServicesFunc != nil {
		return m.GetVIPServicesFunc(ctx)
	}
	return nil, fmt.Errorf("not implemented")
}

func (m *MockClient) GetVIPServicesByTags(ctx context.Context, tags []string) ([]*VIPService, error) {
	if m.GetVIPServicesByTagsFunc != nil {
		return m.GetVIPServicesByTagsFunc(ctx, tags)
	}
	return nil, fmt.Errorf("not implemented")
}

func (m *MockClient) CreateOrUpdateVIPService(ctx context.Context, service *VIPService) error {
	if m.CreateOrUpdateVIPServiceFunc != nil {
		return m.CreateOrUpdateVIPServiceFunc(ctx, service)
	}
	return fmt.Errorf("not implemented")
}

func (m *MockClient) DeleteVIPService(ctx context.Context, serviceName ServiceName) error {
	if m.DeleteVIPServiceFunc != nil {
		return m.DeleteVIPServiceFunc(ctx, serviceName)
	}
	return fmt.Errorf("not implemented")
}

func (m *MockClient) ValidateVIPService(service *VIPService) error {
	if m.ValidateVIPServiceFunc != nil {
		return m.ValidateVIPServiceFunc(service)
	}
	return fmt.Errorf("not implemented")
}
