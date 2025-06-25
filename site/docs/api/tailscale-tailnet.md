---
id: tailscale-tailnet
title: TailscaleTailnet
sidebar_position: 4
---

# TailscaleTailnet API Reference

The `TailscaleTailnet` resource defines the configuration and credentials for connecting to a specific Tailscale network (tailnet).

## Overview

`TailscaleTailnet` allows you to:
- Configure OAuth credentials for Tailscale API access
- Define tailnet-specific settings and policies
- Manage device registration and networking options
- Set up access control lists (ACLs) and security policies

## API Version

```yaml
apiVersion: gateway.tailscale.com/v1alpha1
kind: TailscaleTailnet
```

## Basic Example

```yaml
apiVersion: gateway.tailscale.com/v1alpha1
kind: TailscaleTailnet
metadata:
  name: company-tailnet
  namespace: tailscale-gateway-system
spec:
  tailnet: "company.ts.net"
  oauth:
    clientIdRef:
      name: "tailscale-oauth"
      key: "client-id"
    clientSecretRef:
      name: "tailscale-oauth"
      key: "client-secret"
  
  configuration:
    devices:
      ephemeral: false
      preauth: true
      keyExpiry: "90d"
```

## Specification

### TailscaleTailnetSpec

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `tailnet` | string | Yes | The Tailscale tailnet domain (e.g., "company.ts.net") |
| `oauth` | OAuthConfig | Yes | OAuth configuration for API access |
| `configuration` | TailnetConfiguration | No | Tailnet-specific configuration settings |
| `observability` | ObservabilityConfig | No | Monitoring and logging configuration |

### OAuthConfig

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `clientIdRef` | SecretKeySelector | Yes | Reference to OAuth client ID in Kubernetes secret |
| `clientSecretRef` | SecretKeySelector | Yes | Reference to OAuth client secret in Kubernetes secret |
| `scopes` | []string | No | OAuth scopes (default: ["device:create", "device:read", "device:write"]) |

### TailnetConfiguration

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `devices` | DeviceConfig | No | Device registration and management settings |
| `accessControl` | AccessControlConfig | No | ACL and security policy configuration |
| `networking` | NetworkingConfig | No | Network-specific settings |

### DeviceConfig

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `ephemeral` | bool | No | Create ephemeral devices (default: false) |
| `preauth` | bool | No | Enable device pre-authorization (default: true) |
| `keyExpiry` | string | No | Device key expiry duration (default: "90d") |
| `machineAuth` | bool | No | Enable machine-to-machine authentication (default: true) |
| `requireApproval` | bool | No | Require manual device approval (default: false) |
| `advertiseRoutes` | []string | No | Subnet routes to advertise |
| `tags` | []string | No | Default tags to apply to devices |

### AccessControlConfig

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `defaultAction` | string | No | Default ACL action: "allow" or "deny" (default: "deny") |
| `rules` | []ACLRule | No | Access control rules |
| `groups` | map[string][]string | No | User groups definition |
| `tagOwners` | map[string][]string | No | Tag ownership mapping |

### NetworkingConfig

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `magicDNS` | bool | No | Enable Magic DNS (default: true) |
| `dnsSettings` | DNSConfig | No | DNS configuration |
| `exitNodes` | []string | No | Available exit nodes |

## Usage Examples

### Production Tailnet Configuration

```yaml
apiVersion: gateway.tailscale.com/v1alpha1
kind: TailscaleTailnet
metadata:
  name: prod-tailnet
  namespace: tailscale-gateway-system
spec:
  tailnet: "prod.company.ts.net"
  
  oauth:
    clientIdRef:
      name: "prod-tailscale-oauth"
      key: "client-id"
    clientSecretRef:
      name: "prod-tailscale-oauth"
      key: "client-secret"
    scopes:
      - "device:create"
      - "device:read"
      - "device:write"
      - "tailnet:read"
  
  configuration:
    devices:
      ephemeral: false
      preauth: true
      keyExpiry: "90d"
      machineAuth: true
      requireApproval: false
      advertiseRoutes:
        - "10.0.0.0/8"
        - "172.16.0.0/12"
      tags:
        - "tag:k8s-gateway"
        - "tag:prod-cluster"
    
    accessControl:
      defaultAction: "deny"
      rules:
        # Allow production servers to communicate
        - source: ["tag:prod-server"]
          destination: ["tag:prod-server:*"]
          action: "allow"
        
        # Allow developers to access specific services
        - source: ["group:developers"]
          destination: ["tag:prod-server:80,443,8080"]
          action: "allow"
        
        # Deny access to sensitive databases
        - source: ["*"]
          destination: ["tag:database:5432,3306"]
          action: "deny"
      
      groups:
        developers: ["alice@company.com", "bob@company.com"]
        admins: ["admin@company.com"]
        devops: ["devops@company.com"]
      
      tagOwners:
        "tag:prod-server": ["group:admins", "group:devops"]
        "tag:database": ["group:admins"]
        "tag:k8s-gateway": ["group:devops"]
    
    networking:
      magicDNS: true
      dnsSettings:
        globalNameservers:
          - "1.1.1.1"
          - "8.8.8.8"
        searchDomains:
          - "company.com"
          - "internal.company.com"
      exitNodes:
        - "exit-node-us-east"
        - "exit-node-eu-west"
  
  observability:
    logLevel: "info"
    auditLogging: true
    metrics:
      enabled: true
      interval: "30s"
```

### Development Tailnet Configuration

```yaml
apiVersion: gateway.tailscale.com/v1alpha1
kind: TailscaleTailnet
metadata:
  name: dev-tailnet
  namespace: tailscale-gateway-system
spec:
  tailnet: "dev.company.ts.net"
  
  oauth:
    clientIdRef:
      name: "dev-tailscale-oauth"
      key: "client-id"
    clientSecretRef:
      name: "dev-tailscale-oauth"
      key: "client-secret"
  
  configuration:
    devices:
      ephemeral: true  # Temporary devices for development
      preauth: true
      keyExpiry: "30d"  # Shorter expiry for dev
      requireApproval: false
      tags:
        - "tag:dev-cluster"
        - "tag:k8s-gateway"
    
    accessControl:
      defaultAction: "allow"  # More permissive for development
      rules:
        # Still block access to production resources
        - source: ["tag:dev-cluster"]
          destination: ["tag:prod-server:*"]
          action: "deny"
      
      groups:
        developers: ["dev@company.com"]
      
      tagOwners:
        "tag:dev-cluster": ["group:developers"]
    
    networking:
      magicDNS: true
      dnsSettings:
        globalNameservers:
          - "8.8.8.8"
        searchDomains:
          - "dev.company.com"
  
  observability:
    logLevel: "debug"  # More verbose logging for development
    auditLogging: false
    metrics:
      enabled: true
      interval: "60s"
```

### Multi-Region Tailnet

```yaml
apiVersion: gateway.tailscale.com/v1alpha1
kind: TailscaleTailnet
metadata:
  name: global-tailnet
  namespace: tailscale-gateway-system
spec:
  tailnet: "global.company.ts.net"
  
  oauth:
    clientIdRef:
      name: "global-tailscale-oauth"
      key: "client-id"
    clientSecretRef:
      name: "global-tailscale-oauth"
      key: "client-secret"
  
  configuration:
    devices:
      ephemeral: false
      preauth: true
      keyExpiry: "180d"  # Longer expiry for global deployment
      advertiseRoutes:
        - "10.0.0.0/8"
        - "172.16.0.0/12"
        - "192.168.0.0/16"
      tags:
        - "tag:global-gateway"
        - "tag:multi-region"
    
    accessControl:
      defaultAction: "deny"
      rules:
        # Regional access patterns
        - source: ["tag:us-east"]
          destination: ["tag:us-east:*", "tag:global-service:*"]
          action: "allow"
        
        - source: ["tag:eu-west"]
          destination: ["tag:eu-west:*", "tag:global-service:*"]
          action: "allow"
        
        # Cross-region for specific services
        - source: ["tag:api-gateway"]
          destination: ["tag:database:5432", "tag:cache:6379"]
          action: "allow"
      
      groups:
        us-engineers: ["us-dev@company.com"]
        eu-engineers: ["eu-dev@company.com"]
        global-admins: ["admin@company.com"]
      
      tagOwners:
        "tag:us-east": ["group:us-engineers", "group:global-admins"]
        "tag:eu-west": ["group:eu-engineers", "group:global-admins"]
        "tag:global-service": ["group:global-admins"]
    
    networking:
      magicDNS: true
      dnsSettings:
        globalNameservers:
          - "1.1.1.1"
          - "8.8.8.8"
        searchDomains:
          - "company.com"
          - "us-east.company.com"
          - "eu-west.company.com"
      exitNodes:
        - "us-east-exit"
        - "eu-west-exit"
        - "ap-south-exit"
```

### Secure Enterprise Configuration

```yaml
apiVersion: gateway.tailscale.com/v1alpha1
kind: TailscaleTailnet
metadata:
  name: enterprise-tailnet
  namespace: tailscale-gateway-system
spec:
  tailnet: "enterprise.company.ts.net"
  
  oauth:
    clientIdRef:
      name: "enterprise-tailscale-oauth"
      key: "client-id"
    clientSecretRef:
      name: "enterprise-tailscale-oauth"
      key: "client-secret"
    scopes:
      - "device:create"
      - "device:read"
      - "device:write"
      - "tailnet:read"
      - "acl:read"
      - "acl:write"
  
  configuration:
    devices:
      ephemeral: false
      preauth: false  # Require manual approval for security
      keyExpiry: "30d"  # Frequent key rotation
      machineAuth: true
      requireApproval: true
      advertiseRoutes:
        - "10.100.0.0/16"  # Restricted network range
      tags:
        - "tag:enterprise-gateway"
        - "tag:security-monitored"
    
    accessControl:
      defaultAction: "deny"
      rules:
        # Strict least-privilege access
        - source: ["tag:web-tier"]
          destination: ["tag:app-tier:8080"]
          action: "allow"
        
        - source: ["tag:app-tier"]
          destination: ["tag:db-tier:5432"]
          action: "allow"
        
        # Admin access with restrictions
        - source: ["group:security-admins"]
          destination: ["tag:enterprise-gateway:22,443"]
          action: "allow"
        
        # Audit all database access
        - source: ["*"]
          destination: ["tag:db-tier:*"]
          action: "audit"
      
      groups:
        security-admins: ["security@company.com"]
        web-developers: ["web-dev@company.com"]
        app-developers: ["app-dev@company.com"]
        dba-team: ["dba@company.com"]
      
      tagOwners:
        "tag:enterprise-gateway": ["group:security-admins"]
        "tag:web-tier": ["group:web-developers", "group:security-admins"]
        "tag:app-tier": ["group:app-developers", "group:security-admins"]
        "tag:db-tier": ["group:dba-team", "group:security-admins"]
    
    networking:
      magicDNS: true
      dnsSettings:
        globalNameservers:
          - "10.100.1.1"  # Internal DNS servers
          - "10.100.1.2"
        searchDomains:
          - "internal.company.com"
          - "secure.company.com"
  
  observability:
    logLevel: "info"
    auditLogging: true
    metrics:
      enabled: true
      interval: "15s"  # Frequent metrics for security monitoring
    
    # Security monitoring integration
    securityMonitoring:
      enabled: true
      siemIntegration:
        endpoint: "https://siem.company.com/api/events"
        apiKey:
          secretRef:
            name: "siem-credentials"
            key: "api-key"
```

## Status

The `TailscaleTailnet` resource provides detailed status information:

```yaml
status:
  conditions:
    - type: "Ready"
      status: "True"
      reason: "TailnetConnected"
      message: "Successfully connected to tailnet"
    - type: "Authenticated"
      status: "True"
      reason: "OAuthValid"
      message: "OAuth credentials are valid"
  
  tailnetInfo:
    name: "company.ts.net"
    organization: "Company Inc"
    plan: "Enterprise"
    deviceCount: 45
    userCount: 23
    
  connectionInfo:
    lastConnected: "2024-01-15T10:30:00Z"
    apiEndpoint: "https://api.tailscale.com"
    oauthStatus: "Valid"
    
  deviceRegistration:
    registeredDevices: 3
    activeDevices: 3
    expiredDevices: 0
```

## Best Practices

### 1. **Security Configuration**
```yaml
# Always use least-privilege access
accessControl:
  defaultAction: "deny"
  rules:
    - source: ["group:specific-users"]
      destination: ["tag:specific-service:specific-port"]
      action: "allow"
```

### 2. **Key Management**
```yaml
# Regular key rotation
devices:
  keyExpiry: "30d"  # Monthly rotation for production
  requireApproval: true  # Manual approval for security
```

### 3. **Network Segmentation**
```yaml
# Use specific network ranges
devices:
  advertiseRoutes:
    - "10.100.0.0/16"  # Specific to this deployment
```

### 4. **Monitoring and Auditing**
```yaml
observability:
  auditLogging: true
  metrics:
    enabled: true
    interval: "30s"
```

## Troubleshooting

### Common Issues

#### 1. **OAuth Authentication Failures**
```bash
# Check secret exists and has correct keys
kubectl get secret tailscale-oauth -o yaml

# Verify OAuth scopes
kubectl describe tailscaletailnet company-tailnet
```

#### 2. **Device Registration Issues**
```bash
# Check device status
kubectl get tailscaletailnet company-tailnet -o yaml

# Check operator logs
kubectl logs -n tailscale-gateway-system deployment/tailscale-gateway-operator
```

#### 3. **ACL Configuration Problems**
```bash
# Validate ACL syntax
tailscale acl validate

# Check effective ACLs
tailscale acl get
```

## Related Resources

- **[TailscaleGateway](./tailscale-gateway)** - Main gateway configuration
- **[TailscaleEndpoints](./tailscale-endpoints)** - Service endpoint definitions
- **[Configuration Guide](../configuration/overview)** - Advanced configuration options