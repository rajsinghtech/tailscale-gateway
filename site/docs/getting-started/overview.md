---
id: overview
title: Overview
sidebar_position: 1
---

# Tailscale Gateway Operator Overview

The Tailscale Gateway Operator is a cloud-native service mesh solution that combines the power of [Tailscale](https://tailscale.com/) mesh networking with [Envoy Gateway](https://gateway.envoyproxy.io/) to provide secure access to external services through Tailscale networks.

## What is Tailscale Gateway?

Tailscale Gateway is a Kubernetes operator that:

- **Bridges Networks**: Connects Kubernetes services with external Tailscale networks
- **Enables Service Mesh**: Provides bidirectional traffic flow between Kubernetes and Tailscale
- **Supports Gateway API**: Fully compliant with Kubernetes Gateway API standards
- **Offers Multi-Protocol Support**: Handles HTTP, TCP, UDP, TLS, and gRPC traffic
- **Provides Security**: Zero-trust networking without opening inbound firewall ports

## Key Features

### 🛡️ **Zero-Trust Networking**
- Secure service exposure without opening firewall ports
- Identity-based access control through Tailscale
- Encrypted traffic end-to-end

### ⚡ **Developer Friendly**
- Connect to clusters from laptops without VPNs
- Standard Kubernetes manifests for configuration
- Familiar Gateway API resources

### 🚀 **Production Ready**
- Multi-operator service coordination
- Health-aware failover and load balancing
- Comprehensive observability and metrics

### 🔄 **Bidirectional Traffic Flow**
- **Ingress**: External Tailscale clients → Kubernetes services
- **Egress**: Kubernetes services → External Tailscale endpoints

## Architecture Overview

```
┌─────────────────┐    ┌──────────────────┐    ┌─────────────────────┐
│   Tailscale     │◄──►│   Envoy Gateway  │◄──►│  Kubernetes         │
│   Clients       │    │   (Integrated    │    │  Services           │
└─────────────────┘    │   Extension)     │    └─────────────────────┘
                       └──────────────────┘               ▲
                                │                         │
                                │                         │
                    ┌───────────▼───────────┐             │
                    │  Tailscale Gateway    │             │
                    │     Operator          │             │
                    │  (Integrated Ext.)    │◄────────────┘
                    └───────────────────────┘
```

## Core Components

### 1. **Extension Server**
- gRPC server integrated with the main operator
- Dynamic route and cluster injection
- Gateway API compliance
- Real-time service discovery

### 2. **TailscaleEndpoints Controller**
- Manages service discovery and StatefulSet creation
- Handles external target mappings
- Provides health checking and failover

### 3. **TailscaleGateway Controller**
- Processes Gateway API resources
- Coordinates multi-operator deployments
- Manages VIP service registration

### 4. **Service Coordinator**
- Cross-cluster service sharing
- VIP service registry management
- Automatic service discovery and attachment

## Why Choose Tailscale Gateway?

### **Traditional VPN Limitations**
- Complex setup and maintenance
- Single points of failure
- Broad network access (not service-specific)
- Performance bottlenecks

### **Tailscale Gateway Advantages**
- **Selective Access**: Expose only specific services
- **Auto-Discovery**: Services automatically available on tailnet
- **Zero Configuration**: No client VPN setup required
- **High Performance**: Direct encrypted connections
- **Developer Experience**: Just add to tailnet and access services

## Use Cases

### **Development Teams**
- Access staging/development clusters from anywhere
- No complex VPN setup for new team members
- Secure service-to-service communication

### **Production Workloads**
- Hybrid cloud service mesh
- Secure access to databases and internal APIs
- Multi-cluster service discovery

### **DevOps/Platform Teams**
- Centralized access control through Tailscale
- Automated service discovery and routing
- Comprehensive observability and monitoring

## Next Steps

Ready to get started? Choose your path:

- **[Quick Start](./quickstart)** - Get up and running in 5 minutes
- **[Installation Guide](./installation)** - Detailed installation instructions
- **[Examples](../examples/basic-usage)** - Working examples and configurations
- **[API Reference](../api/tailscale-endpoints)** - Complete API documentation