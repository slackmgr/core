# Prometheus Integration

Slack Manager integrates natively with Prometheus AlertManager via webhooks.

## AlertManager Configuration

Configure AlertManager to send webhooks to Slack Manager:

```yaml
# alertmanager.yml
route:
  receiver: slack-manager
  group_by: ['alertname', 'namespace']
  group_wait: 30s
  group_interval: 5m
  repeat_interval: 4h

receivers:
  - name: slack-manager
    webhook_configs:
      - url: 'http://slack-manager:8080/prometheus-alert/C0123456789'
        send_resolved: true
```

## Channel Routing

### Static Channel (URL)

Route all alerts to a specific channel:

```yaml
webhook_configs:
  - url: 'http://slack-manager:8080/prometheus-alert/C0123456789'
```

### Dynamic Routing (Labels)

Use routing rules to route based on labels:

```yaml
# AlertManager - send to base endpoint
webhook_configs:
  - url: 'http://slack-manager:8080/prometheus-alert'

# Alert rule - include route key
- alert: HighCPU
  expr: cpu_usage > 90
  labels:
    route_key: production
```

```go
// Slack Manager routing rules
settings := &config.APISettings{
    RoutingRules: []*config.RoutingRule{
        {
            Name:      "production",
            Channel:   "C0PROD123",
            HasPrefix: []string{"production"},
        },
    },
}
```

## Label Mapping

Prometheus labels are automatically mapped to alert fields:

| Prometheus Label/Annotation | Alert Field | Priority |
|----------------------------|-------------|----------|
| `summary` (annotation) | Header | 1 |
| `alertname` (label) | Header (fallback) | 2 |
| `description` (annotation) | Text | 1 |
| `message` (annotation) | Text (fallback) | 2 |
| `severity` (label) | Severity | - |
| `correlationid` (annotation) | Correlation ID | 1 |
| Labels combination | Correlation ID (auto) | 2 |
| `runbook_url` (annotation) | Footer link | - |

### Automatic Correlation ID

If no `correlationid` annotation is provided, one is generated from labels:

```
{alertname}-{namespace}-{job}-{service}
```

Example:
```yaml
labels:
  alertname: HighCPU
  namespace: production
  job: node-exporter
```

Generated correlation ID: `highcpu-production-node-exporter`

## Severity Mapping

| Prometheus `severity` | Alert Severity |
|----------------------|----------------|
| `critical`, `error`, `page` | `error` |
| `warning`, `warn` | `warning` |
| `info`, `notice` | `info` |
| (resolved status) | `resolved` |

## Webhook Payload

AlertManager sends webhooks in this format:

```json
{
  "version": "4",
  "groupKey": "{}:{alertname=\"HighCPU\"}",
  "truncatedAlerts": 0,
  "status": "firing",
  "receiver": "slack-manager",
  "groupLabels": {
    "alertname": "HighCPU"
  },
  "commonLabels": {
    "alertname": "HighCPU",
    "severity": "error",
    "namespace": "production"
  },
  "commonAnnotations": {
    "summary": "High CPU detected",
    "description": "CPU usage is above 90%"
  },
  "externalURL": "http://alertmanager:9093",
  "alerts": [
    {
      "status": "firing",
      "labels": {
        "alertname": "HighCPU",
        "instance": "web-01:9090",
        "job": "node-exporter",
        "severity": "error"
      },
      "annotations": {
        "summary": "High CPU on web-01",
        "description": "CPU usage is 95% on web-01",
        "runbook_url": "https://wiki.example.com/runbooks/cpu"
      },
      "startsAt": "2024-01-15T10:30:00.000Z",
      "endsAt": "0001-01-01T00:00:00Z",
      "generatorURL": "http://prometheus:9090/graph?g0.expr=cpu_usage"
    }
  ]
}
```

## Example Alert Rules

### Basic CPU Alert

```yaml
groups:
  - name: cpu
    rules:
      - alert: HighCPU
        expr: 100 - (avg by(instance) (rate(node_cpu_seconds_total{mode="idle"}[5m])) * 100) > 90
        for: 5m
        labels:
          severity: error
        annotations:
          summary: "High CPU usage on {{ $labels.instance }}"
          description: "CPU usage is {{ $value | printf \"%.1f\" }}%"
```

### With Custom Correlation ID

```yaml
- alert: PodCrashLooping
  expr: rate(kube_pod_container_status_restarts_total[15m]) > 0
  for: 5m
  labels:
    severity: warning
  annotations:
    summary: "Pod {{ $labels.pod }} is crash looping"
    description: "Pod {{ $labels.pod }} in namespace {{ $labels.namespace }} has restarted {{ $value }} times"
    correlationid: "crashloop-{{ $labels.namespace }}-{{ $labels.pod }}"
```

### With Runbook Link

```yaml
- alert: DiskSpaceLow
  expr: (node_filesystem_avail_bytes / node_filesystem_size_bytes) * 100 < 10
  for: 10m
  labels:
    severity: warning
  annotations:
    summary: "Low disk space on {{ $labels.instance }}"
    description: "{{ $labels.mountpoint }} has {{ $value | printf \"%.1f\" }}% free"
    runbook_url: "https://wiki.example.com/runbooks/disk-space"
```

## Resolved Alerts

When AlertManager sends `status: resolved`:

1. Alert severity is set to `resolved`
2. Existing issue (same correlation ID) is updated
3. Issue state changes to "resolved"

```yaml
# Enable resolved notifications
webhook_configs:
  - url: 'http://slack-manager:8080/prometheus-alert/C0123456789'
    send_resolved: true
```

## Testing

Test your AlertManager integration:

```bash
# Simulate a firing alert
curl -X POST http://localhost:8080/prometheus-alert/C0123456789 \
  -H "Content-Type: application/json" \
  -d '{
    "status": "firing",
    "alerts": [{
      "status": "firing",
      "labels": {"alertname": "TestAlert", "severity": "warning"},
      "annotations": {"summary": "Test alert from curl"}
    }]
  }'

# Simulate a resolved alert
curl -X POST http://localhost:8080/prometheus-alert/C0123456789 \
  -H "Content-Type: application/json" \
  -d '{
    "status": "resolved",
    "alerts": [{
      "status": "resolved",
      "labels": {"alertname": "TestAlert", "severity": "warning"},
      "annotations": {"summary": "Test alert from curl"}
    }]
  }'
```
