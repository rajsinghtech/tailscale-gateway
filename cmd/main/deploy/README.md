# Tailscale Gateway Operator Deployment

This directory contains deployment artifacts for the Tailscale Gateway Operator.

## Directory Structure

- `crds/` - CustomResourceDefinition YAML files
- `chart/` - Helm chart for the operator
- `manifests/` - Static Kubernetes manifests

## Deployment Options

### Option 1: Helm Chart (Recommended)

The Helm chart provides the most flexible deployment option with customizable values.

#### From OCI Registry (Production)

```bash
# Install from published OCI registry (development version)
helm install tailscale-gateway-operator oci://ghcr.io/rajsinghtech/charts/tailscale-gateway-operator \
  --namespace tailscale-gateway-system \
  --create-namespace \
  --version 0.0.0-latest

# Upgrade the operator
helm upgrade tailscale-gateway-operator oci://ghcr.io/rajsinghtech/charts/tailscale-gateway-operator \
  --namespace tailscale-gateway-system \
  --version 0.0.0-latest

# Uninstall the operator
helm uninstall tailscale-gateway-operator \
  --namespace tailscale-gateway-system
```

#### From Local Chart (Development)

```bash
# Install from local chart for development
helm install tailscale-gateway-operator ./chart \
  --namespace tailscale-gateway-system \
  --create-namespace

# Upgrade the operator
helm upgrade tailscale-gateway-operator ./chart \
  --namespace tailscale-gateway-system

# Uninstall the operator
helm uninstall tailscale-gateway-operator \
  --namespace tailscale-gateway-system
```

#### Customization

Create a `values.yaml` file to customize the deployment:

```yaml
operator:
  image:
    repository: your-registry/tailscale-gateway-operator
    tag: v0.1.0
  
proxy:
  image: tailscale/tailscale:stable

logLevel: debug
```

Then install with:

```bash
helm install tailscale-gateway-operator ./chart \
  --namespace tailscale-gateway-system \
  --create-namespace \
  --values values.yaml
```

### Option 2: Static Manifests

For environments where Helm is not available, use the static manifests:

```bash
# Deploy the operator
kubectl apply -f manifests/operator.yaml

# Remove the operator
kubectl delete -f manifests/operator.yaml
```

### Option 3: Just CRDs

To install only the Custom Resource Definitions:

```bash
kubectl apply -f crds/
```

## Generating Deployment Artifacts

The deployment artifacts are generated from the source code and templates:

```bash
# From the project root
make generate-deploy
```

This will:
1. Copy CRDs from `config/crd/bases/` to `cmd/main/deploy/crds/`
2. Insert CRDs into the Helm chart template
3. Generate static manifests in `cmd/main/deploy/manifests/`

## Configuration

### Helm Values

Key configuration options:

- `operator.replicaCount` - Number of operator replicas (default: 1)
- `operator.image` - Operator container image settings
- `operator.resources` - Resource requests and limits
- `proxy.image` - Default Tailscale proxy image
- `proxy.resources` - Default proxy resource settings
- `installCRDs` - Whether to install CRDs with the chart (default: true)
- `rbac.create` - Whether to create RBAC resources (default: true)
- `logLevel` - Operator log level (debug, info, warn, error)

See `chart/values.yaml` for all available options.

### Environment Variables

The operator supports these environment variables:

- `OPERATOR_NAMESPACE` - Namespace where the operator is running
- `PROXY_IMAGE` - Default image for Tailscale proxy containers
- `PROXY_RESOURCES_*` - Default resource settings for proxy containers 