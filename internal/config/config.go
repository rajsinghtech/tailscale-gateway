// Copyright (c) Tailscale Gateway Authors
// SPDX-License-Identifier: BSD-3-Clause

package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// OperatorConfig holds runtime configuration for the Tailscale Gateway Operator
type OperatorConfig struct {
	// Network Configuration
	MetricsPort     int
	HealthProbePort int
	GRPCPort        int

	// Images
	OperatorImage        string
	ExtensionServerImage string
	TailscaleImage       string

	// Intervals and Timeouts
	SyncInterval        time.Duration
	CleanupInterval     time.Duration
	HealthCheckTimeout  time.Duration
	RequeueInterval     time.Duration
	RequeueIntervalLong time.Duration

	// Feature Flags
	EnableMetrics      bool
	EnableHealthChecks bool
	DebugMode          bool
	LeaderElection     bool

	// Resource Limits
	CPULimit      string
	MemoryLimit   string
	CPURequest    string
	MemoryRequest string

	// Extension Server
	ExtensionReplicas int
	MaxMessageSize    string

	// Logging
	LogLevel  string
	LogFormat string
}

// NewOperatorConfig creates a new OperatorConfig with defaults
func NewOperatorConfig() *OperatorConfig {
	return &OperatorConfig{
		// Network Configuration
		MetricsPort:     DefaultMetricsPort,
		HealthProbePort: DefaultHealthProbePort,
		GRPCPort:        DefaultGRPCPort,

		// Images
		OperatorImage:        DefaultOperatorImage,
		ExtensionServerImage: DefaultExtensionServerImage,
		TailscaleImage:       DefaultTailscaleImage,

		// Intervals and Timeouts
		SyncInterval:        DefaultSyncInterval,
		CleanupInterval:     DefaultCleanupInterval,
		HealthCheckTimeout:  DefaultHealthCheckTimeout,
		RequeueInterval:     DefaultRequeueInterval,
		RequeueIntervalLong: DefaultRequeueIntervalLong,

		// Feature Flags
		EnableMetrics:      true,
		EnableHealthChecks: true,
		DebugMode:          false,
		LeaderElection:     false,

		// Resource Limits
		CPULimit:      DefaultCPULimitOperator,
		MemoryLimit:   DefaultMemoryLimitOperator,
		CPURequest:    DefaultCPURequestOperator,
		MemoryRequest: DefaultMemoryRequestOperator,

		// Extension Server
		ExtensionReplicas: DefaultExtensionReplicas,
		MaxMessageSize:    DefaultMaxMessageSize,

		// Logging
		LogLevel:  "info",
		LogFormat: "json",
	}
}

// LoadFromEnvironment loads configuration values from environment variables
func (c *OperatorConfig) LoadFromEnvironment() error {
	// Network Configuration
	if port := os.Getenv("METRICS_PORT"); port != "" {
		if p, err := strconv.Atoi(port); err == nil {
			c.MetricsPort = p
		}
	}

	if port := os.Getenv("HEALTH_PROBE_PORT"); port != "" {
		if p, err := strconv.Atoi(port); err == nil {
			c.HealthProbePort = p
		}
	}

	if port := os.Getenv("GRPC_PORT"); port != "" {
		if p, err := strconv.Atoi(port); err == nil {
			c.GRPCPort = p
		}
	}

	// Images
	if image := os.Getenv("OPERATOR_IMAGE"); image != "" {
		c.OperatorImage = image
	}

	if image := os.Getenv("EXTENSION_SERVER_IMAGE"); image != "" {
		c.ExtensionServerImage = image
	}

	if image := os.Getenv("TAILSCALE_IMAGE"); image != "" {
		c.TailscaleImage = image
	}

	// Intervals
	if interval := os.Getenv("SYNC_INTERVAL"); interval != "" {
		if d, err := time.ParseDuration(interval); err == nil {
			c.SyncInterval = d
		}
	}

	if interval := os.Getenv("CLEANUP_INTERVAL"); interval != "" {
		if d, err := time.ParseDuration(interval); err == nil {
			c.CleanupInterval = d
		}
	}

	// Feature Flags
	if metrics := os.Getenv("ENABLE_METRICS"); metrics != "" {
		c.EnableMetrics = metrics == "true"
	}

	if health := os.Getenv("ENABLE_HEALTH_CHECKS"); health != "" {
		c.EnableHealthChecks = health == "true"
	}

	if debug := os.Getenv("DEBUG_MODE"); debug != "" {
		c.DebugMode = debug == "true"
	}

	if leader := os.Getenv("LEADER_ELECTION"); leader != "" {
		c.LeaderElection = leader == "true"
	}

	// Resource Limits
	if cpu := os.Getenv("CPU_LIMIT"); cpu != "" {
		c.CPULimit = cpu
	}

	if memory := os.Getenv("MEMORY_LIMIT"); memory != "" {
		c.MemoryLimit = memory
	}

	// Extension Server
	if replicas := os.Getenv("EXTENSION_REPLICAS"); replicas != "" {
		if r, err := strconv.Atoi(replicas); err == nil {
			c.ExtensionReplicas = r
		}
	}

	// Logging
	if level := os.Getenv("LOG_LEVEL"); level != "" {
		c.LogLevel = level
	}

	if format := os.Getenv("LOG_FORMAT"); format != "" {
		c.LogFormat = format
	}

	return nil
}

// Validate validates the configuration
func (c *OperatorConfig) Validate() error {
	// Validate ports
	if c.MetricsPort < 1 || c.MetricsPort > 65535 {
		return fmt.Errorf("invalid metrics port: %d (must be 1-65535)", c.MetricsPort)
	}

	if c.HealthProbePort < 1 || c.HealthProbePort > 65535 {
		return fmt.Errorf("invalid health probe port: %d (must be 1-65535)", c.HealthProbePort)
	}

	if c.GRPCPort < 1 || c.GRPCPort > 65535 {
		return fmt.Errorf("invalid gRPC port: %d (must be 1-65535)", c.GRPCPort)
	}

	// Validate intervals
	if c.SyncInterval < time.Second {
		return fmt.Errorf("sync interval too short: %v (minimum 1s)", c.SyncInterval)
	}

	if c.CleanupInterval < time.Minute {
		return fmt.Errorf("cleanup interval too short: %v (minimum 1m)", c.CleanupInterval)
	}

	// Validate replicas
	if c.ExtensionReplicas < 1 {
		return fmt.Errorf("extension replicas must be at least 1, got: %d", c.ExtensionReplicas)
	}

	// Validate log level
	validLogLevels := map[string]bool{
		"debug": true,
		"info":  true,
		"warn":  true,
		"error": true,
	}
	if !validLogLevels[c.LogLevel] {
		return fmt.Errorf("invalid log level: %s (must be debug, info, warn, or error)", c.LogLevel)
	}

	return nil
}

// GetMetricsAddress returns the formatted metrics bind address
func (c *OperatorConfig) GetMetricsAddress() string {
	return fmt.Sprintf(":%d", c.MetricsPort)
}

// GetHealthProbeAddress returns the formatted health probe bind address
func (c *OperatorConfig) GetHealthProbeAddress() string {
	return fmt.Sprintf(":%d", c.HealthProbePort)
}

// GetGRPCAddress returns the formatted gRPC bind address
func (c *OperatorConfig) GetGRPCAddress() string {
	return fmt.Sprintf(":%d", c.GRPCPort)
}

// ToMap converts the config to a map for logging/debugging
func (c *OperatorConfig) ToMap() map[string]interface{} {
	return map[string]interface{}{
		"metrics_port":         c.MetricsPort,
		"health_probe_port":    c.HealthProbePort,
		"grpc_port":            c.GRPCPort,
		"sync_interval":        c.SyncInterval.String(),
		"cleanup_interval":     c.CleanupInterval.String(),
		"extension_replicas":   c.ExtensionReplicas,
		"enable_metrics":       c.EnableMetrics,
		"enable_health_checks": c.EnableHealthChecks,
		"debug_mode":           c.DebugMode,
		"leader_election":      c.LeaderElection,
		"log_level":            c.LogLevel,
		"log_format":           c.LogFormat,
	}
}
