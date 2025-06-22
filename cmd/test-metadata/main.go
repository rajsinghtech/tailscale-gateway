package main

import (
	"context"
	"fmt"
	"log"

	"github.com/rajsinghtech/tailscale-gateway/internal/tailscale"
)

func main() {
	clientID := "kcSBZN5XmF11CNTRL"
	clientSecret := "tskey-client-kcSBZN5XmF11CNTRL-1qC9TqKavpTzy2svxmWRqTuGkLvxjR4bg"

	ctx := context.Background()

	// Create Tailscale client config
	config := tailscale.ClientConfig{
		Tailnet:      tailscale.DefaultTailnet,
		APIBaseURL:   tailscale.DefaultAPIBaseURL,
		ClientID:     clientID,
		ClientSecret: clientSecret,
	}

	fmt.Printf("Testing tailnet metadata discovery...\n")

	tsClient, err := tailscale.NewClient(ctx, config)
	if err != nil {
		log.Fatalf("Failed to create Tailscale client: %v", err)
	}

	fmt.Printf("✓ Client created successfully\n")

	// Test tailnet metadata discovery
	fmt.Printf("Discovering tailnet metadata...\n")
	metadata, err := tsClient.DiscoverTailnetInfo(ctx)
	if err != nil {
		fmt.Printf("✗ Failed to discover tailnet metadata: %v\n", err)
		return
	}

	fmt.Printf("✓ Tailnet discovery successful!\n")
	fmt.Printf("  Name: %s\n", metadata.Name)
	fmt.Printf("  MagicDNS Suffix: %s\n", metadata.MagicDNSSuffix)
	fmt.Printf("  Organization: %s\n", metadata.Organization)

	fmt.Printf("\nThis shows the tailnet domain that would be used instead of hardcoding '-'\n")
}