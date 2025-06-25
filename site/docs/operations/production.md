---
id: production
title: Production Deployment
sidebar_position: 2
---

# Production Deployment Guide

Best practices and recommendations for deploying Tailscale Gateway Operator in production environments.

## Production Readiness Checklist

### ✅ Security Requirements

- [ ] **OAuth Credentials**: Secure storage in Kubernetes secrets
- [ ] **TLS Configuration**: Proper certificate management
- [ ] **RBAC Setup**: Least-privilege access controls
- [ ] **Network Policies**: Restricted network access
- [ ] **Secret Management**: External secret management integration
- [ ] **Image Security**: Signed and scanned container images

### ✅ High Availability

- [ ] **Multiple Replicas**: Operator and extension server redundancy
- [ ] **Leader Election**: Enabled for controller high availability
- [ ] **Pod Disruption Budgets**: Configured for maintenance resilience
- [ ] **Anti-Affinity Rules**: Spread replicas across nodes/zones
- [ ] **Resource Limits**: Proper CPU and memory constraints
- [ ] **Health Checks**: Comprehensive liveness and readiness probes

### ✅ Monitoring & Observability

- [ ] **Metrics Collection**: Prometheus integration
- [ ] **Log Aggregation**: Centralized logging setup
- [ ] **Distributed Tracing**: OpenTelemetry configuration
- [ ] **Alerting Rules**: Critical alert definitions
- [ ] **Dashboards**: Grafana dashboards for visibility
- [ ] **SLI/SLO Definition**: Service level indicators and objectives

### ✅ Operational Excellence

- [ ] **Backup Strategy**: Configuration and state backup
- [ ] **Disaster Recovery**: Recovery procedures documented
- [ ] **Capacity Planning**: Resource usage projections
- [ ] **Update Strategy**: Rolling update procedures
- [ ] **Runbooks**: Incident response procedures
- [ ] **Documentation**: Complete operational documentation

## Production Configuration

### High Availability Deployment

```yaml title="production-deployment.yaml"
apiVersion: apps/v1
kind: Deployment
metadata:
  name: tailscale-gateway-operator
  namespace: tailscale-system
  labels:
    app: tailscale-gateway-operator
    version: v1.0.0
spec:
  replicas: 3  # Multiple replicas for HA
  strategy:
    type: RollingUpdate
    rollingUpdate:
      maxUnavailable: 1
      maxSurge: 1
  selector:
    matchLabels:
      app: tailscale-gateway-operator
  template:
    metadata:
      labels:
        app: tailscale-gateway-operator
      annotations:
        prometheus.io/scrape: "true"
        prometheus.io/port: "8080"
        prometheus.io/path: "/metrics"
    spec:
      # Security context
      securityContext:
        runAsNonRoot: true
        runAsUser: 65534
        fsGroup: 65534
      
      # Service account with RBAC
      serviceAccountName: tailscale-gateway-operator
      
      # Anti-affinity for HA
      affinity:
        podAntiAffinity:
          preferredDuringSchedulingIgnoredDuringExecution:
          - weight: 100
            podAffinityTerm:
              labelSelector:
                matchExpressions:
                - key: app
                  operator: In
                  values:
                  - tailscale-gateway-operator
              topologyKey: kubernetes.io/hostname
          - weight: 50
            podAffinityTerm:
              labelSelector:
                matchExpressions:
                - key: app
                  operator: In
                  values:
                  - tailscale-gateway-operator
              topologyKey: topology.kubernetes.io/zone
      
      containers:
      - name: manager
        image: ghcr.io/rajsinghtech/tailscale-gateway:v1.0.0
        imagePullPolicy: IfNotPresent
        
        # Security context
        securityContext:
          allowPrivilegeEscalation: false
          readOnlyRootFilesystem: true
          capabilities:
            drop:
            - ALL
        
        # Resource limits
        resources:
          requests:
            cpu: 200m
            memory: 256Mi
          limits:
            cpu: 1000m
            memory: 512Mi
        
        # Environment variables
        env:
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
        - name: LEADER_ELECTION_ENABLED
          value: "true"
        - name: LEADER_ELECTION_ID
          value: "tailscale-gateway-operator"
        - name: METRICS_BIND_ADDRESS
          value: ":8080"
        - name: HEALTH_PROBE_BIND_ADDRESS
          value: ":8081"
        - name: LOG_LEVEL
          value: "info"
        - name: LOG_FORMAT
          value: "json"
        
        # Health probes
        livenessProbe:
          httpGet:
            path: /healthz
            port: 8081
            scheme: HTTP
          initialDelaySeconds: 30
          periodSeconds: 30
          timeoutSeconds: 10
          failureThreshold: 3
          
        readinessProbe:
          httpGet:
            path: /readyz
            port: 8081
            scheme: HTTP
          initialDelaySeconds: 10
          periodSeconds: 10
          timeoutSeconds: 5
          failureThreshold: 3
          
        startupProbe:
          httpGet:
            path: /healthz
            port: 8081
            scheme: HTTP
          initialDelaySeconds: 15
          periodSeconds: 10
          timeoutSeconds: 5
          failureThreshold: 30
        
        # Volume mounts
        volumeMounts:
        - name: tmp
          mountPath: /tmp
        - name: config
          mountPath: /etc/tailscale-gateway
          readOnly: true
      
      volumes:
      - name: tmp
        emptyDir: {}
      - name: config
        configMap:
          name: tailscale-gateway-config
      
      # Node selector for dedicated nodes (optional)
      nodeSelector:
        node-role.kubernetes.io/worker: "true"
      
      # Tolerations for tainted nodes (optional)
      tolerations:
      - key: "dedicated"
        operator: "Equal"
        value: "tailscale-gateway"
        effect: "NoSchedule"

---
# Pod Disruption Budget
apiVersion: policy/v1
kind: PodDisruptionBudget
metadata:
  name: tailscale-gateway-operator-pdb
  namespace: tailscale-system
spec:
  minAvailable: 2  # Keep at least 2 replicas running
  selector:
    matchLabels:
      app: tailscale-gateway-operator
```

### Extension Server Production Configuration

```yaml title="extension-server-production.yaml"
apiVersion: apps/v1
kind: Deployment
metadata:
  name: tailscale-gateway-extension-server
  namespace: tailscale-system
spec:
  replicas: 2  # HA for extension server
  strategy:
    type: RollingUpdate
    rollingUpdate:
      maxUnavailable: 1
      maxSurge: 1
  selector:
    matchLabels:
      app: tailscale-gateway-extension-server
  template:
    metadata:
      labels:
        app: tailscale-gateway-extension-server
    spec:
      securityContext:
        runAsNonRoot: true
        runAsUser: 65534
        fsGroup: 65534
      
      serviceAccountName: tailscale-gateway-extension-server
      
      # Anti-affinity
      affinity:
        podAntiAffinity:
          preferredDuringSchedulingIgnoredDuringExecution:
          - weight: 100
            podAffinityTerm:
              labelSelector:
                matchExpressions:
                - key: app
                  operator: In
                  values:
                  - tailscale-gateway-extension-server
              topologyKey: kubernetes.io/hostname
      
      containers:
      - name: extension-server
        image: ghcr.io/rajsinghtech/tailscale-gateway-extension-server:v1.0.0
        
        securityContext:
          allowPrivilegeEscalation: false
          readOnlyRootFilesystem: true
          capabilities:
            drop:
            - ALL
        
        resources:
          requests:
            cpu: 100m
            memory: 128Mi
          limits:
            cpu: 500m
            memory: 256Mi
        
        env:
        - name: GRPC_PORT
          value: "5005"
        - name: METRICS_PORT
          value: "8080"
        - name: LOG_LEVEL
          value: "info"
        - name: MAX_MESSAGE_SIZE
          value: "1000M"
        
        ports:
        - containerPort: 5005
          name: grpc
          protocol: TCP
        - containerPort: 8080
          name: metrics
          protocol: TCP
        
        livenessProbe:
          grpc:
            port: 5005
          initialDelaySeconds: 15
          periodSeconds: 20
          
        readinessProbe:
          grpc:
            port: 5005
          initialDelaySeconds: 5
          periodSeconds: 10
        
        volumeMounts:
        - name: tmp
          mountPath: /tmp
      
      volumes:
      - name: tmp
        emptyDir: {}

---
# Service for extension server
apiVersion: v1
kind: Service
metadata:
  name: tailscale-gateway-extension-server
  namespace: tailscale-system
  labels:
    app: tailscale-gateway-extension-server
spec:
  type: ClusterIP
  ports:
  - port: 5005
    targetPort: 5005
    protocol: TCP
    name: grpc
  - port: 8080
    targetPort: 8080
    protocol: TCP
    name: metrics
  selector:
    app: tailscale-gateway-extension-server
```

## Security Hardening

### RBAC Configuration

```yaml title="rbac-production.yaml"
---
# Operator service account
apiVersion: v1
kind: ServiceAccount
metadata:
  name: tailscale-gateway-operator
  namespace: tailscale-system
  labels:
    app: tailscale-gateway-operator

---
# Operator cluster role (minimal permissions)
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: tailscale-gateway-operator
rules:
# Core resources
- apiGroups: [""]
  resources: ["services", "endpoints", "configmaps", "secrets"]
  verbs: ["get", "list", "watch", "create", "update", "patch"]
- apiGroups: [""]
  resources: ["events"]
  verbs: ["create", "patch"]

# StatefulSets for tailscale proxies
- apiGroups: ["apps"]
  resources: ["statefulsets"]
  verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]

# Gateway API resources
- apiGroups: ["gateway.networking.k8s.io"]
  resources: ["gateways", "httproutes", "tcproutes", "udproutes", "tlsroutes", "grpcroutes"]
  verbs: ["get", "list", "watch"]

# Custom resources
- apiGroups: ["gateway.tailscale.com"]
  resources: ["tailscaleendpoints", "tailscalegateways", "tailscaleroutepolicies", "tailscaletailnets"]
  verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
- apiGroups: ["gateway.tailscale.com"]
  resources: ["tailscaleendpoints/status", "tailscalegateways/status", "tailscaleroutepolicies/status", "tailscaletailnets/status"]
  verbs: ["get", "update", "patch"]

# Leader election
- apiGroups: ["coordination.k8s.io"]
  resources: ["leases"]
  verbs: ["get", "list", "watch", "create", "update", "patch"]

---
# Bind cluster role to service account
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: tailscale-gateway-operator
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: tailscale-gateway-operator
subjects:
- kind: ServiceAccount
  name: tailscale-gateway-operator
  namespace: tailscale-system

---
# Extension server service account (even more restricted)
apiVersion: v1
kind: ServiceAccount
metadata:
  name: tailscale-gateway-extension-server
  namespace: tailscale-system

---
# Extension server role (namespace-scoped)
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  namespace: tailscale-system
  name: tailscale-gateway-extension-server
rules:
- apiGroups: [""]
  resources: ["configmaps"]
  verbs: ["get", "list", "watch"]
- apiGroups: ["gateway.tailscale.com"]
  resources: ["tailscaleendpoints"]
  verbs: ["get", "list", "watch"]

---
# Bind role to extension server service account
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: tailscale-gateway-extension-server
  namespace: tailscale-system
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: tailscale-gateway-extension-server
subjects:
- kind: ServiceAccount
  name: tailscale-gateway-extension-server
  namespace: tailscale-system
```

### Network Policies

```yaml title="network-policies.yaml"
---
# Operator network policy
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: tailscale-gateway-operator
  namespace: tailscale-system
spec:
  podSelector:
    matchLabels:
      app: tailscale-gateway-operator
  policyTypes:
  - Ingress
  - Egress
  
  ingress:
  # Allow metrics scraping
  - from:
    - namespaceSelector:
        matchLabels:
          name: monitoring
    ports:
    - protocol: TCP
      port: 8080
  
  # Allow health checks
  - from: []
    ports:
    - protocol: TCP
      port: 8081
  
  egress:
  # Kubernetes API server
  - to: []
    ports:
    - protocol: TCP
      port: 6443
  
  # Tailscale API
  - to: []
    ports:
    - protocol: TCP
      port: 443
    # Note: In production, consider restricting to specific IPs
  
  # DNS
  - to:
    - namespaceSelector:
        matchLabels:
          name: kube-system
    ports:
    - protocol: UDP
      port: 53

---
# Extension server network policy
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: tailscale-gateway-extension-server
  namespace: tailscale-system
spec:
  podSelector:
    matchLabels:
      app: tailscale-gateway-extension-server
  policyTypes:
  - Ingress
  - Egress
  
  ingress:
  # Allow gRPC from Envoy Gateway
  - from:
    - namespaceSelector:
        matchLabels:
          name: envoy-gateway-system
    ports:
    - protocol: TCP
      port: 5005
  
  # Allow metrics scraping
  - from:
    - namespaceSelector:
        matchLabels:
          name: monitoring
    ports:
    - protocol: TCP
      port: 8080
  
  egress:
  # Allow communication with operator
  - to:
    - podSelector:
        matchLabels:
          app: tailscale-gateway-operator
    ports:
    - protocol: TCP
      port: 8080  # For coordination
  
  # DNS
  - to:
    - namespaceSelector:
        matchLabels:
          name: kube-system
    ports:
    - protocol: UDP
      port: 53
```

### Secret Management

```yaml title="external-secrets.yaml"
# Using External Secrets Operator for production secret management
apiVersion: external-secrets.io/v1beta1
kind: SecretStore
metadata:
  name: vault-backend
  namespace: tailscale-system
spec:
  provider:
    vault:
      server: "https://vault.company.com"
      path: "secret"
      version: "v2"
      auth:
        kubernetes:
          mountPath: "kubernetes"
          role: "tailscale-gateway"
          serviceAccountRef:
            name: "tailscale-gateway-operator"

---
apiVersion: external-secrets.io/v1beta1
kind: ExternalSecret
metadata:
  name: tailscale-oauth
  namespace: tailscale-system
spec:
  refreshInterval: 24h  # Refresh daily
  secretStoreRef:
    name: vault-backend
    kind: SecretStore
  
  target:
    name: tailscale-oauth
    creationPolicy: Owner
    template:
      type: Opaque
      data:
        client-id: "&#123;&#123; .clientId &#125;&#125;"
        client-secret: "&#123;&#123; .clientSecret &#125;&#125;"
  
  data:
  - secretKey: clientId
    remoteRef:
      key: tailscale/oauth
      property: client_id
  - secretKey: clientSecret
    remoteRef:
      key: tailscale/oauth
      property: client_secret
```

## Backup and Disaster Recovery

### Configuration Backup

```yaml title="backup-cronjob.yaml"
apiVersion: batch/v1
kind: CronJob
metadata:
  name: tailscale-gateway-backup
  namespace: tailscale-system
spec:
  schedule: "0 2 * * *"  # Daily at 2 AM
  jobTemplate:
    spec:
      template:
        spec:
          serviceAccountName: tailscale-gateway-backup
          containers:
          - name: backup
            image: bitnami/kubectl:latest
            command:
            - /bin/bash
            - -c
            - |
              # Create backup directory
              mkdir -p /backup/$(date +%Y%m%d)
              
              # Backup CRDs
              kubectl get crd -o yaml > /backup/$(date +%Y%m%d)/crds.yaml
              
              # Backup custom resources
              kubectl get tailscaleendpoints -A -o yaml > /backup/$(date +%Y%m%d)/endpoints.yaml
              kubectl get tailscalegateways -A -o yaml > /backup/$(date +%Y%m%d)/gateways.yaml
              kubectl get tailscaleroutepolicies -A -o yaml > /backup/$(date +%Y%m%d)/policies.yaml
              kubectl get tailscaletailnets -A -o yaml > /backup/$(date +%Y%m%d)/tailnets.yaml
              
              # Backup operator configuration
              kubectl get configmaps -n tailscale-system -o yaml > /backup/$(date +%Y%m%d)/configmaps.yaml
              
              # Upload to S3 (or your backup storage)
              aws s3 sync /backup/$(date +%Y%m%d) s3://company-backups/tailscale-gateway/$(date +%Y%m%d)/
              
              # Cleanup old local backups
              find /backup -type d -mtime +7 -exec rm -rf {} \;
            
            env:
            - name: AWS_ACCESS_KEY_ID
              valueFrom:
                secretKeyRef:
                  name: backup-credentials
                  key: access-key-id
            - name: AWS_SECRET_ACCESS_KEY
              valueFrom:
                secretKeyRef:
                  name: backup-credentials
                  key: secret-access-key
            
            volumeMounts:
            - name: backup-storage
              mountPath: /backup
          
          volumes:
          - name: backup-storage
            persistentVolumeClaim:
              claimName: backup-pvc
          
          restartPolicy: OnFailure
```

### Disaster Recovery Procedures

```bash title="disaster-recovery.sh"
#!/bin/bash
# Disaster Recovery Script

set -e

BACKUP_DATE=${1:-$(date +%Y%m%d)}
NAMESPACE=${2:-tailscale-system}

echo "Starting disaster recovery for date: $BACKUP_DATE"

# 1. Restore CRDs first
echo "Restoring CRDs..."
kubectl apply -f s3://company-backups/tailscale-gateway/$BACKUP_DATE/crds.yaml

# 2. Wait for CRDs to be established
echo "Waiting for CRDs to be established..."
kubectl wait --for condition=established --timeout=60s crd/tailscaleendpoints.gateway.tailscale.com
kubectl wait --for condition=established --timeout=60s crd/tailscalegateways.gateway.tailscale.com

# 3. Restore namespace and RBAC
echo "Restoring namespace and RBAC..."
kubectl create namespace $NAMESPACE --dry-run=client -o yaml | kubectl apply -f -
kubectl apply -f rbac-production.yaml

# 4. Restore secrets (manual step - fetch from vault)
echo "Restoring secrets..."
kubectl create secret generic tailscale-oauth \
  --from-literal=client-id="$TAILSCALE_CLIENT_ID" \
  --from-literal=client-secret="$TAILSCALE_CLIENT_SECRET" \
  -n $NAMESPACE

# 5. Restore configuration
echo "Restoring configuration..."
kubectl apply -f s3://company-backups/tailscale-gateway/$BACKUP_DATE/configmaps.yaml

# 6. Deploy operator
echo "Deploying operator..."
kubectl apply -f production-deployment.yaml

# 7. Wait for operator to be ready
echo "Waiting for operator to be ready..."
kubectl wait --for=condition=available --timeout=300s deployment/tailscale-gateway-operator -n $NAMESPACE

# 8. Restore custom resources
echo "Restoring custom resources..."
kubectl apply -f s3://company-backups/tailscale-gateway/$BACKUP_DATE/tailnets.yaml
kubectl apply -f s3://company-backups/tailscale-gateway/$BACKUP_DATE/endpoints.yaml
kubectl apply -f s3://company-backups/tailscale-gateway/$BACKUP_DATE/gateways.yaml
kubectl apply -f s3://company-backups/tailscale-gateway/$BACKUP_DATE/policies.yaml

# 9. Verify recovery
echo "Verifying recovery..."
kubectl get pods -n $NAMESPACE
kubectl get tailscalegateways -A
kubectl get tailscaleendpoints -A

echo "Disaster recovery completed successfully!"
```

## Capacity Planning

### Resource Estimation

```yaml title="resource-calculator.yaml"
# Resource estimation guidelines for production

# Base operator requirements per 100 TailscaleEndpoints:
# CPU: 100m (baseline) + 10m per endpoint
# Memory: 128Mi (baseline) + 2Mi per endpoint

# Extension server requirements per 1000 requests/second:
# CPU: 50m + 50m per 1000 RPS
# Memory: 64Mi + 32Mi per 1000 RPS

# Example calculation for 500 endpoints, 5000 RPS:
operator:
  cpu: "600m"      # 100m + (500 * 10m/100) = 150m (round up for safety)
  memory: "256Mi"  # 128Mi + (500 * 2Mi/100) = 138Mi (round up)

extensionServer:
  cpu: "300m"      # 50m + (5000/1000 * 50m) = 300m
  memory: "224Mi"  # 64Mi + (5000/1000 * 32Mi) = 224Mi

# Recommended production scaling:
scaling:
  horizontal:
    # Operator: 1 replica per 1000 endpoints
    # Extension Server: 1 replica per 10000 RPS
  vertical:
    # Monitor CPU utilization - scale up if >70%
    # Monitor memory usage - scale up if >80%
```

### Horizontal Pod Autoscaler

```yaml title="hpa.yaml"
apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata:
  name: tailscale-gateway-operator-hpa
  namespace: tailscale-system
spec:
  scaleTargetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: tailscale-gateway-operator
  minReplicas: 3
  maxReplicas: 10
  metrics:
  - type: Resource
    resource:
      name: cpu
      target:
        type: Utilization
        averageUtilization: 70
  - type: Resource
    resource:
      name: memory
      target:
        type: Utilization
        averageUtilization: 80
  - type: Pods
    pods:
      metric:
        name: reconcile_queue_length
      target:
        type: AverageValue
        averageValue: "50"
  
  behavior:
    scaleUp:
      stabilizationWindowSeconds: 300
      policies:
      - type: Percent
        value: 100
        periodSeconds: 60
    scaleDown:
      stabilizationWindowSeconds: 900
      policies:
      - type: Percent
        value: 50
        periodSeconds: 300

---
apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata:
  name: tailscale-gateway-extension-server-hpa
  namespace: tailscale-system
spec:
  scaleTargetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: tailscale-gateway-extension-server
  minReplicas: 2
  maxReplicas: 20
  metrics:
  - type: Resource
    resource:
      name: cpu
      target:
        type: Utilization
        averageUtilization: 70
  - type: Pods
    pods:
      metric:
        name: grpc_requests_per_second
      target:
        type: AverageValue
        averageValue: "1000"
```

## Update and Rollback Procedures

### Rolling Update Strategy

```yaml title="update-strategy.yaml"
# Production update procedure
apiVersion: v1
kind: ConfigMap
metadata:
  name: update-procedures
  namespace: tailscale-system
data:
  update.sh: |
    #!/bin/bash
    # Safe production update procedure
    
    set -e
    
    NEW_VERSION=$1
    if [ -z "$NEW_VERSION" ]; then
      echo "Usage: $0 <new-version>"
      exit 1
    fi
    
    echo "Starting update to version $NEW_VERSION"
    
    # 1. Pre-update backup
    echo "Creating pre-update backup..."
    kubectl create job backup-pre-update-$(date +%s) \
      --from=cronjob/tailscale-gateway-backup \
      -n tailscale-system
    
    # 2. Update operator with rollback capability
    echo "Updating operator..."
    kubectl patch deployment tailscale-gateway-operator \
      -n tailscale-system \
      -p '{"spec":{"template":{"spec":{"containers":[{"name":"manager","image":"ghcr.io/rajsinghtech/tailscale-gateway:'$NEW_VERSION'"}]}}}}'
    
    # 3. Wait for rollout
    echo "Waiting for rollout to complete..."
    kubectl rollout status deployment/tailscale-gateway-operator \
      -n tailscale-system \
      --timeout=600s
    
    # 4. Health check
    echo "Performing health check..."
    sleep 30
    kubectl get pods -n tailscale-system -l app=tailscale-gateway-operator
    
    # 5. Functional verification
    echo "Running functional tests..."
    if ! ./verify-functionality.sh; then
      echo "Functional tests failed! Rolling back..."
      kubectl rollout undo deployment/tailscale-gateway-operator -n tailscale-system
      exit 1
    fi
    
    # 6. Update extension server
    echo "Updating extension server..."
    kubectl patch deployment tailscale-gateway-extension-server \
      -n tailscale-system \
      -p '{"spec":{"template":{"spec":{"containers":[{"name":"extension-server","image":"ghcr.io/rajsinghtech/tailscale-gateway-extension-server:'$NEW_VERSION'"}]}}}}'
    
    kubectl rollout status deployment/tailscale-gateway-extension-server \
      -n tailscale-system \
      --timeout=600s
    
    echo "Update completed successfully!"
  
  verify-functionality.sh: |
    #!/bin/bash
    # Functional verification script
    
    # Check operator health
    if ! curl -f http://tailscale-gateway-operator:8081/healthz; then
      echo "Operator health check failed"
      return 1
    fi
    
    # Check extension server health
    if ! grpcurl -plaintext tailscale-gateway-extension-server:5005 grpc.health.v1.Health/Check; then
      echo "Extension server health check failed"
      return 1
    fi
    
    # Check custom resources
    if ! kubectl get tailscalegateways --all-namespaces -o jsonpath='{.items[0].status.conditions[?(@.type=="Ready")].status}' | grep -q "True"; then
      echo "TailscaleGateway not ready"
      return 1
    fi
    
    echo "All functional tests passed"
    return 0
```

## Production Monitoring

### SLI/SLO Definitions

```yaml title="sli-slo.yaml"
# Service Level Indicators and Objectives
apiVersion: v1
kind: ConfigMap
metadata:
  name: sli-slo-definitions
  namespace: monitoring
data:
  sli-slo.yaml: |
    sli:
      # Availability SLI
      availability:
        name: "Operator Availability"
        description: "Percentage of time the operator is responding to health checks"
        query: "avg_over_time(up{job='tailscale-gateway-operator'}[5m])"
        
      # Reconciliation Success Rate SLI
      reconciliation_success_rate:
        name: "Reconciliation Success Rate"
        description: "Percentage of successful reconciliations"
        query: |
          (
            rate(controller_runtime_reconcile_total{result="success"}[5m]) / 
            rate(controller_runtime_reconcile_total[5m])
          ) * 100
      
      # Extension Server Response Time SLI
      response_time:
        name: "Extension Server Response Time"
        description: "95th percentile of gRPC response times"
        query: "histogram_quantile(0.95, rate(grpc_server_request_duration_seconds_bucket[5m]))"
      
      # Service Health Check Success Rate SLI
      service_health:
        name: "Service Health Check Success Rate"
        description: "Percentage of successful health checks"
        query: |
          (
            rate(health_check_success_total[5m]) / 
            (rate(health_check_success_total[5m]) + rate(health_check_failure_total[5m]))
          ) * 100
    
    slo:
      # 99.9% availability (43 minutes downtime per month)
      availability:
        target: 99.9
        window: "30d"
        
      # 99.5% reconciliation success rate
      reconciliation_success_rate:
        target: 99.5
        window: "7d"
        
      # 95th percentile response time < 100ms
      response_time:
        target: 0.1  # 100ms
        window: "1h"
        
      # 98% service health check success rate
      service_health:
        target: 98.0
        window: "24h"
```

## Runbooks

### Incident Response Runbook

```yaml title="runbook.yaml"
apiVersion: v1
kind: ConfigMap
metadata:
  name: incident-runbook
  namespace: tailscale-system
data:
  runbook.md: |
    # Tailscale Gateway Incident Response Runbook
    
    ## Common Incident Types
    
    ### 1. Operator Pod CrashLooping
    
    **Symptoms:**
    - Operator pods restarting frequently
    - Alert: `TailscaleGatewayOperatorDown`
    
    **Investigation:**
    ```bash
    # Check pod status
    kubectl get pods -n tailscale-system -l app=tailscale-gateway-operator
    
    # Check logs
    kubectl logs -n tailscale-system deployment/tailscale-gateway-operator --previous
    
    # Check events
    kubectl get events -n tailscale-system --sort-by='.lastTimestamp'
    ```
    
    **Common Causes & Solutions:**
    - **OOM Killed**: Increase memory limits
    - **OAuth Issues**: Verify secret exists and is valid
    - **RBAC Issues**: Check service account permissions
    - **Resource Conflicts**: Check for competing operators
    
    ### 2. High Reconciliation Error Rate
    
    **Symptoms:**
    - Alert: `HighReconciliationErrorRate`
    - Services not updating properly
    
    **Investigation:**
    ```bash
    # Check reconciliation metrics
    curl http://tailscale-gateway-operator:8080/metrics | grep reconcile_errors
    
    # Check specific controller logs
    kubectl logs -n tailscale-system deployment/tailscale-gateway-operator | grep ERROR
    ```
    
    **Common Causes & Solutions:**
    - **Tailscale API Rate Limits**: Implement backoff, check quota
    - **Network Issues**: Verify connectivity to api.tailscale.com
    - **Invalid Configurations**: Validate TailscaleEndpoints specs
    
    ### 3. Extension Server Connectivity Issues
    
    **Symptoms:**
    - Alert: `ExtensionServerDown`
    - Routes not being injected
    
    **Investigation:**
    ```bash
    # Test gRPC connectivity
    grpcurl -plaintext tailscale-gateway-extension-server:5005 grpc.health.v1.Health/Check
    
    # Check Envoy Gateway configuration
    kubectl get envoyproxy -A -o yaml
    ```
    
    **Solutions:**
    - Restart extension server pods
    - Verify Envoy Gateway extension configuration
    - Check network policies
    
    ## Emergency Procedures
    
    ### Complete Operator Restart
    ```bash
    # Scale down
    kubectl scale deployment tailscale-gateway-operator --replicas=0 -n tailscale-system
    
    # Wait for pods to terminate
    kubectl wait --for=delete pod -l app=tailscale-gateway-operator -n tailscale-system --timeout=60s
    
    # Scale back up
    kubectl scale deployment tailscale-gateway-operator --replicas=3 -n tailscale-system
    ```
    
    ### Rollback to Previous Version
    ```bash
    # Check rollout history
    kubectl rollout history deployment/tailscale-gateway-operator -n tailscale-system
    
    # Rollback to previous version
    kubectl rollout undo deployment/tailscale-gateway-operator -n tailscale-system
    
    # Monitor rollback
    kubectl rollout status deployment/tailscale-gateway-operator -n tailscale-system
    ```
    
    ## Escalation Contacts
    
    - **Primary On-Call**: Platform Team (+1-xxx-xxx-xxxx)
    - **Secondary On-Call**: Infrastructure Team (+1-xxx-xxx-xxxx)
    - **Manager**: Engineering Manager (+1-xxx-xxx-xxxx)
```

## Performance Optimization

### Production Performance Tuning

```yaml title="performance-tuning.yaml"
apiVersion: v1
kind: ConfigMap
metadata:
  name: performance-config
  namespace: tailscale-system
data:
  config.yaml: |
    # High-performance production configuration
    operator:
      # Controller tuning
      maxConcurrentReconciles: 20
      syncPeriod: "30s"
      resyncPeriod: "10m"
      
      # Client configuration
      clientConfig:
        qps: 50
        burst: 100
        timeout: "30s"
        
      # Cache tuning
      cache:
        syncPeriod: "30s"
        resyncPeriod: "10m"
        
      # Memory optimization
      gc:
        targetPercentage: 80
        
    extensionServer:
      # gRPC performance
      grpcServer:
        maxReceiveMessageSize: 104857600  # 100MB
        maxSendMessageSize: 104857600     # 100MB
        keepaliveTime: "30s"
        keepaliveTimeout: "5s"
        maxConnectionIdle: "300s"
        maxConnectionAge: "30m"
        
      # Connection pooling
      connectionPool:
        maxIdleConns: 100
        maxIdleConnsPerHost: 10
        idleConnTimeout: "90s"
        
    serviceCoordinator:
      # Coordination optimization
      batchSize: 100
      batchTimeout: "1s"
      maxRetries: 3
      cleanupInterval: "5m"
```

## Cost Optimization

### Resource Right-Sizing

```yaml title="cost-optimization.yaml"
# Cost-optimized configuration for different environments

# Development environment (lower costs)
development:
  operator:
    replicas: 1
    resources:
      requests:
        cpu: "50m"
        memory: "64Mi"
      limits:
        cpu: "200m"
        memory: "128Mi"
  
  extensionServer:
    replicas: 1
    resources:
      requests:
        cpu: "25m"
        memory: "32Mi"
      limits:
        cpu: "100m"
        memory: "64Mi"

# Staging environment (balanced)
staging:
  operator:
    replicas: 2
    resources:
      requests:
        cpu: "100m"
        memory: "128Mi"
      limits:
        cpu: "500m"
        memory: "256Mi"
  
  extensionServer:
    replicas: 1
    resources:
      requests:
        cpu: "50m"
        memory: "64Mi"
      limits:
        cpu: "250m"
        memory: "128Mi"

# Production environment (high availability)
production:
  operator:
    replicas: 3
    resources:
      requests:
        cpu: "200m"
        memory: "256Mi"
      limits:
        cpu: "1000m"
        memory: "512Mi"
  
  extensionServer:
    replicas: 2
    resources:
      requests:
        cpu: "100m"
        memory: "128Mi"
      limits:
        cpu: "500m"
        memory: "256Mi"
```

## Compliance and Auditing

### Audit Logging Configuration

```yaml title="audit-config.yaml"
apiVersion: v1
kind: ConfigMap
metadata:
  name: audit-config
  namespace: tailscale-system
data:
  audit.yaml: |
    audit:
      enabled: true
      
      # Audit events to log
      events:
        - "create"
        - "update"
        - "delete"
        - "patch"
      
      # Resources to audit
      resources:
        - "tailscaleendpoints"
        - "tailscalegateways"
        - "tailscaleroutepolicies"
        - "tailscaletailnets"
        - "secrets"
        - "configmaps"
      
      # Audit log configuration
      logging:
        format: "json"
        destination: "stdout"
        level: "info"
        
        # Additional metadata
        metadata:
          user: true
          timestamp: true
          resource: true
          action: true
          
      # Retention policy
      retention:
        days: 90
        
      # External audit systems
      external:
        enabled: true
        endpoints:
          - type: "webhook"
            url: "https://audit.company.com/webhook"
            headers:
              "Authorization": "Bearer ${AUDIT_TOKEN}"
          - type: "siem"
            url: "https://siem.company.com/api/events"
```

## Next Steps

- **[Monitoring Guide](./monitoring)** - Detailed monitoring setup
- **[Troubleshooting](./troubleshooting)** - Common issues and solutions
- **[Configuration Reference](../configuration/overview)** - Advanced configurations