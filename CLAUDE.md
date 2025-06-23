# CLAUDE.md
*note for claude* you are allowed to update this as we make changes 
This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

The Tailscale Gateway Operator combines the power of [Tailscale](https://tailscale.com/) mesh networking with [Envoy Gateway](https://gateway.envoyproxy.io/) to provide a cloud-native service mesh solution that enables secure access to external services through Tailscale networks.

## Architecture Overview

The operator follows a unified architecture combining the power of Tailscale mesh networking with Envoy Gateway for complete bidirectional traffic flow:

1. **Integrated Extension Server**: gRPC server running within the main operator for dynamic route and cluster injection
2. **TailscaleEndpoints Controller**: Manages service discovery, StatefulSet creation, and Service exposure
3. **HTTPRoute Discovery**: Automatic discovery and integration of Gateway API HTTPRoutes with Tailscale backends
4. **Service Mesh Integration**: Complete service discovery bridging Kubernetes Services and Tailscale networks

### Bidirectional Traffic Flow Architecture

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
                    └───────────┬───────────┘
                                │
          ┌─────────────────────┼─────────────────────┐
          │                     │                     │
    ┌─────▼──────┐    ┌────────▼────────┐    ┌───────▼─────┐
    │TailscaleEnd│    │   HTTPRoute     │    │   Service   │
    │  points    │    │   Discovery     │    │  Discovery  │
    │ + Services │    │   + Backend     │    │   + Mesh    │
    │(StatefulSets)│   │   Mapping       │    │ Integration │
    └────────────┘    └─────────────────┘    └─────────────┘
          │                     │                     │
┌─────────┼─────────┐           │           ┌─────────┼─────────┐
│         │         │           │           │         │         │
│  ┌──────▼──────┐  │     ┌─────▼─────┐     │  ┌──────▼──────┐  │
│  │   Ingress   │  │     │HTTPRoutes │     │  │ K8s Service │  │
│  │StatefulSet  │  │     │Backends   │     │  │  Backends   │  │
│  │+ Service    │  │     │           │     │  │             │  │
│  └─────────────┘  │     └───────────┘     │  └─────────────┘  │
│                   │                       │                   │
│  ┌─────────────┐  │                       │  ┌─────────────┐  │
│  │   Egress    │  │                       │  │ Tailscale   │  │
│  │StatefulSet  │  │                       │  │ Ingress     │  │
│  │+ Service    │  │                       │  │ Services    │  │
│  └─────────────┘  │                       │  └─────────────┘  │
└───────────────────┘                       └───────────────────┘
```

### Integrated Extension Server Architecture

- **Single Process**: Extension server runs within the main operator process
- **Shared State**: Direct access to controller caches and Kubernetes clients
- **HTTPRoute Awareness**: Discovers and processes Gateway API HTTPRoutes automatically
- **Service Discovery**: Maps HTTPRoute backends to Kubernetes Services and TailscaleEndpoints
- **Bidirectional Routes**: Supports both ingress (external → Tailscale) and egress (Tailscale → external) traffic

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

1. **Main Operator**: `tailscale-gateway:latest` (includes controllers)
2. **Integrated Extension Server**: `tailscale-gateway-extension-server:latest` (includes extension server + manager)

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

- **Integrated Extension Server**: `internal/extension/server.go` - Complete gRPC implementation with HTTPRoute discovery
- **Extension Server Main**: `cmd/extension-server/main.go` - Integrated entry point with controller-runtime manager
- **TailscaleEndpoints Controller**: `internal/controller/tailscaleendpoints_controller.go` - Service creation and StatefulSet management
- **TailscaleEndpoints CRD**: `api/v1alpha1/tailscaleendpoints_types.go` - With `externalTarget` field for Envoy integration
- **Extension Server RBAC**: `test/simple-extension-server.yaml` - RBAC for HTTPRoutes and Services access
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

## Tailscale StatefulSet and State Management Patterns

### **Learnings from Tailscale k8s-operator**

Based on analysis of the official Tailscale k8s-operator codebase, here are the key patterns for StatefulSet creation and state persistence that we follow:

### **State Storage Strategy**

The Tailscale k8s-operator uses a hybrid approach for state persistence:

```go
// Primary state storage: Kubernetes Secrets
args = append(args, "--state=kube:"+cfg.KubeSecret)
if cfg.StateDir == "" {
    cfg.StateDir = "/tmp"
}
args = append(args, "--statedir="+cfg.StateDir)
```

**Key State Management Principles:**
1. **Primary State Storage**: Kubernetes Secrets (`--state=kube:<secret-name>`)
2. **Temporary State Directory**: `/tmp` (default) for transient state
3. **TS_STATE_DIR**: Environment variable defaults to empty, falls back to `/tmp`
4. **Secret-based Persistence**: All persistent state stored in Kubernetes Secrets

### **StatefulSet Creation Patterns**

**Main Creation Logic** (from `sts.go`):
- Uses embedded YAML templates (`proxyYaml` and `userspaceProxyYaml`) as base configurations
- Two modes: userspace networking and privileged kernel networking
- StatefulSet names generated using `statefulSetNameBase()` to avoid Kubernetes name length limits

### **Volume Management Strategy**

**Configuration Volume Mounting:**
```go
configVolume := corev1.Volume{
    Name: "tailscaledconfig",
    VolumeSource: corev1.VolumeSource{
        Secret: &corev1.SecretVolumeSource{
            SecretName: proxySecret,
        },
    },
}
container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
    Name:      "tailscaledconfig",
    ReadOnly:  true,
    MountPath: "/etc/tsconfig",
})
```

**Multi-Tailnet Volume Strategy:**
For high-availability ProxyGroups, each replica gets its own configuration:
```go
for i := range pgReplicas(pg) {
    volumes = append(volumes, corev1.Volume{
        Name: fmt.Sprintf("tailscaledconfig-%d", i),
        VolumeSource: corev1.VolumeSource{
            Secret: &corev1.SecretVolumeSource{
                SecretName: pgConfigSecretName(pg.Name, i),
            },
        },
    })
}
```

### **Container Environment Configuration**

**Critical Environment Variables:**
```go
TS_KUBE_SECRET: Secret name for state storage
TS_STATE: "kube:$(POD_NAME)" for ProxyGroups
TS_EXPERIMENTAL_VERSIONED_CONFIG_DIR: "/etc/tsconfig"
TS_USERSPACE: "true" or "false" for networking mode
POD_IP, POD_NAME, POD_UID: Kubernetes Pod metadata
```

### **State Secret Management**

**Secret Structure:**
- **State Secrets**: Store persistent Tailscale daemon state
- **Config Secrets**: Store versioned tailscaled configuration files
- **Naming Pattern**: `<name>-<replica-index>` for ProxyGroups, `<service-name>-0` for single proxies

**State Consistency Checking:**
```go
func hasConsistentState(d map[string][]byte) bool {
    var (
        _, hasCurrent = d[string(ipn.CurrentProfileStateKey)]
        _, hasKnown   = d[string(ipn.KnownProfilesStateKey)]
        _, hasMachine = d[string(ipn.MachineKeyStateKey)]
        hasProfile    bool
    )
    // Validates complete state is present
}
```

### **Implementation Patterns for Tailscale Gateway Operator**

**1. Secret-based State Storage Pattern:**
```go
// For each tailnet connection
TS_KUBE_SECRET: "<tailnet-specific-secret-name>"
TS_EXPERIMENTAL_VERSIONED_CONFIG_DIR: "/etc/tsconfig/<tailnet-name>"
POD_NAME: Used in secret naming for ProxyGroups
POD_UID: For state validation
```

**2. Multi-Tailnet Isolation:**
- Each tailnet connection gets isolated state secrets
- Separate configuration volumes per tailnet
- Pod naming includes tailnet identifier for state isolation

**3. Volume Mount Strategy:**
- Mount config secrets read-only to `/etc/tsconfig`
- Use `TS_EXPERIMENTAL_VERSIONED_CONFIG_DIR` to point to config mount path
- Implement graceful shutdown hooks to ensure state consistency

**4. Critical Environment Variables for Multi-Tailnet:**
```yaml
env:
- name: TS_KUBE_SECRET
  value: "tailnet-<tailnet-name>-<pod-name>"
- name: TS_EXPERIMENTAL_VERSIONED_CONFIG_DIR
  value: "/etc/tsconfig/<tailnet-name>"
- name: POD_NAME
  valueFrom:
    fieldRef:
      fieldPath: metadata.name
- name: POD_UID
  valueFrom:
    fieldRef:
      fieldPath: metadata.uid
```

This pattern ensures reliable state persistence, proper isolation between tailnet connections, and follows Kubernetes best practices for StatefulSet management established by the official Tailscale k8s-operator.

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