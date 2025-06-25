// Copyright (c) Tailscale Gateway Authors
// SPDX-License-Identifier: BSD-3-Clause

package config

import (
	"time"
)

// Network Ports - Centralized port configuration
const (
	// Core Service Ports
	DefaultMetricsPort     = 8080
	DefaultHealthProbePort = 8081
	DefaultGRPCPort        = 5005

	// Protocol Default Ports
	DefaultHTTPPort         = 80
	DefaultHTTPSPort        = 443
	DefaultGRPCStandardPort = 9090
	DefaultUDPDNSPort       = 53

	// Tailscale Standard Ports
	TailscaleWebPort   = 80
	TailscaleHTTPSPort = 443
	TailscaleGRPCPort  = 9090
)

// Docker Images - Centralized image configuration
const (
	// Operator Images
	DefaultOperatorImage        = "ghcr.io/rajsinghtech/tailscale-gateway-operator:latest"
	DefaultExtensionServerImage = "ghcr.io/rajsinghtech/tailscale-gateway-extension-server:latest"

	// Tailscale Images
	DefaultTailscaleImage = "tailscale/tailscale:stable"
	DefaultTailscaleTag   = "stable"
)

// Timeouts and Intervals - Centralized timing configuration
const (
	// Sync and Update Intervals
	DefaultSyncInterval         = 30 * time.Second
	DefaultTailnetSyncInterval  = 5 * time.Minute
	DefaultCleanupInterval      = 5 * time.Minute
	DefaultCacheRefreshInterval = 1 * time.Hour

	// Health Check Intervals
	DefaultHealthCheckInterval = 10 * time.Second
	DefaultHealthCheckTimeout  = 3 * time.Second
	DefaultProbeInterval       = 5 * time.Second

	// Cache and Cleanup
	DefaultClientCacheTimeout  = 30 * time.Minute
	DefaultServiceCacheTimeout = 30 * time.Minute
	DefaultStaleThreshold      = 30 * time.Minute

	// Retry and Requeue Intervals
	DefaultRequeueInterval     = 1 * time.Minute
	DefaultRequeueIntervalLong = 5 * time.Minute
	DefaultBackoffInterval     = 10 * time.Second

	// HTTP Server Timeouts
	DefaultHTTPReadTimeout  = 10 * time.Second
	DefaultHTTPWriteTimeout = 10 * time.Second
	DefaultHTTPIdleTimeout  = 30 * time.Second
)

// Resource Limits - Centralized resource configuration
const (
	// CPU Limits
	DefaultCPULimitOperator  = "500m"
	DefaultCPULimitExtension = "500m"
	DefaultCPULimitProxy     = "1000m"

	// Memory Limits
	DefaultMemoryLimitOperator  = "128Mi"
	DefaultMemoryLimitExtension = "128Mi"
	DefaultMemoryLimitProxy     = "256Mi"

	// CPU Requests
	DefaultCPURequestOperator  = "100m"
	DefaultCPURequestExtension = "100m"
	DefaultCPURequestProxy     = "200m"

	// Memory Requests
	DefaultMemoryRequestOperator  = "64Mi"
	DefaultMemoryRequestExtension = "64Mi"
	DefaultMemoryRequestProxy     = "128Mi"
)

// Feature Flags and Behavior
const (
	// Extension Server
	DefaultExtensionReplicas = 2
	DefaultMaxMessageSize    = "1000M"

	// Service Discovery
	DefaultServiceTags  = "tag:k8s-operator"
	DefaultMaxEndpoints = 100

	// Retry Policies
	DefaultMaxRetries = 3
	DefaultRetryDelay = 5 * time.Second
)

// Protocol Inference - Centralized port-to-protocol mapping
var (
	// HTTPPortMap defines ports that should default to HTTP protocol
	HTTPPortMap = map[int32]bool{
		80:   true,
		8080: true,
		8081: true,
		3000: true,
		8000: true,
		9000: true,
	}

	// HTTPSPortMap defines ports that should default to HTTPS protocol
	HTTPSPortMap = map[int32]bool{
		443:  true,
		8443: true,
		9443: true,
	}

	// GRPCPortMap defines ports that should default to gRPC protocol
	GRPCPortMap = map[int32]bool{
		9090:  true,
		50051: true,
	}

	// DefaultServicePorts defines the default ports for VIP services
	DefaultServicePorts = []string{"tcp:80", "tcp:443"}
)

// InferProtocolFromPort determines the protocol based on port number
func InferProtocolFromPort(port int32) string {
	if HTTPSPortMap[port] {
		return "HTTPS"
	}
	if GRPCPortMap[port] {
		return "GRPC"
	}
	if HTTPPortMap[port] {
		return "HTTP"
	}
	// Default to HTTP for unknown ports
	return "HTTP"
}

// IsStandardHTTPPort returns true if the port is a standard HTTP port
func IsStandardHTTPPort(port int32) bool {
	return HTTPPortMap[port]
}

// IsStandardHTTPSPort returns true if the port is a standard HTTPS port
func IsStandardHTTPSPort(port int32) bool {
	return HTTPSPortMap[port]
}

// IsStandardGRPCPort returns true if the port is a standard gRPC port
func IsStandardGRPCPort(port int32) bool {
	return GRPCPortMap[port]
}
