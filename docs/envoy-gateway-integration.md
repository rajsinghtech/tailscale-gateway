# Envoy Gateway Integration

This document describes how the Tailscale Gateway Operator integrates with Envoy Gateway using extension servers to provide dynamic route and cluster injection.

## Architecture Overview

The integration follows this pattern:

```
┌─────────────────┐    ┌──────────────────┐    ┌─────────────────────┐
│   Tailscale     │────│   Envoy Gateway  │────│  External Services  │
│   Clients       │    │   (with routes   │    │  (via egress proxy) │
└─────────────────┘    │   injected by    │    └─────────────────────┘
                       │   extension)     │               ▲
                       └──────────────────┘               │
                                │                         │
                                │                         │
                       ┌──────────────────┐               │
                       │ Tailscale Gateway│───────────────┘
                       │ Operator with    │
                       │ Integrated       │
                       │ Extension Server │
                       └──────────────────┘
                                │
                                │
                       ┌──────────────────┐
                       │ TailscaleEndpoints│
                       │    Resources      │
                       └──────────────────┘
```

## Flow Description

1. **Egress Path**: Tailscale clients → Envoy Gateway → External Services
   - TailscaleEndpoints egress services act as backends for Envoy Gateway routes
   - Extension server injects routes from `/api/{service}` to external backends
   - External services are reached through Tailscale egress proxy StatefulSets

2. **Ingress Path**: External clients → Envoy Gateway → Tailscale services  
   - TailscaleEndpoints ingress services point to Envoy Gateway
   - External clients reach Tailscale services through the gateway

## Extension Server Implementation

The extension server implements the Envoy Gateway Extension API with these hooks:

### PostVirtualHostModify Hook
- Discovers TailscaleEndpoints resources with `ExternalTarget` defined
- Injects routes with pattern `/api/{service}` → external backend cluster
- Example: `/api/web-service` → `external-backend-web-service` cluster

### PostTranslateModify Hook  
- Creates Envoy clusters pointing to external backend services
- Maps `external-backend-{service}` to actual external service addresses
- Configures load balancing and health checking

## Configuration

### 1. Deploy Tailscale Gateway Operator (with integrated extension server)

```bash
# Extension server is now integrated into the main operator
kubectl apply -f config/manager/deployment.yaml
```

### 2. Configure Envoy Gateway

```bash
kubectl apply -f config/envoy-gateway/envoy-gateway-config.yaml
```

### 3. Create TailscaleEndpoints with External Targets

```yaml
apiVersion: gateway.tailscale.com/v1alpha1
kind: TailscaleEndpoints
metadata:
  name: example-endpoints
spec:
  endpoints:
  - name: web-service
    port: 80
    protocol: HTTP
    externalTarget: "example.com:80"  # External service to route to
  - name: api-service
    port: 443
    protocol: HTTPS
    externalTarget: "api.example.com:443"
```

## Extension Server Configuration

The integrated extension server is configured via environment variables in the operator:

- `EXTENSION_SERVER_ENABLED`: Enable extension server (default: true)
- `EXTENSION_SERVER_PORT`: gRPC server port (default: 5005)
- Plus standard Kubernetes service account permissions to read TailscaleEndpoints

## Envoy Gateway Configuration

Key configuration in the EnvoyGateway resource:

```yaml
extensionManager:
  maxMessageSize: 1000M
  hooks:
    xdsTranslator:
      post:
      - VirtualHost    # Inject routes
      - Translation    # Inject clusters
  service:
    fqdn:
      hostname: tailscale-gateway-operator.tailscale-gateway-system.svc.cluster.local
      port: 5005
```

## Route Patterns

The extension server creates these route patterns:

- `/api/{service}` → Routes to external backend for service
- Prefix rewriting strips `/api/{service}` when forwarding

Example:
- Request: `GET /api/web-service/status`
- Forwarded to external backend: `GET /status`

## Cluster Configuration

External backend clusters use static endpoint discovery:

```yaml
name: external-backend-web-service
type: STATIC  
load_assignment:
  endpoints:
  - lb_endpoints:
    - endpoint:
        address:
          socket_address:
            address: "example.com"
            port_value: 80
```

## Testing

1. Deploy a TailscaleEndpoints resource with `externalTarget`
2. Check extension server logs for route/cluster injection
3. Verify Envoy configuration includes injected routes and clusters
4. Test traffic flow: Tailscale client → Gateway → External service

## Troubleshooting

### Extension Server Logs
```bash
# Extension server logs are integrated into the operator logs
kubectl logs -n tailscale-gateway-system deployment/tailscale-gateway-operator -c manager
```

### Envoy Configuration Dump
```bash
kubectl exec -n envoy-gateway-system deployment/envoy-gateway -- \
  curl -s http://localhost:19000/config_dump
```

### Route Verification
Check that routes are injected with the expected patterns:
```bash
# Look for tailscale-egress-* routes in the config dump
kubectl exec -n envoy-gateway-system deployment/envoy-gateway -- \
  curl -s http://localhost:19000/config_dump | jq '.configs[].dynamic_route_configs[].route_config.virtual_hosts[].routes[]'
```