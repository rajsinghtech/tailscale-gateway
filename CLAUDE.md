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

## ✅ **CURRENT ARCHITECTURE: TailscaleService-Centric with Integrated Extension Server (2025-06-26)**

**Primary architecture centered on TailscaleService CRD with integrated extension server for Gateway API support:**

### **Core CRDs**
1. **TailscaleService** (Service Management) - VIP service creation with optional proxy infrastructure
2. **TailscaleTailnet** (Credentials) - OAuth credential and tailnet connection management
3. **TailscaleGateway** (Gateway Scoping) - **Essential** for telling the extension server which Envoy Gateway instances to process

### **TailscaleService CRD (Primary)**
- **All-in-one resource** combining VIP service configuration and optional proxy infrastructure
- **Direct VIP service specification** - no complex selectors needed
- **Optional proxy creation** using production-grade ProxyBuilder
- **Simple backend definitions** (kubernetes, external, tailscale types)
- **Gateway API compatible** as backendRefs in HTTPRoutes
- **Self-contained** - can work independently or reference TailscaleTailnet for credentials

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

### **Integrated Extension Server with Multi-Protocol Support**
✅ **Fully integrated into main operator process** with complete Gateway API support:
- **Multi-Protocol Support**: Discovers TailscaleService backends in HTTPRoute, GRPCRoute, TCPRoute, UDPRoute, and TLSRoute
- **Gateway-Scoped Processing**: Only processes routes that reference gateways with TailscaleGateway resources
- **Real VIP Address Resolution**: Uses TailscaleService status and backend configurations instead of placeholder addresses
- **Protocol-Aware Clustering**: Creates separate Envoy clusters for each protocol (http, grpc, tcp, udp, tls)
- **Priority-based Load Balancing**: Supports weight/priority from backend specs across all protocols
- **Health-Aware Failover**: Integrates with TailscaleService backend health status
- **Command Line Flags**: `--extension-grpc-port=5005` and `--enable-extension-server=true`
- **Multi-Tenant Support**: Different TailscaleGateway resources can scope different Gateway instances

## Project Overview

The Tailscale Gateway Operator combines the power of [Tailscale](https://tailscale.com/) mesh networking with [Envoy Gateway](https://gateway.envoyproxy.io/) to provide a cloud-native service mesh solution.

## Architecture Overview

**TailscaleService-centric architecture with supporting components:**

1. **TailscaleService Controller**: Primary controller managing VIP services and optional proxy infrastructure
2. **TailscaleTailnet Controller**: OAuth credential and tailnet connection management
3. **TailscaleGateway Controller**: Legacy multi-tailnet orchestration (deprecated)
4. **Integrated Extension Server**: Gateway API integration running within main operator process
5. **ProxyBuilder**: Shared utility for production-grade proxy StatefulSet creation

### **Current File Structure**
- `api/v1alpha1/tailscaleservice_types.go` - Primary CRD for service management
- `api/v1alpha1/tailnettailscale_types.go` - OAuth credential management CRD
- `api/v1alpha1/common_types.go` - Shared status and error types
- `internal/controller/tailscaleservice_controller.go` - Primary controller using ProxyBuilder
- `internal/controller/tailscaletailnet_controller.go` - Tailnet credential validation
- `internal/controller/tailscalegateway_controller.go` - Legacy orchestration (to be deprecated)
- `internal/proxy/builder.go` - Production-grade proxy creation utilities
- `internal/extension/server.go` - Integrated extension server with real VIP resolution
- `cmd/main.go` - All controller registration and extension server startup

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

- ✅ **TailscaleService-Centric Design**: Primary CRD with supporting OAuth credential management
- ✅ **Production-Ready Proxy Creation**: Shared ProxyBuilder with k8s-operator patterns
- ✅ **Gateway API Compatible**: Direct TailscaleService backend support in HTTPRoutes
- ✅ **Integrated Extension Server**: No separate deployment needed, real VIP address resolution
- ✅ **Maintainable**: Clear CRD relationships and simplified architecture
- ✅ **Build Success**: All compilation errors fixed, deprecated imports updated

## ✅ **ARCHITECTURAL FIXES COMPLETED (2025-06-26)**

**Successfully fixed all identified architectural issues:**

### **Issues Fixed**

1. ✅ **Extension Server Integration**: Fully integrated into main operator with command line flags
2. ✅ **VIP Address Resolution**: Real endpoint resolution from TailscaleService status and backends  
3. ✅ **Controller Registration**: All 3 controllers properly registered and working
4. ✅ **Deprecated Imports**: Updated Gateway API imports to use Install() instead of AddToScheme()
5. ✅ **Documentation Accuracy**: Updated CLAUDE.md to reflect actual multi-CRD architecture
6. ✅ **Multi-Protocol Support**: Extension server now supports HTTPRoute, GRPCRoute, TCPRoute, UDPRoute, and TLSRoute

### **Architectural Clarity Achieved**

- **TailscaleService**: Primary CRD for VIP service management with optional proxy infrastructure
- **TailscaleTailnet**: Supporting CRD for OAuth credential management  
- **TailscaleGateway**: **Essential** CRD for gateway scoping - tells the extension server which Envoy Gateway instances to process
- **Extension Server**: Integrated gRPC server on port 5005 with gateway-scoped processing
- **ProxyBuilder**: Shared production-grade proxy infrastructure creation

### **Command Line Usage**
```bash
# Start operator with integrated extension server
./main --extension-grpc-port=5005 --enable-extension-server=true

# Disable extension server if not using Gateway API
./main --enable-extension-server=false
```

## ✅ **TAILSCALEGATEWAY ROLE CLARIFIED (2025-06-26)**

**TailscaleGateway is essential for telling the operator which gateways to watch and process:**

### **Why TailscaleGateway is Essential**

Without TailscaleGateway resources, the extension server would process **every route in the cluster** (HTTP/GRPC/TCP/UDP/TLS) that has TailscaleService backends. TailscaleGateway provides:

1. **Gateway Selection**: Tell the operator which specific Envoy Gateway instances to integrate with
2. **Multi-Tenant Isolation**: Different teams can have separate gateway configurations
3. **Environment Separation**: Production and staging can have isolated configurations
4. **Selective Processing**: Only routes (HTTP/GRPC/TCP/UDP/TLS) that reference configured gateways get Tailscale integration

### **TailscaleGateway Usage Pattern**
```yaml
apiVersion: gateway.tailscale.com/v1alpha1
kind: TailscaleGateway
metadata:
  name: prod-gateway-config
  namespace: production
spec:
  gatewayRef:
    group: gateway.networking.k8s.io
    kind: Gateway
    name: envoy-gateway
    namespace: gateway-system
  tailnets:
  - name: prod-tailnet
    tailscaleTailnetRef:
      name: prod-tailnet-credentials
```

### **Multi-Protocol Route Examples**

**HTTPRoute with TailscaleService backend:**
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

**TCPRoute with TailscaleService backend:**
```yaml
apiVersion: gateway.networking.k8s.io/v1alpha2
kind: TCPRoute
spec:
  rules:
  - backendRefs:
    - group: gateway.tailscale.com
      kind: TailscaleService
      name: database-service
```

**UDPRoute with TailscaleService backend:**
```yaml
apiVersion: gateway.networking.k8s.io/v1alpha2
kind: UDPRoute
spec:
  rules:
  - backendRefs:
    - group: gateway.tailscale.com
      kind: TailscaleService
      name: dns-service
```

### **How Gateway Scoping Works**

1. **Extension Server Discovery**: Extension server watches all TailscaleGateway resources
2. **Gateway Registry**: Maintains a registry of which Gateway instances should be processed
3. **Route Filtering**: Only processes routes (HTTP/GRPC/TCP/UDP/TLS) that reference registered gateways
4. **Scoped Processing**: `Route → parentRefs → Gateway → TailscaleGateway → Process TailscaleService backends`

### **Backward Compatibility**

- **No TailscaleGateway resources**: Extension server processes all routes (HTTP/GRPC/TCP/UDP/TLS) with TailscaleService backends
- **With TailscaleGateway resources**: Extension server only processes routes for registered gateways

This ensures existing deployments continue working while new deployments get proper gateway scoping.

# important-instruction-reminders
Do what has been asked; nothing more, nothing less.
NEVER create files unless they're absolutely necessary for achieving your goal.
ALWAYS prefer editing an existing file to creating a new one.
NEVER proactively create documentation files (*.md) or README files. Only create documentation files if explicitly requested by the User.