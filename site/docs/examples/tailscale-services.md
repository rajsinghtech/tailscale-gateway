---
id: tailscale-services
title: TailscaleServices Examples
sidebar_position: 4
---

# TailscaleServices Examples

This guide demonstrates how to use TailscaleServices to create sophisticated service mesh configurations with selector-based endpoint aggregation, load balancing, and auto-provisioning.

## Basic Service with Selector

Create a service that aggregates multiple TailscaleEndpoints using label selectors:

### Step 1: Create TailscaleEndpoints with Labels

```yaml title="web-endpoints.yaml"
# East region endpoint
apiVersion: gateway.tailscale.com/v1alpha1
kind: TailscaleEndpoints
metadata:
  name: web-east
  namespace: production
  labels:
    service-type: web
    environment: production
    region: east
spec:
  tailnet: company-tailnet
  tags: ["tag:web-proxy", "tag:east-region"]
  proxy:
    replicas: 2
    connectionType: bidirectional
  ports:
    - port: 80
      protocol: TCP
    - port: 443
      protocol: TCP
---
# West region endpoint
apiVersion: gateway.tailscale.com/v1alpha1
kind: TailscaleEndpoints
metadata:
  name: web-west
  namespace: production
  labels:
    service-type: web
    environment: production
    region: west
spec:
  tailnet: company-tailnet
  tags: ["tag:web-proxy", "tag:west-region"]
  proxy:
    replicas: 2
    connectionType: bidirectional
  ports:
    - port: 80
      protocol: TCP
    - port: 443
      protocol: TCP
```

### Step 2: Create TailscaleServices with Selector

```yaml title="web-service.yaml"
apiVersion: gateway.tailscale.com/v1alpha1
kind: TailscaleServices
metadata:
  name: production-web
  namespace: production
spec:
  # Select all web endpoints in production
  selector:
    matchLabels:
      service-type: web
      environment: production
  
  # Create VIP service
  vipService:
    name: "svc:production-web"
    ports: ["tcp:80", "tcp:443"]
    tags: ["tag:web-service", "tag:production"]
    comment: "Production web service with multi-region support"
  
  # Configure load balancing
  loadBalancing:
    strategy: round-robin
    healthCheck:
      enabled: true
      path: "/health"
      interval: "30s"
```

### Result

The TailscaleServices will:
1. Automatically discover both `web-east` and `web-west` endpoints
2. Create a VIP service `svc:production-web` 
3. Register all proxy devices from both endpoints as backends
4. Provide round-robin load balancing between regions
5. Monitor health at `/health` endpoint

## Failover Configuration

Configure primary/secondary failover between regions:

```yaml title="failover-service.yaml"
apiVersion: gateway.tailscale.com/v1alpha1
kind: TailscaleServices
metadata:
  name: web-service-ha
  namespace: production
spec:
  selector:
    matchLabels:
      service-type: web
      environment: production
  
  vipService:
    name: "svc:web-ha"
    ports: ["tcp:80", "tcp:443"]
    tags: ["tag:web-service", "tag:high-availability"]
  
  loadBalancing:
    strategy: failover
    healthCheck:
      enabled: true
      path: "/health"
      interval: "15s"
      healthyThreshold: 2
      unhealthyThreshold: 3
    failoverConfig:
      primaryEndpoints: ["web-east"]     # Prefer east region
      secondaryEndpoints: ["web-west"]   # Failover to west
      failbackDelay: "300s"              # Wait 5 minutes before failing back
```

## Auto-Provisioning Example

Create a service that automatically provisions endpoints when none match the selector:

```yaml title="auto-provisioned-service.yaml"
apiVersion: gateway.tailscale.com/v1alpha1
kind: TailscaleServices
metadata:
  name: auto-web-service
  namespace: development
spec:
  # Selector for endpoints that don't exist yet
  selector:
    matchLabels:
      app: auto-web
      tier: frontend
      environment: development
  
  vipService:
    name: "svc:auto-web"
    ports: ["tcp:3000"]
    tags: ["tag:auto-service", "tag:development"]
  
  # Template for auto-creating endpoints
  endpointTemplate:
    tailnet: development-tailnet
    tags: ["tag:auto-web", "tag:k8s-proxy"]
    proxy:
      replicas: 2
      connectionType: bidirectional
    ports:
      - port: 3000
        protocol: TCP
    labels:
      app: auto-web           # Must match selector
      tier: frontend          # Must match selector  
      environment: development # Must match selector
      auto-provisioned: "true"
```

### What Happens

1. TailscaleServices checks for endpoints matching the selector
2. None found, so it creates a TailscaleEndpoints using the template
3. The auto-created endpoint has the annotation `tailscale-gateway.com/auto-provisioned: "true"`
4. When the TailscaleServices is deleted, the auto-created endpoint is cleaned up via owner references

## Advanced Selector with Expressions

Use complex selectors for sophisticated endpoint selection:

```yaml title="advanced-selector.yaml"
apiVersion: gateway.tailscale.com/v1alpha1
kind: TailscaleServices
metadata:
  name: distributed-api
  namespace: production
spec:
  selector:
    matchLabels:
      service-type: api
      environment: production
    matchExpressions:
      # Include multiple regions
      - key: region
        operator: In
        values: ["east", "west", "central"]
      # Exclude deprecated endpoints
      - key: deprecated
        operator: DoesNotExist
      # Only include high-performance tier
      - key: performance-tier
        operator: In
        values: ["high", "premium"]
  
  vipService:
    name: "svc:distributed-api"
    ports: ["tcp:8080", "tcp:8443"]
    tags: ["tag:api-service", "tag:distributed"]
  
  loadBalancing:
    strategy: weighted
    weights:
      api-east: 50      # 50% to east
      api-west: 30      # 30% to west  
      api-central: 20   # 20% to central
    healthCheck:
      enabled: true
      path: "/api/health"
      interval: "20s"
```

## External Service Integration

Include external (non-Kubernetes) services in the load balancing:

```yaml title="hybrid-service.yaml"
apiVersion: gateway.tailscale.com/v1alpha1
kind: TailscaleServices
metadata:
  name: hybrid-api
  namespace: production
spec:
  selector:
    matchLabels:
      service-type: api
      deployment-type: kubernetes
  
  vipService:
    name: "svc:hybrid-api"
    ports: ["tcp:8080"]
    tags: ["tag:hybrid-service"]
  
  # Include external services  
  serviceDiscovery:
    externalServices:
      - name: legacy-api
        address: legacy.internal.com
        port: 8080
        protocol: HTTP
        weight: 10          # Lower weight for legacy system
      - name: cloud-api
        address: api.cloud-provider.com
        port: 443
        protocol: HTTPS
        weight: 30          # Higher weight for cloud service
  
  loadBalancing:
    strategy: weighted
    # Kubernetes endpoints get remaining weight automatically
```

## VIP Service Discovery

Discover and attach to existing VIP services instead of creating new ones:

```yaml title="shared-service.yaml"
apiVersion: gateway.tailscale.com/v1alpha1
kind: TailscaleServices
metadata:
  name: shared-database
  namespace: production
spec:
  selector:
    matchLabels:
      service-type: database-proxy
      environment: production
  
  # Try to discover existing VIP services first
  serviceDiscovery:
    discoverVIPServices: true
    vipServiceSelector:
      tags: ["tag:shared-database"]
      serviceNames: ["svc:postgres-cluster", "svc:redis-cluster"]
  
  vipService:
    name: "svc:database-access"
    ports: ["tcp:5432", "tcp:6379"]
    tags: ["tag:database-service", "tag:shared"]
```

## Complete Multi-Tier Application

Example of a complete application with multiple TailscaleServices:

```yaml title="complete-app.yaml"
# Frontend service
apiVersion: gateway.tailscale.com/v1alpha1
kind: TailscaleServices
metadata:
  name: frontend-service
  namespace: production
spec:
  selector:
    matchLabels:
      tier: frontend
      app: webapp
      environment: production
  
  vipService:
    name: "svc:webapp-frontend"
    ports: ["tcp:80", "tcp:443"]
    tags: ["tag:frontend", "tag:webapp"]
  
  loadBalancing:
    strategy: round-robin
    healthCheck:
      enabled: true
      path: "/health"
      interval: "30s"
  
  endpointTemplate:
    tailnet: production-tailnet
    tags: ["tag:frontend-proxy"]
    proxy:
      replicas: 3
      connectionType: ingress
    labels:
      tier: frontend
      app: webapp
      environment: production
---
# Backend API service
apiVersion: gateway.tailscale.com/v1alpha1
kind: TailscaleServices
metadata:
  name: api-service
  namespace: production
spec:
  selector:
    matchLabels:
      tier: backend
      app: webapp
      environment: production
  
  vipService:
    name: "svc:webapp-api"
    ports: ["tcp:8080"]
    tags: ["tag:backend", "tag:api"]
  
  loadBalancing:
    strategy: least-connections
    healthCheck:
      enabled: true
      path: "/api/health"
      interval: "15s"
  
  endpointTemplate:
    tailnet: production-tailnet
    tags: ["tag:backend-proxy"]
    proxy:
      replicas: 5
      connectionType: bidirectional
    labels:
      tier: backend
      app: webapp
      environment: production
---
# Database service
apiVersion: gateway.tailscale.com/v1alpha1
kind: TailscaleServices
metadata:
  name: database-service
  namespace: production
spec:
  selector:
    matchLabels:
      tier: database
      app: webapp
      environment: production
  
  vipService:
    name: "svc:webapp-database"
    ports: ["tcp:5432"]
    tags: ["tag:database", "tag:postgres"]
  
  loadBalancing:
    strategy: failover
    failoverConfig:
      primaryEndpoints: ["postgres-primary"]
      secondaryEndpoints: ["postgres-replica"]
      failbackDelay: "600s"
  
  serviceDiscovery:
    # Include managed database services
    externalServices:
      - name: managed-postgres
        address: postgres.managed-service.com
        port: 5432
        protocol: TCP
        weight: 50
```

## Gateway API Integration

Use TailscaleServices as backends in Gateway API resources:

```yaml title="gateway-integration.yaml"
# TailscaleServices definition
apiVersion: gateway.tailscale.com/v1alpha1
kind: TailscaleServices
metadata:
  name: api-backends
  namespace: production
spec:
  selector:
    matchLabels:
      service-type: api
      environment: production
  
  vipService:
    name: "svc:api-backends"
    ports: ["tcp:8080"]
    tags: ["tag:api-gateway-backend"]
---
# HTTPRoute using TailscaleServices as backend
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: api-route
  namespace: production
spec:
  parentRefs:
    - name: envoy-gateway
  hostnames:
    - "api.company.com"
  rules:
    - matches:
        - path:
            type: PathPrefix
            value: "/api/"
      backendRefs:
        - group: gateway.tailscale.com
          kind: TailscaleServices    # Use TailscaleServices as backend
          name: api-backends
          port: 8080
      filters:
        - type: URLRewrite
          urlRewrite:
            path:
              type: ReplacePrefixMatch
              replacePrefixMatch: "/"
```

## Monitoring and Observability

Check the status of your TailscaleServices:

```bash
# View service status
kubectl get tailscaleservices -o wide

# Check detailed status
kubectl describe tailscaleservices production-web

# View selected endpoints
kubectl get tailscaleservices production-web -o jsonpath='{.status.selectedEndpoints[*].name}'

# Check VIP service status
kubectl get tailscaleservices production-web -o jsonpath='{.status.vipServiceStatus}'
```

## Best Practices

### 1. Label Strategy
- Use consistent, meaningful labels across your TailscaleEndpoints
- Plan your label hierarchy for flexibility
- Use environment-based selectors for isolation

### 2. Load Balancing
- Start with round-robin for most use cases
- Use failover for high-availability requirements
- Implement health checks for production services
- Monitor and adjust weights based on performance

### 3. Auto-Provisioning
- Use auto-provisioning for dynamic environments
- Set appropriate replica counts in templates
- Monitor resource usage to prevent sprawl
- Use owner references for proper cleanup

### 4. Service Discovery
- Enable VIP service discovery to avoid duplicates
- Use consistent tagging across operators
- Plan service naming to avoid conflicts
- Monitor multi-operator coordination

These examples demonstrate the powerful selector-based architecture of TailscaleServices, enabling sophisticated service mesh configurations with automatic endpoint management, intelligent load balancing, and seamless integration with Gateway API resources.