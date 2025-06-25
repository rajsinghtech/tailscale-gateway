// Copyright (c) Tailscale Gateway Authors
// SPDX-License-Identifier: BSD-3-Clause

package tailscale

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sync"
	"time"

	gatewayv1alpha1 "github.com/rajsinghtech/tailscale-gateway/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// MultiTailnetManager manages multiple Tailscale client connections with cleanup and refresh logic
// This manager allows the operator to connect to multiple tailnets simultaneously with proper caching
type MultiTailnetManager struct {
	clients       map[string]*clientCacheEntry
	mu            sync.RWMutex
	cleanupTicker *time.Ticker
	stopCleanup   chan struct{}
}

// clientCacheEntry represents a cached Tailscale client with metadata
type clientCacheEntry struct {
	client     Client
	createdAt  time.Time
	lastUsed   time.Time
	secretHash string // Hash of the secret used to create the client
	tailnetKey string // Key for the TailscaleTailnet resource
}

// NewMultiTailnetManager creates a new MultiTailnetManager with cleanup logic
func NewMultiTailnetManager() *MultiTailnetManager {
	m := &MultiTailnetManager{
		clients:     make(map[string]*clientCacheEntry),
		stopCleanup: make(chan struct{}),
	}

	// Start cleanup goroutine
	m.cleanupTicker = time.NewTicker(5 * time.Minute) // Cleanup every 5 minutes
	go m.cleanup()

	return m
}

// GetClient returns a Tailscale client for the specified tailnet
// Implements caching, automatic refresh, and client lifecycle management
func (m *MultiTailnetManager) GetClient(ctx context.Context, k8sClient client.Client, tailnetName, namespace string) (Client, error) {
	key := fmt.Sprintf("%s/%s", namespace, tailnetName)

	m.mu.RLock()
	entry, exists := m.clients[key]
	if exists {
		// Check if client needs refresh
		if !m.shouldRefreshClient(ctx, k8sClient, entry, tailnetName, namespace) {
			entry.lastUsed = time.Now()
			m.mu.RUnlock()
			return entry.client, nil
		}
	}
	m.mu.RUnlock()

	// Need to create or refresh client
	return m.createAndCacheClient(ctx, k8sClient, tailnetName, namespace, key)
}

// createAndCacheClient creates a new client and caches it
func (m *MultiTailnetManager) createAndCacheClient(ctx context.Context, k8sClient client.Client, tailnetName, namespace, key string) (Client, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Double-check that another goroutine didn't create it
	if entry, exists := m.clients[key]; exists {
		if !m.shouldRefreshClient(ctx, k8sClient, entry, tailnetName, namespace) {
			entry.lastUsed = time.Now()
			return entry.client, nil
		}
	}

	// Find the TailscaleTailnet resource
	tailnet := &gatewayv1alpha1.TailscaleTailnet{}
	if err := k8sClient.Get(ctx, client.ObjectKey{Name: tailnetName, Namespace: namespace}, tailnet); err != nil {
		return nil, fmt.Errorf("failed to get TailscaleTailnet %s/%s: %w", namespace, tailnetName, err)
	}

	// Create new client using the tailnet configuration
	tsClient, err := m.createClientFromTailnet(ctx, k8sClient, tailnet)
	if err != nil {
		return nil, fmt.Errorf("failed to create Tailscale client for %s/%s: %w", namespace, tailnetName, err)
	}

	// Create secret hash for cache invalidation
	secretHash := m.computeSecretHash(tailnet)

	// Cache the client
	m.clients[key] = &clientCacheEntry{
		client:     tsClient,
		createdAt:  time.Now(),
		lastUsed:   time.Now(),
		secretHash: secretHash,
		tailnetKey: key,
	}

	return tsClient, nil
}

// shouldRefreshClient determines if a cached client should be refreshed
func (m *MultiTailnetManager) shouldRefreshClient(ctx context.Context, k8sClient client.Client, entry *clientCacheEntry, tailnetName, namespace string) bool {
	// Refresh if client is older than 1 hour
	if time.Since(entry.createdAt) > time.Hour {
		return true
	}

	// Refresh if TailscaleTailnet resource has changed
	tailnet := &gatewayv1alpha1.TailscaleTailnet{}
	if err := k8sClient.Get(ctx, client.ObjectKey{Name: tailnetName, Namespace: namespace}, tailnet); err != nil {
		// If we can't get the resource, keep the existing client
		return false
	}

	currentHash := m.computeSecretHash(tailnet)
	return currentHash != entry.secretHash
}

// computeSecretHash computes a hash of the OAuth secret configuration
func (m *MultiTailnetManager) computeSecretHash(tailnet *gatewayv1alpha1.TailscaleTailnet) string {
	data := fmt.Sprintf("%s:%s:%s:%s",
		tailnet.Spec.OAuthSecretName,
		tailnet.Spec.OAuthSecretNamespace,
		tailnet.Spec.Tailnet,
		tailnet.ResourceVersion,
	)
	hash := sha256.Sum256([]byte(data))
	return fmt.Sprintf("%x", hash)
}

// createClientFromTailnet creates a new Tailscale client from a TailscaleTailnet resource
func (m *MultiTailnetManager) createClientFromTailnet(ctx context.Context, k8sClient client.Client, tailnet *gatewayv1alpha1.TailscaleTailnet) (Client, error) {
	// Determine the namespace for the OAuth secret
	secretNamespace := tailnet.Namespace
	if tailnet.Spec.OAuthSecretNamespace != "" {
		secretNamespace = tailnet.Spec.OAuthSecretNamespace
	}

	// Build paths to OAuth credential files
	clientIDPath := fmt.Sprintf("/var/secrets/%s/%s/client_id", secretNamespace, tailnet.Spec.OAuthSecretName)
	clientSecretPath := fmt.Sprintf("/var/secrets/%s/%s/client_secret", secretNamespace, tailnet.Spec.OAuthSecretName)

	// Determine tailnet identifier
	tailnetID := tailnet.Spec.Tailnet
	if tailnetID == "" {
		tailnetID = DefaultTailnet // Use default tailnet
	}

	// Create the client using secret files
	return NewClientFromSecretFiles(ctx, tailnetID, DefaultAPIBaseURL, clientIDPath, clientSecretPath)
}

// cleanup runs periodic cleanup of unused clients
func (m *MultiTailnetManager) cleanup() {
	for {
		select {
		case <-m.cleanupTicker.C:
			m.performCleanup()
		case <-m.stopCleanup:
			return
		}
	}
}

// performCleanup removes clients that haven't been used recently
func (m *MultiTailnetManager) performCleanup() {
	m.mu.Lock()
	defer m.mu.Unlock()

	cutoff := time.Now().Add(-30 * time.Minute) // Remove clients unused for 30 minutes
	for key, entry := range m.clients {
		if entry.lastUsed.Before(cutoff) {
			delete(m.clients, key)
		}
	}
}

// RemoveClient removes a client from the manager and cleans up resources
func (m *MultiTailnetManager) RemoveClient(tailnetName, namespace string) {
	key := fmt.Sprintf("%s/%s", namespace, tailnetName)

	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.clients, key)
}

// InvalidateClient removes a specific client from the cache (useful for error handling)
// This is an alias for RemoveClient to maintain compatibility
func (m *MultiTailnetManager) InvalidateClient(tailnetName, namespace string) {
	m.RemoveClient(tailnetName, namespace)
}

// ListClients returns a list of all active client keys
func (m *MultiTailnetManager) ListClients() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	keys := make([]string, 0, len(m.clients))
	for key := range m.clients {
		keys = append(keys, key)
	}
	return keys
}

// Cleanup removes all clients and stops cleanup routines
func (m *MultiTailnetManager) Cleanup() {
	// Stop cleanup goroutine
	close(m.stopCleanup)
	if m.cleanupTicker != nil {
		m.cleanupTicker.Stop()
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Clear all clients
	m.clients = make(map[string]*clientCacheEntry)
}
