# CLAUDE.md
*note for claude* you are allowed to update this as we make changes 
This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

The Tailscale Gateway Operator combines the power of [Tailscale](https://tailscale.com/) mesh networking with [Envoy Gateway](https://gateway.envoyproxy.io/) to provide a tailscale envoy control plane

## Architecture Overview

The operator follows a multi-layered architecture:

1. **Multi-Tailnet Manager**: Manages multiple Tailscale network connections with isolated state per tailnet
2. **Extension Server**: gRPC server that implements Envoy Gateway extension hooks for dynamic route injection
3. **Kubernetes Controllers**: Reconcile custom resources (TailscaleGateway, TailscaleRoutePolicy, TailscaleProxyGroup)

```
┌─────────────────┐    ┌──────────────────┐    ┌─────────────────────┐
│   Client        │────│   Envoy Gateway  │────│  Tailscale Gateway  │
│   Requests      │    │   (with routes   │    │      Operator       │
└─────────────────┘    │   injected by    │    └─────────────────────┘
                       │   extension)     │               │
                       └──────────────────┘               │
                                │                         │
                                │                         │
                       ┌──────────────────┐               │
                       │   Extension      │───────────────┘
                       │     Server       │
                       └──────────────────┘
                                │
                                │
                       ┌──────────────────┐
                       │   Multi-Tailnet  │
                       │     Manager      │
                       └──────────────────┘
                                │
                    ┌───────────┼───────────┐
                    │           │           │
            ┌───────▼────┐ ┌────▼────┐ ┌───▼─────┐
            │ Corp        │ │  Dev    │ │Staging  │
            │ Tailnet     │ │Tailnet  │ │Tailnet  │
            └────────────┘ └─────────┘ └─────────┘
```

## OAuth Authentication & API Access Patterns

### OAuth Client Credentials Flow
The operator uses OAuth 2.0 client credentials flow for Tailscale API authentication:

```go
credentials := clientcredentials.Config{
    ClientID:     string(clientID),
    ClientSecret: string(clientSecret),
    TokenURL:     "https://login.tailscale.com/api/v2/oauth/token",
}
c := tailscale.NewClient(defaultTailnet, nil)
c.UserAgent = "tailscale-k8s-operator"
c.HTTPClient = credentials.Client(ctx)
```

### Secret Management
- **Client credentials** stored in Kubernetes Secret (`operator-oauth`)
- **Files mounted** at `/oauth/client_id` and `/oauth/client_secret`
- **Environment variables** `CLIENT_ID_FILE` and `CLIENT_SECRET_FILE` point to mounted files
- **Auth key creation** via API for dynamic device registration

### Key Capabilities Pattern
```go
caps := tailscale.KeyCapabilities{
    Devices: tailscale.KeyDeviceCapabilities{
        Create: tailscale.KeyDeviceCreateCapabilities{
            Reusable:      false,
            Preauthorized: true,
            Tags:          strings.Split(operatorTags, ","),
        },
    },
}
authkey, _, err := tsc.CreateKey(ctx, caps)
```

## ProxyGroup High Availability Architecture

### StatefulSet-Based Design
- **Ordered identity**: Each replica has predictable name (`<name>-0`, `<name>-1`, etc.)
- **Persistent state**: Each pod has dedicated state Secret
- **Default replicas**: 2 for HA, configurable via `.spec.replicas`
- **Owner references**: Proper cleanup with garbage collection

### Ingress vs Egress Types

**Ingress ProxyGroups:**
```go
// Run proxies in cert share mode to ensure only one TLS cert per HA Ingress
envs = append(envs, corev1.EnvVar{
    Name:  "TS_EXPERIMENTAL_CERT_SHARE",
    Value: "true",
})
```

**Egress ProxyGroups:**
```go
envs = append(envs, corev1.EnvVar{
    Name:  "TS_ENABLE_HEALTH_CHECK",
    Value: "true",
})
```

### Why Headless Services for Ingress HA

The operator uses headless services for ingress HA for several critical reasons:

1. **Direct Pod IP Access**: Headless services return all Pod IPs via DNS rather than a single cluster IP
2. **Load Distribution**: Clients can connect directly to individual proxy replicas
3. **Health Awareness**: EndpointSlices track Pod readiness, routing traffic only to ready replicas
4. **Cert Coordination**: `TS_EXPERIMENTAL_CERT_SHARE=true` prevents multiple TLS cert issuance
5. **Service Discovery**: DNS resolution provides all available endpoints for client-side load balancing

```go
// ExternalName Service points to headless service for egress proxies
clusterDomain := retrieveClusterDomain(a.tsNamespace, logger)
headlessSvcName := hsvc.Name + "." + hsvc.Namespace + ".svc." + clusterDomain
svc.Spec.ExternalName = headlessSvcName
svc.Spec.Type = corev1.ServiceTypeExternalName
```

### Graceful Failover Mechanisms

**Health Check Endpoints:**
- **Local health check** accessible on Pod IP:9002
- **Pre-stop hooks** for egress services ensure traffic drains before termination
- **HEP pings calculation**: `replicas * 3` pings to ensure all backends are tested

**Lifecycle Management:**
```go
c.Lifecycle = &corev1.Lifecycle{
    PreStop: &corev1.LifecycleHandler{
        HTTPGet: &corev1.HTTPGetAction{
            Path: kubetypes.EgessServicesPreshutdownEP,
            Port: intstr.FromInt(defaultLocalAddrPort),
        },
    },
}
```

## Extension Server Implementation (xDS Hooks)

### gRPC Service Interface
The extension server implements the `EnvoyGatewayExtension` gRPC service:

```protobuf
service EnvoyGatewayExtension {
    rpc PostRouteModify(PostRouteModifyRequest) returns (PostRouteModifyResponse);
    rpc PostVirtualHostModify(PostVirtualHostModifyRequest) returns (PostVirtualHostModifyResponse);
    rpc PostHTTPListenerModify(PostHTTPListenerModifyRequest) returns (PostHTTPListenerModifyResponse);
    rpc PostTranslateModify(PostTranslateModifyRequest) returns (PostTranslateModifyResponse);
}
```

### Hook Types and Usage

**Route Modification Hook:**
- **Purpose**: Modify individual routes generated by Envoy Gateway
- **Use case**: Configure TypedPerFilterConfig for ext_authz filters
- **Context**: Receives extension resources and hostnames
- **Execution**: Only on routes from HTTPRoutes using extension resources

**VirtualHost Modification Hook:**
- **Purpose**: Modify VirtualHosts or inject entirely new routes
- **Use case**: Add tailnet-specific routing rules
- **Execution**: Always executed when extension is loaded

**Translation Modification Hook:**
- **Purpose**: Modify clusters and secrets in xDS config
- **Use case**: Inject clusters for ext_authz or custom upstreams
- **Scope**: Full control over final xDS resources

### Extension Resource Watching
- **Dynamic watches**: Envoy Gateway watches extension CRDs automatically
- **Resource passing**: Extension resources sent as Unstructured to hooks
- **Reconciliation**: Changes trigger full gateway reconfiguration
- **Race condition prevention**: Central watching eliminates sync issues

## Kubernetes API Patterns

### CRD Structure
Following Tailscale k8s-operator patterns:
- All CRDs implement required Kubernetes interfaces via generated DeepCopy methods
- Use kubebuilder markers for CRD generation
- Custom LocalPolicyTargetReference type for Gateway API compatibility

### Controller Runtime Patterns
- **Field indexing** for efficient cross-resource queries
- **Event handlers** with MapFunc for complex resource relationships
- **Owner references** for proper cleanup in multi-cluster setups
- **Finalizers** for safe resource deletion
- **Status conditions** following Kubernetes conventions

### Resource Types
1. **TailscaleTailnet**: Defines a tailnet with secret mapping
1. **TailscaleGateway**: Main integration with Envoy Gateway, supports multiple tailnets
2. **TailscaleRoutePolicy**: Advanced routing policies with conditions and actions  
3. **TailscaleProxyGroup**: High-availability proxy deployments

## Multi-Cluster and Multi-Tailnet Support

### Owner Reference Management
- **Operator identification**: Each operator instance has unique stable ID
- **Multi-cluster cleanup**: Resources only deleted if no other owner refs exist
- **Tailscale Service sharing**: Multiple operators can reference same service
- **Annotation-based tracking**: Owner references stored in service annotations

### State Isolation
- **Per-tailnet state**: Separate Secrets for each tailnet connection
- **Config versioning**: Capability-versioned configs for compatibility
- **Auth key management**: Separate auth keys per proxy instance
- **Device registration**: Each proxy registers as distinct tailnet device

## OAuth Credential Validation Pattern

### TailscaleTailnet Validation Strategy
When a TailscaleTailnet resource is created, the operator validates OAuth credentials using the following pattern:

1. **OAuth Client Setup**:
```go
config := tailscale.ClientConfig{
    Tailnet:      tailnetName,
    APIBaseURL:   tailscale.DefaultAPIBaseURL,
    ClientID:     clientID,
    ClientSecret: clientSecret,
}
tsClient, err := tailscale.NewClient(ctx, config)
```

2. **Auth Key Creation for Validation**:
```go
caps := tailscaleclient.KeyCapabilities{}
caps.Devices.Create.Reusable = false      // Single use
caps.Devices.Create.Ephemeral = true      // Auto-cleanup
caps.Devices.Create.Preauthorized = true  // Skip approval
caps.Devices.Create.Tags = []string{"tag:k8s-operator"}

keyMeta, err := tsClient.CreateKey(ctx, caps)
```

3. **Immediate Cleanup**:
- Validation auth keys are deleted immediately after successful creation
- Ephemeral keys auto-expire as backup cleanup mechanism
- Only key metadata (ID) is logged, never the actual key secret

### Error Handling Strategy
- **401 Unauthorized**: Invalid OAuth client credentials
- **403 Forbidden**: Valid credentials but insufficient permissions
- **API Errors**: Wrapped with context for debugging
- **Retry Logic**: 5-minute requeue on validation failures

### Status Conditions
TailscaleTailnet resources maintain three key status conditions:
- **TailnetAuthenticated**: OAuth credentials are valid
- **TailnetValidated**: Configuration is valid
- **TailnetReady**: Tailnet connection is operational

## Critical Architecture Decisions

### Why OAuth Client Credentials Over API Keys
1. **Scoped Permissions**: OAuth clients can have limited, specific permissions
2. **Renewable**: OAuth tokens can be refreshed without manual intervention
3. **Auditable**: OAuth usage is tracked in Tailscale admin console
4. **Secure**: No long-lived API keys stored in cluster
5. **Standardized**: Industry-standard OAuth 2.0 client credentials flow

### Auth Key Validation Benefits
1. **Permission Verification**: Ensures OAuth client can create auth keys
2. **Tailnet Access**: Validates access to the specified tailnet
3. **Tag Validation**: Verifies tag permissions are configured correctly
4. **Early Failure**: Catches configuration issues before proxy deployment

### Resource Lifecycle Management
1. **Finalizers**: Ensure proper cleanup on resource deletion
2. **Owner References**: Enable garbage collection of dependent resources
3. **Status Tracking**: Comprehensive status reporting for operators
4. **Retry Strategy**: Exponential backoff with circuit breaker patterns

### Performance Considerations
- **Validation Caching**: OAuth validation results cached per sync interval (5 minutes)
- **Batch Operations**: Multiple auth key operations batched when possible
- **Resource Efficiency**: Minimal API calls during normal operation
- **Error Recovery**: Fast failure detection with immediate retry on transient errors

## Tested OAuth Integration

### Successful Validation Test Results
The OAuth credential validation has been successfully tested with the following results:

```
Testing OAuth credentials:
Client ID: kcSBZN5XmF11CNTRL
Creating Tailscale client...
✓ Client created successfully
Testing OAuth credential validation...
1. Testing device list access...
   ✓ Successfully listed 15 devices
2. Testing auth key creation (validation pattern)...
   ✓ Successfully created validation auth key with ID: kJ6m9Y1LyQ11CNTRL
3. Cleaning up validation auth key...
   ✓ Successfully cleaned up validation auth key: kJ6m9Y1LyQ11CNTRL

OAuth validation test completed.
```

### Key Validation Capabilities Confirmed
- ✅ **OAuth Client Credentials Flow**: Successfully authenticates with Tailscale API
- ✅ **Device API Access**: Can list devices in the tailnet (15 devices discovered)
- ✅ **Auth Key Creation**: Can create ephemeral auth keys for validation
- ✅ **Auth Key Cleanup**: Can delete created auth keys for proper resource management
- ✅ **Error Handling**: Proper error detection for invalid credentials (tested with previous invalid keys)

### Integration Status
The TailscaleTailnet controller is ready for production use with proper OAuth credential validation. The auth key creation pattern successfully validates:
1. **OAuth credential validity** 
2. **API permissions** for device and key management
3. **Tailnet access** permissions
4. **Tag assignment** capabilities (tag:k8s-operator)

### Next Steps for Full Integration
1. ✅ **Deploy the operator in a Kubernetes cluster** - Successfully tested in Kind cluster
2. ✅ **Apply TailscaleTailnet resources with valid OAuth secrets** - Fully working with status conditions
3. Implement the Extension Server for Envoy Gateway integration
4. Test end-to-end route injection functionality

## Production Testing Results (Kind Cluster)

### Successful Deployment Test
The operator has been successfully tested in a Kind cluster with the following results:

#### ✅ **Valid OAuth Credentials Test**
```yaml
apiVersion: gateway.tailscale.com/v1alpha1
kind: TailscaleTailnet
metadata:
  name: test-tailnet
spec:
  tailnet: "-"
  oauthSecretName: tailscale-oauth
  oauthSecretNamespace: default
  tags: ["tag:k8s-operator"]
```

**Results:**
- ✅ **OAuth Validation**: Successfully created and cleaned up validation auth keys
- ✅ **Status Conditions**: `Authenticated=True`, `Ready=True`
- ✅ **Event Logging**: `AuthenticationSucceeded` events recorded
- ✅ **Finalizer Management**: Proper resource lifecycle management
- ✅ **Continuous Reconciliation**: 5-minute sync interval working correctly

**Logs:**
```
Successfully created validation auth key with ID: kaJoqqaxRA11CNTRL
Successfully cleaned up validation auth key: kaJoqqaxRA11CNTRL
Successfully reconciled TailscaleTailnet
```

#### ✅ **Invalid OAuth Credentials Test**
**Results:**
- ✅ **Error Detection**: Properly identified 401 Unauthorized errors  
- ✅ **Event Logging**: `AuthenticationFailed` warning events
- ✅ **Retry Logic**: Exponential backoff retry on failures
- ✅ **Status Updates**: Proper condition updates on failure

**Error Handling:**
```
OAuth validation failed: failed to create validation auth key: 
Post "https://api.tailscale.com/api/v2/tailnet/-/keys": 
oauth2: cannot fetch token: 401 Unauthorized
Response: {"message":"API token invalid"}
```

### Deployment Architecture Validated
- ✅ **Container Image**: Successfully built and loaded into Kind cluster
- ✅ **RBAC Permissions**: Proper ClusterRole and ServiceAccount configuration
- ✅ **CRD Management**: Custom resources with OAuth specification working
- ✅ **Health Checks**: Liveness and readiness probes functional
- ✅ **Resource Management**: CPU/memory limits and requests configured

The TailscaleTailnet OAuth validation system is production-ready and successfully tested in a real Kubernetes environment.

## Automatic Tailnet Discovery

### Dynamic Metadata Collection
The operator now automatically discovers essential tailnet metadata instead of requiring manual configuration:

#### ✅ **Tailnet Domain Discovery**
```yaml
status:
  tailnetInfo:
    name: tail8eff9.ts.net        # Automatically discovered from device names
    organization: Personal        # Inferred from domain structure  
  conditions:
  - type: Validated
    status: "True"
    reason: Ready
    message: "Tailnet metadata discovered successfully"
```

#### **Discovery Process**
1. **OAuth Validation**: Validates credentials by creating ephemeral auth keys
2. **Device Enumeration**: Lists devices to extract tailnet domain from device names  
3. **Domain Extraction**: Parses MagicDNS domain from device hostnames (e.g., `hostname.tail8eff9.ts.net`)
4. **Organization Inference**: Determines if tailnet is Personal (`tail*`) or Organization (custom domain)
5. **Status Update**: Populates `tailnetInfo` with discovered metadata

#### **API Pattern**
```go
// Extract tailnet domain from device names
devices, err := client.Devices().List(ctx)
parts := strings.SplitN(devices[0].Name, ".", 2)
tailnetDomain := parts[1] // "tail8eff9.ts.net"
```

#### **Benefits**
- ✅ **Eliminates Manual Configuration**: No need to specify tailnet name in CRD spec
- ✅ **Uses Real Domain**: Gets actual `*.ts.net` domain instead of generic `-`
- ✅ **Organization Detection**: Automatically identifies Personal vs Organization tailnets
- ✅ **Status Visibility**: Provides tailnet metadata for operators and users
- ✅ **Gateway Integration Ready**: Real domain names enable proper route configuration

This automatic discovery ensures the operator works with any valid OAuth credentials without requiring users to know or configure their tailnet domain manually.

#### **Enhanced kubectl Output**
The CRD printer columns now display discovered tailnet information instead of spec values:

```bash
$ kubectl get tailscaletailnet
NAME           STATUS   TAILNET            ORGANIZATION
test-tailnet   Ready    tail8eff9.ts.net   Personal
```

**Columns:**
- **Status**: Ready condition status (`Ready`, `Pending`, `Failed`)
- **Tailnet**: Auto-discovered tailnet domain (e.g., `tail8eff9.ts.net`)
- **Organization**: Auto-inferred organization type (`Personal`, `Organization`)

This provides immediate visibility into the discovered tailnet metadata without needing to inspect the full resource status.
