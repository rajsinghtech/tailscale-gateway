---
id: monitoring
title: Monitoring and Observability
sidebar_position: 1
---

# Monitoring and Observability

Comprehensive monitoring setup for the Tailscale Gateway Operator, including metrics, logging, tracing, and alerting.

## Overview

The Tailscale Gateway Operator provides extensive observability features:

- **Metrics**: Prometheus-compatible metrics for performance monitoring
- **Logging**: Structured logging with configurable levels
- **Tracing**: OpenTelemetry-based distributed tracing
- **Health Checks**: Liveness and readiness probes
- **Alerting**: Pre-configured alerts for common issues

## Metrics Configuration

### Enable Metrics Collection

```yaml title="metrics-config.yaml"
apiVersion: v1
kind: ConfigMap
metadata:
  name: monitoring-config
  namespace: tailscale-system
data:
  config.yaml: |
    metrics:
      enabled: true
      port: 8080
      path: "/metrics"
      
      # Metric collection settings
      collection:
        interval: "15s"
        timeout: "10s"
        
      # Custom metrics
      custom:
        enabled: true
        labels:
          cluster: "production"
          region: "us-east-1"
          
    # Health probes
    health:
      enabled: true
      port: 8081
      livenessPath: "/healthz"
      readinessPath: "/readyz"
```

### ServiceMonitor for Prometheus

```yaml title="servicemonitor.yaml"
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: tailscale-gateway-operator
  namespace: tailscale-system
  labels:
    app: tailscale-gateway-operator
spec:
  selector:
    matchLabels:
      app: tailscale-gateway-operator
  endpoints:
  - port: metrics
    interval: 30s
    path: /metrics
    scheme: http
```

### Key Metrics

The operator exposes the following metrics:

#### Controller Metrics
- `controller_runtime_reconcile_total` - Total reconciliation attempts
- `controller_runtime_reconcile_time_seconds` - Time spent in reconciliation
- `controller_runtime_reconcile_errors_total` - Reconciliation errors

#### Extension Server Metrics
- `grpc_server_requests_total` - Total gRPC requests
- `grpc_server_request_duration_seconds` - gRPC request duration
- `grpc_server_errors_total` - gRPC server errors

#### Service Coordination Metrics
- `service_coordinator_services_total` - Total managed services
- `service_coordinator_registrations_total` - Service registrations
- `service_coordinator_cleanup_operations_total` - Cleanup operations

#### Health Check Metrics
- `health_check_duration_seconds` - Health check duration
- `health_check_success_total` - Successful health checks
- `health_check_failure_total` - Failed health checks

## Prometheus Configuration

### Prometheus Deployment

```yaml title="prometheus.yaml"
apiVersion: apps/v1
kind: Deployment
metadata:
  name: prometheus
  namespace: monitoring
spec:
  replicas: 1
  selector:
    matchLabels:
      app: prometheus
  template:
    metadata:
      labels:
        app: prometheus
    spec:
      containers:
      - name: prometheus
        image: prom/prometheus:v2.45.0
        ports:
        - containerPort: 9090
        volumeMounts:
        - name: config
          mountPath: /etc/prometheus
        - name: storage
          mountPath: /prometheus
        args:
          - '--config.file=/etc/prometheus/prometheus.yml'
          - '--storage.tsdb.path=/prometheus'
          - '--web.console.libraries=/etc/prometheus/console_libraries'
          - '--web.console.templates=/etc/prometheus/consoles'
          - '--storage.tsdb.retention.time=15d'
          - '--web.enable-lifecycle'
      volumes:
      - name: config
        configMap:
          name: prometheus-config
      - name: storage
        emptyDir: {}

---
apiVersion: v1
kind: ConfigMap
metadata:
  name: prometheus-config
  namespace: monitoring
data:
  prometheus.yml: |
    global:
      scrape_interval: 15s
      evaluation_interval: 15s
    
    rule_files:
      - "alert_rules.yml"
    
    alerting:
      alertmanagers:
        - static_configs:
            - targets:
              - alertmanager:9093
    
    scrape_configs:
      # Tailscale Gateway Operator
      - job_name: 'tailscale-gateway-operator'
        static_configs:
          - targets: ['tailscale-gateway-operator.tailscale-system:8080']
        scrape_interval: 30s
        metrics_path: /metrics
        
      # Extension Server
      - job_name: 'tailscale-gateway-extension-server'
        static_configs:
          - targets: ['tailscale-gateway-extension-server.tailscale-system:8080']
        scrape_interval: 30s
        
      # Kubernetes components
      - job_name: 'kubernetes-pods'
        kubernetes_sd_configs:
          - role: pod
        relabel_configs:
          - source_labels: [__meta_kubernetes_pod_annotation_prometheus_io_scrape]
            action: keep
            regex: true
          - source_labels: [__meta_kubernetes_pod_annotation_prometheus_io_path]
            action: replace
            target_label: __metrics_path__
            regex: (.+)
```

## Grafana Dashboards

### Operator Overview Dashboard

```json title="operator-dashboard.json"
{
  "dashboard": {
    "id": null,
    "title": "Tailscale Gateway Operator",
    "tags": ["tailscale", "gateway", "operator"],
    "timezone": "browser",
    "panels": [
      {
        "id": 1,
        "title": "Reconciliation Rate",
        "type": "graph",
        "targets": [
          {
            "expr": "rate(controller_runtime_reconcile_total[5m])",
            "legendFormat": "&#123;&#123;controller&#125;&#125; - &#123;&#123;result&#125;&#125;"
          }
        ],
        "yAxes": [
          {
            "label": "Reconciliations/sec"
          }
        ]
      },
      {
        "id": 2,
        "title": "Reconciliation Duration",
        "type": "graph",
        "targets": [
          {
            "expr": "histogram_quantile(0.95, rate(controller_runtime_reconcile_time_seconds_bucket[5m]))",
            "legendFormat": "95th percentile"
          },
          {
            "expr": "histogram_quantile(0.50, rate(controller_runtime_reconcile_time_seconds_bucket[5m]))",
            "legendFormat": "50th percentile"
          }
        ]
      },
      {
        "id": 3,
        "title": "Active Services",
        "type": "singlestat",
        "targets": [
          {
            "expr": "service_coordinator_services_total",
            "legendFormat": "Services"
          }
        ]
      },
      {
        "id": 4,
        "title": "Health Check Success Rate",
        "type": "graph",
        "targets": [
          {
            "expr": "rate(health_check_success_total[5m]) / (rate(health_check_success_total[5m]) + rate(health_check_failure_total[5m])) * 100",
            "legendFormat": "Success Rate %"
          }
        ]
      }
    ],
    "time": {
      "from": "now-1h",
      "to": "now"
    },
    "refresh": "30s"
  }
}
```

### Service Health Dashboard

```yaml title="service-health-dashboard.yaml"
apiVersion: v1
kind: ConfigMap
metadata:
  name: service-health-dashboard
  namespace: monitoring
data:
  dashboard.json: |
    {
      "dashboard": {
        "title": "Tailscale Service Health",
        "panels": [
          {
            "title": "Service Availability",
            "type": "table",
            "targets": [
              {
                "expr": "up{job=~\"tailscale.*\"}",
                "format": "table",
                "instant": true
              }
            ],
            "transformations": [
              {
                "id": "organize",
                "options": {
                  "includeByName": {
                    "instance": true,
                    "job": true,
                    "Value": true
                  },
                  "renameByName": {
                    "Value": "Status"
                  }
                }
              }
            ]
          },
          {
            "title": "Response Time Distribution",
            "type": "heatmap",
            "targets": [
              {
                "expr": "increase(grpc_server_request_duration_seconds_bucket[1m])",
                "legendFormat": "&#123;&#123;le&#125;&#125;"
              }
            ]
          }
        ]
      }
    }
```

## Logging Configuration

### Structured Logging Setup

```yaml title="logging-config.yaml"
apiVersion: v1
kind: ConfigMap
metadata:
  name: logging-config
  namespace: tailscale-system
data:
  logging.yaml: |
    logging:
      # Global logging settings
      level: "info"  # debug, info, warn, error
      format: "json"  # json, console
      
      # Component-specific logging
      components:
        controller:
          level: "info"
          output: "stdout"
        extensionServer:
          level: "debug"
          output: "stdout"
        serviceCoordinator:
          level: "info"
          output: "stdout"
      
      # Log rotation
      rotation:
        enabled: true
        maxSize: "100MB"
        maxFiles: 10
        maxAge: "7d"
        compress: true
      
      # Structured fields
      fields:
        cluster: "production"
        component: "tailscale-gateway"
        version: "v1.0.0"
      
      # Sampling (for high-volume logs)
      sampling:
        enabled: true
        rate: 100  # Log every 100th message for debug level
        levels:
          debug: 100
          info: 1
          warn: 1
          error: 1
```

### Fluentd Configuration for Log Aggregation

```yaml title="fluentd-config.yaml"
apiVersion: v1
kind: ConfigMap
metadata:
  name: fluentd-config
  namespace: logging
data:
  fluent.conf: |
    <source>
      @type tail
      @id tailscale_operator_logs
      path /var/log/containers/tailscale-gateway-operator-*.log
      pos_file /var/log/fluentd-tailscale-operator.log.pos
      tag kubernetes.tailscale.operator
      format json
      time_format %Y-%m-%dT%H:%M:%S.%NZ
    </source>
    
    <filter kubernetes.tailscale.**>
      @type kubernetes_metadata
      @id filter_tailscale_metadata
    </filter>
    
    <filter kubernetes.tailscale.**>
      @type parser
      key_name log
      reserve_data true
      <parse>
        @type json
        time_key ts
        time_format %Y-%m-%dT%H:%M:%S.%NZ
      </parse>
    </filter>
    
    <match kubernetes.tailscale.**>
      @type elasticsearch
      @id out_es_tailscale
      host elasticsearch.logging.svc.cluster.local
      port 9200
      logstash_format true
      logstash_prefix tailscale-gateway
      <buffer>
        @type file
        path /var/log/fluentd-buffers/tailscale-gateway.buffer
        flush_mode interval
        retry_type exponential_backoff
        flush_thread_count 2
        flush_interval 5s
        retry_forever true
        retry_max_interval 30s
        chunk_limit_size 2M
        queue_limit_length 8
        overflow_action block
      </buffer>
    </match>
```

## Distributed Tracing

### OpenTelemetry Configuration

```yaml title="opentelemetry-config.yaml"
apiVersion: v1
kind: ConfigMap
metadata:
  name: otel-config
  namespace: tailscale-system
data:
  config.yaml: |
    tracing:
      enabled: true
      
      # Sampling configuration
      sampling:
        rate: 0.1  # Sample 10% of traces
        
      # Exporter configuration
      exporters:
        jaeger:
          endpoint: "http://jaeger-collector.observability:14268/api/traces"
          timeout: "10s"
          
        otlp:
          endpoint: "http://otel-collector.observability:4317"
          compression: "gzip"
          timeout: "10s"
      
      # Resource attributes
      resource:
        attributes:
          service.name: "tailscale-gateway-operator"
          service.version: "v1.0.0"
          deployment.environment: "production"
          k8s.cluster.name: "main-cluster"
          k8s.namespace.name: "tailscale-system"
      
      # Instrumentation
      instrumentation:
        http:
          enabled: true
          captureHeaders: true
        grpc:
          enabled: true
        database:
          enabled: true
        redis:
          enabled: true
```

### Jaeger Deployment

```yaml title="jaeger.yaml"
apiVersion: apps/v1
kind: Deployment
metadata:
  name: jaeger
  namespace: observability
spec:
  replicas: 1
  selector:
    matchLabels:
      app: jaeger
  template:
    metadata:
      labels:
        app: jaeger
    spec:
      containers:
      - name: jaeger
        image: jaegertracing/all-in-one:1.47
        ports:
        - containerPort: 16686
          name: ui
        - containerPort: 14268
          name: collector
        env:
        - name: COLLECTOR_OTLP_ENABLED
          value: "true"
        - name: SPAN_STORAGE_TYPE
          value: "elasticsearch"
        - name: ES_SERVER_URLS
          value: "http://elasticsearch.observability:9200"

---
apiVersion: v1
kind: Service
metadata:
  name: jaeger
  namespace: observability
spec:
  selector:
    app: jaeger
  ports:
  - name: ui
    port: 16686
    targetPort: 16686
  - name: collector
    port: 14268
    targetPort: 14268
```

## Alerting Rules

### Prometheus Alerting Rules

```yaml title="alert-rules.yaml"
apiVersion: v1
kind: ConfigMap
metadata:
  name: alert-rules
  namespace: monitoring
data:
  alert_rules.yml: |
    groups:
    - name: tailscale-gateway.rules
      rules:
      # Operator availability
      - alert: TailscaleGatewayOperatorDown
        expr: up{job="tailscale-gateway-operator"} == 0
        for: 5m
        labels:
          severity: critical
          component: operator
        annotations:
          summary: "Tailscale Gateway Operator is down"
          description: "The Tailscale Gateway Operator has been down for more than 5 minutes."
      
      # High reconciliation error rate
      - alert: HighReconciliationErrorRate
        expr: |
          (
            rate(controller_runtime_reconcile_errors_total[5m]) / 
            rate(controller_runtime_reconcile_total[5m])
          ) > 0.1
        for: 10m
        labels:
          severity: warning
          component: controller
        annotations:
          summary: "High reconciliation error rate"
          description: "Reconciliation error rate is &#123;&#123; $value | humanizePercentage &#125;&#125; for controller &#123;&#123; $labels.controller &#125;&#125;"
      
      # Extension server connectivity
      - alert: ExtensionServerDown
        expr: up{job="tailscale-gateway-extension-server"} == 0
        for: 3m
        labels:
          severity: critical
          component: extension-server
        annotations:
          summary: "Extension server is down"
          description: "The Tailscale Gateway extension server is unreachable."
      
      # Service health check failures
      - alert: ServiceHealthCheckFailure
        expr: |
          (
            rate(health_check_failure_total[5m]) / 
            (rate(health_check_success_total[5m]) + rate(health_check_failure_total[5m]))
          ) > 0.5
        for: 5m
        labels:
          severity: warning
          component: health-check
        annotations:
          summary: "High health check failure rate"
          description: "Health check failure rate is &#123;&#123; $value | humanizePercentage &#125;&#125; for service &#123;&#123; $labels.service &#125;&#125;"
      
      # Memory usage
      - alert: HighMemoryUsage
        expr: |
          (
            container_memory_working_set_bytes{container="manager", namespace="tailscale-system"} / 
            container_spec_memory_limit_bytes{container="manager", namespace="tailscale-system"}
          ) > 0.8
        for: 10m
        labels:
          severity: warning
          component: operator
        annotations:
          summary: "High memory usage"
          description: "Memory usage is &#123;&#123; $value | humanizePercentage &#125;&#125; of limit."
      
      # gRPC request errors
      - alert: HighGRPCErrorRate
        expr: |
          (
            rate(grpc_server_errors_total[5m]) / 
            rate(grpc_server_requests_total[5m])
          ) > 0.05
        for: 5m
        labels:
          severity: warning
          component: extension-server
        annotations:
          summary: "High gRPC error rate"
          description: "gRPC error rate is &#123;&#123; $value | humanizePercentage &#125;&#125;"
```

### Alertmanager Configuration

```yaml title="alertmanager.yaml"
apiVersion: v1
kind: ConfigMap
metadata:
  name: alertmanager-config
  namespace: monitoring
data:
  alertmanager.yml: |
    global:
      smtp_smarthost: 'smtp.company.com:587'
      smtp_from: 'alerts@company.com'
    
    route:
      group_by: ['alertname', 'component']
      group_wait: 10s
      group_interval: 10s
      repeat_interval: 1h
      receiver: 'default'
      routes:
      - match:
          severity: critical
        receiver: 'critical-alerts'
      - match:
          component: operator
        receiver: 'platform-team'
      - match:
          component: extension-server
        receiver: 'gateway-team'
    
    receivers:
    - name: 'default'
      email_configs:
      - to: 'team@company.com'
        subject: 'Tailscale Gateway Alert: &#123;&#123; .GroupLabels.alertname &#125;&#125;'
        body: |
          &#123;&#123; range .Alerts &#125;&#125;
          Alert: &#123;&#123; .Annotations.summary &#125;&#125;
          Description: &#123;&#123; .Annotations.description &#125;&#125;
          Labels: &#123;&#123; range .Labels.SortedPairs &#125;&#125; &#123;&#123; .Name &#125;&#125;=&#123;&#123; .Value &#125;&#125; &#123;&#123; end &#125;&#125;
          &#123;&#123; end &#125;&#125;
    
    - name: 'critical-alerts'
      email_configs:
      - to: 'oncall@company.com'
        subject: 'CRITICAL: Tailscale Gateway Alert'
      slack_configs:
      - api_url: 'https://hooks.slack.com/services/...'
        channel: '#alerts'
        title: 'Critical Alert'
        text: '&#123;&#123; .CommonAnnotations.summary &#125;&#125;'
    
    - name: 'platform-team'
      email_configs:
      - to: 'platform-team@company.com'
    
    - name: 'gateway-team'
      email_configs:
      - to: 'gateway-team@company.com'
```

## Health Checks

### Liveness and Readiness Probes

```yaml title="health-probes.yaml"
apiVersion: apps/v1
kind: Deployment
metadata:
  name: tailscale-gateway-operator
  namespace: tailscale-system
spec:
  template:
    spec:
      containers:
      - name: manager
        livenessProbe:
          httpGet:
            path: /healthz
            port: 8081
            scheme: HTTP
          initialDelaySeconds: 15
          periodSeconds: 20
          timeoutSeconds: 5
          failureThreshold: 3
          
        readinessProbe:
          httpGet:
            path: /readyz
            port: 8081
            scheme: HTTP
          initialDelaySeconds: 5
          periodSeconds: 10
          timeoutSeconds: 5
          failureThreshold: 3
          
        # Startup probe for slow starts
        startupProbe:
          httpGet:
            path: /healthz
            port: 8081
            scheme: HTTP
          initialDelaySeconds: 10
          periodSeconds: 10
          timeoutSeconds: 5
          failureThreshold: 30  # Allow up to 5 minutes for startup
```

### Custom Health Check Endpoints

```yaml title="custom-health-checks.yaml"
apiVersion: v1
kind: ConfigMap
metadata:
  name: health-check-config
  namespace: tailscale-system
data:
  health.yaml: |
    healthChecks:
      # Custom health check implementations
      custom:
        - name: "tailscale-connectivity"
          endpoint: "/health/tailscale"
          timeout: "10s"
          interval: "30s"
          
        - name: "extension-server-grpc"
          endpoint: "/health/grpc"
          timeout: "5s"
          interval: "15s"
          
        - name: "service-registry"
          endpoint: "/health/registry"
          timeout: "5s"
          interval: "60s"
      
      # Dependency checks
      dependencies:
        - name: "kubernetes-api"
          type: "http"
          url: "https://kubernetes.default.svc.cluster.local/healthz"
          timeout: "5s"
          
        - name: "tailscale-api"
          type: "http"
          url: "https://api.tailscale.com/health"
          timeout: "10s"
```

## Performance Monitoring

### Resource Usage Dashboards

```yaml title="resource-dashboard.yaml"
apiVersion: v1
kind: ConfigMap
metadata:
  name: resource-dashboard
  namespace: monitoring
data:
  dashboard.json: |
    {
      "dashboard": {
        "title": "Tailscale Gateway Resources",
        "panels": [
          {
            "title": "CPU Usage",
            "type": "graph",
            "targets": [
              {
                "expr": "rate(container_cpu_usage_seconds_total{namespace=\"tailscale-system\"}[5m]) * 100",
                "legendFormat": "&#123;&#123; container &#125;&#125;"
              }
            ]
          },
          {
            "title": "Memory Usage",
            "type": "graph",
            "targets": [
              {
                "expr": "container_memory_working_set_bytes{namespace=\"tailscale-system\"} / 1024 / 1024",
                "legendFormat": "&#123;&#123; container &#125;&#125; MB"
              }
            ]
          },
          {
            "title": "Network I/O",
            "type": "graph",
            "targets": [
              {
                "expr": "rate(container_network_receive_bytes_total{namespace=\"tailscale-system\"}[5m])",
                "legendFormat": "RX &#123;&#123; container &#125;&#125;"
              },
              {
                "expr": "rate(container_network_transmit_bytes_total{namespace=\"tailscale-system\"}[5m])",
                "legendFormat": "TX &#123;&#123; container &#125;&#125;"
              }
            ]
          }
        ]
      }
    }
```

## Log Analysis

### Common Log Queries

```bash
# View operator startup logs
kubectl logs -n tailscale-system deployment/tailscale-gateway-operator | grep "Starting"

# Monitor reconciliation errors
kubectl logs -n tailscale-system deployment/tailscale-gateway-operator | grep "ERROR.*reconcile"

# Check extension server gRPC logs
kubectl logs -n tailscale-system deployment/tailscale-gateway-extension-server | grep "grpc"

# Monitor service coordination
kubectl logs -n tailscale-system deployment/tailscale-gateway-operator | grep "ServiceCoordinator"

# Track health check failures
kubectl logs -n tailscale-system deployment/tailscale-gateway-operator | grep "health.*failed"
```

### Kibana/Elasticsearch Queries

```json
{
  "query": {
    "bool": {
      "must": [
        {"match": {"kubernetes.namespace": "tailscale-system"&#125;&#125;,
        {"match": {"component": "tailscale-gateway"&#125;&#125;,
        {"range": {"@timestamp": {"gte": "now-1h"&#125;&#125;}
      ],
      "should": [
        {"match": {"level": "ERROR"&#125;&#125;,
        {"match": {"level": "WARN"&#125;&#125;
      ]
    }
  },
  "sort": [{"@timestamp": {"order": "desc"&#125;&#125;]
}
```

## Troubleshooting Common Issues

### Debugging Performance Issues

1. **High CPU Usage**:
   ```bash
   # Check CPU metrics
   kubectl top pods -n tailscale-system
   
   # Analyze reconciliation rate
   curl http://tailscale-gateway-operator:8080/metrics | grep reconcile_total
   ```

2. **Memory Leaks**:
   ```bash
   # Monitor memory growth
   kubectl top pods -n tailscale-system --containers
   
   # Check for goroutine leaks
   curl http://tailscale-gateway-operator:8080/debug/pprof/goroutine
   ```

3. **Extension Server Issues**:
   ```bash
   # Check gRPC connection status
   grpcurl -plaintext tailscale-gateway-extension-server:5005 grpc.health.v1.Health/Check
   
   # Monitor request latency
   curl http://tailscale-gateway-extension-server:8080/metrics | grep grpc_duration
   ```

## Next Steps

- **[Production Deployment](./production)** - Production best practices
- **[Troubleshooting Guide](./troubleshooting)** - Common issues and solutions
- **[Configuration Reference](../configuration/overview)** - Advanced configurations