// Copyright (c) Tailscale Gateway Authors
// SPDX-License-Identifier: BSD-3-Clause

package tailscale

import (
	"context"
	"fmt"
	"os"
	"strings"

	"golang.org/x/oauth2/clientcredentials"
	"tailscale.com/client/tailscale/v2"
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
	Devices(ctx context.Context) ([]tailscale.Device, error)
	// Auth key operations
	CreateKey(ctx context.Context, caps tailscale.KeyCapabilities) (*tailscale.Key, error)
	DeleteKey(ctx context.Context, id string) error
	// Tailnet metadata operations
	DiscoverTailnetInfo(ctx context.Context) (*TailnetMetadata, error)
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
	*tailscale.Client
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
	c := &tailscale.Client{
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
func (c *clientImpl) Devices(ctx context.Context) ([]tailscale.Device, error) {
	return c.Client.Devices().List(ctx)
}

// CreateKey creates a new auth key with the specified capabilities
func (c *clientImpl) CreateKey(ctx context.Context, caps tailscale.KeyCapabilities) (*tailscale.Key, error) {
	req := tailscale.CreateKeyRequest{
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

// MockClient provides a mock implementation for testing
type MockClient struct {
	DevicesFunc         func(ctx context.Context) ([]tailscale.Device, error)
	CreateKeyFunc       func(ctx context.Context, caps tailscale.KeyCapabilities) (*tailscale.Key, error)
	DeleteKeyFunc       func(ctx context.Context, id string) error
	DiscoverTailnetFunc func(ctx context.Context) (*TailnetMetadata, error)
}

func (m *MockClient) Devices(ctx context.Context) ([]tailscale.Device, error) {
	if m.DevicesFunc != nil {
		return m.DevicesFunc(ctx)
	}
	return nil, fmt.Errorf("not implemented")
}

func (m *MockClient) CreateKey(ctx context.Context, caps tailscale.KeyCapabilities) (*tailscale.Key, error) {
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
