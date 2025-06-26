# Tailscale Gateway Operator Helm Chart

[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)

A Helm chart for deploying the Tailscale Gateway Kubernetes operator that integrates Tailscale mesh networking with Envoy Gateway.

## Prerequisites

- Kubernetes v1.25+
- Helm v3.8+  
- Envoy Gateway installed (prerequisite)
- Tailscale account with OAuth credentials

## Installation

### Step 1: Install Envoy Gateway (Prerequisite)

```bash
# Install Envoy Gateway using latest development version
helm install my-gateway-helm oci://docker.io/envoyproxy/gateway-helm \
  --version 0.0.0-latest \
  -n envoy-gateway-system \
  --create-namespace
```

### Step 2: Install Tailscale Gateway Operator

```bash
# Install from OCI registry (development version)
helm install tailscale-gateway-operator oci://ghcr.io/rajsinghtech/charts/tailscale-gateway-operator \
  --namespace tailscale-gateway-system \
  --create-namespace \
  --version 0.0.0-latest
```

## Configuration

### Values

The chart supports extensive configuration through values:

```yaml
# Operator configuration
operator:
  image:
    repository: ghcr.io/rajsinghtech/tailscale-gateway
    tag: latest
  replicaCount: 1

# Extension server configuration (integrated into main operator)
extensionServer:
  grpcPort: 5005

# OAuth configuration
oauth:
  existingSecret: tailscale-oauth
  clientIdKey: client-id
  clientSecretKey: client-secret
```

### Customization

Create a custom `values.yaml` and install:

```bash
helm install tailscale-gateway-operator oci://ghcr.io/rajsinghtech/charts/tailscale-gateway-operator \
  --namespace tailscale-gateway-system \
  --create-namespace \
  --version 0.0.0-latest \
  --values custom-values.yaml
```

## Parameters

| Parameter | Description | Default |
|-----------|-------------|---------|
| `operator.image.repository` | Operator image repository | `ghcr.io/rajsinghtech/tailscale-gateway` |
| `operator.image.tag` | Operator image tag | `""` (uses chart appVersion) |
| `extensionServer.grpcPort` | gRPC port for integrated extension server | `5005` |
| `oauth.existingSecret` | Name of existing OAuth secret | `""` |
| `rbac.create` | Create RBAC resources | `true` |
| `serviceAccount.create` | Create service account | `true` |

## Upgrading

To upgrade to a newer version:

```bash
helm upgrade tailscale-gateway-operator oci://ghcr.io/rajsinghtech/charts/tailscale-gateway-operator \
  --namespace tailscale-gateway-system \
  --version 0.0.0-latest
```
