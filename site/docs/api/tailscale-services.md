---
id: tailscale-services
title: TailscaleServices
sidebar_position: 4
---

# TailscaleServices

TailscaleServices provides the service mesh layer for the Tailscale Gateway Operator, implementing Kubernetes-style selector patterns to create VIP services that aggregate multiple TailscaleEndpoints with advanced load balancing, health checking, and auto-provisioning capabilities.

## Overview

TailscaleServices represents the highest-level abstraction in the Tailscale Gateway architecture, sitting above the infrastructure layer (TailscaleEndpoints) to provide sophisticated service management. It uses familiar Kubernetes selector patterns to dynamically compose services from multiple endpoint resources.

### Architecture Pattern

```
TailscaleServices ──selects via labels──> TailscaleEndpoints
       │                                         │
       │ creates VIP service                     │ creates StatefulSets
       ▼                                         ▼
Tailscale VIP Service                   Tailscale Proxy Pods
(web-service.tailnet.ts.net)           (machines in tailnet)
       │                                         │
       └────────── routes traffic to ────────────┘
```

## Specification

### Core Fields

```yaml
apiVersion: gateway.tailscale.com/v1alpha1
kind: TailscaleServices
metadata:
  name: web-service
spec:
  # Kubernetes-style label selector (REQUIRED)
  selector:
    matchLabels:
      service-type: web
      environment: production
    matchExpressions:
      - key: region
        operator: In
        values: ["east", "west"]
  
  # VIP service configuration
  vipService:
    name: "svc:web-service"
    ports: ["tcp:80", "tcp:443"]
    tags: ["tag:web-service", "tag:production"]
    comment: "Production web service"
    autoApprove: true
    annotations:
      environment: "production"
      team: "backend"
  
  # Load balancing strategies
  loadBalancing:
    strategy: round-robin  # round-robin, least-connections, weighted, failover
    healthCheck:
      enabled: true
      path: "/health"
      interval: "30s"
      timeout: "5s"
      healthyThreshold: 2
      unhealthyThreshold: 3
    weights:
      web-east: 70
      web-west: 30
    failoverConfig:
      primaryEndpoints: ["web-east"]
      secondaryEndpoints: ["web-west"]
      failbackDelay: "60s"
  
  # Service discovery configuration
  serviceDiscovery:
    discoverVIPServices: true
    vipServiceSelector:
      tags: ["tag:shared-service"]
      serviceNames: ["svc:external-api"]
    externalServices:
      - name: external-api
        address: api.example.com
        port: 443
        protocol: HTTPS
        weight: 10
  
  # Auto-provisioning template
  endpointTemplate:
    tailnet: production-tailnet
    tags: ["tag:auto-web", "tag:k8s"]
    proxy:
      replicas: 3
      connectionType: bidirectional
    ports:
      - port: 80
        protocol: TCP
    labels:
      service-type: web    # Must match selector
      environment: production
      auto-provisioned: "true"
```

## Selector Pattern

TailscaleServices uses the same label selector pattern as Kubernetes Services, providing familiar and powerful selection capabilities.

### Basic Label Matching

```yaml
selector:
  matchLabels:
    service-type: web
    environment: production
```

### Advanced Expression Matching

```yaml
selector:
  matchExpressions:
    - key: region
      operator: In
      values: ["east", "west", "central"]
    - key: tier
      operator: NotIn
      values: ["deprecated"]
    - key: auto-provisioned
      operator: Exists
```

### Dynamic Selection

The controller continuously monitors TailscaleEndpoints and automatically updates service membership when:
- New TailscaleEndpoints are created that match the selector
- Existing TailscaleEndpoints labels change
- TailscaleEndpoints are deleted

## Load Balancing Strategies

### Round Robin (Default)

Distributes requests evenly across all healthy backends.

```yaml
loadBalancing:
  strategy: round-robin
```

### Least Connections

Routes to the backend with the fewest active connections.

```yaml
loadBalancing:
  strategy: least-connections
```

### Weighted Distribution

Distributes traffic based on configured weights.

```yaml
loadBalancing:
  strategy: weighted
  weights:
    web-east: 70     # 70% of traffic
    web-west: 20     # 20% of traffic
    web-backup: 10   # 10% of traffic
```

### Failover with Primary/Secondary Tiers

Uses primary endpoints preferentially, failing over to secondary when primary endpoints are unhealthy.

```yaml
loadBalancing:
  strategy: failover
  failoverConfig:
    primaryEndpoints: ["web-primary-east", "web-primary-west"]
    secondaryEndpoints: ["web-backup-central"]
    failbackDelay: "120s"  # Wait 2 minutes before failing back
```

## Auto-Provisioning

When no TailscaleEndpoints match the selector, TailscaleServices can automatically create them using the endpointTemplate.

### Auto-Provisioning Template

```yaml
endpointTemplate:
  tailnet: production-tailnet
  tags: ["tag:auto-web", "tag:k8s-proxy"]
  proxy:
    replicas: 2
    connectionType: bidirectional
  ports:
    - port: 80
      protocol: TCP
    - port: 443
      protocol: TCP
  labels:
    service-type: web        # Must match selector
    environment: production  # Must match selector
    region: auto            # Additional labels
```

### Auto-Created Resources

Auto-provisioned TailscaleEndpoints have:
- **Annotation**: `tailscale-gateway.com/auto-provisioned: "true"`
- **Owner References**: Point to the creating TailscaleServices
- **Labels**: Must match the selector requirements
- **Automatic Cleanup**: Deleted when no longer selected

## Service Discovery

### VIP Service Discovery

Discover and attach to existing Tailscale VIP services instead of creating new ones.

```yaml
serviceDiscovery:
  discoverVIPServices: true
  vipServiceSelector:
    tags: ["tag:shared-infrastructure"]
    serviceNames: ["svc:database", "svc:cache"]
```

### External Service Integration

Include non-Kubernetes backends in the service.

```yaml
serviceDiscovery:
  externalServices:
    - name: legacy-api
      address: legacy.internal.com
      port: 8080
      protocol: HTTP
      weight: 5
    - name: cloud-service
      address: api.cloud-provider.com
      port: 443
      protocol: HTTPS
      weight: 15
```

## Health Checking

Service-level health checking monitors all backends and adjusts traffic routing based on health status.

```yaml
loadBalancing:
  healthCheck:
    enabled: true
    path: "/health"           # HTTP health check path
    interval: "30s"           # Check every 30 seconds
    timeout: "5s"             # 5 second timeout
    healthyThreshold: 2       # 2 consecutive successes = healthy
    unhealthyThreshold: 3     # 3 consecutive failures = unhealthy
```

### Health Check Protocols

- **HTTP/HTTPS**: Uses GET requests to specified path
- **TCP**: Tests port connectivity
- **UDP**: Tests port availability (limited)

## Multi-Operator Coordination

TailscaleServices integrates with the ServiceCoordinator for multi-operator service sharing.

### Service Registry Integration

```yaml
# Multiple operators can share the same VIP service
vipService:
  name: "svc:shared-database"
  # First operator creates the service
  # Subsequent operators attach as consumers
```

### Coordination Status

```yaml
status:
  serviceInfo:
    multiOperatorService: true
    ownerOperator: "operator-cluster-1"
    consumerOperators: ["operator-cluster-2", "operator-cluster-3"]
    serviceRegistryKey: "shared-database-registry"
    lastCoordination: "2025-06-25T10:30:00Z"
```

## Status Fields

TailscaleServices provides comprehensive status information about service health, backend distribution, and coordination state.

### Selected Endpoints Status

```yaml
status:
  selectedEndpoints:
    - name: web-east
      namespace: production
      status: Ready
      machines: ["web-east-0", "web-east-1"]
      lastSelected: "2025-06-25T10:25:00Z"
      labels:
        service-type: web
        environment: production
        region: east
      healthStatus:
        healthyMachines: 2
        unhealthyMachines: 0
        lastHealthCheck: "2025-06-25T10:30:00Z"
```

### VIP Service Status

```yaml
status:
  vipServiceStatus:
    created: true
    serviceName: "svc:web-service"
    dnsName: "web-service.production-tailnet.ts.net"
    addresses: ["100.100.100.50"]
    backendCount: 4
    healthyBackendCount: 4
    serviceTags: ["tag:web-service", "tag:production"]
    lastRegistration: "2025-06-25T10:30:00Z"
```

### Load Balancing Status

```yaml
status:
  loadBalancingStatus:
    strategy: weighted
    endpointWeights:
      web-east: 70
      web-west: 30
    requestCounts:
      web-east: 1450
      web-west: 620
    failoverStatus:
      activeTier: primary
      lastFailover: "2025-06-24T15:20:00Z"
      failoverReason: "primary-endpoints-unhealthy"
```

## Complete Example

```yaml
apiVersion: gateway.tailscale.com/v1alpha1
kind: TailscaleServices
metadata:
  name: production-web-service
  namespace: production
spec:
  selector:
    matchLabels:
      service-type: web
      environment: production
    matchExpressions:
      - key: region
        operator: In
        values: ["east", "west"]
  
  vipService:
    name: "svc:production-web"
    ports: ["tcp:80", "tcp:443"]
    tags: ["tag:web-service", "tag:production", "tag:multi-region"]
    comment: "Production web service with multi-region failover"
    autoApprove: true
    annotations:
      team: "platform"
      environment: "production"
      monitoring: "enabled"
  
  loadBalancing:
    strategy: failover
    healthCheck:
      enabled: true
      path: "/health"
      interval: "15s"
      timeout: "3s"
      healthyThreshold: 2
      unhealthyThreshold: 3
    failoverConfig:
      primaryEndpoints: ["web-east"]
      secondaryEndpoints: ["web-west"]
      failbackDelay: "300s"
  
  serviceDiscovery:
    discoverVIPServices: true
    vipServiceSelector:
      tags: ["tag:shared-infrastructure"]
    externalServices:
      - name: cdn-service
        address: cdn.example.com
        port: 443
        protocol: HTTPS
        weight: 5
  
  endpointTemplate:
    tailnet: production-tailnet
    tags: ["tag:auto-web", "tag:k8s-proxy", "tag:production"]
    proxy:
      replicas: 2
      connectionType: bidirectional
    ports:
      - port: 80
        protocol: TCP
      - port: 443
        protocol: TCP
    labels:
      service-type: web
      environment: production
      region: auto
      auto-provisioned: "true"
---
# This will be auto-created if no matching TailscaleEndpoints exist
apiVersion: gateway.tailscale.com/v1alpha1
kind: TailscaleEndpoints
metadata:
  name: production-web-service-auto
  namespace: production
  annotations:
    tailscale-gateway.com/auto-provisioned: "true"
  ownerReferences:
    - apiVersion: gateway.tailscale.com/v1alpha1
      kind: TailscaleServices
      name: production-web-service
      uid: "..."
spec:
  tailnet: production-tailnet
  tags: ["tag:auto-web", "tag:k8s-proxy", "tag:production"]
  proxy:
    replicas: 2
    connectionType: bidirectional
  ports:
    - port: 80
      protocol: TCP
    - port: 443
      protocol: TCP
```

## Integration with Gateway API

TailscaleServices can be used as backends in Gateway API resources:

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: web-route
spec:
  parentRefs:
    - name: envoy-gateway
  rules:
    - matches:
        - path:
            type: PathPrefix
            value: "/api/"
      backendRefs:
        - group: gateway.tailscale.com
          kind: TailscaleServices  # Use TailscaleServices as backend
          name: production-web-service
          port: 80
```

## Best Practices

### Selector Design

1. **Use Stable Labels**: Ensure selector labels are stable and meaningful
2. **Avoid Over-Selection**: Be specific to prevent unintended endpoint inclusion
3. **Use Expressions for Flexibility**: Leverage `matchExpressions` for complex selection logic

### Load Balancing Strategy

1. **Start with Round Robin**: Use default strategy unless specific requirements exist
2. **Use Failover for HA**: Implement primary/secondary for high availability
3. **Monitor Health Checks**: Enable health checking for production services
4. **Configure Appropriate Timeouts**: Balance responsiveness with stability

### Auto-Provisioning

1. **Use for Dynamic Workloads**: Ideal for auto-scaling environments
2. **Set Appropriate Labels**: Ensure auto-provisioned endpoints have correct labels
3. **Monitor Resource Usage**: Track auto-provisioning to prevent resource sprawl
4. **Use Owner References**: Ensure proper cleanup via owner references

### Multi-Operator Coordination

1. **Use Discovery-First**: Let TailscaleServices discover existing VIP services
2. **Coordinate Tags**: Use consistent tagging across operators
3. **Monitor Service Registry**: Track multi-operator service sharing
4. **Plan for Conflicts**: Design VIP service naming to avoid conflicts