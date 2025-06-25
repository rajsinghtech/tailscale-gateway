---
id: multi-protocol
title: Multi-Protocol Examples
sidebar_position: 2
---

# Multi-Protocol Examples

This guide demonstrates advanced configurations for different protocols supported by Tailscale Gateway.

## Protocol Support Overview

Tailscale Gateway supports multiple protocols for different types of services:

| Protocol | Use Cases | Gateway API Resource | Features |
|----------|-----------|---------------------|----------|
| **HTTP** | REST APIs, web applications | HTTPRoute | Path routing, header manipulation |
| **HTTPS** | Secure web services | HTTPRoute | TLS termination, certificate management |
| **TCP** | Databases, custom protocols | TCPRoute | Load balancing, health checks |
| **UDP** | DNS, real-time communications | UDPRoute | Stateless routing |
| **TLS** | Encrypted TCP connections | TLSRoute | SNI routing, certificate passthrough |
| **gRPC** | High-performance RPC | GRPCRoute | Method routing, streaming support |

## HTTP/HTTPS Services

### Secure REST API with TLS

```yaml title="https-api-example.yaml"
---
apiVersion: gateway.tailscale.com/v1alpha1
kind: TailscaleEndpoints
metadata:
  name: secure-apis
  namespace: production
spec:
  tailnet: "company.ts.net"
  endpoints:
    - name: auth-api
      tailscaleIP: "100.64.0.10"
      tailscaleFQDN: "auth.company.ts.net"
      port: 443
      protocol: "HTTPS"
      externalTarget: "auth.internal.company.com:443"
      healthCheck:
        enabled: true
        type: "HTTP"
        path: "/health"
        interval: "30s"
        timeout: "10s"
        headers:
          "Authorization": "Bearer health-token"
      metadata:
        tls: "terminate"
        cert-manager: "letsencrypt"

---
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: auth-api-route
  namespace: production
spec:
  parentRefs:
    - name: envoy-gateway
      namespace: envoy-gateway-system
  hostnames:
    - "auth.company.ts.net"
  rules:
    - matches:
        - path:
            type: PathPrefix
            value: "/api/v1/"
      backendRefs:
        - group: gateway.tailscale.com
          kind: TailscaleEndpoints
          name: secure-apis
          port: 443
      filters:
        - type: RequestHeaderModifier
          requestHeaderModifier:
            add:
              - name: "X-Forwarded-Proto"
                value: "https"
              - name: "X-Request-ID"
                value: "{request_id}"
```

### Web Application with Path-Based Routing

```yaml title="web-app-routing.yaml"
---
apiVersion: gateway.tailscale.com/v1alpha1
kind: TailscaleEndpoints
metadata:
  name: web-applications
  namespace: default
spec:
  tailnet: "company.ts.net"
  endpoints:
    # Frontend application
    - name: frontend
      tailscaleIP: "100.64.0.20"
      tailscaleFQDN: "app.company.ts.net"
      port: 3000
      protocol: "HTTP"
      externalTarget: "frontend.internal.company.com:3000"
      healthCheck:
        enabled: true
        path: "/"
        interval: "60s"
      metadata:
        app: "frontend"
        version: "v2.1.0"

    # Admin dashboard
    - name: admin
      tailscaleIP: "100.64.0.21"
      tailscaleFQDN: "admin.company.ts.net"
      port: 3001
      protocol: "HTTP"
      externalTarget: "admin.internal.company.com:3001"
      healthCheck:
        enabled: true
        path: "/admin/health"
        interval: "60s"
        headers:
          "X-Admin-Check": "true"

---
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: web-app-routes
  namespace: default
spec:
  parentRefs:
    - name: envoy-gateway
      namespace: envoy-gateway-system
  rules:
    # Main application
    - matches:
        - headers:
            - name: "Host"
              value: "app.company.ts.net"
        - path:
            type: PathPrefix
            value: "/"
      backendRefs:
        - group: gateway.tailscale.com
          kind: TailscaleEndpoints
          name: web-applications
          port: 3000
      filters:
        - type: ResponseHeaderModifier
          responseHeaderModifier:
            add:
              - name: "X-Frame-Options"
                value: "DENY"
              - name: "X-Content-Type-Options"
                value: "nosniff"
    
    # Admin routes with authentication
    - matches:
        - headers:
            - name: "Host"
              value: "admin.company.ts.net"
        - path:
            type: PathPrefix
            value: "/admin/"
      backendRefs:
        - group: gateway.tailscale.com
          kind: TailscaleEndpoints
          name: web-applications
          port: 3001
      filters:
        - type: RequestHeaderModifier
          requestHeaderModifier:
            add:
              - name: "X-Admin-Request"
                value: "true"
```

## TCP Services

### Database Cluster with Load Balancing

```yaml title="database-cluster.yaml"
---
apiVersion: gateway.tailscale.com/v1alpha1
kind: TailscaleEndpoints
metadata:
  name: database-cluster
  namespace: production
spec:
  tailnet: "company.ts.net"
  endpoints:
    # Primary database
    - name: postgres-primary
      tailscaleIP: "100.64.1.10"
      tailscaleFQDN: "db-primary.company.ts.net"
      port: 5432
      protocol: "TCP"
      externalTarget: "postgres-1.internal.company.com:5432"
      healthCheck:
        enabled: true
        type: "TCP"
        interval: "30s"
        timeout: "5s"
        healthyThreshold: 2
        unhealthyThreshold: 3
      metadata:
        role: "primary"
        weight: "100"

    # Read replica 1
    - name: postgres-replica1
      tailscaleIP: "100.64.1.11"
      tailscaleFQDN: "db-replica1.company.ts.net"
      port: 5432
      protocol: "TCP"
      externalTarget: "postgres-2.internal.company.com:5432"
      healthCheck:
        enabled: true
        type: "TCP"
        interval: "30s"
        timeout: "5s"
      metadata:
        role: "replica"
        weight: "50"

    # Read replica 2
    - name: postgres-replica2
      tailscaleIP: "100.64.1.12"
      tailscaleFQDN: "db-replica2.company.ts.net"
      port: 5432
      protocol: "TCP"
      externalTarget: "postgres-3.internal.company.com:5432"
      healthCheck:
        enabled: true
        type: "TCP"
        interval: "30s"
        timeout: "5s"
      metadata:
        role: "replica"
        weight: "50"

---
# Primary database route (writes)
apiVersion: gateway.networking.k8s.io/v1alpha2
kind: TCPRoute
metadata:
  name: postgres-primary-route
  namespace: production
spec:
  parentRefs:
    - name: envoy-gateway
      namespace: envoy-gateway-system
  rules:
    - backendRefs:
        - group: gateway.tailscale.com
          kind: TailscaleEndpoints
          name: database-cluster
          port: 5432
          weight: 100  # Primary only

---
# Read replica route (reads)
apiVersion: gateway.networking.k8s.io/v1alpha2
kind: TCPRoute
metadata:
  name: postgres-replica-route
  namespace: production
spec:
  parentRefs:
    - name: envoy-gateway-read
      namespace: envoy-gateway-system
  rules:
    - backendRefs:
        # Load balance between replicas
        - group: gateway.tailscale.com
          kind: TailscaleEndpoints
          name: database-cluster
          port: 5432
          weight: 50  # Replica 1
        - group: gateway.tailscale.com
          kind: TailscaleEndpoints
          name: database-cluster
          port: 5432
          weight: 50  # Replica 2
```

### Redis Cluster Configuration

```yaml title="redis-cluster.yaml"
---
apiVersion: gateway.tailscale.com/v1alpha1
kind: TailscaleEndpoints
metadata:
  name: redis-cluster
  namespace: caching
spec:
  tailnet: "company.ts.net"
  endpoints:
    # Redis master
    - name: redis-master
      tailscaleIP: "100.64.2.10"
      tailscaleFQDN: "redis-master.company.ts.net"
      port: 6379
      protocol: "TCP"
      externalTarget: "redis-master.internal.company.com:6379"
      healthCheck:
        enabled: true
        type: "TCP"
        interval: "15s"
        timeout: "3s"
      metadata:
        role: "master"
        cluster: "main"

    # Redis sentinels
    - name: redis-sentinel1
      tailscaleIP: "100.64.2.11"
      tailscaleFQDN: "redis-sentinel1.company.ts.net"
      port: 26379
      protocol: "TCP"
      externalTarget: "redis-sentinel-1.internal.company.com:26379"
      healthCheck:
        enabled: true
        type: "TCP"
        interval: "30s"
      metadata:
        role: "sentinel"

    - name: redis-sentinel2
      tailscaleIP: "100.64.2.12"
      tailscaleFQDN: "redis-sentinel2.company.ts.net"
      port: 26379
      protocol: "TCP"
      externalTarget: "redis-sentinel-2.internal.company.com:26379"
      healthCheck:
        enabled: true
        type: "TCP"
        interval: "30s"
      metadata:
        role: "sentinel"
```

## UDP Services

### DNS Server Configuration

```yaml title="dns-services.yaml"
---
apiVersion: gateway.tailscale.com/v1alpha1
kind: TailscaleEndpoints
metadata:
  name: dns-services
  namespace: infrastructure
spec:
  tailnet: "company.ts.net"
  endpoints:
    # Primary DNS
    - name: dns-primary
      tailscaleIP: "100.64.3.10"
      tailscaleFQDN: "dns1.company.ts.net"
      port: 53
      protocol: "UDP"
      externalTarget: "dns-1.internal.company.com:53"
      healthCheck:
        enabled: true
        type: "UDP"
        interval: "60s"
        timeout: "5s"
      metadata:
        type: "authoritative"
        zone: "company.com"

    # Secondary DNS
    - name: dns-secondary
      tailscaleIP: "100.64.3.11"
      tailscaleFQDN: "dns2.company.ts.net"
      port: 53
      protocol: "UDP"
      externalTarget: "dns-2.internal.company.com:53"
      healthCheck:
        enabled: true
        type: "UDP"
        interval: "60s"
        timeout: "5s"
      metadata:
        type: "authoritative"
        zone: "company.com"

---
apiVersion: gateway.networking.k8s.io/v1alpha2
kind: UDPRoute
metadata:
  name: dns-route
  namespace: infrastructure
spec:
  parentRefs:
    - name: envoy-gateway
      namespace: envoy-gateway-system
  rules:
    - backendRefs:
        - group: gateway.tailscale.com
          kind: TailscaleEndpoints
          name: dns-services
          port: 53
```

### Real-Time Gaming Services

```yaml title="gaming-services.yaml"
---
apiVersion: gateway.tailscale.com/v1alpha1
kind: TailscaleEndpoints
metadata:
  name: game-servers
  namespace: gaming
spec:
  tailnet: "gaming.ts.net"
  endpoints:
    # Game server 1
    - name: game-server-1
      tailscaleIP: "100.64.4.10"
      tailscaleFQDN: "game1.gaming.ts.net"
      port: 7777
      protocol: "UDP"
      externalTarget: "gameserver-1.internal.gaming.com:7777"
      healthCheck:
        enabled: true
        type: "UDP"
        interval: "30s"
        timeout: "2s"
      metadata:
        game: "shooter"
        region: "us-east"
        max-players: "64"

    # Game server 2
    - name: game-server-2
      tailscaleIP: "100.64.4.11"
      tailscaleFQDN: "game2.gaming.ts.net"
      port: 7777
      protocol: "UDP"
      externalTarget: "gameserver-2.internal.gaming.com:7777"
      healthCheck:
        enabled: true
        type: "UDP"
        interval: "30s"
        timeout: "2s"
      metadata:
        game: "shooter"
        region: "us-west"
        max-players: "64"
```

## gRPC Services

### Microservices with gRPC

```yaml title="grpc-services.yaml"
---
apiVersion: gateway.tailscale.com/v1alpha1
kind: TailscaleEndpoints
metadata:
  name: grpc-services
  namespace: microservices
spec:
  tailnet: "company.ts.net"
  endpoints:
    # User service
    - name: user-service
      tailscaleIP: "100.64.5.10"
      tailscaleFQDN: "user-grpc.company.ts.net"
      port: 9090
      protocol: "gRPC"
      externalTarget: "user-service.internal.company.com:9090"
      healthCheck:
        enabled: true
        type: "HTTP"
        path: "/grpc.health.v1.Health/Check"
        interval: "30s"
        headers:
          "Content-Type": "application/grpc"
      metadata:
        service: "user.v1.UserService"
        version: "v1.2.0"

    # Order service
    - name: order-service
      tailscaleIP: "100.64.5.11"
      tailscaleFQDN: "order-grpc.company.ts.net"
      port: 9091
      protocol: "gRPC"
      externalTarget: "order-service.internal.company.com:9091"
      healthCheck:
        enabled: true
        type: "HTTP"
        path: "/grpc.health.v1.Health/Check"
        interval: "30s"
        headers:
          "Content-Type": "application/grpc"
      metadata:
        service: "order.v1.OrderService"
        version: "v1.1.0"

---
apiVersion: gateway.networking.k8s.io/v1alpha2
kind: GRPCRoute
metadata:
  name: grpc-api-routes
  namespace: microservices
spec:
  parentRefs:
    - name: envoy-gateway
      namespace: envoy-gateway-system
  hostnames:
    - "grpc.company.ts.net"
  rules:
    # User service methods
    - matches:
        - method:
            service: "user.v1.UserService"
            method: "GetUser"
      backendRefs:
        - group: gateway.tailscale.com
          kind: TailscaleEndpoints
          name: grpc-services
          port: 9090
    
    - matches:
        - method:
            service: "user.v1.UserService"
            method: "CreateUser"
      backendRefs:
        - group: gateway.tailscale.com
          kind: TailscaleEndpoints
          name: grpc-services
          port: 9090
    
    # Order service methods
    - matches:
        - method:
            service: "order.v1.OrderService"
      backendRefs:
        - group: gateway.tailscale.com
          kind: TailscaleEndpoints
          name: grpc-services
          port: 9091
```

## TLS Services

### TLS Passthrough Configuration

```yaml title="tls-passthrough.yaml"
---
apiVersion: gateway.tailscale.com/v1alpha1
kind: TailscaleEndpoints
metadata:
  name: tls-services
  namespace: secure
spec:
  tailnet: "company.ts.net"
  endpoints:
    # Secure API with client certificates
    - name: secure-api
      tailscaleIP: "100.64.6.10"
      tailscaleFQDN: "secure-api.company.ts.net"
      port: 8443
      protocol: "TLS"
      externalTarget: "secure-api.internal.company.com:8443"
      healthCheck:
        enabled: true
        type: "TCP"
        interval: "30s"
        timeout: "10s"
      metadata:
        tls-mode: "passthrough"
        client-cert-required: "true"

    # Database with TLS
    - name: secure-postgres
      tailscaleIP: "100.64.6.11"
      tailscaleFQDN: "secure-db.company.ts.net"
      port: 5432
      protocol: "TLS"
      externalTarget: "postgres-ssl.internal.company.com:5432"
      healthCheck:
        enabled: true
        type: "TCP"
        interval: "60s"
      metadata:
        tls-mode: "passthrough"
        database: "postgresql"

---
apiVersion: gateway.networking.k8s.io/v1alpha2
kind: TLSRoute
metadata:
  name: secure-services-route
  namespace: secure
spec:
  parentRefs:
    - name: envoy-gateway
      namespace: envoy-gateway-system
  hostnames:
    - "secure-api.company.ts.net"
    - "secure-db.company.ts.net"
  rules:
    - backendRefs:
        - group: gateway.tailscale.com
          kind: TailscaleEndpoints
          name: tls-services
          port: 8443
```

## Mixed Protocol Example

### Complete Application Stack

```yaml title="full-stack-example.yaml"
---
apiVersion: gateway.tailscale.com/v1alpha1
kind: TailscaleEndpoints
metadata:
  name: fullstack-app
  namespace: production
spec:
  tailnet: "company.ts.net"
  serviceDiscovery:
    autoRegister: true
    vipServices:
      - name: "webapp-cluster"
        port: 80
        targetEndpoint: "frontend"
      - name: "api-cluster"
        port: 8080
        targetEndpoint: "backend"
  endpoints:
    # Frontend (HTTP)
    - name: frontend
      tailscaleIP: "100.64.7.10"
      tailscaleFQDN: "app.company.ts.net"
      port: 80
      protocol: "HTTP"
      externalTarget: "frontend.prod.svc.cluster.local:80"
      healthCheck:
        enabled: true
        path: "/health"
        interval: "30s"

    # Backend API (HTTPS)
    - name: backend
      tailscaleIP: "100.64.7.11"
      tailscaleFQDN: "api.company.ts.net"
      port: 8080
      protocol: "HTTPS"
      externalTarget: "backend.prod.svc.cluster.local:8080"
      healthCheck:
        enabled: true
        path: "/api/health"
        interval: "30s"

    # Database (TCP)
    - name: database
      tailscaleIP: "100.64.7.12"
      tailscaleFQDN: "db.company.ts.net"
      port: 5432
      protocol: "TCP"
      externalTarget: "postgres.prod.svc.cluster.local:5432"
      healthCheck:
        enabled: true
        type: "TCP"
        interval: "60s"

    # Cache (TCP)
    - name: redis
      tailscaleIP: "100.64.7.13"
      tailscaleFQDN: "cache.company.ts.net"
      port: 6379
      protocol: "TCP"
      externalTarget: "redis.prod.svc.cluster.local:6379"
      healthCheck:
        enabled: true
        type: "TCP"
        interval: "30s"

    # gRPC API (gRPC)
    - name: grpc-api
      tailscaleIP: "100.64.7.14"
      tailscaleFQDN: "grpc.company.ts.net"
      port: 9090
      protocol: "gRPC"
      externalTarget: "grpc-service.prod.svc.cluster.local:9090"
      healthCheck:
        enabled: true
        type: "HTTP"
        path: "/grpc.health.v1.Health/Check"
        interval: "30s"
```

## Protocol-Specific Best Practices

### HTTP/HTTPS Services
- Use `HTTPRoute` for path-based routing and header manipulation
- Enable health checks with appropriate endpoints
- Configure TLS termination for HTTPS services
- Set proper timeout values for web applications

### TCP Services
- Use `TCPRoute` for connection-based routing
- Configure connection pooling for databases
- Set appropriate health check intervals
- Use weight-based load balancing for replicas

### UDP Services
- Use `UDPRoute` for stateless protocols
- Configure shorter health check timeouts
- Consider session affinity for stateful protocols
- Monitor packet loss and latency

### gRPC Services
- Use `GRPCRoute` for method-level routing
- Configure gRPC health checking
- Enable streaming support where needed
- Set appropriate message size limits

### TLS Services
- Use `TLSRoute` for SNI-based routing
- Configure certificate management
- Enable passthrough mode for end-to-end encryption
- Validate certificate chains

## Troubleshooting Protocol Issues

### Common Protocol Problems

1. **HTTP 502 Bad Gateway**
   ```bash
   # Check backend health
   kubectl describe tailscaleendpoints fullstack-app
   ```

2. **TCP Connection Refused**
   ```bash
   # Test direct connectivity
   kubectl exec -it deployment/tailscale-gateway-operator -- nc -zv 100.64.7.12 5432
   ```

3. **UDP Packet Loss**
   ```bash
   # Check network statistics
   kubectl logs -n tailscale-system deployment/tailscale-gateway-operator | grep UDP
   ```

4. **gRPC Method Not Found**
   ```bash
   # Verify service definitions
   kubectl get grpcroutes -o yaml
   ```

## Next Steps

- **[Production Deployment](../operations/production)** - Production best practices
- **[Monitoring Guide](../operations/monitoring)** - Set up observability
- **[Configuration Reference](../configuration/overview)** - Advanced configurations