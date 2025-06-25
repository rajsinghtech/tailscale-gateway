---
id: tailscale-route-policy
title: TailscaleRoutePolicy
sidebar_position: 3
---

# TailscaleRoutePolicy API Reference

The `TailscaleRoutePolicy` resource defines advanced routing policies and traffic management rules for Tailscale Gateway traffic.

## Overview

`TailscaleRoutePolicy` allows you to:
- Define sophisticated routing rules based on various conditions
- Implement traffic splitting and canary deployments
- Configure rate limiting and circuit breaker policies
- Set up custom authentication and authorization rules

## API Version

```yaml
apiVersion: gateway.tailscale.com/v1alpha1
kind: TailscaleRoutePolicy
```

## Basic Example

```yaml
apiVersion: gateway.tailscale.com/v1alpha1
kind: TailscaleRoutePolicy
metadata:
  name: api-routing-policy
  namespace: production
spec:
  targetRef:
    group: gateway.tailscale.com
    kind: TailscaleEndpoints
    name: api-services
  
  rules:
    - matches:
        - headers:
            - name: "X-User-Type"
              value: "premium"
      actions:
        - type: "Route"
          route:
            backendRefs:
              - name: "premium-api-service"
                weight: 100
    
    - matches:
        - path:
            type: PathPrefix
            value: "/v2/"
      actions:
        - type: "Route"
          route:
            backendRefs:
              - name: "api-v2-service"
                weight: 100
```

## Specification

### TailscaleRoutePolicySpec

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `targetRef` | LocalPolicyTargetReference | Yes | Target resource this policy applies to |
| `rules` | []PolicyRule | Yes | List of routing rules |
| `defaults` | PolicyDefaults | No | Default policies applied to all traffic |

### PolicyRule

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `matches` | []RouteMatch | No | Conditions for when this rule applies |
| `actions` | []PolicyAction | Yes | Actions to take when rule matches |
| `priority` | int32 | No | Rule priority (higher number = higher priority) |

### RouteMatch

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `path` | PathMatch | No | HTTP path matching criteria |
| `headers` | []HeaderMatch | No | HTTP header matching criteria |
| `queryParams` | []QueryParamMatch | No | Query parameter matching criteria |
| `method` | string | No | HTTP method (GET, POST, PUT, DELETE, etc.) |

### PolicyAction

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `type` | string | Yes | Action type: Route, Redirect, RequestHeaderModifier, etc. |
| `route` | RouteAction | No | Routing configuration (for type: Route) |
| `redirect` | RedirectAction | No | Redirect configuration (for type: Redirect) |
| `requestHeaderModifier` | HeaderModifier | No | Request header modification |
| `responseHeaderModifier` | HeaderModifier | No | Response header modification |
| `rateLimiting` | RateLimitingAction | No | Rate limiting configuration |
| `authentication` | AuthenticationAction | No | Authentication requirements |

## Usage Examples

### Traffic Splitting and Canary Deployment

```yaml
apiVersion: gateway.tailscale.com/v1alpha1
kind: TailscaleRoutePolicy
metadata:
  name: canary-deployment-policy
  namespace: production
spec:
  targetRef:
    group: gateway.tailscale.com
    kind: TailscaleEndpoints
    name: web-services
  
  rules:
    # Canary deployment: 10% traffic to v2, 90% to v1
    - matches:
        - path:
            type: PathPrefix
            value: "/api/"
      actions:
        - type: "Route"
          route:
            backendRefs:
              - name: "api-v1-service"
                weight: 90
              - name: "api-v2-service"
                weight: 10
            timeout: "30s"
            retryPolicy:
              numRetries: 3
              perTryTimeout: "10s"
    
    # Beta users get v2 exclusively
    - matches:
        - headers:
            - name: "X-Beta-User"
              value: "true"
        - path:
            type: PathPrefix
            value: "/api/"
      actions:
        - type: "Route"
          route:
            backendRefs:
              - name: "api-v2-service"
                weight: 100
      priority: 100  # Higher priority than general traffic splitting
```

### Authentication and Authorization

```yaml
apiVersion: gateway.tailscale.com/v1alpha1
kind: TailscaleRoutePolicy
metadata:
  name: auth-policy
  namespace: production
spec:
  targetRef:
    group: gateway.tailscale.com
    kind: TailscaleEndpoints
    name: secure-services
  
  rules:
    # Admin endpoints require admin token
    - matches:
        - path:
            type: PathPrefix
            value: "/admin/"
      actions:
        - type: "Authentication"
          authentication:
            type: "JWT"
            jwtProvider:
              issuer: "https://auth.company.com"
              audiences: ["admin-api"]
              jwksUri: "https://auth.company.com/.well-known/jwks.json"
            requiredClaims:
              - name: "role"
                value: "admin"
        
        - type: "RequestHeaderModifier"
          requestHeaderModifier:
            add:
              - name: "X-Authenticated-User"
                value: "{jwt.sub}"
              - name: "X-User-Role"
                value: "{jwt.role}"
    
    # Public endpoints allow anonymous access
    - matches:
        - path:
            type: PathPrefix
            value: "/public/"
      actions:
        - type: "Route"
          route:
            backendRefs:
              - name: "public-api-service"
                weight: 100
    
    # All other endpoints require basic authentication
    - actions:
        - type: "Authentication"
          authentication:
            type: "Basic"
            realm: "API Access"
        
        - type: "Route"
          route:
            backendRefs:
              - name: "api-service"
                weight: 100
```

### Rate Limiting and Circuit Breaking

```yaml
apiVersion: gateway.tailscale.com/v1alpha1
kind: TailscaleRoutePolicy
metadata:
  name: rate-limiting-policy
  namespace: production
spec:
  targetRef:
    group: gateway.tailscale.com
    kind: TailscaleEndpoints
    name: api-services
  
  rules:
    # Premium users get higher rate limits
    - matches:
        - headers:
            - name: "X-API-Tier"
              value: "premium"
      actions:
        - type: "RateLimiting"
          rateLimiting:
            requestsPerSecond: 1000
            burstSize: 2000
            keyExtractor:
              type: "Header"
              header: "X-API-Key"
        
        - type: "Route"
          route:
            backendRefs:
              - name: "premium-api-service"
                weight: 100
    
    # Standard users get basic rate limits
    - matches:
        - headers:
            - name: "X-API-Tier"
              value: "standard"
      actions:
        - type: "RateLimiting"
          rateLimiting:
            requestsPerSecond: 100
            burstSize: 200
            keyExtractor:
              type: "Header"
              header: "X-API-Key"
        
        - type: "Route"
          route:
            backendRefs:
              - name: "standard-api-service"
                weight: 100
    
    # Anonymous users get very limited access
    - actions:
        - type: "RateLimiting"
          rateLimiting:
            requestsPerSecond: 10
            burstSize: 20
            keyExtractor:
              type: "ClientIP"
        
        - type: "Route"
          route:
            backendRefs:
              - name: "public-api-service"
                weight: 100
  
  # Global circuit breaker
  defaults:
    circuitBreaker:
      enabled: true
      maxConnections: 1000
      maxPendingRequests: 100
      consecutiveErrors: 5
      interval: "30s"
      baseEjectionTime: "30s"
      maxEjectionPercent: 50
```

### Geographic Routing

```yaml
apiVersion: gateway.tailscale.com/v1alpha1
kind: TailscaleRoutePolicy
metadata:
  name: geo-routing-policy
  namespace: production
spec:
  targetRef:
    group: gateway.tailscale.com
    kind: TailscaleEndpoints
    name: global-services
  
  rules:
    # Route EU users to EU servers
    - matches:
        - headers:
            - name: "CF-IPCountry"
              type: "RegularExpression"
              value: "DE|FR|IT|ES|NL|GB"
      actions:
        - type: "Route"
          route:
            backendRefs:
              - name: "eu-api-service"
                weight: 100
        
        - type: "ResponseHeaderModifier"
          responseHeaderModifier:
            add:
              - name: "X-Served-By"
                value: "eu-cluster"
    
    # Route US users to US servers
    - matches:
        - headers:
            - name: "CF-IPCountry"
              value: "US"
      actions:
        - type: "Route"
          route:
            backendRefs:
              - name: "us-api-service"
                weight: 100
        
        - type: "ResponseHeaderModifier"
          responseHeaderModifier:
            add:
              - name: "X-Served-By"
                value: "us-cluster"
    
    # Default to global service
    - actions:
        - type: "Route"
          route:
            backendRefs:
              - name: "global-api-service"
                weight: 100
```

### Advanced Header Manipulation

```yaml
apiVersion: gateway.tailscale.com/v1alpha1
kind: TailscaleRoutePolicy
metadata:
  name: header-manipulation-policy
  namespace: production
spec:
  targetRef:
    group: gateway.tailscale.com
    kind: TailscaleEndpoints
    name: api-services
  
  rules:
    # API versioning through headers
    - matches:
        - headers:
            - name: "Accept"
              type: "RegularExpression"
              value: "application/vnd\\.api\\.v2\\+json"
      actions:
        - type: "RequestHeaderModifier"
          requestHeaderModifier:
            add:
              - name: "X-API-Version"
                value: "v2"
              - name: "X-Request-ID"
                value: "{request_id}"
            set:
              - name: "Accept"
                value: "application/json"
        
        - type: "Route"
          route:
            backendRefs:
              - name: "api-v2-service"
                weight: 100
    
    # Legacy API support
    - matches:
        - path:
            type: PathPrefix
            value: "/v1/"
      actions:
        - type: "RequestHeaderModifier"
          requestHeaderModifier:
            add:
              - name: "X-API-Version"
                value: "v1"
              - name: "X-Legacy-Request"
                value: "true"
        
        - type: "ResponseHeaderModifier"
          responseHeaderModifier:
            add:
              - name: "X-Deprecation-Warning"
                value: "This API version is deprecated. Please migrate to v2."
              - name: "X-Sunset"
                value: "2024-12-31"
        
        - type: "Route"
          route:
            backendRefs:
              - name: "api-v1-service"
                weight: 100
```

## Policy Inheritance and Precedence

### Rule Priority
- Higher priority number = higher precedence
- Rules without priority default to 0
- Rules with same priority are evaluated in order

### Target Hierarchy
```yaml
# Gateway-level policy (lowest precedence)
targetRef:
  kind: Gateway
  name: envoy-gateway

# TailscaleGateway-level policy (medium precedence)
targetRef:
  group: gateway.tailscale.com
  kind: TailscaleGateway
  name: production-gateway

# TailscaleEndpoints-level policy (highest precedence)
targetRef:
  group: gateway.tailscale.com
  kind: TailscaleEndpoints
  name: api-services
```

## Status

```yaml
status:
  conditions:
    - type: "Ready"
      status: "True"
      reason: "PolicyApplied"
      message: "Policy rules are applied successfully"
  
  targetRef:
    group: gateway.tailscale.com
    kind: TailscaleEndpoints
    name: api-services
    
  appliedRules:
    - ruleIndex: 0
      matches: 15
      lastMatched: "2024-01-15T10:30:00Z"
    - ruleIndex: 1
      matches: 3
      lastMatched: "2024-01-15T10:25:00Z"
```

## Best Practices

### 1. **Rule Organization**
```yaml
# Use descriptive names and organize by function
rules:
  # High priority authentication rules
  - matches: [...]
    priority: 100
    
  # Medium priority routing rules
  - matches: [...]
    priority: 50
    
  # Default fallback rules
  - actions: [...]
    priority: 0
```

### 2. **Performance Considerations**
```yaml
# Order rules by frequency of matching
rules:
  # Most common paths first
  - matches:
      - path:
          type: PathPrefix
          value: "/api/v1/"
    # ...
  
  # Less common paths later
  - matches:
      - path:
          type: PathPrefix
          value: "/admin/"
    # ...
```

### 3. **Security Practices**
```yaml
# Always validate and sanitize headers
requestHeaderModifier:
  remove:
    - "X-Forwarded-For"  # Remove potentially spoofed headers
  add:
    - name: "X-Gateway-Processed"
      value: "true"
```

## Related Resources

- **[TailscaleGateway](./tailscale-gateway)** - Main gateway configuration
- **[TailscaleEndpoints](./tailscale-endpoints)** - Service endpoint definitions
- **[Configuration Guide](../configuration/overview)** - Advanced configuration options