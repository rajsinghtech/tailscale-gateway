Overall Goals: 
- The user is trying to attach Tailscale VIP services to a kind: Gateway as backendRef's in the gateway API.
- The user is automatically trying to share the kind: GatewayAPI type routes to a tailnet.

# The kind: TailscaleTailnet:
This is non namespaced, by default can be referenced by any TailscaleEndpoint in any namespace. Should have some sort of 
Should have status's about the tailnet like name and organization and its id's.
``` yaml
apiVersion: gateway.tailscale.com/v1alpha1
kind: TailscaleTailnet
metadata:
  name: test-tailnet # Name of tailnet 
spec:
  oauthSecretName: tailscale-oauth # Oauth Key for the tailnet
  oauthSecretNamespace: default
  tags:
  - tag:k8s # Default tags to apply to tailscale endpoints proxies.
status:
  tailnetInfo:
    name: tail8eff9.ts.net # operator gathers info about the tailnets oauth key 
    organization: Personal

```

# The kind: TailscaleEndpoints:
This resource focuses on the infrastructure layer - creating and managing Tailscale proxy StatefulSets:
- Spinning up a set of proxy groups (ingress/egress StatefulSets) following the Tailscale k8s-operator patterns
- Storing mappings of ingress/egress proxy pod/port IPs in status
- Using k8s secrets for state management with the TailscaleTailnet mapping to generate authkeys with appended default tags

``` yaml
apiVersion: gateway.tailscale.com/v1alpha1
kind: TailscaleEndpoints
metadata:
  name: web-proxies
  labels:
    app: web
    environment: production
spec:
  tailnet: test-tailnet # References the TailscaleTailnet resource
  
  # Tags for the Tailscale machines (used in ACL policies)
  tags:
    - "tag:k8s-proxy"
    - "tag:web-service"
  
  # Proxy configuration for StatefulSet creation
  proxy:
    replicas: 2
    connectionType: bidirectional # ingress, egress, or bidirectional
    image: "tailscale/tailscale:latest"
    resources:
      requests:
        cpu: "100m"
        memory: "128Mi"
      limits:
        cpu: "500m"
        memory: "256Mi"
    
  # Ports that the proxy StatefulSets should serve
  ports:
    - port: 80
      protocol: TCP
      name: http
    - port: 443
      protocol: TCP
      name: https

status:
  # StatefulSet references
  statefulSetRefs:
    - name: web-proxies-ingress
      namespace: default
      connectionType: ingress
      readyReplicas: 2
      desiredReplicas: 2
    - name: web-proxies-egress
      namespace: default
      connectionType: egress
      readyReplicas: 2
      desiredReplicas: 2
  
  # Proxy pod information
  endpointStatus:
    - name: web-proxies-ingress-0
      tailscaleIP: 100.68.203.214
      healthStatus: Healthy
    - name: web-proxies-ingress-1
      tailscaleIP: 100.68.203.215
      healthStatus: Healthy
  
  # Overall status
  totalEndpoints: 4
  healthyEndpoints: 4
  conditions:
    - type: StatefulSetsReady
      status: "True"
      reason: AllReplicasReady
```

# The kind: TailscaleServices:
This resource manages the service mesh layer - creating VIP services and selecting endpoints via labels:
- Uses Kubernetes-style label selectors to dynamically select TailscaleEndpoints
- Creates and manages Tailscale VIP services (svc:name format)
- Handles service-level health checking and load balancing
- Supports auto-provisioning of TailscaleEndpoints when none match the selector

``` yaml
apiVersion: gateway.tailscale.com/v1alpha1
kind: TailscaleServices
metadata:
  name: web-service
spec:
  # Select TailscaleEndpoints by labels (like Kubernetes Services select Pods)
  selector:
    matchLabels:
      app: web
      environment: production
  
  # VIP service configuration
  vipService:
    name: "svc:web-service" # Tailscale VIP service name
    ports:
      - "tcp:80"
      - "tcp:443"
    tags:
      - "tag:web-service"
      - "tag:production"
    comment: "Production web service"
    autoApprove: true
    
  # Load balancing configuration
  loadBalancing:
    strategy: round-robin # round-robin, least-connections, weighted, failover
    healthCheck:
      enabled: true
      path: "/health"
      interval: "30s"
      timeout: "5s"
  
  # Optional: Auto-provision TailscaleEndpoints if none match selector
  endpointTemplate:
    tailnet: test-tailnet
    tags:
      - "tag:k8s-proxy"
      - "tag:auto-provisioned"
    proxy:
      replicas: 3
      connectionType: bidirectional
    ports:
      - port: 80
        protocol: TCP
      - port: 443
        protocol: TCP
    labels:
      app: web
      environment: production

status:
  # Selected endpoints
  selectedEndpoints:
    - name: web-proxies
      namespace: default
      machines: ["web-proxies-ingress-0", "web-proxies-ingress-1"]
      status: Ready
      healthStatus:
        healthyMachines: 2
        unhealthyMachines: 0
  
  # VIP service status
  vipServiceStatus:
    created: true
    serviceName: "svc:web-service"
    addresses: ["100.115.92.5", "fd7a:115c:a1e0::1"]
    dnsName: "web-service.tail8eff9.ts.net"
    backendCount: 2
    healthyBackendCount: 2
  
  # Overall status
  totalBackends: 2
  healthyBackends: 2
  conditions:
    - type: VIPServiceReady
      status: "True"
      reason: VIPServiceCreated
```

# How They Work Together:

1. **TailscaleEndpoints** creates the infrastructure:
   - StatefulSets for Tailscale proxy pods
   - Manages the actual Tailscale machines that appear in your tailnet
   - Handles state management and pod lifecycle

2. **TailscaleServices** creates the service mesh:
   - Selects TailscaleEndpoints using label selectors
   - Creates VIP services that route to selected endpoints
   - Manages service-level concerns like health checking and load balancing

3. **Gateway API Integration**:
   - Use TailscaleServices as backendRefs in HTTPRoute/TCPRoute/etc
   - The extension server discovers and processes these routes
   - Traffic flows: Gateway → TailscaleServices VIP → Selected TailscaleEndpoints → Backend services

4. **Auto-Provisioning Flow**:
   - Define TailscaleServices with selector and endpointTemplate
   - If no TailscaleEndpoints match selector → auto-create from template
   - Created endpoints automatically have correct labels for selection
   - Cleanup handled via owner references