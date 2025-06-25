---
slug: /
id: intro
title: Introduction
sidebar_position: 1
---

# Tailscale Gateway Operator

Welcome to the Tailscale Gateway Operator documentation! This operator combines the power of [Tailscale](https://tailscale.com/) mesh networking with [Envoy Gateway](https://gateway.envoyproxy.io/) to provide secure access to external services through Tailscale networks.

## What is Tailscale Gateway?

Tailscale Gateway is a Kubernetes operator that bridges Kubernetes services with external Tailscale networks, enabling:

- **Zero-Trust Networking**: Secure service exposure without opening firewall ports
- **Service Mesh Integration**: Bidirectional traffic flow between Kubernetes and Tailscale
- **Gateway API Compliance**: Full compliance with Kubernetes Gateway API standards
- **Multi-Protocol Support**: HTTP, TCP, UDP, TLS, and gRPC traffic handling

## Quick Start

Get up and running in under 5 minutes:

1. **Install the Operator**
   ```bash
   helm install tailscale-gateway tailscale-gateway/tailscale-gateway \
     --namespace tailscale-system --create-namespace
   ```

2. **Configure Your First Service**
   ```yaml
   apiVersion: gateway.tailscale.com/v1alpha1
   kind: TailscaleEndpoints
   metadata:
     name: my-service
   spec:
     tailnet: "company.ts.net"
     endpoints:
       - name: api
         tailscaleIP: "100.64.0.10"
         tailscaleFQDN: "api.company.ts.net"
         port: 8080
         protocol: "HTTP"
         externalTarget: "api.internal.company.com:8080"
   ```

3. **Create Gateway Routes**
   ```yaml
   apiVersion: gateway.networking.k8s.io/v1
   kind: HTTPRoute
   metadata:
     name: api-route
   spec:
     parentRefs:
       - name: envoy-gateway
     rules:
       - backendRefs:
           - group: gateway.tailscale.com
             kind: TailscaleEndpoints
             name: my-service
             port: 8080
   ```

## Key Features

### 🛡️ **Zero-Trust Security**
- No inbound firewall ports required
- Identity-based access control
- End-to-end encryption

### ⚡ **Developer Experience**
- Connect to clusters from anywhere
- Standard Kubernetes manifests
- Familiar Gateway API resources

### 🚀 **Production Ready**
- Multi-operator coordination
- Health-aware load balancing
- Comprehensive observability

### 🔄 **Bidirectional Traffic**
- **Ingress**: External clients → Kubernetes services
- **Egress**: Kubernetes services → External endpoints

## Architecture

```
┌─────────────────┐    ┌──────────────────┐    ┌─────────────────────┐
│   Tailscale     │◄──►│   Envoy Gateway  │◄──►│  Kubernetes         │
│   Clients       │    │   (Integrated    │    │  Services           │
└─────────────────┘    │   Extension)     │    └─────────────────────┘
                       └──────────────────┘
```

## Use Cases

- **Development Teams**: Access staging/dev clusters without VPN
- **Production Workloads**: Hybrid cloud service mesh
- **DevOps Teams**: Centralized access control and service discovery

## Documentation

Explore the comprehensive documentation:

- **[Getting Started](./getting-started/overview)** - Complete setup guide
- **[Examples](./examples/basic-usage)** - Real-world configurations
- **[API Reference](./api/tailscale-endpoints)** - Complete API docs
- **[Operations](./operations/monitoring)** - Production deployment

## Community

- 🐛 **[Report Issues](https://github.com/rajsinghtech/tailscale-gateway/issues)**
- 💬 **[Discussions](https://github.com/rajsinghtech/tailscale-gateway/discussions)**
- 📖 **[Source Code](https://github.com/rajsinghtech/tailscale-gateway)**

Ready to get started? Check out our **[Quick Start Guide](./getting-started/quickstart)**! 