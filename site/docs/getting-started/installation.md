---
id: installation
title: Installation
sidebar_position: 3
---

# Installation Guide

This guide covers detailed installation options for the Tailscale Gateway Operator.

## Prerequisites

### Required Components

| Component | Version | Description |
|-----------|---------|-------------|
| **Kubernetes** | v1.25+ | Target cluster for operator deployment |
| **kubectl** | Latest | Kubernetes CLI tool |
| **Helm** | v3.8+ | Package manager for Kubernetes |
| **Envoy Gateway** | v1.0.0+ | Gateway API implementation |
| **Tailscale Account** | - | With admin permissions for OAuth setup |

### Supported Platforms

- ✅ **Kubernetes Distributions**: EKS, GKE, AKS, kind, k3s, OpenShift
- ✅ **Operating Systems**: Linux, macOS, Windows
- ✅ **Architectures**: x86_64, ARM64

## Installation Methods

Choose the installation method that best fits your environment:

### Method 1: Helm Installation (Recommended)

The Helm chart provides the easiest way to install and manage the operator.

#### Step 1: Install from OCI Registry

The Tailscale Gateway Operator is available as an OCI package:

#### Step 2: Create Namespace

```bash
kubectl create namespace tailscale-gateway-system
```

#### Step 3: Configure OAuth Credentials

Create a Kubernetes secret with your Tailscale OAuth credentials:

```bash
kubectl create secret generic tailscale-oauth \
  --from-literal=client-id=YOUR_CLIENT_ID \
  --from-literal=client-secret=YOUR_CLIENT_SECRET \
  -n tailscale-gateway-system
```

#### Step 4: Install the Operator

```bash
# Install Tailscale Gateway Operator from OCI registry with development version
helm install tailscale-gateway-operator oci://ghcr.io/rajsinghtech/charts/tailscale-gateway-operator \
  --namespace tailscale-gateway-system \
  --create-namespace \
  --version 0.0.0-latest
```

#### Step 5: Verify Installation

```bash
kubectl get pods -n tailscale-gateway-system
kubectl get crd | grep tailscale
```

### Method 2: Raw Manifests

For environments where Helm is not available:

#### Step 1: Download Manifests

```bash
# Download the latest release manifests
curl -L -o tailscale-gateway.yaml \
  https://github.com/rajsinghtech/tailscale-gateway/releases/latest/download/operator.yaml
```

#### Step 2: Create OAuth Secret

```bash
kubectl create namespace tailscale-gateway-system

kubectl create secret generic tailscale-oauth \
  --from-literal=client-id=YOUR_CLIENT_ID \
  --from-literal=client-secret=YOUR_CLIENT_SECRET \
  -n tailscale-gateway-system
```

#### Step 3: Apply Manifests

```bash
kubectl apply -f tailscale-gateway.yaml
```

### Method 3: Development Installation

For development and testing purposes:

#### Step 1: Clone Repository

```bash
git clone https://github.com/rajsinghtech/tailscale-gateway.git
cd tailscale-gateway
```

#### Step 2: Build and Deploy

```bash
# Generate CRDs and manifests
make manifests

# Build Docker images
make docker-build

# Load images into kind (if using kind)
kind load docker-image tailscale-gateway:latest
# Extension server is now integrated into the main operator image

# Deploy to cluster
make deploy
```

## Envoy Gateway Setup

Tailscale Gateway requires Envoy Gateway to be installed first.

### Install Envoy Gateway

Envoy Gateway is a prerequisite for Tailscale Gateway. Install it first:

```bash
# Install Envoy Gateway using Helm with latest development version
helm install my-gateway-helm oci://docker.io/envoyproxy/gateway-helm \
  --version 0.0.0-latest \
  -n envoy-gateway-system \
  --create-namespace

# Wait for deployment to be ready
kubectl wait --timeout=5m -n envoy-gateway-system \
  deployment/envoy-gateway --for=condition=Available
```

### Configure Envoy Gateway

Create a Gateway API Gateway resource:

```yaml title="envoy-gateway.yaml"
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: envoy-gateway
  namespace: envoy-gateway-system
spec:
  gatewayClassName: eg
  listeners:
    - name: http
      protocol: HTTP
      port: 80
    - name: https
      protocol: HTTPS
      port: 443
      tls:
        mode: Terminate
        certificateRefs:
          - name: gateway-tls
```

Apply the gateway:

```bash
kubectl apply -f envoy-gateway.yaml
```

## Configuration Options

### Helm Chart Values

The Helm chart supports extensive customization through values:

```yaml title="values.yaml"
# Operator configuration
operator:
  image:
    repository: ghcr.io/rajsinghtech/tailscale-gateway
    tag: latest
  replicas: 1
  resources:
    limits:
      cpu: 500m
      memory: 128Mi
    requests:
      cpu: 10m
      memory: 64Mi

# Extension server configuration (integrated into main operator)
extensionServer:
  enabled: true
  grpcPort: 5005
  resources:
    limits:
      cpu: 500m
      memory: 128Mi

# OAuth configuration
oauth:
  existingSecret: tailscale-oauth
  clientIdKey: client-id
  clientSecretKey: client-secret

# Metrics and monitoring
metrics:
  enabled: true
  port: 8080

# Health probes
healthProbe:
  enabled: true
  port: 8081

# RBAC
rbac:
  create: true

# Service accounts
serviceAccount:
  create: true
  name: ""
```

### Custom Installation

Install with custom values:

```bash
helm install tailscale-gateway tailscale-gateway/tailscale-gateway \
  --namespace tailscale-gateway-system \
  --values custom-values.yaml
```

## OAuth Setup Guide

### Step 1: Access Tailscale Admin Console

1. Go to [Tailscale Admin Console](https://login.tailscale.com/admin/settings/oauth)
2. Sign in with your Tailscale account

### Step 2: Create OAuth Client

1. Click **"Generate OAuth client"**
2. Enter a descriptive name: `Tailscale Gateway Operator`
3. Add required scopes:
   - `device:create` - Create new devices
   - `device:read` - Read device information
   - `device:write` - Modify device settings
   - `tailnet:read` - Read tailnet information

### Step 3: Save Credentials

1. Copy the **Client ID** and **Client Secret**
2. Store them securely (they won't be shown again)

### Step 4: Create Kubernetes Secret

```bash
kubectl create secret generic tailscale-oauth \
  --from-literal=client-id=YOUR_CLIENT_ID \
  --from-literal=client-secret=YOUR_CLIENT_SECRET \
  -n tailscale-gateway-system
```

## Verification

### Check Operator Status

```bash
# Check pod status
kubectl get pods -n tailscale-gateway-system

# Check operator logs
kubectl logs -n tailscale-gateway-system deployment/tailscale-gateway-operator

# Check integrated extension server logs (within operator)
kubectl logs -n tailscale-gateway-system deployment/tailscale-gateway-operator -c manager
```

### Verify CRDs

```bash
# List custom resource definitions
kubectl get crd | grep tailscale

# Expected output:
# tailscaleendpoints.gateway.tailscale.com
# tailscalegateways.gateway.tailscale.com
# tailscaleroutepolicies.gateway.tailscale.com
# tailscaletailnets.gateway.tailscale.com
```

### Test API Access

```bash
# Create a simple TailscaleTailnet resource
cat <<EOF | kubectl apply -f -
apiVersion: gateway.tailscale.com/v1alpha1
kind: TailscaleTailnet
metadata:
  name: test-tailnet
  namespace: default
spec:
  tailnet: "your-tailnet.ts.net"
EOF

# Check if it was created successfully
kubectl get tailscaletailnets
```

## Troubleshooting Installation

### Common Issues

#### 1. **OAuth Authentication Failures**

**Error**: `Failed to authenticate with Tailscale API`

**Solution**:
```bash
# Verify secret exists and has correct keys
kubectl get secret tailscale-oauth -n tailscale-gateway-system -o yaml

# Check if credentials are valid
kubectl logs -n tailscale-gateway-system deployment/tailscale-gateway-operator | grep oauth
```

#### 2. **CRD Installation Issues**

**Error**: `Custom Resource Definitions not found`

**Solution**:
```bash
# Manually install CRDs
kubectl apply -f https://github.com/rajsinghtech/tailscale-gateway/releases/latest/download/crds.yaml
```

#### 3. **Envoy Gateway Not Found**

**Error**: `Gateway envoy-gateway not found`

**Solution**:
```bash
# Install Envoy Gateway first
helm install eg oci://docker.io/envoyproxy/gateway-helm --version v1.0.0 -n envoy-gateway-system --create-namespace
```

#### 4. **Permission Issues**

**Error**: `User cannot list/create resources`

**Solution**:
```bash
# Check RBAC configuration
kubectl get clusterrole,clusterrolebinding | grep tailscale

# Verify service account permissions
kubectl auth can-i create tailscaleendpoints --as=system:serviceaccount:tailscale-gateway-system:tailscale-gateway-operator
```

### Debug Commands

```bash
# Get comprehensive status
kubectl get all -n tailscale-gateway-system

# Check events for errors
kubectl get events -n tailscale-gateway-system --sort-by='.lastTimestamp'

# Describe problematic resources
kubectl describe pod -n tailscale-gateway-system -l app=tailscale-gateway-operator

# View detailed logs with timestamps
kubectl logs -n tailscale-gateway-system deployment/tailscale-gateway-operator --timestamps=true
```

## Next Steps

After successful installation:

1. **[Quick Start](./quickstart)** - Deploy your first gateway
2. **[Configuration Guide](../configuration/overview)** - Learn about configuration options
3. **[Examples](../examples/basic-usage)** - Explore practical examples
4. **[Operations](../operations/monitoring)** - Set up monitoring and observability