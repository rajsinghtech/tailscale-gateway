# CLAUDE.md
*note for claude* you are allowed to update this as we make changes 
This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

The Tailscale Gateway Operator combines the power of [Tailscale](https://tailscale.com/) mesh networking with [Envoy Gateway](https://gateway.envoyproxy.io/) to provide a cloud-native service mesh solution that enables secure access to external services through Tailscale networks.

## Architecture Overview

The operator follows a multi-layered architecture enabling traffic flow from Tailscale clients to external services through Envoy Gateway:

1. **Extension Server**: gRPC server implementing Envoy Gateway extension hooks for dynamic route and cluster injection
2. **TailscaleEndpoints Controller**: Manages Tailscale service discovery and external service mappings  
3. **Multi-Tailnet Manager**: Manages multiple Tailscale network connections with isolated state per tailnet

### Traffic Flow Architecture

```
┌─────────────────┐    ┌──────────────────┐    ┌─────────────────────┐
│   Tailscale     │────│   Envoy Gateway  │────│  External Services  │
│   Clients       │    │   (with routes   │    │  (via extension     │
└─────────────────┘    │   injected by    │    │   server mapping)   │
                       │   extension)     │    └─────────────────────┘
                       └──────────────────┘               ▲
                                │                         │
                                │                         │
                       ┌──────────────────┐               │
                       │   Extension      │───────────────┘
                       │     Server       │
                       └──────────────────┘
                                │
                                │
                       ┌──────────────────┐
                       │ TailscaleEndpoints│
                       │    Resources      │
                       └──────────────────┘
                                │
                    ┌───────────┼───────────┐
                    │           │           │
            ┌───────▼────┐ ┌────▼────┐ ┌───▼─────┐
            │ Corp        │ │  Dev    │ │Staging  │
            │ Tailnet     │ │Tailnet  │ │Tailnet  │
            └────────────┘ └─────────┘ └─────────┘
```

### Flow Description

- **Egress Path**: Tailscale clients → Envoy Gateway → External Services  
- **Route Injection**: Extension server injects `/api/{service}` routes to external backends
- **Service Discovery**: TailscaleEndpoints resources define external service mappings

## Envoy Gateway Extension Server Implementation

### Extension Hooks

The extension server implements four key hooks:

1. **PostVirtualHostModify**: Injects routes from `/api/{service}` to external backends
2. **PostTranslateModify**: Creates clusters pointing to external services  
3. **PostRouteModify**: (Pass-through for future enhancements)
4. **PostHTTPListenerModify**: (Pass-through for future enhancements)

### Service Discovery

The extension server discovers services from TailscaleEndpoints resources:

```go
// TailscaleServiceMapping represents egress service to external backend mapping
type TailscaleServiceMapping struct {
    ServiceName     string // e.g., "web-service"
    ClusterName     string // e.g., "external-backend-web-service"  
    EgressService   string // e.g., "test-endpoints-web-service-egress.default.svc.cluster.local"
    ExternalBackend string // e.g., "httpbin.org:80"
    Port           uint32 // e.g., 80
    Protocol       string // e.g., "HTTP"
}
```

### Route Configuration

Routes are created with prefix matching and rewriting:

- **Request**: `GET /api/web-service/status`
- **Forwarded**: `GET /status` to `httpbin.org:80`

## Kubernetes API Patterns

### CRD Structure
Following Tailscale k8s-operator patterns:
- All CRDs implement required Kubernetes interfaces via generated DeepCopy methods
- Use kubebuilder markers for CRD generation
- Custom LocalPolicyTargetReference type for Gateway API compatibility

### Resource Types
1. **TailscaleEndpoints**: Service discovery with external target mappings
   - New `externalTarget` field for Envoy Gateway integration
   - Format: `"hostname:port"` or `"service.namespace.svc.cluster.local:port"`
2. **TailscaleGateway**: Main integration with Envoy Gateway, supports multiple tailnets
3. **TailscaleRoutePolicy**: Advanced routing policies with conditions and actions
4. **TailscaleProxyGroup**: High-availability proxy deployments

## Extension Server Configuration

```yaml
extensionManager:
  maxMessageSize: 1000M
  hooks:
    xdsTranslator:
      post:
      - VirtualHost    # Inject routes
      - Translation    # Inject clusters
  service:
    fqdn:
      hostname: tailscale-gateway-extension-server.default.svc.cluster.local
      port: 5005
```

## Build and Deployment

### Docker Images

1. **Main Operator**: `tailscale-gateway:latest`
2. **Extension Server**: `tailscale-gateway-extension-server:latest`

### Make Targets

```bash
# Generate CRDs and code
make manifests generate

# Build images
docker build -t tailscale-gateway:latest .
docker build -f cmd/extension-server/Dockerfile -t tailscale-gateway-extension-server:latest .

# Deploy to kind
kind load docker-image tailscale-gateway:latest
kind load docker-image tailscale-gateway-extension-server:latest
```

## Key Files and Locations

- **Extension Server**: `internal/extension/server.go` - Complete gRPC implementation
- **Extension Command**: `cmd/extension-server/main.go` - Executable entry point
- **TailscaleEndpoints CRD**: `api/v1alpha1/tailscaleendpoints_types.go` - With `externalTarget` field
- **Extension Deployment**: `config/extension-server/deployment.yaml` - Kubernetes resources
- **Envoy Gateway Config**: `config/envoy-gateway/envoy-gateway-config.yaml` - Integration setup
- **Test Resources**: `test/` - Kind cluster configuration and test manifests

## Multi-Operator Service Coordination

### ✅ **Implementation Status: COMPLETE**

The operator implements comprehensive multi-operator service coordination, enabling multiple operators across different clusters to efficiently share Tailscale VIP services instead of creating duplicate services.

### **ServiceCoordinator Architecture**

Located at `internal/service/coordinator.go`, the ServiceCoordinator manages the shared service registry pattern:

```go
type ServiceCoordinator struct {
    tsClient   tailscale.Client
    kubeClient client.Client
    operatorID string
    clusterID  string
    logger     *zap.SugaredLogger
}
```

### **Service Registry Pattern**

```go
type ServiceRegistration struct {
    ServiceName      tailscale.ServiceName   `json:"serviceName"`
    OwnerOperator    string                  `json:"ownerOperator"`   // First operator to create
    ConsumerClusters map[string]ConsumerInfo `json:"consumers"`       // All operators using this service
    VIPAddresses     []string                `json:"vipAddresses"`
    LastUpdated      time.Time               `json:"lastUpdated"`
}
```

### **Service Attachment vs Creation Logic**

```go
// Automatic service discovery and attachment
func (sc *ServiceCoordinator) EnsureServiceForRoute(ctx context.Context, routeName string, targetBackend string) (*ServiceRegistration, error) {
    serviceName := sc.GenerateServiceName(targetBackend)
    
    // First, check if service already exists
    existingService, err := sc.tsClient.GetVIPService(ctx, serviceName)
    if existingService != nil {
        // Service exists - attach to it
        return sc.attachToExistingService(ctx, existingService, routeName)
    }
    
    // Service doesn't exist - create new one
    return sc.createNewService(ctx, serviceName, targetBackend, routeName)
}
```

### **Metadata Storage in Tailscale VIP Annotations**

```go
func (sc *ServiceCoordinator) encodeServiceRegistry(registry *ServiceRegistration) map[string]string {
    registryJSON, _ := json.Marshal(registry)
    
    return map[string]string{
        "gateway.tailscale.com/service-registry": string(registryJSON),
        "gateway.tailscale.com/owner-operator":   registry.OwnerOperator,
        "gateway.tailscale.com/consumer-count":   strconv.Itoa(len(registry.ConsumerClusters)),
        "gateway.tailscale.com/last-updated":     registry.LastUpdated.Format(time.RFC3339),
        "gateway.tailscale.com/schema-version":   "v1",
    }
}
```

### **TailscaleGateway Controller Integration**

The TailscaleGateway controller at `internal/controller/tailscalegateway_controller.go` now integrates the ServiceCoordinator:

```go
// Process HTTPRoutes and ensure VIP services if ServiceCoordinator is available
if r.ServiceCoordinator != nil {
    if err := r.reconcileServiceCoordination(ctx, gateway); err != nil {
        r.updateCondition(gateway, "ServicesReady", metav1.ConditionFalse, "ServiceError", err.Error())
        return ctrl.Result{RequeueAfter: time.Minute}, err
    }
    r.updateCondition(gateway, "ServicesReady", metav1.ConditionTrue, "ServicesConfigured", "VIP services are configured")
}
```

### **Dynamic HTTPRoute Watching**

```go
func (r *TailscaleGatewayReconciler) SetupWithManager(mgr ctrl.Manager) error {
    return ctrl.NewControllerManagedBy(mgr).
        For(&gatewayv1alpha1.TailscaleGateway{}).
        Watches(
            &gwapiv1.HTTPRoute{},
            handler.EnqueueRequestsFromMapFunc(r.mapHTTPRouteToTailscaleGateway),
        ).
        Watches(
            &gatewayv1alpha1.TailscaleTailnet{},
            handler.EnqueueRequestsFromMapFunc(r.mapTailnetToTailscaleGateway),
        ).
        Complete(r)
}
```

### **Service Lifecycle Management**

#### **Automatic Service Cleanup**
```go
func (sc *ServiceCoordinator) DetachFromService(ctx context.Context, serviceName tailscale.ServiceName, routeName string) error {
    // Remove route from consumer
    consumer.Routes = removeString(consumer.Routes, routeName)
    
    if len(consumer.Routes) == 0 {
        // No more routes - remove consumer entirely
        delete(registry.ConsumerClusters, consumerKey)
    }
    
    // Check if we should delete the service entirely
    if len(registry.ConsumerClusters) == 0 {
        // No consumers left - delete the service
        if err := sc.tsClient.DeleteVIPService(ctx, serviceName); err != nil {
            return fmt.Errorf("failed to delete unused service: %w", err)
        }
    }
}
```

#### **Stale Consumer Cleanup**
```go
func (sc *ServiceCoordinator) CleanupStaleConsumers(ctx context.Context) error {
    staleThreshold := time.Now().Add(-30 * time.Minute) // 30 minutes
    
    for consumerKey, consumer := range registry.ConsumerClusters {
        if consumer.LastSeen.Before(staleThreshold) {
            delete(registry.ConsumerClusters, consumerKey)
            updated = true
        }
    }
}
```

### **Benefits of Multi-Operator Coordination**

1. ✅ **Resource Efficiency**: Multiple operators share VIP services instead of creating duplicates
2. ✅ **Automatic Discovery**: Operators automatically discover and attach to existing services
3. ✅ **Graceful Cleanup**: Services are only deleted when no operators are using them
4. ✅ **Cross-Cluster Support**: Operators in different clusters can coordinate through Tailscale VIP annotations
5. ✅ **Fault Tolerance**: Stale consumer cleanup prevents orphaned service references
6. ✅ **Event-Driven**: HTTPRoute changes trigger immediate reconciliation across all related gateways

### **Integration Status**

- ✅ **ServiceCoordinator Implementation**: Complete with service registry pattern
- ✅ **TailscaleGateway Controller Integration**: HTTPRoute processing and dynamic watching
- ✅ **Service Lifecycle Management**: Attachment, detachment, and cleanup logic
- ✅ **Metadata Management**: VIP service annotations for cross-operator coordination
- ✅ **RBAC Permissions**: Proper permissions for HTTPRoute and service management
- ✅ **Status Reporting**: ServiceInfo tracking in TailscaleGateway status

This multi-operator service coordination system successfully addresses the requirement: *"multiple operators in different clusters should be able to handle each other publishing services because if ones already published wouldn't we want to attach to that service instead?"*

## Development Commands

```bash
# Lint and type check (run these before committing)
make lint
make test

# Generate manifests after CRD changes
make manifests

# Build and test locally
make docker-build
make deploy
```

# important-instruction-reminders
Do what has been asked; nothing more, nothing less.
NEVER create files unless they're absolutely necessary for achieving your goal.
ALWAYS prefer editing an existing file to creating a new one.
NEVER proactively create documentation files (*.md) or README files. Only create documentation files if explicitly requested by the User.