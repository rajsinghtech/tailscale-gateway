# TailscaleGateway Implementation Plan

## Overview

This plan outlines the implementation of a TailscaleGateway operator that combines Tailscale's mesh networking with Envoy Gateway's advanced routing capabilities, supporting both ingress (tailnet → cluster) and egress (cluster → tailnet) traffic patterns.

## Architecture Components

### 1. Core Resources

#### TailscaleGateway CRD
```yaml
apiVersion: gateway.tailscale.com/v1alpha1
kind: TailscaleGateway
metadata:
  name: corp-gateway
spec:
  # Reference to existing Envoy Gateway
  gatewayRef:
    name: eg-gateway
    namespace: envoy-gateway-system
  
  # Tailnet configurations
  tailnets:
  - name: corp
    tailscaleTailnetRef:
      name: corp-tailnet
    serviceDiscovery:
      enabled: true
      patterns:
      - "*.corp.internal"
      - "api-*"
      excludePatterns:
      - "proxy-*"
    routeGeneration:
      ingress:
        hostPattern: "{service}.{tailnet}.gateway.local"
        pathPrefix: "/"
        protocol: HTTPS
      egress:
        hostPattern: "{service}.tailscale.local" 
        pathPrefix: "/tailscale/{service}/"
        protocol: HTTP
  - name: dev
    tailscaleTailnetRef:
      name: dev-tailnet
    serviceDiscovery:
      enabled: true
      patterns:
      - "dev-*"

  # Extension server configuration
  extensionServer:
    image: tailscale-gateway-extension:latest
    replicas: 2
    resources:
      requests:
        memory: "128Mi"
        cpu: "100m"
```

#### TailscaleEndpoints CRD  
```yaml
apiVersion: gateway.tailscale.com/v1alpha1
kind: TailscaleEndpoints
metadata:
  name: corp-endpoints
  labels:
    gateway.tailscale.com/tailnet: corp
spec:
  tailnet: corp
  endpoints:
  - name: api-server
    tailscaleIP: 100.64.0.10
    tailscaleFQDN: api-server.corp.ts.net
    port: 8080
    protocol: HTTP
    tags:
    - "tag:api"
    - "tag:production"
  - name: database
    tailscaleIP: 100.64.0.20
    tailscaleFQDN: database.corp.ts.net
    port: 5432
    protocol: TCP
    tags:
    - "tag:database"
status:
  discoveredEndpoints: 15
  lastSync: "2025-06-22T20:30:00Z"
  conditions:
  - type: Synced
    status: True
    reason: DiscoverySuccessful
```

### 2. Extension Server Architecture

#### Extension Server Components
```
┌─────────────────┐    ┌──────────────────┐    ┌─────────────────────┐
│   TailscaleGW   │────│  Extension       │────│  Envoy Gateway      │
│   Controller    │    │  Server          │    │  (Route Injection)  │
└─────────────────┘    └──────────────────┘    └─────────────────────┘
         │                       │                         │
         │                       │                         │
         ▼                       ▼                         ▼
┌─────────────────┐    ┌──────────────────┐    ┌─────────────────────┐
│ Multi-Tailnet   │    │ Service          │    │ xDS Configuration   │
│ Manager         │    │ Discovery        │    │ (Routes/Clusters)   │
└─────────────────┘    └──────────────────┘    └─────────────────────┘
         │                       │                         
         │                       │                         
         ▼                       ▼                         
┌─────────────────┐    ┌──────────────────┐               
│ TailscaleEndpts │    │ Route Generator  │               
│ Resources       │    │ (Ingress/Egress) │               
└─────────────────┘    └──────────────────┘               
```

#### Extension Server Implementation
```go
type TailscaleExtensionServer struct {
    multiTailnetManager *multitailnet.Manager
    routeGenerator      *routing.Generator
    endpointsManager    *endpoints.Manager
    gatewayController   *gateway.Controller
}

// PostVirtualHostModify injects Tailscale routes
func (s *TailscaleExtensionServer) PostVirtualHostModify(
    ctx context.Context,
    req *pb.PostVirtualHostModifyRequest,
) (*pb.PostVirtualHostModifyResponse, error) {
    
    vHost := req.VirtualHost
    
    // Get all TailscaleGateways that match this VirtualHost
    gateways := s.gatewayController.GetGatewaysForVirtualHost(vHost.Domains)
    
    for _, gateway := range gateways {
        // Generate ingress routes (tailnet → cluster)
        ingressRoutes := s.routeGenerator.GenerateIngressRoutes(gateway)
        vHost.Routes = append(vHost.Routes, ingressRoutes...)
        
        // Generate egress routes (cluster → tailnet)  
        egressRoutes := s.routeGenerator.GenerateEgressRoutes(gateway)
        vHost.Routes = append(vHost.Routes, egressRoutes...)
    }
    
    return &pb.PostVirtualHostModifyResponse{VirtualHost: vHost}, nil
}

// PostTranslateModify injects Tailscale clusters
func (s *TailscaleExtensionServer) PostTranslateModify(
    ctx context.Context,
    req *pb.PostTranslateModifyRequest,
) (*pb.PostTranslateModifyResponse, error) {
    
    clusters := req.Clusters
    
    // Add clusters for all discovered Tailscale endpoints
    for _, endpoints := range s.endpointsManager.GetAllEndpoints() {
        tailscaleClusters := s.generateTailscaleClusters(endpoints)
        clusters = append(clusters, tailscaleClusters...)
    }
    
    return &pb.PostTranslateModifyResponse{
        Clusters: clusters,
        Secrets:  req.Secrets,
    }, nil
}
```

### 3. Multi-Tailnet Manager

#### Service Discovery Engine
```go
type MultiTailnetManager struct {
    tailnets map[string]*TailnetConnection
    discoveryConfig *DiscoveryConfig
    eventBus *events.Bus
}

type TailnetConnection struct {
    name string
    client tailscale.Client
    discoveredServices map[string]*TailscaleService
    lastSync time.Time
}

type TailscaleService struct {
    Name         string
    TailscaleIP  string
    FQDN         string
    Port         int
    Protocol     string
    Tags         []string
    HealthStatus string
    LastSeen     time.Time
}

// DiscoverServices polls Tailscale API for services
func (m *MultiTailnetManager) DiscoverServices(ctx context.Context) error {
    for tailnetName, conn := range m.tailnets {
        devices, err := conn.client.Devices(ctx)
        if err != nil {
            continue
        }
        
        services := m.extractServicesFromDevices(devices, tailnetName)
        
        // Update discovered services
        conn.discoveredServices = services
        conn.lastSync = time.Now()
        
        // Notify Extension Server of changes
        m.eventBus.Publish(&events.ServicesUpdated{
            Tailnet:  tailnetName,
            Services: services,
        })
    }
    return nil
}
```

### 4. Traffic Flow Patterns

#### Ingress Pattern (Tailnet → Cluster)
```
[Tailnet Device] ──HTTPS──► [Envoy Gateway] ──HTTP──► [K8s Service] ──► [Pod]
                                   │
                            ┌──────▼──────┐
                            │ Generated   │
                            │ Route:      │
                            │ *.ts.local  │
                            │ → cluster   │
                            └─────────────┘
```

#### Egress Pattern (Cluster → Tailnet)
```
[Pod] ──HTTP──► [K8s Service] ──► [Envoy Gateway] ──HTTPS──► [Tailnet Device]
                                         │
                                  ┌──────▼──────┐
                                  │ Generated   │
                                  │ Route:      │
                                  │ /tailscale/ │
                                  │ → tailnet   │
                                  └─────────────┘
```

## Implementation Phases

### Phase 1: Foundation (Week 1-2)
- [ ] Create TailscaleGateway CRD
- [ ] Create TailscaleEndpoints CRD  
- [ ] Implement basic controller framework
- [ ] Set up Extension Server skeleton

### Phase 2: Service Discovery (Week 3-4)
- [ ] Implement Multi-Tailnet Manager
- [ ] Build service discovery from Tailscale API
- [ ] Create TailscaleEndpoints controller
- [ ] Add basic health checking

### Phase 3: Route Generation (Week 5-6)
- [ ] Implement Extension Server hooks
- [ ] Build route generation for ingress patterns
- [ ] Build route generation for egress patterns
- [ ] Add cluster generation for Tailscale endpoints

### Phase 4: Integration (Week 7-8)
- [ ] Integrate with existing Envoy Gateway
- [ ] End-to-end testing with real traffic
- [ ] Add observability and metrics
- [ ] Performance optimization

### Phase 5: Advanced Features (Week 9-10)
- [ ] Policy integration (rate limiting, auth)
- [ ] Multi-cluster support
- [ ] TLS certificate management
- [ ] Disaster recovery patterns

## Key Benefits

### For Ingress (Tailnet → Cluster)
- **Zero Configuration**: Automatic service discovery and route generation
- **Security**: Tailscale's zero-trust networking with Envoy's policy enforcement
- **Observability**: Rich metrics and tracing through Envoy Gateway
- **Scalability**: Envoy's high-performance proxy with Tailscale's mesh networking

### For Egress (Cluster → Tailnet)  
- **Service Mesh Integration**: Cluster workloads can access Tailscale services seamlessly
- **Load Balancing**: Envoy's advanced load balancing for Tailscale endpoints
- **Circuit Breaking**: Resilience patterns for Tailscale service dependencies
- **Policy Enforcement**: Fine-grained access control and rate limiting

## Technical Considerations

### Service Discovery
- **Polling Strategy**: 30-second intervals with exponential backoff on errors
- **Caching**: Local cache with TTL to reduce API calls
- **Filtering**: Support include/exclude patterns for service discovery
- **Health Checking**: Integrate with Tailscale device health status

### Route Management
- **Conflict Resolution**: Prioritize TailscaleGateway routes over default Gateway API routes
- **Dynamic Updates**: Real-time route updates as services change
- **Namespace Isolation**: Support multiple TailscaleGateways per cluster
- **Host Collision**: Prevent routing conflicts between different tailnets

### Security
- **RBAC**: Minimal required permissions for Tailscale API access
- **Secret Management**: Secure storage of OAuth credentials
- **Network Policies**: Automatic NetworkPolicy generation for Tailscale traffic
- **mTLS**: Optional mutual TLS between Envoy and Tailscale services

### Operations
- **Monitoring**: Prometheus metrics for discovery, routing, and health
- **Alerting**: Alert on discovery failures, route conflicts, endpoint health
- **Debugging**: Rich logging and tracing for troubleshooting
- **Backup/Restore**: State management for disaster recovery

This implementation provides a robust, scalable solution for integrating Tailscale's mesh networking with Envoy Gateway's advanced traffic management capabilities, supporting both ingress and egress patterns while maintaining the benefits of both systems.