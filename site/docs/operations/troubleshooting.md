---
id: troubleshooting
title: Troubleshooting Guide
sidebar_position: 3
---

# Troubleshooting Guide

Common issues and solutions for the Tailscale Gateway Operator.

## Quick Diagnostics

### Health Check Commands

```bash
# Check operator status
kubectl get pods -n tailscale-system
kubectl get tailscalegateways,tailscaleendpoints -A

# Check operator logs
kubectl logs -n tailscale-system deployment/tailscale-gateway-operator --tail=100

# Check extension server logs
kubectl logs -n tailscale-system deployment/tailscale-gateway-extension-server --tail=100

# Check custom resource status
kubectl describe tailscalegateway <gateway-name>
kubectl describe tailscaleendpoints <endpoints-name>
```

### System Information

```bash
# Check Kubernetes version
kubectl version --short

# Check Envoy Gateway status
kubectl get gateways,httproutes -A
kubectl get pods -n envoy-gateway-system

# Check network connectivity
kubectl exec -it deployment/tailscale-gateway-operator -- curl -I https://api.tailscale.com
```

## Common Issues

### 1. Operator Installation Issues

#### Problem: Operator Pods CrashLooping

**Symptoms:**
- Operator pods in `CrashLoopBackOff` state
- Error messages in pod logs

**Diagnosis:**
```bash
# Check pod status
kubectl get pods -n tailscale-system -l app=tailscale-gateway-operator

# Check recent logs
kubectl logs -n tailscale-system deployment/tailscale-gateway-operator --previous

# Check events
kubectl get events -n tailscale-system --sort-by='.lastTimestamp'
```

**Common Causes & Solutions:**

1. **Missing OAuth Credentials**
   ```bash
   # Check if secret exists
   kubectl get secret tailscale-oauth -n tailscale-system
   
   # Verify secret contents
   kubectl get secret tailscale-oauth -n tailscale-system -o yaml
   
   # Create missing secret
   kubectl create secret generic tailscale-oauth \
     --from-literal=client-id=YOUR_CLIENT_ID \
     --from-literal=client-secret=YOUR_CLIENT_SECRET \
     -n tailscale-system
   ```

2. **RBAC Permission Issues**
   ```bash
   # Check service account
   kubectl get serviceaccount tailscale-gateway-operator -n tailscale-system
   
   # Check cluster role binding
   kubectl get clusterrolebinding tailscale-gateway-operator
   
   # Verify permissions
   kubectl auth can-i create tailscaleendpoints --as=system:serviceaccount:tailscale-system:tailscale-gateway-operator
   ```

3. **Resource Limits Exceeded**
   ```bash
   # Check resource usage
   kubectl top pods -n tailscale-system
   
   # Increase resource limits
   kubectl patch deployment tailscale-gateway-operator -n tailscale-system -p '
   {
     "spec": {
       "template": {
         "spec": {
           "containers": [
             {
               "name": "manager",
               "resources": {
                 "limits": {
                   "memory": "512Mi",
                   "cpu": "1000m"
                 }
               }
             }
           ]
         }
       }
     }
   }'
   ```

#### Problem: CRDs Not Installing

**Symptoms:**
- `no matches for kind "TailscaleEndpoints"`
- CRD installation errors

**Solution:**
```bash
# Check if CRDs exist
kubectl get crd | grep tailscale

# Manually install CRDs
kubectl apply -f https://github.com/rajsinghtech/tailscale-gateway/releases/latest/download/crds.yaml

# Verify CRD installation
kubectl get crd tailscaleendpoints.gateway.tailscale.com -o yaml
```

### 2. Tailscale Authentication Issues

#### Problem: OAuth Authentication Failures

**Symptoms:**
- `Failed to authenticate with Tailscale API`
- `Invalid OAuth credentials`

**Diagnosis:**
```bash
# Check OAuth secret
kubectl get secret tailscale-oauth -n tailscale-system -o jsonpath='{.data.client-id}' | base64 -d
kubectl get secret tailscale-oauth -n tailscale-system -o jsonpath='{.data.client-secret}' | base64 -d

# Test API connectivity
kubectl exec -it deployment/tailscale-gateway-operator -- curl -H "Authorization: Bearer $(echo -n 'CLIENT_ID:CLIENT_SECRET' | base64)" https://api.tailscale.com/api/v2/tailnet
```

**Solutions:**

1. **Verify OAuth Credentials**
   - Check Tailscale Admin Console for correct Client ID/Secret
   - Ensure OAuth client has required scopes:
     - `device:create`
     - `device:read`
     - `device:write`
     - `tailnet:read`

2. **Recreate OAuth Secret**
   ```bash
   kubectl delete secret tailscale-oauth -n tailscale-system
   kubectl create secret generic tailscale-oauth \
     --from-literal=client-id=YOUR_CORRECT_CLIENT_ID \
     --from-literal=client-secret=YOUR_CORRECT_CLIENT_SECRET \
     -n tailscale-system
   
   # Restart operator
   kubectl rollout restart deployment/tailscale-gateway-operator -n tailscale-system
   ```

#### Problem: Device Registration Failures

**Symptoms:**
- Devices not appearing in Tailscale admin console
- `Failed to create device` errors

**Diagnosis:**
```bash
# Check TailscaleTailnet status
kubectl get tailscaletailnets -o yaml

# Check device creation logs
kubectl logs -n tailscale-system deployment/tailscale-gateway-operator | grep "device"
```

**Solutions:**

1. **Check Tailnet Configuration**
   ```yaml
   apiVersion: gateway.tailscale.com/v1alpha1
   kind: TailscaleTailnet
   metadata:
     name: my-tailnet
   spec:
     tailnet: "correct-tailnet.ts.net"  # Verify this is correct
     configuration:
       devices:
         preauth: true  # Enable pre-authorization
         requireApproval: false  # Disable manual approval
   ```

2. **Verify Network Connectivity**
   ```bash
   # Test connectivity to Tailscale API
   kubectl exec -it deployment/tailscale-gateway-operator -- curl -I https://api.tailscale.com
   
   # Check DNS resolution
   kubectl exec -it deployment/tailscale-gateway-operator -- nslookup api.tailscale.com
   ```

### 3. Extension Server Issues

#### Problem: Extension Server Not Responding

**Symptoms:**
- Routes not being injected into Envoy Gateway
- gRPC connection errors
- `ExtensionServerDown` alerts

**Diagnosis:**
```bash
# Check extension server status
kubectl get pods -n tailscale-system -l app=tailscale-gateway-extension-server

# Check gRPC health
grpcurl -plaintext tailscale-gateway-extension-server.tailscale-system:5005 grpc.health.v1.Health/Check

# Check Envoy Gateway configuration
kubectl get envoyproxy -A -o yaml | grep -A 20 extensionManager
```

**Solutions:**

1. **Restart Extension Server**
   ```bash
   kubectl rollout restart deployment/tailscale-gateway-extension-server -n tailscale-system
   ```

2. **Check Network Policies**
   ```bash
   # Verify network policy allows gRPC traffic
   kubectl get networkpolicy -n tailscale-system
   
   # Test connectivity from Envoy Gateway
   kubectl exec -n envoy-gateway-system deployment/envoy-gateway -- nc -zv tailscale-gateway-extension-server.tailscale-system 5005
   ```

3. **Verify Envoy Gateway Extension Configuration**
   ```yaml
   # Check extension manager configuration
   apiVersion: gateway.envoyproxy.io/v1alpha1
   kind: EnvoyProxy
   metadata:
     name: custom-proxy-config
     namespace: envoy-gateway-system
   spec:
     extensionManager:
       service:
         fqdn:
           hostname: tailscale-gateway-extension-server.tailscale-system.svc.cluster.local
           port: 5005
   ```

#### Problem: Route Injection Failures

**Symptoms:**
- HTTPRoutes created but traffic not routed correctly
- Missing routes in Envoy configuration

**Diagnosis:**
```bash
# Check HTTPRoute status
kubectl describe httproute <route-name>

# Check TailscaleEndpoints status
kubectl describe tailscaleendpoints <endpoints-name>

# Check extension server logs for route processing
kubectl logs -n tailscale-system deployment/tailscale-gateway-extension-server | grep "route"
```

**Solutions:**

1. **Verify Backend References**
   ```yaml
   # Ensure backendRefs are correct
   backendRefs:
     - group: gateway.tailscale.com
       kind: TailscaleEndpoints
       name: correct-endpoints-name  # Must match existing resource
       port: 8080  # Must match endpoint port
   ```

2. **Check Extension Server Permissions**
   ```bash
   # Verify extension server can read TailscaleEndpoints
   kubectl auth can-i get tailscaleendpoints --as=system:serviceaccount:tailscale-system:tailscale-gateway-extension-server
   ```

### 4. Service Discovery Issues

#### Problem: Services Not Discovered

**Symptoms:**
- TailscaleEndpoints created but services not accessible
- Health checks failing
- VIP services not registered

**Diagnosis:**
```bash
# Check endpoint status
kubectl get tailscaleendpoints -o wide

# Check health check status
kubectl describe tailscaleendpoints <endpoints-name> | grep -A 10 "Health"

# Check service coordinator logs
kubectl logs -n tailscale-system deployment/tailscale-gateway-operator | grep "ServiceCoordinator"
```

**Solutions:**

1. **Verify External Target Connectivity**
   ```bash
   # Test connectivity to external target
   kubectl exec -it deployment/tailscale-gateway-operator -- curl -I http://external-target:8080/health
   
   # Check DNS resolution
   kubectl exec -it deployment/tailscale-gateway-operator -- nslookup external-target
   ```

2. **Fix Health Check Configuration**
   ```yaml
   apiVersion: gateway.tailscale.com/v1alpha1
   kind: TailscaleEndpoints
   metadata:
     name: my-service
   spec:
     endpoints:
       - name: api
         healthCheck:
           enabled: true
           path: "/health"  # Ensure this path exists
           expectedStatus: 200  # Verify expected status code
           timeout: "10s"
           interval: "30s"
   ```

3. **Check Network Policies and Firewalls**
   ```bash
   # Verify StatefulSet can reach external target
   kubectl exec -it my-service-statefulset-0 -- curl -I http://external-target:8080
   ```

#### Problem: Cross-Cluster Service Coordination Failures

**Symptoms:**
- Multiple operators creating duplicate VIP services
- Service registry inconsistencies
- Stale service registrations

**Diagnosis:**
```bash
# Check service coordinator status
kubectl logs -n tailscale-system deployment/tailscale-gateway-operator | grep "coordination"

# Check VIP service annotations
tailscale serve status

# Check for duplicate services
kubectl get tailscaleendpoints -A -o jsonpath='{range .items[*]}{.metadata.name}{": "}{.spec.serviceDiscovery.vipServices[*].name}{"\n"}{end}'
```

**Solutions:**

1. **Enable Service Coordination**
   ```yaml
   # In TailscaleGateway
   spec:
     serviceCoordination:
       enabled: true
       strategy: "shared"
   ```

2. **Clean Up Stale Registrations**
   ```bash
   # Force cleanup of stale services
   kubectl annotate tailscalegateway <gateway-name> "gateway.tailscale.com/force-cleanup=true"
   ```

### 5. Performance Issues

#### Problem: High CPU/Memory Usage

**Symptoms:**
- Operator pods consuming excessive resources
- Slow reconciliation times
- OOMKilled events

**Diagnosis:**
```bash
# Check resource usage
kubectl top pods -n tailscale-system

# Check metrics
curl http://tailscale-gateway-operator:8080/metrics | grep -E "(cpu|memory|reconcile)"

# Check for memory leaks
kubectl exec -it deployment/tailscale-gateway-operator -- curl http://localhost:8080/debug/pprof/heap > heap.prof
```

**Solutions:**

1. **Increase Resource Limits**
   ```yaml
   spec:
     template:
       spec:
         containers:
         - name: manager
           resources:
             limits:
               cpu: "1000m"
               memory: "1Gi"
             requests:
               cpu: "200m"
               memory: "256Mi"
   ```

2. **Optimize Controller Settings**
   ```yaml
   env:
   - name: CONTROLLER_MAX_CONCURRENT_RECONCILES
     value: "5"  # Reduce from default 10
   - name: SYNC_PERIOD
     value: "60s"  # Increase from default 30s
   ```

#### Problem: Slow Route Updates

**Symptoms:**
- Long delays between configuration changes and route updates
- High reconciliation latency

**Diagnosis:**
```bash
# Check reconciliation metrics
curl http://tailscale-gateway-operator:8080/metrics | grep reconcile_duration

# Check extension server performance
curl http://tailscale-gateway-extension-server:8080/metrics | grep grpc_duration
```

**Solutions:**

1. **Optimize Extension Server**
   ```yaml
   env:
   - name: MAX_MESSAGE_SIZE
     value: "100M"  # Reduce if too large
   - name: GRPC_KEEPALIVE_TIME
     value: "30s"
   ```

2. **Use Horizontal Pod Autoscaler**
   ```yaml
   apiVersion: autoscaling/v2
   kind: HorizontalPodAutoscaler
   metadata:
     name: extension-server-hpa
   spec:
     scaleTargetRef:
       apiVersion: apps/v1
       kind: Deployment
       name: tailscale-gateway-extension-server
     minReplicas: 2
     maxReplicas: 10
     metrics:
     - type: Resource
       resource:
         name: cpu
         target:
           type: Utilization
           averageUtilization: 70
   ```

### 6. Network Connectivity Issues

#### Problem: Unable to Reach Services

**Symptoms:**
- Connections timeout to Tailscale IPs
- Services unreachable from Tailscale clients
- DNS resolution failures

**Diagnosis:**
```bash
# Test from Tailscale client
tailscale ping 100.64.0.10

# Check StatefulSet status
kubectl get statefulsets -n tailscale-system

# Check Tailscale node status
tailscale status

# Test DNS resolution
nslookup service.company.ts.net
```

**Solutions:**

1. **Verify StatefulSet Health**
   ```bash
   # Check StatefulSet pods
   kubectl get pods -l app.kubernetes.io/name=tailscale-endpoint

   # Check pod logs
   kubectl logs <endpoint-pod-name> -c tailscaled
   ```

2. **Check Network Policies**
   ```bash
   # Verify network policies allow traffic
   kubectl get networkpolicy -A

   # Test connectivity between pods
   kubectl exec -it <source-pod> -- nc -zv <target-ip> <port>
   ```

3. **Verify Route Advertisement**
   ```bash
   # Check advertised routes
   kubectl exec -it <endpoint-pod> -- tailscale status --json | jq '.Self.PrimaryRoutes'
   ```

## Debug Tools and Commands

### Operator Debug Commands

```bash
# Enable debug logging
kubectl patch deployment tailscale-gateway-operator -n tailscale-system -p '
{
  "spec": {
    "template": {
      "spec": {
        "containers": [
          {
            "name": "manager",
            "env": [
              {
                "name": "LOG_LEVEL",
                "value": "debug"
              }
            ]
          }
        ]
      }
    }
  }
}'

# Get operator metrics
kubectl port-forward -n tailscale-system deployment/tailscale-gateway-operator 8080:8080
curl http://localhost:8080/metrics

# Get pprof data
curl http://localhost:8080/debug/pprof/goroutine?debug=1
```

### Extension Server Debug Commands

```bash
# Check gRPC health
grpcurl -plaintext localhost:5005 grpc.health.v1.Health/Check

# List gRPC services
grpcurl -plaintext localhost:5005 list

# Enable extension server debug logging
kubectl patch deployment tailscale-gateway-extension-server -n tailscale-system -p '
{
  "spec": {
    "template": {
      "spec": {
        "containers": [
          {
            "name": "extension-server",
            "env": [
              {
                "name": "LOG_LEVEL",
                "value": "debug"
              }
            ]
          }
        ]
      }
    }
  }
}'
```

### Tailscale Debug Commands

```bash
# Check Tailscale status in StatefulSet pod
kubectl exec -it <endpoint-pod-name> -- tailscale status

# Check Tailscale logs
kubectl exec -it <endpoint-pod-name> -- tailscale logs

# Test connectivity
kubectl exec -it <endpoint-pod-name> -- tailscale ping <target-ip>

# Check routes
kubectl exec -it <endpoint-pod-name> -- ip route show
```

## Performance Monitoring

### Key Metrics to Monitor

```bash
# Controller metrics
controller_runtime_reconcile_total
controller_runtime_reconcile_time_seconds
controller_runtime_reconcile_errors_total

# Extension server metrics
grpc_server_requests_total
grpc_server_request_duration_seconds
grpc_server_errors_total

# Service coordinator metrics
service_coordinator_services_total
service_coordinator_registrations_total

# Health check metrics
health_check_success_total
health_check_failure_total
health_check_duration_seconds
```

### Alerting Rules

```yaml
# High error rate
- alert: HighReconciliationErrorRate
  expr: |
    (
      rate(controller_runtime_reconcile_errors_total[5m]) / 
      rate(controller_runtime_reconcile_total[5m])
    ) > 0.1
  for: 10m

# Extension server down
- alert: ExtensionServerDown
  expr: up{job="tailscale-gateway-extension-server"} == 0
  for: 3m

# High memory usage
- alert: HighMemoryUsage
  expr: |
    (
      container_memory_working_set_bytes{container="manager"} / 
      container_spec_memory_limit_bytes{container="manager"}
    ) > 0.8
  for: 10m
```

## Getting Help

### Log Collection Script

```bash
#!/bin/bash
# collect-logs.sh - Collect diagnostic information

echo "Collecting Tailscale Gateway diagnostics..."

# Create output directory
mkdir -p tailscale-gateway-logs
cd tailscale-gateway-logs

# Operator information
kubectl get pods -n tailscale-system > pods.txt
kubectl get tailscalegateways,tailscaleendpoints -A > resources.txt
kubectl describe deployment tailscale-gateway-operator -n tailscale-system > operator-deployment.txt

# Logs
kubectl logs -n tailscale-system deployment/tailscale-gateway-operator --tail=1000 > operator.log
kubectl logs -n tailscale-system deployment/tailscale-gateway-extension-server --tail=1000 > extension-server.log

# Events
kubectl get events -A --sort-by='.lastTimestamp' > events.txt

# Resource definitions
kubectl get tailscalegateways -A -o yaml > gateways.yaml
kubectl get tailscaleendpoints -A -o yaml > endpoints.yaml

# Envoy Gateway information
kubectl get gateways,httproutes -A > gateway-api-resources.txt

echo "Diagnostics collected in tailscale-gateway-logs/"
```

### Support Information

When reporting issues, please include:

1. **Environment Information**
   - Kubernetes version
   - Operator version
   - Envoy Gateway version

2. **Resource Definitions**
   - TailscaleGateway YAML
   - TailscaleEndpoints YAML
   - HTTPRoute YAML

3. **Logs**
   - Operator logs
   - Extension server logs
   - Relevant pod logs

4. **Error Messages**
   - Exact error messages
   - Stack traces if available

### Community Support

- 🐛 **[GitHub Issues](https://github.com/rajsinghtech/tailscale-gateway/issues)** - Report bugs
- 💬 **[Discussions](https://github.com/rajsinghtech/tailscale-gateway/discussions)** - Ask questions
- 📖 **[Documentation](https://rajsinghtech.github.io/tailscale-gateway)** - Complete guides

## Related Documentation

- **[Monitoring Guide](./monitoring)** - Set up comprehensive monitoring
- **[Production Deployment](./production)** - Production best practices
- **[Configuration Reference](../configuration/overview)** - Advanced configurations