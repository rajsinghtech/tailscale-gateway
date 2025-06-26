# CLAUDE.md
*note for claude* you are allowed to update this as we make changes 
This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

# important-instruction-reminders
Do what has been asked; nothing more, nothing less.
NEVER create files unless they're absolutely necessary for achieving your goal.
ALWAYS prefer editing an existing file to creating a new one.
NEVER proactively create documentation files (*.md) or README files. Only create documentation files if explicitly requested by the User.
At the end of tasks update Claude.md with stuff you wish to remember about this app
You can always reference Tailscale Repos '../tailscale' '../tailscale-client-go-v2' '../corp'\ Envoy Gateway Repo '../gateway' Envoy Gateway Externsion server example repo '../ai-gateway'
Update '/site' docs when making changes user facing changes to the app, never care about depreciating just remove it

## ✅ **SIMPLIFIED: Extension Server Architecture Consolidation (2025-06-26)**

**Successfully consolidated the extension server into the main operator to simplify deployment and maintenance:**

### **Changes Made**

1. **Removed Separate Extension Server Components**:
   - ✅ Deleted `cmd/extension-server/Dockerfile`
   - ✅ Removed extension server Helm templates (`extension-server-*.yaml`)
   - ✅ Updated GitHub Actions workflows to remove extension server build
   - ✅ Cleaned up Makefile targets for extension server

2. **Updated Main Operator to Include Extension Server**:
   - ✅ Added extension server gRPC port (5005) to main operator deployment
   - ✅ Created service for extension server within main operator pod
   - ✅ Updated Envoy Gateway configuration to point to integrated service
   - ✅ Updated command line arguments to include `--extension-grpc-port`

3. **Configuration Updates**:
   - ✅ Updated Helm values to reflect integrated architecture
   - ✅ Updated API types default image references
   - ✅ Updated CRD default values
   - ✅ Updated configuration constants

4. **Documentation Updates**:
   - ✅ Updated installation documentation
   - ✅ Updated architecture documentation
   - ✅ Updated troubleshooting guides
   - ✅ Updated configuration examples

### **Benefits of Integration**

1. **Simplified Deployment**: Single Docker image and Helm chart instead of two
2. **Reduced Resource Usage**: No separate pods for extension server
3. **Easier Maintenance**: Single deployment to manage and monitor
4. **Improved Performance**: No network calls between operator and extension server
5. **Simplified Configuration**: Single service endpoint for Envoy Gateway

### **Migration Notes**

- **Backward Compatibility**: Existing TailscaleGateway resources continue to work
- **Service Name Change**: Envoy Gateway configuration updated to point to `tailscale-gateway-operator-extension-server` service
- **Single Image**: Only `ghcr.io/rajsinghtech/tailscale-gateway-operator:latest` image needed
- **Port Configuration**: Extension server gRPC port (5005) now exposed by main operator

**Architecture Status**: Extension server is now fully integrated into the main operator process, providing the same functionality with simplified deployment and reduced operational complexity.

## ✅ **CURRENT ARCHITECTURE: Simplified Single CRD Design (2025-06-26)**

**Successfully simplified to a single TailscaleService CRD with integrated proxy builder architecture:**

### **TailscaleService CRD (Singular)**
- **All-in-one resource** combining VIP service configuration and optional proxy infrastructure
- **Direct VIP service specification** - no complex selectors needed
- **Optional proxy creation** using production-grade ProxyBuilder
- **Simple backend definitions** (kubernetes, external, tailscale types)
- **Gateway API compatible** as backendRefs

### **ProxyBuilder Architecture**
Created `internal/proxy/builder.go` containing production-grade StatefulSet creation patterns:

**Key Features:**
- ✅ **Official Tailscale k8s-operator Patterns**: Follows exact ProxyGroup environment variable and volume mounting patterns
- ✅ **Production-Grade Security**: Complete security context, health probes, and RBAC creation
- ✅ **Multi-Tailnet Support**: Isolated configuration mounting per tailnet with proper sanitization
- ✅ **Configuration Management**: Automated config secret creation with tailscaled configurations
- ✅ **Service Account Creation**: Dedicated RBAC setup with minimal permissions per proxy

**ProxyBuilder Usage Pattern:**
```go
proxyBuilder := proxy.NewProxyBuilder(client)
proxySpec := &proxy.ProxySpec{
    Name:           "web-service-proxy",
    Namespace:      "default", 
    Tailnet:        "company-tailnet",
    ConnectionType: "bidirectional",
    Tags:           []string{"tag:k8s-proxy", "tag:web-service"},
    Ports:          []PortMapping{{Name: "http", Port: 80, Protocol: "TCP"}},
    ProxyConfig:    &ProxySpec{Replicas: &[]int32{2}[0]},
    OwnerRef:       ownerReference,
    Labels:         resourceLabels,
    Replicas:       2,
}
err := proxyBuilder.CreateProxyInfrastructure(ctx, proxySpec)
```

### **Extension Server**
Integrated into the main operator process (simplified to handle only TailscaleService backends):
- **Gateway API Compliance**: Supports TailscaleService as backendRefs in all route types
- **Direct Integration**: No complex resource indexing or coordination
- **Health-Aware Failover**: Priority-based load balancing
- **Single Process**: Extension server runs within the main operator (no separate deployment needed)

## Project Overview

The Tailscale Gateway Operator combines the power of [Tailscale](https://tailscale.com/) mesh networking with [Envoy Gateway](https://gateway.envoyproxy.io/) to provide a cloud-native service mesh solution.

## Architecture Overview

Simple unified architecture:

1. **TailscaleService Controller**: Manages VIP services and optional proxy infrastructure
2. **Extension Server**: Handles Gateway API route discovery and TailscaleService backends
3. **ProxyBuilder**: Shared utility for production-grade proxy StatefulSet creation

### **Current File Structure**
- `api/v1alpha1/tailscaleservice_types.go` - Single simplified CRD
- `internal/controller/tailscaleservice_controller.go` - Main controller using ProxyBuilder
- `internal/proxy/builder.go` - Shared proxy creation utilities
- `internal/extension/server.go` - Simplified extension server (284 lines)
- `cmd/main.go` - Controller registration

## Development Commands

```bash
# Lint and type check
make lint
make test

# Generate manifests after CRD changes
make manifests

# Build and test locally
make docker-build
make deploy
```

## Key Implementation Patterns

### TailscaleService Example
```yaml
apiVersion: gateway.tailscale.com/v1alpha1
kind: TailscaleService
metadata:
  name: web-service
spec:
  vipService:
    name: "svc:web-service"
    ports: ["tcp:80", "tcp:443"]
    tags: ["tag:web-service"]
  proxy:
    replicas: 2
    connectionType: "bidirectional"
    image: "tailscale/tailscale:latest"
  tailnet: "company-tailnet"
  backends:
    - type: kubernetes
      service: "web-app.default.svc.cluster.local:80"
      weight: 100
```

### Gateway API Integration
TailscaleService can be used as backendRefs:
```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
spec:
  rules:
  - backendRefs:
    - group: gateway.tailscale.com
      kind: TailscaleService
      name: web-service
```

## Build and Deployment

### Docker Images
- **Main Operator**: `tailscale-gateway:latest` (includes controllers + ProxyBuilder + integrated extension server)

### Make Targets
```bash
# Generate CRDs and code
make manifests generate

# Build images
docker build -t tailscale-gateway:latest .

# Deploy to kind
kind load docker-image tailscale-gateway:latest
```

## Architecture Benefits

- ✅ **Dramatically Reduced Complexity**: Single CRD instead of multiple coordinating resources
- ✅ **Production-Ready Proxy Creation**: Shared ProxyBuilder with k8s-operator patterns
- ✅ **Gateway API Compatible**: Direct TailscaleService backend support
- ✅ **Maintainable**: Much simpler to understand and debug
- ✅ **Build Success**: All compilation errors fixed

# important-instruction-reminders
Do what has been asked; nothing more, nothing less.
NEVER create files unless they're absolutely necessary for achieving your goal.
ALWAYS prefer editing an existing file to creating a new one.
NEVER proactively create documentation files (*.md) or README files. Only create documentation files if explicitly requested by the User.