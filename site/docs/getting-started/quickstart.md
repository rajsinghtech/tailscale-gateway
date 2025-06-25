---
id: quickstart
title: Quick Start
sidebar_position: 2
---

# Quick Start Guide

Get Tailscale Gateway up and running in your Kubernetes cluster in under 5 minutes.

## Prerequisites

Before you begin, ensure you have:

- ✅ **Kubernetes cluster** (v1.25+)
- ✅ **kubectl** configured to access your cluster
- ✅ **Helm 3.8+** installed
- ✅ **Tailscale account** with admin access
- ✅ **Envoy Gateway** installed in your cluster

## Step 1: Install Envoy Gateway

If you haven't already installed Envoy Gateway:

```bash
# Install Envoy Gateway
helm install eg oci://docker.io/envoyproxy/gateway-helm --version v1.0.0 -n envoy-gateway-system --create-namespace

# Wait for it to be ready
kubectl wait --timeout=5m -n envoy-gateway-system deployment/envoy-gateway --for=condition=Available
```

## Step 2: Create Tailscale OAuth Credentials

1. Go to the [Tailscale Admin Console](https://login.tailscale.com/admin/settings/oauth)
2. Click **Generate OAuth client**
3. Add these scopes:
   - `device:create`
   - `device:read`
   - `device:write`
   - `tailnet:read`
4. Save the **Client ID** and **Client Secret**

## Step 3: Install Tailscale Gateway Operator

```bash
# Add the Helm repository
helm repo add tailscale-gateway https://rajsinghtech.github.io/tailscale-gateway
helm repo update

# Create the namespace
kubectl create namespace tailscale-system

# Create OAuth secret
kubectl create secret generic tailscale-oauth \
  --from-literal=client-id=YOUR_CLIENT_ID \
  --from-literal=client-secret=YOUR_CLIENT_SECRET \
  -n tailscale-system

# Install the operator
helm install tailscale-gateway tailscale-gateway/tailscale-gateway \
  --namespace tailscale-system \
  --set oauth.existingSecret=tailscale-oauth
```

## Step 4: Configure Your First Gateway

Create a `TailscaleGateway` resource to connect your cluster:

```yaml title="gateway.yaml"
apiVersion: gateway.tailscale.com/v1alpha1
kind: TailscaleGateway
metadata:
  name: my-gateway
  namespace: default
spec:
  gatewayRef:
    kind: Gateway
    name: envoy-gateway
  tailnets:
    - name: my-tailnet
      tailscaleTailnetRef:
        kind: TailscaleTailnet
        name: my-tailnet
```

Apply the configuration:

```bash
kubectl apply -f gateway.yaml
```

## Step 5: Expose Your First Service

Create a `TailscaleEndpoints` resource to expose a service:

```yaml title="endpoints.yaml"
apiVersion: gateway.tailscale.com/v1alpha1
kind: TailscaleEndpoints
metadata:
  name: web-services
  namespace: default
spec:
  tailnet: "your-tailnet.ts.net"
  endpoints:
    - name: httpbin
      tailscaleIP: "100.64.0.10"
      tailscaleFQDN: "httpbin.your-tailnet.ts.net"
      port: 80
      protocol: "HTTP"
      externalTarget: "httpbin.org:80"
      healthCheck:
        enabled: true
        path: "/status/200"
        interval: "30s"
```

Apply the configuration:

```bash
kubectl apply -f endpoints.yaml
```

## Step 6: Create Gateway API Routes

Create an `HTTPRoute` to route traffic through the gateway:

```yaml title="route.yaml"
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: httpbin-route
  namespace: default
spec:
  parentRefs:
    - name: envoy-gateway
  hostnames:
    - "api.your-tailnet.ts.net"
  rules:
    - matches:
        - path:
            type: PathPrefix
            value: "/httpbin/"
      backendRefs:
        - group: gateway.tailscale.com
          kind: TailscaleEndpoints
          name: web-services
          port: 80
      filters:
        - type: URLRewrite
          urlRewrite:
            path:
              type: ReplacePrefixMatch
              replacePrefixMatch: "/"
```

Apply the route:

```bash
kubectl apply -f route.yaml
```

## Step 7: Test Your Setup

1. **Check operator status**:
   ```bash
   kubectl get pods -n tailscale-system
   kubectl logs -n tailscale-system deployment/tailscale-gateway-operator
   ```

2. **Verify resources**:
   ```bash
   kubectl get tailscalegateways,tailscaleendpoints,httproutes
   ```

3. **Test connectivity**:
   From any device on your Tailscale network:
   ```bash
   curl https://api.your-tailnet.ts.net/httpbin/status/200
   ```

## What Just Happened?

🎉 **Congratulations!** You've successfully:

1. ✅ Installed Tailscale Gateway Operator
2. ✅ Connected your cluster to Tailscale
3. ✅ Exposed an external service (`httpbin.org`) through your tailnet
4. ✅ Created Gateway API routes for traffic routing
5. ✅ Made the service accessible to all Tailscale clients

## Next Steps

Now that you have a working setup:

- **[Explore Examples](../examples/basic-usage)** - More real-world configurations
- **[Configure Multi-Protocol Services](../examples/multi-protocol)** - TCP, UDP, gRPC routing
- **[Set Up Monitoring](../operations/monitoring)** - Observability and metrics
- **[Production Deployment](../operations/production)** - Best practices for production

## Troubleshooting

If something isn't working:

1. **Check operator logs**:
   ```bash
   kubectl logs -n tailscale-system deployment/tailscale-gateway-operator -f
   ```

2. **Verify OAuth credentials**:
   ```bash
   kubectl get secret tailscale-oauth -n tailscale-system -o yaml
   ```

3. **Check Envoy Gateway status**:
   ```bash
   kubectl get gateways,httproutes -A
   ```

4. **Review the [Troubleshooting Guide](../operations/troubleshooting)** for common issues

## Need Help?

- 📖 **[Full Documentation](../)**
- 🐛 **[GitHub Issues](https://github.com/rajsinghtech/tailscale-gateway/issues)**
- 💬 **[Community Discussions](https://github.com/rajsinghtech/tailscale-gateway/discussions)**