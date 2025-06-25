---
id: basic-usage
title: Basic Usage Examples
sidebar_position: 1
---

# Basic Usage Examples

This guide provides practical examples for common Tailscale Gateway use cases.

## Example 1: Exposing a Web Application

Expose an external web application through your Tailscale network.

### Scenario
- External service: `httpbin.org`
- Make it accessible at: `httpbin.company.ts.net`
- Add custom routing through Gateway API

### Configuration

```yaml title="web-app-example.yaml"
---
# Define the external endpoint
apiVersion: gateway.tailscale.com/v1alpha1
kind: TailscaleEndpoints
metadata:
  name: web-apps
  namespace: default
spec:
  tailnet: "company.ts.net"
  endpoints:
    - name: httpbin
      tailscaleIP: "100.64.0.10"
      tailscaleFQDN: "httpbin.company.ts.net"
      port: 80
      protocol: "HTTP"
      externalTarget: "httpbin.org:80"
      healthCheck:
        enabled: true
        path: "/status/200"
        interval: "30s"
        timeout: "10s"

---
# Create Gateway API route
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: httpbin-route
  namespace: default
spec:
  parentRefs:
    - name: envoy-gateway
      namespace: envoy-gateway-system
  hostnames:
    - "httpbin.company.ts.net"
  rules:
    - matches:
        - path:
            type: PathPrefix
            value: "/"
      backendRefs:
        - group: gateway.tailscale.com
          kind: TailscaleEndpoints
          name: web-apps
          port: 80
```

### Testing

```bash
# Apply the configuration
kubectl apply -f web-app-example.yaml

# Test from any Tailscale device
curl http://httpbin.company.ts.net/json
```

## Example 2: Database Access

Securely expose a database through Tailscale.

### Scenario
- PostgreSQL database at `db.internal.company.com:5432`
- Accessible to developers on Tailscale at `postgres.company.ts.net`
- TCP-based connection with health checking

### Configuration

```yaml title="database-example.yaml"
---
apiVersion: gateway.tailscale.com/v1alpha1
kind: TailscaleEndpoints
metadata:
  name: databases
  namespace: production
spec:
  tailnet: "company.ts.net"
  endpoints:
    - name: postgres-primary
      tailscaleIP: "100.64.0.20"
      tailscaleFQDN: "postgres.company.ts.net"
      port: 5432
      protocol: "TCP"
      externalTarget: "db.internal.company.com:5432"
      healthCheck:
        enabled: true
        type: "TCP"
        interval: "60s"
        timeout: "5s"
        healthyThreshold: 2
        unhealthyThreshold: 3

---
apiVersion: gateway.networking.k8s.io/v1alpha2
kind: TCPRoute
metadata:
  name: postgres-route
  namespace: production
spec:
  parentRefs:
    - name: envoy-gateway
      namespace: envoy-gateway-system
  rules:
    - backendRefs:
        - group: gateway.tailscale.com
          kind: TailscaleEndpoints
          name: databases
          port: 5432
```

### Testing

```bash
# Apply the configuration
kubectl apply -f database-example.yaml

# Connect from any Tailscale device
psql -h postgres.company.ts.net -p 5432 -U username -d database_name
```

## Example 3: Multi-Service API Gateway

Create a gateway that routes to multiple backend services.

### Scenario
- Multiple APIs: user service, payment service, notification service
- Single entry point: `api.company.ts.net`
- Path-based routing to different backends

### Configuration

```yaml title="api-gateway-example.yaml"
---
apiVersion: gateway.tailscale.com/v1alpha1
kind: TailscaleEndpoints
metadata:
  name: api-services
  namespace: production
spec:
  tailnet: "company.ts.net"
  endpoints:
    - name: user-service
      tailscaleIP: "100.64.0.30"
      tailscaleFQDN: "users.company.ts.net"
      port: 8080
      protocol: "HTTP"
      externalTarget: "users.internal.company.com:8080"
      healthCheck:
        enabled: true
        path: "/health"
        interval: "30s"
    
    - name: payment-service
      tailscaleIP: "100.64.0.31"
      tailscaleFQDN: "payments.company.ts.net"
      port: 8081
      protocol: "HTTP"
      externalTarget: "payments.internal.company.com:8081"
      healthCheck:
        enabled: true
        path: "/health"
        interval: "30s"
    
    - name: notification-service
      tailscaleIP: "100.64.0.32"
      tailscaleFQDN: "notifications.company.ts.net"
      port: 8082
      protocol: "HTTP"
      externalTarget: "notifications.internal.company.com:8082"
      healthCheck:
        enabled: true
        path: "/health"
        interval: "30s"

---
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: api-gateway-route
  namespace: production
spec:
  parentRefs:
    - name: envoy-gateway
      namespace: envoy-gateway-system
  hostnames:
    - "api.company.ts.net"
  rules:
    # User service routing
    - matches:
        - path:
            type: PathPrefix
            value: "/users/"
      backendRefs:
        - group: gateway.tailscale.com
          kind: TailscaleEndpoints
          name: api-services
          port: 8080
      filters:
        - type: URLRewrite
          urlRewrite:
            path:
              type: ReplacePrefixMatch
              replacePrefixMatch: "/api/v1/"
    
    # Payment service routing
    - matches:
        - path:
            type: PathPrefix
            value: "/payments/"
      backendRefs:
        - group: gateway.tailscale.com
          kind: TailscaleEndpoints
          name: api-services
          port: 8081
      filters:
        - type: URLRewrite
          urlRewrite:
            path:
              type: ReplacePrefixMatch
              replacePrefixMatch: "/api/v1/"
    
    # Notification service routing
    - matches:
        - path:
            type: PathPrefix
            value: "/notifications/"
      backendRefs:
        - group: gateway.tailscale.com
          kind: TailscaleEndpoints
          name: api-services
          port: 8082
      filters:
        - type: URLRewrite
          urlRewrite:
            path:
              type: ReplacePrefixMatch
              replacePrefixMatch: "/api/v1/"
```

### Testing

```bash
# Apply the configuration
kubectl apply -f api-gateway-example.yaml

# Test different API endpoints
curl http://api.company.ts.net/users/profile
curl http://api.company.ts.net/payments/history
curl http://api.company.ts.net/notifications/unread
```

## Example 4: Development Environment Access

Provide developers easy access to staging/development services.

### Scenario
- Development cluster services
- Temporary access for debugging
- Multiple protocols (HTTP, TCP, UDP)

### Configuration

```yaml title="dev-environment-example.yaml"
---
apiVersion: gateway.tailscale.com/v1alpha1
kind: TailscaleGateway
metadata:
  name: dev-gateway
  namespace: development
spec:
  gatewayRef:
    kind: Gateway
    name: envoy-gateway
    namespace: envoy-gateway-system
  tailnets:
    - name: dev-tailnet
      tailscaleTailnetRef:
        kind: TailscaleTailnet
        name: dev-tailnet
      routeGeneration:
        ingress:
          hostPattern: "{service}.dev.company.ts.net"
          pathPrefix: "/"
        egress:
          hostPattern: "{service}.internal.dev"
          pathPrefix: "/dev/{service}/"

---
apiVersion: gateway.tailscale.com/v1alpha1
kind: TailscaleEndpoints
metadata:
  name: dev-services
  namespace: development
spec:
  tailnet: "company.ts.net"
  endpoints:
    # Frontend application
    - name: frontend
      tailscaleIP: "100.64.0.40"
      tailscaleFQDN: "frontend.dev.company.ts.net"
      port: 3000
      protocol: "HTTP"
      externalTarget: "frontend.dev.cluster.local:3000"
      healthCheck:
        enabled: true
        path: "/"
        interval: "60s"
    
    # Backend API
    - name: backend-api
      tailscaleIP: "100.64.0.41"
      tailscaleFQDN: "api.dev.company.ts.net"
      port: 8080
      protocol: "HTTP"
      externalTarget: "backend.dev.cluster.local:8080"
      healthCheck:
        enabled: true
        path: "/health"
        interval: "60s"
    
    # Redis cache
    - name: redis
      tailscaleIP: "100.64.0.42"
      tailscaleFQDN: "redis.dev.company.ts.net"
      port: 6379
      protocol: "TCP"
      externalTarget: "redis.dev.cluster.local:6379"
      healthCheck:
        enabled: true
        type: "TCP"
        interval: "120s"

---
# HTTP routes for web services
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: dev-web-routes
  namespace: development
spec:
  parentRefs:
    - name: envoy-gateway
      namespace: envoy-gateway-system
  rules:
    - matches:
        - headers:
            - name: "Host"
              value: "frontend.dev.company.ts.net"
      backendRefs:
        - group: gateway.tailscale.com
          kind: TailscaleEndpoints
          name: dev-services
          port: 3000
    
    - matches:
        - headers:
            - name: "Host"
              value: "api.dev.company.ts.net"
      backendRefs:
        - group: gateway.tailscale.com
          kind: TailscaleEndpoints
          name: dev-services
          port: 8080

---
# TCP route for Redis
apiVersion: gateway.networking.k8s.io/v1alpha2
kind: TCPRoute
metadata:
  name: dev-redis-route
  namespace: development
spec:
  parentRefs:
    - name: envoy-gateway
      namespace: envoy-gateway-system
  rules:
    - backendRefs:
        - group: gateway.tailscale.com
          kind: TailscaleEndpoints
          name: dev-services
          port: 6379
```

### Testing

```bash
# Apply the configuration
kubectl apply -f dev-environment-example.yaml

# Access services from developer machines
curl http://frontend.dev.company.ts.net
curl http://api.dev.company.ts.net/health
redis-cli -h redis.dev.company.ts.net -p 6379
```

## Example 5: Load Balancing and Failover

Configure multiple backends with automatic failover.

### Scenario
- Primary and backup API servers
- Health-based traffic distribution
- Automatic failover to backup

### Configuration

```yaml title="failover-example.yaml"
---
apiVersion: gateway.tailscale.com/v1alpha1
kind: TailscaleEndpoints
metadata:
  name: ha-services
  namespace: production
spec:
  tailnet: "company.ts.net"
  endpoints:
    # Primary server
    - name: api-primary
      tailscaleIP: "100.64.0.50"
      tailscaleFQDN: "api-primary.company.ts.net"
      port: 8080
      protocol: "HTTP"
      externalTarget: "api-primary.internal.company.com:8080"
      healthCheck:
        enabled: true
        path: "/health"
        interval: "15s"
        timeout: "5s"
        healthyThreshold: 2
        unhealthyThreshold: 2
      metadata:
        priority: "primary"
        weight: "100"
    
    # Backup server
    - name: api-backup
      tailscaleIP: "100.64.0.51"
      tailscaleFQDN: "api-backup.company.ts.net"
      port: 8080
      protocol: "HTTP"
      externalTarget: "api-backup.internal.company.com:8080"
      healthCheck:
        enabled: true
        path: "/health"
        interval: "15s"
        timeout: "5s"
        healthyThreshold: 2
        unhealthyThreshold: 2
      metadata:
        priority: "backup"
        weight: "50"

---
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: ha-api-route
  namespace: production
spec:
  parentRefs:
    - name: envoy-gateway
      namespace: envoy-gateway-system
  hostnames:
    - "api.company.ts.net"
  rules:
    - matches:
        - path:
            type: PathPrefix
            value: "/"
      backendRefs:
        # Primary backend (higher weight)
        - group: gateway.tailscale.com
          kind: TailscaleEndpoints
          name: ha-services
          port: 8080
          weight: 100
        # Backup backend (lower weight)
        - group: gateway.tailscale.com
          kind: TailscaleEndpoints
          name: ha-services
          port: 8080
          weight: 50
```

## Example 6: Cross-Cluster Service Discovery

Enable service discovery across multiple Kubernetes clusters.

### Scenario
- Services distributed across clusters
- Automatic registration and discovery
- Cross-cluster communication

### Configuration

```yaml title="cross-cluster-example.yaml"
---
# Cluster 1 configuration
apiVersion: gateway.tailscale.com/v1alpha1
kind: TailscaleEndpoints
metadata:
  name: cluster1-services
  namespace: production
spec:
  tailnet: "company.ts.net"
  serviceDiscovery:
    autoRegister: true
    vipServices:
      - name: "user-service-cluster"
        port: 8080
        targetEndpoint: "user-service"
  endpoints:
    - name: user-service
      tailscaleIP: "100.64.1.10"
      tailscaleFQDN: "users-c1.company.ts.net"
      port: 8080
      protocol: "HTTP"
      externalTarget: "user-service.production.svc.cluster.local:8080"
      healthCheck:
        enabled: true
        path: "/health"
      metadata:
        cluster: "cluster-1"
        region: "us-east-1"

---
# Cluster 2 configuration
apiVersion: gateway.tailscale.com/v1alpha1
kind: TailscaleEndpoints
metadata:
  name: cluster2-services
  namespace: production
spec:
  tailnet: "company.ts.net"
  serviceDiscovery:
    autoRegister: true
    vipServices:
      - name: "payment-service-cluster"
        port: 8081
        targetEndpoint: "payment-service"
  endpoints:
    - name: payment-service
      tailscaleIP: "100.64.2.10"
      tailscaleFQDN: "payments-c2.company.ts.net"
      port: 8081
      protocol: "HTTP"
      externalTarget: "payment-service.production.svc.cluster.local:8081"
      healthCheck:
        enabled: true
        path: "/health"
      metadata:
        cluster: "cluster-2"
        region: "us-west-2"

---
# Global routing configuration
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: global-api-route
  namespace: production
spec:
  parentRefs:
    - name: envoy-gateway
      namespace: envoy-gateway-system
  hostnames:
    - "api.company.ts.net"
  rules:
    # Route to user service (cluster 1)
    - matches:
        - path:
            type: PathPrefix
            value: "/users/"
      backendRefs:
        - group: gateway.tailscale.com
          kind: TailscaleEndpoints
          name: cluster1-services
          port: 8080
    
    # Route to payment service (cluster 2)
    - matches:
        - path:
            type: PathPrefix
            value: "/payments/"
      backendRefs:
        - group: gateway.tailscale.com
          kind: TailscaleEndpoints
          name: cluster2-services
          port: 8081
```

## Common Patterns

### Pattern 1: Environment-Specific Naming

```yaml
# Production
name: prod-api-services
tailnet: "company.ts.net"

# Staging
name: staging-api-services
tailnet: "staging.company.ts.net"

# Development
name: dev-api-services
tailnet: "dev.company.ts.net"
```

### Pattern 2: Service Grouping

```yaml
# Group by function
name: auth-services      # Authentication services
name: data-services      # Database and data services
name: api-services       # REST APIs
name: grpc-services      # gRPC services

# Group by team
name: frontend-services  # Frontend team services
name: backend-services   # Backend team services
name: platform-services  # Platform team services
```

### Pattern 3: Health Check Configurations

```yaml
# Web services
healthCheck:
  enabled: true
  path: "/health"
  interval: "30s"
  timeout: "10s"

# Databases
healthCheck:
  enabled: true
  type: "TCP"
  interval: "60s"
  timeout: "5s"

# Critical services
healthCheck:
  enabled: true
  interval: "15s"
  timeout: "5s"
  healthyThreshold: 1
  unhealthyThreshold: 2
```

## Best Practices

### 1. **Resource Organization**
- Use descriptive names that include environment and purpose
- Group related services in the same `TailscaleEndpoints` resource
- Use namespaces to separate environments

### 2. **Health Check Strategy**
- Always enable health checks for production services
- Use appropriate check intervals (30s for web, 60s for databases)
- Configure dedicated health check endpoints

### 3. **Security Considerations**
- Use HTTPS/TLS for all production APIs
- Implement proper authentication in health check endpoints
- Monitor access patterns and logs

### 4. **Naming Conventions**
- Use DNS-compliant names (no underscores)
- Include environment prefix: `prod-`, `staging-`, `dev-`
- Be descriptive: `user-api` instead of `api1`

### 5. **Monitoring and Observability**
- Set up monitoring for endpoint health
- Track traffic patterns and performance
- Use metadata labels for organization

## Troubleshooting

### Common Issues

1. **Endpoint not reachable**: Check Tailscale connectivity and firewall rules
2. **Health checks failing**: Verify health check endpoints and configurations
3. **Route not working**: Confirm Gateway API parentRefs and backend configurations
4. **Service discovery issues**: Check VIP service registration and metadata

### Debug Commands

```bash
# Check endpoint status
kubectl get tailscaleendpoints -o wide

# Describe specific resource
kubectl describe tailscaleendpoints web-services

# Check operator logs
kubectl logs -n tailscale-gateway-system deployment/tailscale-gateway-operator

# Test connectivity
kubectl exec -it deployment/tailscale-gateway-operator -- tailscale ping 100.64.0.10
```

## Next Steps

- **[Multi-Protocol Examples](../examples/multi-protocol)** - Advanced protocol configurations
- **[Production Deployment](../operations/production)** - Production best practices
- **[Monitoring Guide](../operations/monitoring)** - Set up observability
- **[Troubleshooting](../operations/troubleshooting)** - Common issues and solutions