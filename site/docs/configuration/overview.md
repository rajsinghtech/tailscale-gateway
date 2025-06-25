---
id: overview
title: Configuration Overview
sidebar_position: 1
---

# Configuration Overview

This guide covers advanced configuration options for the Tailscale Gateway Operator, including custom resource configuration, operator settings, and integration options.

## Configuration Hierarchy

The Tailscale Gateway Operator uses a layered configuration approach:

```
┌─────────────────────────────────┐
│        Operator Config          │  ← Global settings, OAuth, logging
├─────────────────────────────────┤
│      TailscaleGateway           │  ← Gateway-level configuration
├─────────────────────────────────┤
│     TailscaleEndpoints          │  ← Service-specific settings
├─────────────────────────────────┤
│    TailscaleRoutePolicy         │  ← Advanced routing rules
└─────────────────────────────────┘
```

## Global Operator Configuration

### Environment Variables

The operator reads configuration from environment variables and command-line flags:

```yaml title="operator-config.yaml"
apiVersion: apps/v1
kind: Deployment
metadata:
  name: tailscale-gateway-operator
  namespace: tailscale-system
spec:
  template:
    spec:
      containers:
      - name: manager
        env:
        # OAuth Configuration
        - name: TAILSCALE_CLIENT_ID
          valueFrom:
            secretKeyRef:
              name: tailscale-oauth
              key: client-id
        - name: TAILSCALE_CLIENT_SECRET
          valueFrom:
            secretKeyRef:
              name: tailscale-oauth
              key: client-secret
        
        # Operator Settings
        - name: OPERATOR_NAMESPACE
          value: "tailscale-system"
        - name: METRICS_BIND_ADDRESS
          value: ":8080"
        - name: HEALTH_PROBE_BIND_ADDRESS
          value: ":8081"
        
        # Extension Server
        - name: EXTENSION_SERVER_ENABLED
          value: "true"
        - name: EXTENSION_SERVER_PORT
          value: "5005"
        - name: EXTENSION_SERVER_MAX_MESSAGE_SIZE
          value: "1000M"
        
        # Service Coordination
        - name: SERVICE_COORDINATOR_ENABLED
          value: "true"
        - name: SERVICE_COORDINATOR_CLUSTER_ID
          value: "main-cluster"
        - name: SERVICE_COORDINATOR_OPERATOR_ID
          value: "operator-1"
        
        # Logging and Debugging
        - name: LOG_LEVEL
          value: "info"
        - name: LOG_FORMAT
          value: "json"
        - name: DEBUG_MODE
          value: "false"
        
        # Performance Tuning
        - name: CONTROLLER_MAX_CONCURRENT_RECONCILES
          value: "10"
        - name: SYNC_PERIOD
          value: "30s"
        - name: LEADER_ELECTION_ENABLED
          value: "true"
```

### ConfigMap Configuration

For more complex configuration, use a ConfigMap:

```yaml title="operator-configmap.yaml"
apiVersion: v1
kind: ConfigMap
metadata:
  name: tailscale-gateway-config
  namespace: tailscale-system
data:
  config.yaml: |
    operator:
      namespace: "tailscale-system"
      metricsBindAddress: ":8080"
      healthProbeBindAddress: ":8081"
      leaderElection: true
      maxConcurrentReconciles: 10
      syncPeriod: "30s"
    
    extensionServer:
      enabled: true
      port: 5005
      maxMessageSize: "1000M"
      grpcOptions:
        maxReceiveMessageSize: 104857600  # 100MB
        maxSendMessageSize: 104857600     # 100MB
        keepaliveTime: "30s"
        keepaliveTimeout: "5s"
    
    serviceCoordinator:
      enabled: true
      clusterId: "main-cluster"
      operatorId: "operator-1"
      cleanupInterval: "5m"
      staleThreshold: "30m"
    
    tailscale:
      baseURL: "https://api.tailscale.com"
      timeout: "30s"
      retryAttempts: 3
      retryDelay: "5s"
    
    logging:
      level: "info"
      format: "json"
      development: false
    
    features:
      tagBasedDiscovery: true
      vipServices: true
      multiTailnetSupport: true
      crossClusterCoordination: true
```

## TailscaleGateway Configuration

### Basic Gateway Configuration

```yaml title="basic-gateway.yaml"
apiVersion: gateway.tailscale.com/v1alpha1
kind: TailscaleGateway
metadata:
  name: production-gateway
  namespace: production
spec:
  # Gateway API reference
  gatewayRef:
    kind: Gateway
    name: envoy-gateway
    namespace: envoy-gateway-system
  
  # Multi-tailnet configuration
  tailnets:
    - name: main-tailnet
      tailscaleTailnetRef:
        kind: TailscaleTailnet
        name: main-tailnet
      
      # Route generation settings
      routeGeneration:
        ingress:
          enabled: true
          hostPattern: "{service}.company.ts.net"
          pathPrefix: "/"
          defaultBackend:
            weight: 100
        egress:
          enabled: true
          hostPattern: "{service}.internal.company.com"
          pathPrefix: "/api/{service}/"
          rewriteRules:
            - from: "/api/{service}/"
              to: "/"
  
  # Service coordination
  serviceCoordination:
    enabled: true
    strategy: "shared"  # or "isolated"
    
  # Health monitoring
  healthCheck:
    enabled: true
    interval: "30s"
    timeout: "10s"
    
  # Resource limits
  resources:
    statefulSetReplicas: 1
    resourceRequests:
      cpu: "100m"
      memory: "128Mi"
    resourceLimits:
      cpu: "500m"
      memory: "256Mi"
```

### Advanced Gateway Configuration

```yaml title="advanced-gateway.yaml"
apiVersion: gateway.tailscale.com/v1alpha1
kind: TailscaleGateway
metadata:
  name: enterprise-gateway
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
      
      routeGeneration:
        ingress:
          enabled: true
          hostPattern: "{service}.prod.company.ts.net"
          pathPrefix: "/"
          filters:
            - type: "RequestHeaderModifier"
              requestHeaderModifier:
                add:
                  - name: "X-Environment"
                    value: "production"
                  - name: "X-Gateway-Version"
                    value: "v1.0.0"
        
        egress:
          enabled: true
          hostPattern: "{service}.prod.internal"
          pathPrefix: "/prod/{service}/"
          timeout: "30s"
          retryPolicy:
            numRetries: 3
            perTryTimeout: "10s"
            backoffBaseInterval: "1s"
            backoffMaxInterval: "10s"
    
    # Staging tailnet
    - name: staging-tailnet
      tailscaleTailnetRef:
        kind: TailscaleTailnet
        name: staging-tailnet
      
      routeGeneration:
        ingress:
          enabled: true
          hostPattern: "{service}.staging.company.ts.net"
          pathPrefix: "/staging/"
        egress:
          enabled: true
          hostPattern: "{service}.staging.internal"
          pathPrefix: "/staging/{service}/"
  
  # Load balancing configuration
  loadBalancing:
    strategy: "round_robin"  # round_robin, least_request, random
    healthChecking:
      enabled: true
      interval: "30s"
      timeout: "10s"
      unhealthyThreshold: 3
      healthyThreshold: 2
  
  # Security settings
  security:
    tlsMode: "strict"  # strict, permissive, disabled
    mtls:
      enabled: true
      certificateRef:
        name: "tailscale-mtls-cert"
        namespace: "tailscale-system"
    
    # Access control
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
      path: "/metrics"
    
    tracing:
      enabled: true
      samplingRate: 0.1
      jaegerEndpoint: "http://jaeger-collector.observability:14268/api/traces"
    
    logging:
      level: "info"
      accessLogs: true
      errorLogs: true
```

## TailscaleEndpoints Configuration

### Service Discovery Configuration

```yaml title="service-discovery.yaml"
apiVersion: gateway.tailscale.com/v1alpha1
kind: TailscaleEndpoints
metadata:
  name: auto-discovery
  namespace: production
spec:
  tailnet: "company.ts.net"
  
  # Tag-based discovery
  tagSelectors:
    - key: "env"
      operator: "In"
      values: ["production", "staging"]
    - key: "service-type"
      operator: "Exists"
    - key: "team"
      operator: "NotIn"
      values: ["deprecated"]
  
  # Service discovery settings
  serviceDiscovery:
    enabled: true
    autoRegister: true
    discoveryInterval: "60s"
    
    # VIP service configuration
    vipServices:
      - name: "web-cluster"
        port: 80
        targetEndpoint: "web-service"
        healthCheck:
          enabled: true
          interval: "30s"
      
      - name: "api-cluster" 
        port: 8080
        targetEndpoint: "api-service"
        loadBalancing:
          strategy: "least_request"
    
    # Discovery filters
    filters:
      includeOffline: false
      includeExpired: false
      maxAge: "24h"
      
    # Metadata propagation
    metadataPropagation:
      enabled: true
      allowedKeys:
        - "version"
        - "region"
        - "team"
        - "environment"
  
  # Static endpoint definitions
  endpoints:
    - name: legacy-service
      tailscaleIP: "100.64.0.100"
      tailscaleFQDN: "legacy.company.ts.net"
      port: 8080
      protocol: "HTTP"
      externalTarget: "legacy.internal.company.com:8080"
      
      # Advanced health checking
      healthCheck:
        enabled: true
        type: "HTTP"
        path: "/health"
        interval: "30s"
        timeout: "10s"
        healthyThreshold: 2
        unhealthyThreshold: 3
        expectedStatus: 200
        headers:
          "User-Agent": "TailscaleGateway/HealthCheck"
          "Accept": "application/json"
        
        # Retry configuration
        retryPolicy:
          attempts: 3
          backoff: "exponential"
          baseDelay: "1s"
          maxDelay: "10s"
      
      # Connection settings
      connectionSettings:
        maxConnections: 100
        maxPendingRequests: 50
        maxRequests: 1000
        maxRetries: 3
        connectTimeout: "10s"
        tcpKeepalive:
          enabled: true
          time: "600s"
          interval: "60s"
          probes: 9
      
      # Circuit breaker
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

### Advanced Endpoint Configuration

```yaml title="advanced-endpoints.yaml"
apiVersion: gateway.tailscale.com/v1alpha1
kind: TailscaleEndpoints
metadata:
  name: enterprise-services
  namespace: production
spec:
  tailnet: "company.ts.net"
  
  # Global endpoint settings
  defaults:
    healthCheck:
      enabled: true
      interval: "30s"
      timeout: "10s"
      healthyThreshold: 2
      unhealthyThreshold: 3
    
    connectionSettings:
      connectTimeout: "10s"
      tcpKeepalive:
        enabled: true
        time: "600s"
        interval: "60s"
    
    metadata:
      environment: "production"
      cluster: "main"
  
  endpoints:
    # High-availability API service
    - name: api-service
      tailscaleIP: "100.64.0.50"
      tailscaleFQDN: "api.company.ts.net"
      port: 8080
      protocol: "HTTPS"
      externalTarget: "api.internal.company.com:8080"
      
      # Override defaults
      healthCheck:
        enabled: true
        type: "HTTP"
        path: "/api/v1/health"
        interval: "15s"  # More frequent for critical service
        timeout: "5s"
        expectedStatus: 200
        headers:
          "Authorization": "Bearer health-check-token"
        
        # Custom health check logic
        customChecks:
          - name: "database-connectivity"
            endpoint: "/health/database"
            criticalityLevel: "high"
          - name: "cache-status"
            endpoint: "/health/cache"
            criticalityLevel: "medium"
      
      # Performance optimization
      connectionSettings:
        maxConnections: 500
        maxPendingRequests: 100
        http2Settings:
          enabled: true
          maxConcurrentStreams: 100
          initialStreamWindow: 65536
          initialConnectionWindow: 1048576
      
      # Advanced routing
      routingOptions:
        hashPolicy:
          enabled: true
          hashOn: "header"
          headerName: "X-User-ID"
        
        weightedClusters:
          - name: "primary"
            weight: 80
            target: "api-primary.internal.company.com:8080"
          - name: "canary"
            weight: 20
            target: "api-canary.internal.company.com:8080"
      
      # Security configuration
      security:
        tls:
          enabled: true
          minVersion: "TLSv1.2"
          maxVersion: "TLSv1.3"
          cipherSuites:
            - "TLS_AES_256_GCM_SHA384"
            - "TLS_CHACHA20_POLY1305_SHA256"
          certificateValidation:
            enabled: true
            trustedCA: "company-ca"
            subjectAltNameMatching: true
        
        # Rate limiting
        rateLimiting:
          enabled: true
          requestsPerSecond: 1000
          burstSize: 2000
          
    # Database service with connection pooling
    - name: database-service
      tailscaleIP: "100.64.0.51"
      tailscaleFQDN: "db.company.ts.net"
      port: 5432
      protocol: "TCP"
      externalTarget: "postgres.internal.company.com:5432"
      
      # Database-specific settings
      connectionSettings:
        maxConnections: 50  # Limit for database
        connectionTimeout: "30s"
        idleTimeout: "300s"
        
        # Connection pooling
        connectionPool:
          enabled: true
          maxIdleConnections: 10
          maxActiveConnections: 50
          maxLifetime: "1h"
          maxIdleTime: "30m"
      
      # Database health checking
      healthCheck:
        enabled: true
        type: "TCP"
        interval: "60s"
        timeout: "10s"
        
        # Custom database health check
        customChecks:
          - name: "replication-lag"
            query: "SELECT pg_last_wal_receive_lsn() = pg_last_wal_replay_lsn()"
            timeout: "5s"
            criticalityLevel: "high"
```

## TailscaleTailnet Configuration

### Multi-Tailnet Setup

```yaml title="multi-tailnet.yaml"
---
# Production tailnet
apiVersion: gateway.tailscale.com/v1alpha1
kind: TailscaleTailnet
metadata:
  name: prod-tailnet
  namespace: production
spec:
  tailnet: "prod.company.ts.net"
  
  # OAuth configuration (specific to this tailnet)
  oauth:
    clientIdRef:
      name: "prod-tailscale-oauth"
      key: "client-id"
    clientSecretRef:
      name: "prod-tailscale-oauth"
      key: "client-secret"
  
  # Tailnet-specific settings
  configuration:
    # Device settings
    devices:
      ephemeral: false
      preauth: true
      advertiseRoutes:
        - "10.0.0.0/8"
        - "172.16.0.0/12"
      
      # Security settings
      keyExpiry: "90d"
      machineAuth: true
      requireApproval: false
      
      # Networking
      magicDNS: true
      dnsSettings:
        globalNameservers:
          - "1.1.1.1"
          - "8.8.8.8"
        searchDomains:
          - "company.com"
          - "internal.company.com"
    
    # ACL policies
    accessControl:
      defaultAction: "deny"
      rules:
        # Allow prod services to communicate
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
      
      # Groups and tags
      groups:
        developers: ["user1@company.com", "user2@company.com"]
        admins: ["admin@company.com"]
      
      tagOwners:
        "tag:prod-server": ["group:admins"]
        "tag:database": ["group:admins"]
        "tag:cache": ["group:developers", "group:admins"]
  
  # Monitoring and logging
  observability:
    logLevel: "info"
    auditLogging: true
    metrics:
      enabled: true
      interval: "30s"

---
# Staging tailnet
apiVersion: gateway.tailscale.com/v1alpha1
kind: TailscaleTailnet
metadata:
  name: staging-tailnet
  namespace: staging
spec:
  tailnet: "staging.company.ts.net"
  
  oauth:
    clientIdRef:
      name: "staging-tailscale-oauth"
      key: "client-id"
    clientSecretRef:
      name: "staging-tailscale-oauth"
      key: "client-secret"
  
  configuration:
    devices:
      ephemeral: true  # Temporary for staging
      preauth: true
      keyExpiry: "30d"
      requireApproval: false
    
    accessControl:
      defaultAction: "allow"  # More permissive for staging
      rules:
        - source: ["*"]
          destination: ["*"]
          action: "allow"
```

## Performance Tuning

### Operator Performance

```yaml title="performance-config.yaml"
# High-performance operator configuration
apiVersion: v1
kind: ConfigMap
metadata:
  name: performance-config
  namespace: tailscale-system
data:
  config.yaml: |
    operator:
      # Controller performance
      maxConcurrentReconciles: 20
      syncPeriod: "15s"
      resyncPeriod: "5m"
      
      # Memory and CPU optimization
      garbageCollection:
        enabled: true
        interval: "5m"
        memoryThreshold: "80%"
      
      # Connection pooling
      clientConfig:
        qps: 100
        burst: 200
        timeout: "30s"
        
    extensionServer:
      # gRPC performance
      grpcOptions:
        maxReceiveMessageSize: 209715200  # 200MB
        maxSendMessageSize: 209715200     # 200MB
        keepaliveTime: "30s"
        keepaliveTimeout: "5s"
        maxConnectionIdle: "300s"
        maxConnectionAge: "1800s"
        
      # Connection management
      connectionPool:
        maxIdleConnections: 100
        maxActiveConnections: 500
        connectionTimeout: "10s"
        idleTimeout: "60s"
        
    serviceCoordinator:
      # Coordination performance
      cleanupInterval: "2m"
      staleThreshold: "15m"
      batchSize: 50
      maxRetries: 5
      
    # Caching configuration
    cache:
      serviceRegistry:
        ttl: "5m"
        maxSize: 1000
      
      routeCache:
        ttl: "1m"
        maxSize: 5000
        
      healthCheckCache:
        ttl: "30s"
        maxSize: 10000
```

### Resource Limits

```yaml title="resource-limits.yaml"
apiVersion: apps/v1
kind: Deployment
metadata:
  name: tailscale-gateway-operator
  namespace: tailscale-system
spec:
  template:
    spec:
      containers:
      - name: manager
        resources:
          requests:
            cpu: "200m"
            memory: "256Mi"
          limits:
            cpu: "1000m"
            memory: "512Mi"
        
        # JVM tuning for extension server
        env:
        - name: JAVA_OPTS
          value: "-Xms128m -Xmx256m -XX:+UseG1GC -XX:MaxGCPauseMillis=100"
        
        # Go runtime tuning
        - name: GOGC
          value: "100"
        - name: GOMEMLIMIT
          value: "400MiB"
```

## Security Configuration

### TLS Configuration

```yaml title="tls-config.yaml"
apiVersion: v1
kind: ConfigMap
metadata:
  name: tls-config
  namespace: tailscale-system
data:
  tls.yaml: |
    tls:
      # Global TLS settings
      minVersion: "TLSv1.2"
      maxVersion: "TLSv1.3"
      
      # Cipher suites
      cipherSuites:
        - "TLS_AES_256_GCM_SHA384"
        - "TLS_CHACHA20_POLY1305_SHA256"
        - "TLS_AES_128_GCM_SHA256"
        - "TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384"
        - "TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305"
      
      # Certificate management
      certificates:
        # CA certificate for validation
        ca:
          configMapRef:
            name: "company-ca-cert"
            key: "ca.crt"
        
        # Client certificates for mTLS
        client:
          secretRef:
            name: "tailscale-client-cert"
            keyFile: "tls.key"
            certFile: "tls.crt"
        
        # Server certificates
        server:
          secretRef:
            name: "tailscale-server-cert"
            keyFile: "tls.key"
            certFile: "tls.crt"
      
      # mTLS configuration
      mutualTLS:
        enabled: true
        verifyClientCert: true
        requireClientCert: true
        
      # OCSP stapling
      ocsp:
        enabled: true
        responderURL: "http://ocsp.company.com"
        cacheTimeout: "1h"
```

## Validation and Defaults

### Schema Validation

```yaml title="validation-config.yaml"
apiVersion: v1
kind: ConfigMap
metadata:
  name: validation-config
  namespace: tailscale-system
data:
  validation.yaml: |
    validation:
      # Endpoint validation
      endpoints:
        namePattern: "^[a-z0-9-]+$"
        maxNameLength: 63
        
        # IP validation
        tailscaleIPRanges:
          - "100.64.0.0/10"
          - "fd7a:115c:a1e0::/48"
        
        # Port validation
        portRanges:
          - min: 1
            max: 65535
            protocols: ["TCP", "UDP"]
          - min: 80
            max: 80
            protocols: ["HTTP"]
          - min: 443
            max: 443
            protocols: ["HTTPS"]
        
        # Protocol validation
        supportedProtocols:
          - "HTTP"
          - "HTTPS"
          - "TCP"
          - "UDP"
          - "TLS"
          - "gRPC"
      
      # Health check validation
      healthChecks:
        minInterval: "10s"
        maxInterval: "300s"
        minTimeout: "1s"
        maxTimeout: "60s"
        maxThreshold: 10
        
      # Service discovery validation
      serviceDiscovery:
        maxVIPServices: 100
        maxTagSelectors: 10
        supportedOperators:
          - "In"
          - "NotIn"
          - "Exists"
          - "DoesNotExist"
```

## Next Steps

- **[API Reference](../api/tailscale-endpoints)** - Complete API documentation
- **[Examples](../examples/basic-usage)** - Working configuration examples
- **[Operations Guide](../operations/monitoring)** - Production deployment and monitoring