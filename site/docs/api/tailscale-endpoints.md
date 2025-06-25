---
id: tailscale-endpoints
title: TailscaleEndpoints
sidebar_position: 1
---

# TailscaleEndpoints API Reference

The `TailscaleEndpoints` resource defines a collection of Tailscale endpoints that can be used as backends in Gateway API routes.

## Overview

`TailscaleEndpoints` allows you to:
- Define external services accessible through Tailscale
- Configure health checks for endpoint monitoring
- Enable service discovery across clusters
- Support multiple protocols (HTTP, TCP, UDP, TLS, gRPC)

## API Version

```yaml
apiVersion: gateway.tailscale.com/v1alpha1
kind: TailscaleEndpoints
```

## Basic Example

```yaml
apiVersion: gateway.tailscale.com/v1alpha1
kind: TailscaleEndpoints
metadata:
  name: web-services
  namespace: default
spec:
  tailnet: "company.ts.net"
  endpoints:
    - name: api-server
      tailscaleIP: "100.64.0.10"
      tailscaleFQDN: "api-server.company.ts.net"
      port: 8080
      protocol: "HTTP"
      externalTarget: "api.internal.company.com:8080"
      healthCheck:
        enabled: true
        path: "/health"
        interval: "30s"
```

## Specification

### TailscaleEndpointsSpec

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `tailnet` | string | Yes | The Tailscale tailnet domain (e.g., "company.ts.net") |
| `endpoints` | []TailscaleEndpoint | Yes | List of endpoint definitions |
| `serviceDiscovery` | ServiceDiscoveryConfig | No | Configuration for VIP service discovery |
| `tagSelectors` | []TagSelector | No | Tag-based endpoint discovery configuration |

### TailscaleEndpoint

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | Yes | Unique endpoint name (DNS-compliant) |
| `tailscaleIP` | string | Yes | Tailscale IP address (100.x.x.x) |
| `tailscaleFQDN` | string | Yes | Fully qualified domain name in tailnet |
| `port` | int32 | Yes | Port number (1-65535) |
| `protocol` | string | Yes | Protocol: HTTP, HTTPS, TCP, UDP, TLS, gRPC |
| `externalTarget` | string | No | External backend target (hostname:port) |
| `healthCheck` | HealthCheckConfig | No | Health check configuration |
| `metadata` | map[string]string | No | Additional metadata labels |

### HealthCheckConfig

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `enabled` | bool | No | Enable health checking (default: false) |
| `type` | string | No | Health check type: HTTP, TCP, UDP (default: HTTP) |
| `path` | string | No | HTTP path for health checks (default: "/") |
| `interval` | string | No | Check interval (default: "30s") |
| `timeout` | string | No | Request timeout (default: "10s") |
| `healthyThreshold` | int32 | No | Consecutive successes to mark healthy (default: 2) |
| `unhealthyThreshold` | int32 | No | Consecutive failures to mark unhealthy (default: 3) |
| `expectedStatus` | int32 | No | Expected HTTP status code (default: 200) |
| `headers` | map[string]string | No | Custom HTTP headers |

### ServiceDiscoveryConfig

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `vipServices` | []VIPServiceConfig | No | VIP service configuration |
| `autoRegister` | bool | No | Automatically register as VIP service (default: true) |

### VIPServiceConfig

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | Yes | VIP service name |
| `port` | int32 | Yes | VIP service port |
| `targetEndpoint` | string | Yes | Target endpoint name |

### TagSelector

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `key` | string | Yes | Tag key to match |
| `operator` | string | Yes | Operator: In, NotIn, Exists, DoesNotExist |
| `values` | []string | No | Values to match (required for In/NotIn) |

## Usage Examples

### Multi-Protocol Endpoints

```yaml
apiVersion: gateway.tailscale.com/v1alpha1
kind: TailscaleEndpoints
metadata:
  name: backend-services
  namespace: production
spec:
  tailnet: "company.ts.net"
  endpoints:
    # HTTP API
    - name: user-api
      tailscaleIP: "100.64.0.10"
      tailscaleFQDN: "user-api.company.ts.net"
      port: 8080
      protocol: "HTTP"
      externalTarget: "users.internal.company.com:8080"
      healthCheck:
        enabled: true
        path: "/health"
        interval: "30s"
    
    # Database (TCP)
    - name: postgres
      tailscaleIP: "100.64.0.11"
      tailscaleFQDN: "postgres.company.ts.net"
      port: 5432
      protocol: "TCP"
      externalTarget: "postgres.internal.company.com:5432"
      healthCheck:
        enabled: true
        type: "TCP"
        interval: "60s"
    
    # gRPC Service
    - name: grpc-service
      tailscaleIP: "100.64.0.12"
      tailscaleFQDN: "grpc.company.ts.net"
      port: 9090
      protocol: "gRPC"
      externalTarget: "grpc.internal.company.com:9090"
      healthCheck:
        enabled: true
        type: "HTTP"
        path: "/grpc.health.v1.Health/Check"
```

### Tag-Based Discovery

```yaml
apiVersion: gateway.tailscale.com/v1alpha1
kind: TailscaleEndpoints
metadata:
  name: auto-discovery
  namespace: default
spec:
  tailnet: "company.ts.net"
  tagSelectors:
    - key: "env"
      operator: "In"
      values: ["production", "staging"]
    - key: "service-type"
      operator: "Exists"
  serviceDiscovery:
    autoRegister: true
    vipServices:
      - name: "web-cluster"
        port: 80
        targetEndpoint: "web-service"
```

### Advanced Health Checks

```yaml
apiVersion: gateway.tailscale.com/v1alpha1
kind: TailscaleEndpoints
metadata:
  name: monitored-services
  namespace: default
spec:
  tailnet: "company.ts.net"
  endpoints:
    - name: api-service
      tailscaleIP: "100.64.0.20"
      tailscaleFQDN: "api.company.ts.net"
      port: 8080
      protocol: "HTTP"
      externalTarget: "api.backend.com:8080"
      healthCheck:
        enabled: true
        type: "HTTP"
        path: "/api/v1/health"
        interval: "15s"
        timeout: "5s"
        healthyThreshold: 3
        unhealthyThreshold: 2
        expectedStatus: 200
        headers:
          "Authorization": "Bearer health-check-token"
          "User-Agent": "TailscaleGateway/v1.0"
```

## Gateway API Integration

Use `TailscaleEndpoints` as backends in Gateway API routes:

### HTTPRoute Example

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: api-route
  namespace: default
spec:
  parentRefs:
    - name: envoy-gateway
  hostnames:
    - "api.company.ts.net"
  rules:
    - matches:
        - path:
            type: PathPrefix
            value: "/api/"
      backendRefs:
        - group: gateway.tailscale.com
          kind: TailscaleEndpoints
          name: web-services
          port: 8080
```

### TCPRoute Example

```yaml
apiVersion: gateway.networking.k8s.io/v1alpha2
kind: TCPRoute
metadata:
  name: database-route
  namespace: default
spec:
  parentRefs:
    - name: envoy-gateway
  rules:
    - backendRefs:
        - group: gateway.tailscale.com
          kind: TailscaleEndpoints
          name: backend-services
          port: 5432
```

## Status

The `TailscaleEndpoints` resource provides detailed status information:

```yaml
status:
  conditions:
    - type: "Ready"
      status: "True"
      reason: "EndpointsConfigured"
      message: "All endpoints are configured and healthy"
  endpoints:
    - name: "api-server"
      status: "Healthy"
      lastChecked: "2024-01-15T10:30:00Z"
      address: "100.64.0.10:8080"
    - name: "postgres"
      status: "Unhealthy"
      lastChecked: "2024-01-15T10:29:45Z"
      address: "100.64.0.11:5432"
      error: "Connection timeout"
  vipServices:
    - name: "web-cluster"
      status: "Registered"
      vipAddress: "100.100.100.10"
      lastUpdated: "2024-01-15T10:25:00Z"
```

## Validation Rules

### Endpoint Name
- Must be DNS-compliant (RFC 1123)
- No underscores allowed
- Maximum 63 characters
- Must be unique within the resource

### Port Range
- Must be between 1 and 65535
- Common ports: 80 (HTTP), 443 (HTTPS), 5432 (PostgreSQL), 3306 (MySQL)

### Protocol Support
- **HTTP/HTTPS**: Web services and APIs
- **TCP**: Databases, custom protocols
- **UDP**: DNS, real-time communications
- **TLS**: Encrypted TCP connections
- **gRPC**: High-performance RPC

### Health Check Types
- **HTTP**: Path-based health checks with status codes
- **TCP**: Connection-based health checks
- **UDP**: Echo-based health checks (limited)

## Best Practices

### 1. **Naming Convention**
```yaml
# Good: descriptive and environment-specific
name: prod-user-api

# Bad: generic names
name: service1
```

### 2. **Health Check Configuration**
```yaml
# Production: frequent checks with reasonable thresholds
healthCheck:
  enabled: true
  interval: "30s"
  timeout: "10s"
  healthyThreshold: 2
  unhealthyThreshold: 3

# Development: less frequent checks
healthCheck:
  enabled: true
  interval: "60s"
  timeout: "15s"
```

### 3. **Protocol Selection**
- Use **HTTP** for REST APIs and web services
- Use **TCP** for databases and persistent connections
- Use **gRPC** for high-performance microservices
- Use **UDP** for DNS and real-time applications

### 4. **Security Considerations**
- Always use HTTPS/TLS for production APIs
- Configure appropriate health check endpoints
- Use least-privilege access controls
- Monitor endpoint access patterns

## Migration Guide

### From Annotations to Native Support

**Old (annotation-based)**:
```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  annotations:
    gateway.tailscale.com/backend-type: "tailscale"
spec: # ...
```

**New (native backend)**:
```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
spec:
  rules:
    - backendRefs:
        - group: gateway.tailscale.com
          kind: TailscaleEndpoints
          name: web-services
```

### From Static to Dynamic Discovery

**Static Configuration**:
```yaml
spec:
  endpoints:
    - name: service-1
      tailscaleIP: "100.64.0.10"
    # ... manual endpoint definitions
```

**Dynamic Discovery**:
```yaml
spec:
  tagSelectors:
    - key: "service-type"
      operator: "Exists"
```

## Troubleshooting

### Common Issues

#### 1. **Endpoint Not Reachable**
```bash
# Check endpoint status
kubectl describe tailscaleendpoints web-services

# Verify Tailscale connectivity
kubectl exec -it deployment/tailscale-gateway-operator -- tailscale ping 100.64.0.10
```

#### 2. **Health Check Failures**
```bash
# Check health check configuration
kubectl get tailscaleendpoints web-services -o yaml

# Debug health check manually
curl -v http://100.64.0.10:8080/health
```

#### 3. **Gateway Route Not Working**
```bash
# Verify backendRef configuration
kubectl describe httproute api-route

# Check extension server logs
kubectl logs -n tailscale-gateway-system deployment/tailscale-gateway-extension-server
```

## Related Resources

- **[TailscaleGateway](../api/tailscale-gateway)** - Main gateway configuration
- **[TailscaleRoutePolicy](../api/tailscale-route-policy)** - Advanced routing policies
- **[TailscaleTailnet](../api/tailscale-tailnet)** - Tailnet configuration
- **[Configuration Guide](../configuration/overview)** - Advanced configuration options