---
id: tailscale-gateway
title: TailscaleGateway
sidebar_position: 2
---

# TailscaleGateway API Reference

The `TailscaleGateway` resource defines the main integration between Envoy Gateway and Tailscale networks, supporting multiple tailnets and advanced routing configurations.

## Overview

`TailscaleGateway` allows you to:
- Connect Kubernetes clusters to multiple Tailscale networks
- Configure automatic route generation for services
- Enable cross-cluster service coordination
- Define advanced routing policies and load balancing

## API Version

```yaml
apiVersion: gateway.tailscale.com/v1alpha1
kind: TailscaleGateway
```

## Basic Example

```yaml
apiVersion: gateway.tailscale.com/v1alpha1
kind: TailscaleGateway
metadata:
  name: production-gateway
  namespace: production
spec:
  gatewayRef:
    kind: Gateway
    name: envoy-gateway
    namespace: envoy-gateway-system
  tailnets:
    - name: main-tailnet
      tailscaleTailnetRef:
        kind: TailscaleTailnet
        name: main-tailnet
      routeGeneration:
        ingress:
          enabled: true
          hostPattern: "&#123;service&#125;.company.ts.net"
```

## Specification

### TailscaleGatewaySpec

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `gatewayRef` | LocalPolicyTargetReference | Yes | Reference to Gateway API Gateway resource |
| `tailnets` | []TailnetConfiguration | Yes | List of tailnet configurations |
| `serviceCoordination` | ServiceCoordinationConfig | No | Cross-cluster service coordination settings |
| `loadBalancing` | LoadBalancingConfig | No | Global load balancing configuration |
| `security` | SecurityConfig | No | Security and access control settings |
| `observability` | ObservabilityConfig | No | Monitoring and tracing configuration |

### TailnetConfiguration

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | Yes | Unique name for this tailnet configuration |
| `tailscaleTailnetRef` | LocalPolicyTargetReference | Yes | Reference to TailscaleTailnet resource |
| `routeGeneration` | RouteGenerationConfig | No | Automatic route generation settings |
| `priority` | int32 | No | Priority for multi-tailnet routing (default: 100) |

### RouteGenerationConfig

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `ingress` | IngressRouteConfig | No | Ingress route generation settings |
| `egress` | EgressRouteConfig | No | Egress route generation settings |

### IngressRouteConfig

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `enabled` | bool | No | Enable automatic ingress route generation (default: false) |
| `hostPattern` | string | No | Host pattern template (e.g., "&#123;service&#125;.company.ts.net") |
| `pathPrefix` | string | No | Default path prefix for routes (default: "/") |
| `defaultBackend` | BackendConfig | No | Default backend configuration |
| `filters` | []HTTPRouteFilter | No | Default filters to apply to generated routes |

### EgressRouteConfig

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `enabled` | bool | No | Enable automatic egress route generation (default: false) |
| `hostPattern` | string | No | Host pattern for external targets |
| `pathPrefix` | string | No | Path prefix for egress routes |
| `rewriteRules` | []RewriteRule | No | URL rewrite rules for egress traffic |
| `timeout` | string | No | Default timeout for egress requests |
| `retryPolicy` | RetryPolicy | No | Retry policy for failed requests |

## Usage Examples

### Multi-Tailnet Gateway

```yaml
apiVersion: gateway.tailscale.com/v1alpha1
kind: TailscaleGateway
metadata:
  name: multi-tailnet-gateway
  namespace: production
spec:
  gatewayRef:
    kind: Gateway
    name: envoy-gateway
    namespace: envoy-gateway-system
  
  tailnets:
    # Production tailnet
    - name: prod-tailnet
      tailscaleTailnetRef:
        kind: TailscaleTailnet
        name: prod-tailnet
      priority: 100
      routeGeneration:
        ingress:
          enabled: true
          hostPattern: "&#123;service&#125;.prod.company.ts.net"
          pathPrefix: "/"
        egress:
          enabled: true
          hostPattern: "&#123;service&#125;.prod.internal"
          pathPrefix: "/prod/&#123;service&#125;/"
          timeout: "30s"
    
    # Staging tailnet
    - name: staging-tailnet
      tailscaleTailnetRef:
        kind: TailscaleTailnet
        name: staging-tailnet
      priority: 50
      routeGeneration:
        ingress:
          enabled: true
          hostPattern: "&#123;service&#125;.staging.company.ts.net"
          pathPrefix: "/staging/"
  
  # Service coordination for cross-cluster deployments
  serviceCoordination:
    enabled: true
    strategy: "shared"
  
  # Global load balancing
  loadBalancing:
    strategy: "round_robin"
    healthChecking:
      enabled: true
      interval: "30s"
      timeout: "10s"
```

### High Availability Configuration

```yaml
apiVersion: gateway.tailscale.com/v1alpha1
kind: TailscaleGateway
metadata:
  name: ha-gateway
  namespace: production
spec:
  gatewayRef:
    kind: Gateway
    name: envoy-gateway
    namespace: envoy-gateway-system
  
  tailnets:
    - name: primary-tailnet
      tailscaleTailnetRef:
        kind: TailscaleTailnet
        name: primary-tailnet
      routeGeneration:
        ingress:
          enabled: true
          hostPattern: "&#123;service&#125;.company.ts.net"
          defaultBackend:
            weight: 100
            healthCheck:
              enabled: true
              interval: "15s"
  
  # Security configuration
  security:
    tlsMode: "strict"
    mtls:
      enabled: true
      certificateRef:
        name: "gateway-mtls-cert"
        namespace: "tailscale-gateway-system"
    accessControl:
      allowedTags:
        - "prod:server"
        - "enterprise:user"
      deniedTags:
        - "dev:testing"
  
  # Observability
  observability:
    metrics:
      enabled: true
      port: 9090
    tracing:
      enabled: true
      samplingRate: 0.1
      jaegerEndpoint: "http://jaeger-collector:14268/api/traces"
    logging:
      level: "info"
      accessLogs: true
```

### Advanced Routing Configuration

```yaml
apiVersion: gateway.tailscale.com/v1alpha1
kind: TailscaleGateway
metadata:
  name: advanced-routing-gateway
  namespace: production
spec:
  gatewayRef:
    kind: Gateway
    name: envoy-gateway
    namespace: envoy-gateway-system
  
  tailnets:
    - name: main-tailnet
      tailscaleTailnetRef:
        kind: TailscaleTailnet
        name: main-tailnet
      routeGeneration:
        ingress:
          enabled: true
          hostPattern: "&#123;service&#125;.company.ts.net"
          pathPrefix: "/"
          filters:
            - type: "RequestHeaderModifier"
              requestHeaderModifier:
                add:
                  - name: "X-Gateway-Version"
                    value: "v1.0.0"
                  - name: "X-Request-ID"
                    value: "&#123;request_id&#125;"
        
        egress:
          enabled: true
          hostPattern: "&#123;service&#125;.internal.company.com"
          pathPrefix: "/api/&#123;service&#125;/"
          rewriteRules:
            - from: "/api/&#123;service&#125;/"
              to: "/"
          timeout: "30s"
          retryPolicy:
            numRetries: 3
            perTryTimeout: "10s"
            backoffBaseInterval: "1s"
            backoffMaxInterval: "10s"
  
  # Advanced load balancing
  loadBalancing:
    strategy: "least_request"
    healthChecking:
      enabled: true
      interval: "15s"
      timeout: "5s"
      healthyThreshold: 2
      unhealthyThreshold: 3
    
    # Circuit breaker configuration
    circuitBreaker:
      enabled: true
      maxConnections: 1000
      maxPendingRequests: 100
      maxRequests: 1000
      maxRetries: 3
      consecutiveErrors: 5
      interval: "30s"
      baseEjectionTime: "30s"
      maxEjectionPercent: 50
```

## Status

The `TailscaleGateway` resource provides comprehensive status information:

```yaml
status:
  conditions:
    - type: "Ready"
      status: "True"
      reason: "GatewayConfigured"
      message: "Gateway is configured and accepting traffic"
    - type: "TailnetsReady"
      status: "True"
      reason: "AllTailnetsConnected"
      message: "All configured tailnets are connected"
  
  tailnets:
    - name: "main-tailnet"
      status: "Connected"
      lastConnected: "2024-01-15T10:30:00Z"
      servicesCount: 15
      routesGenerated: 25
    
  serviceInfo:
    totalServices: 15
    healthyServices: 14
    unhealthyServices: 1
    
  gatewayInfo:
    listenerCount: 2
    routeCount: 25
    lastReconciled: "2024-01-15T10:35:00Z"
```

## Validation Rules

### Gateway Reference
- Must reference an existing Gateway API Gateway resource
- Gateway must be in the same cluster
- Namespace reference must exist if specified

### Tailnet Configuration
- At least one tailnet must be configured
- Tailnet names must be unique within the gateway
- TailscaleTailnet references must exist

### Route Generation
- Host patterns must be valid DNS templates
- Path prefixes must start with "/"
- Rewrite rules must have valid from/to patterns

## Best Practices

### 1. **Multi-Tailnet Organization**
```yaml
# Organize by environment
tailnets:
  - name: production
    priority: 100
  - name: staging
    priority: 50
  - name: development
    priority: 10
```

### 2. **Security Configuration**
```yaml
security:
  tlsMode: "strict"  # Always use strict TLS in production
  accessControl:
    allowedTags: ["prod:server"]  # Restrict access to specific tags
```

### 3. **Observability Setup**
```yaml
observability:
  metrics:
    enabled: true
  tracing:
    enabled: true
    samplingRate: 0.1  # 10% sampling for production
  logging:
    level: "info"
    accessLogs: true
```

### 4. **Health Checking**
```yaml
loadBalancing:
  healthChecking:
    enabled: true
    interval: "30s"  # Appropriate for production workloads
    timeout: "10s"
    healthyThreshold: 2
    unhealthyThreshold: 3
```

## Troubleshooting

### Common Issues

#### 1. **Gateway Not Ready**
```bash
# Check gateway status
kubectl describe tailscalegateway production-gateway

# Check referenced Gateway resource
kubectl describe gateway envoy-gateway -n envoy-gateway-system
```

#### 2. **Tailnet Connection Issues**
```bash
# Check TailscaleTailnet status
kubectl get tailscaletailnets

# Check operator logs
kubectl logs -n tailscale-gateway-system deployment/tailscale-gateway-operator
```

#### 3. **Route Generation Problems**
```bash
# Check generated routes
kubectl get httproutes,tcproutes -A

# Check integrated extension server logs (within operator)
kubectl logs -n tailscale-gateway-system deployment/tailscale-gateway-operator -c manager
```

## Related Resources

- **[TailscaleEndpoints](./tailscale-endpoints)** - Service endpoint definitions
- **[TailscaleRoutePolicy](./tailscale-route-policy)** - Advanced routing policies
- **[TailscaleTailnet](./tailscale-tailnet)** - Tailnet configuration
- **[Configuration Guide](../configuration/overview)** - Advanced configuration options