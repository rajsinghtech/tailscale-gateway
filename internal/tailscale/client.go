// Copyright (c) Tailscale Gateway Authors
// SPDX-License-Identifier: BSD-3-Clause

package tailscale

import (
	"context"
	"fmt"
	"os"
	"strings"

	"golang.org/x/oauth2/clientcredentials"
	tailscaleclient "tailscale.com/client/tailscale/v2"
)

// VIP Service types - placeholder until official Tailscale API supports them
type (
	// ServiceName represents a Tailscale service name
	ServiceName string

	// VIPService represents a Tailscale VIP service
	VIPService struct {
		ID          string            `json:"id,omitempty"`
		Name        ServiceName       `json:"name"`
		Tags        []string          `json:"tags,omitempty"`
		Comment     string            `json:"comment,omitempty"`
		Annotations map[string]string `json:"annotations,omitempty"`
		Ports       []string          `json:"ports,omitempty"`
		Addrs       []string          `json:"addrs,omitempty"`
	}
)

const (
	// DefaultTailnet is used in API calls to indicate the default tailnet
	DefaultTailnet = "-"
	// DefaultAPIBaseURL is the default Tailscale API base URL
	DefaultAPIBaseURL = "https://api.tailscale.com"
)

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
	CreateOrUpdateVIPService(ctx context.Context, service *VIPService) error
	DeleteVIPService(ctx context.Context, serviceName ServiceName) error
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
	tailnet string
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

	return &clientImpl{Client: c, tailnet: config.Tailnet}, nil
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
	// TODO: Implement when Tailscale API supports VIP services
	// For now, return a mock implementation
	return nil, fmt.Errorf("VIP service %s not found", serviceName)
}

// GetVIPServices gets all VIP services in the tailnet
func (c *clientImpl) GetVIPServices(ctx context.Context) ([]*VIPService, error) {
	// TODO: Implement when Tailscale API supports VIP services
	// For now, return empty list
	return []*VIPService{}, nil
}

// CreateOrUpdateVIPService creates or updates a VIP service
func (c *clientImpl) CreateOrUpdateVIPService(ctx context.Context, service *VIPService) error {
	// TODO: Implement when Tailscale API supports VIP services
	// For now, simulate success
	fmt.Printf("Mock VIP service created/updated: %s\n", service.Name)
	return nil
}

// DeleteVIPService deletes a VIP service by name
func (c *clientImpl) DeleteVIPService(ctx context.Context, serviceName ServiceName) error {
	// TODO: Implement when Tailscale API supports VIP services
	// For now, simulate success
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
	CreateOrUpdateVIPServiceFunc func(ctx context.Context, service *VIPService) error
	DeleteVIPServiceFunc         func(ctx context.Context, serviceName ServiceName) error
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
