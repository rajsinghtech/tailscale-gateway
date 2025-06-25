# Tailscale Gateway Operator

Tailscale Gateway Operator is an open source Kubernetes operator that integrates [Tailscale](https://tailscale.com/) mesh networking with [Envoy Gateway](https://gateway.envoyproxy.io/) to provide secure, cloud-native service mesh capabilities for connecting Kubernetes clusters to Tailscale networks.

## Quick Start

### Prerequisites

- Kubernetes v1.25+
- Helm v3.8+
- Tailscale account with OAuth credentials

### Installation

1. **Install Envoy Gateway (prerequisite):**
   ```bash
   helm install my-gateway-helm oci://docker.io/envoyproxy/gateway-helm \
     --version 0.0.0-latest \
     -n envoy-gateway-system \
     --create-namespace
   ```

2. **Install Tailscale Gateway Operator:**
   ```bash
   # Create OAuth secret
   kubectl create namespace tailscale-gateway-system
   kubectl create secret generic tailscale-oauth \
     --from-literal=client-id=YOUR_CLIENT_ID \
     --from-literal=client-secret=YOUR_CLIENT_SECRET \
     -n tailscale-gateway-system

   # Install the operator
   helm install tailscale-gateway-operator oci://ghcr.io/rajsinghtech/charts/tailscale-gateway-operator \
     --namespace tailscale-gateway-system \
     --create-namespace \
     --version 0.0.0-latest
   ```

3. **Create Tailnet resource:**
   ```yaml
   apiVersion: gateway.tailscale.com/v1alpha1
   kind: TailscaleTailnet
   metadata:
     name: my-tailnet
     namespace: default
   spec:
     oauthSecretName: tailscale-oauth
     oauthSecretNamespace: tailscale-gateway-system
     tags:
       - "tag:k8s"
       - "tag:gateway"
   ```

4. **Configure your first gateway:**
   ```yaml
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
         port: 80
         protocol: "HTTP"
         externalTarget: "httpbin.org:80"
   ```

## Features

- **Gateway API Compliance**: Full support for HTTPRoute, TCPRoute, UDPRoute, TLSRoute, and GRPCRoute
- **Multi-Protocol Support**: HTTP, TCP, UDP, and gRPC traffic routing
- **Service Discovery**: Automatic discovery and integration with Kubernetes Services
- **Multi-Tailnet Support**: Connect to multiple Tailscale networks simultaneously
- **Health-Aware Failover**: Priority-based load balancing with automatic failover
- **Cross-Cluster Coordination**: Multiple operators can share VIP services efficiently
- **Production Ready**: Complete observability, metrics, and health checks

## Architecture

The operator combines Tailscale mesh networking with Envoy Gateway through:

- **Integrated Extension Server**: gRPC server for dynamic route and cluster injection
- **Custom Resource Definitions**: `TailscaleGateway`, `TailscaleEndpoints`, `TailscaleRoutePolicy`
- **Service Mesh Integration**: Bidirectional traffic flow between Kubernetes and Tailscale networks
- **Multi-Operator Coordination**: Efficient VIP service sharing across clusters

## Documentation

- 📚 **[Full Documentation](https://rajsinghtech.github.io/tailscale-gateway/)**
- 🚀 **[Quick Start Guide](https://rajsinghtech.github.io/tailscale-gateway/docs/getting-started/quickstart)**
- 📖 **[Installation Guide](https://rajsinghtech.github.io/tailscale-gateway/docs/getting-started/installation)**
- 🔧 **[Configuration Examples](https://rajsinghtech.github.io/tailscale-gateway/docs/examples/basic-usage)**