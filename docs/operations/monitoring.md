# Monitoring Guide

Monitor Slack Manager health, performance, and reliability.

## Metrics

### Available Metrics

Slack Manager exposes metrics via the `Metrics` interface:

| Metric | Type | Description |
|--------|------|-------------|
| `alerts_received_total` | Counter | Total alerts received by API |
| `alerts_processed_total` | Counter | Total alerts processed by manager |
| `alerts_ignored_total` | Counter | Alerts ignored (filtered) |
| `issues_created_total` | Counter | New issues created |
| `issues_resolved_total` | Counter | Issues resolved |
| `issues_archived_total` | Counter | Issues archived |
| `queue_messages_sent_total` | Counter | Messages sent to queue |
| `queue_messages_received_total` | Counter | Messages received from queue |
| `slack_api_calls_total` | Counter | Slack API calls made |
| `slack_api_errors_total` | Counter | Slack API errors |
| `rate_limit_drops_total` | Counter | Alerts dropped due to rate limit |
| `request_duration_seconds` | Histogram | HTTP request latency |
| `queue_processing_duration_seconds` | Histogram | Alert processing time |

### Prometheus Configuration

```yaml
# prometheus.yml
scrape_configs:
  - job_name: 'slack-manager-api'
    static_configs:
      - targets: ['slack-manager-api:8080']
    metrics_path: /metrics

  - job_name: 'slack-manager'
    static_configs:
      - targets: ['slack-manager:8080']
    metrics_path: /metrics
```

### Grafana Dashboard

```json
{
  "dashboard": {
    "title": "Slack Manager",
    "panels": [
      {
        "title": "Alert Rate",
        "type": "graph",
        "targets": [
          {
            "expr": "rate(alerts_received_total[5m])",
            "legendFormat": "received"
          },
          {
            "expr": "rate(alerts_processed_total[5m])",
            "legendFormat": "processed"
          }
        ]
      },
      {
        "title": "Issue Lifecycle",
        "type": "graph",
        "targets": [
          {
            "expr": "rate(issues_created_total[5m])",
            "legendFormat": "created"
          },
          {
            "expr": "rate(issues_resolved_total[5m])",
            "legendFormat": "resolved"
          }
        ]
      },
      {
        "title": "Slack API Errors",
        "type": "graph",
        "targets": [
          {
            "expr": "rate(slack_api_errors_total[5m])",
            "legendFormat": "{{error_type}}"
          }
        ]
      },
      {
        "title": "Request Latency (p99)",
        "type": "graph",
        "targets": [
          {
            "expr": "histogram_quantile(0.99, rate(request_duration_seconds_bucket[5m]))",
            "legendFormat": "p99"
          }
        ]
      }
    ]
  }
}
```

## Logging

### Log Format

Slack Manager uses structured JSON logging:

```json
{
  "level": "info",
  "time": "2024-01-15T10:30:00Z",
  "caller": "api/handle_alert.go:42",
  "msg": "Alert received",
  "channel_id": "C0123456789",
  "correlation_id": "cpu-high-web01",
  "severity": "error"
}
```

### Log Levels

| Level | Usage |
|-------|-------|
| `debug` | Detailed debugging information |
| `info` | Normal operational events |
| `warn` | Warning conditions (recoverable) |
| `error` | Error conditions |

### Log Aggregation

#### CloudWatch Logs

```yaml
# ECS task definition
logConfiguration:
  logDriver: awslogs
  options:
    awslogs-group: /ecs/slack-manager
    awslogs-region: us-east-1
    awslogs-stream-prefix: api
```

#### Fluentd

```conf
<source>
  @type tail
  path /var/log/slack-manager/*.log
  pos_file /var/log/td-agent/slack-manager.pos
  tag slack-manager
  <parse>
    @type json
  </parse>
</source>

<match slack-manager>
  @type elasticsearch
  host elasticsearch
  port 9200
  index_name slack-manager
</match>
```

## Alerting

### Prometheus Alerting Rules

```yaml
# alerting-rules.yml
groups:
  - name: slack-manager
    rules:
      - alert: HighErrorRate
        expr: rate(slack_api_errors_total[5m]) > 0.1
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "High Slack API error rate"
          description: "Error rate is {{ $value }} errors/sec"

      - alert: QueueBacklog
        expr: aws_sqs_approximate_number_of_messages_visible > 1000
        for: 10m
        labels:
          severity: warning
        annotations:
          summary: "Large queue backlog"
          description: "{{ $value }} messages waiting in queue"

      - alert: HighLatency
        expr: histogram_quantile(0.99, rate(request_duration_seconds_bucket[5m])) > 1
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "High API latency"
          description: "p99 latency is {{ $value }}s"

      - alert: RateLimitDrops
        expr: rate(rate_limit_drops_total[5m]) > 0
        for: 1m
        labels:
          severity: info
        annotations:
          summary: "Alerts being rate limited"
          description: "Dropping {{ $value }} alerts/sec due to rate limiting"

      - alert: ServiceDown
        expr: up{job="slack-manager-api"} == 0
        for: 1m
        labels:
          severity: critical
        annotations:
          summary: "Slack Manager API is down"
          description: "API server is not responding"
```

## Health Monitoring

### Health Check Endpoint

```bash
# Check API health
curl http://localhost:8080/ping
# Expected: pong

# With status code check
curl -f http://localhost:8080/ping || echo "Service unhealthy"
```

### AWS Health Checks

```hcl
# ALB health check
resource "aws_lb_target_group" "api" {
  # ...

  health_check {
    enabled             = true
    healthy_threshold   = 2
    interval            = 30
    matcher             = "200"
    path                = "/ping"
    port                = "traffic-port"
    protocol            = "HTTP"
    timeout             = 5
    unhealthy_threshold = 3
  }
}
```

## Debugging

### Enable Debug Logging

```bash
export LOG_LEVEL=debug
```

### Request Tracing

Add request ID to trace requests:

```bash
curl -H "X-Request-ID: debug-123" http://localhost:8080/alert/C123 -d '...'
```

Look for request ID in logs:

```bash
grep "debug-123" /var/log/slack-manager/api.log
```

### Queue Debugging

Check SQS queue status:

```bash
aws sqs get-queue-attributes \
  --queue-url $ALERT_QUEUE_URL \
  --attribute-names All
```

### Slack API Debugging

Enable Slack API debug logging:

```go
cfg.Debug = true
```

## Runbooks

### High Error Rate

1. Check Slack API status: https://status.slack.com
2. Review error logs for specific error types
3. Check rate limits: `slack_api_rate_limited_total`
4. Verify bot token validity

### Queue Backlog

1. Check manager pod health
2. Review processing errors in logs
3. Scale manager replicas if needed
4. Check DynamoDB capacity

### Service Unavailable

1. Check pod status: `kubectl get pods`
2. Review pod logs: `kubectl logs <pod>`
3. Check resource limits
4. Verify network connectivity

## SLIs and SLOs

### Suggested SLIs

| SLI | Measurement |
|-----|-------------|
| Availability | Successful `/ping` responses / Total requests |
| Latency | p99 request duration |
| Error Rate | Failed requests / Total requests |
| Throughput | Alerts processed per minute |

### Suggested SLOs

| SLO | Target |
|-----|--------|
| Availability | 99.9% |
| Latency (p99) | < 500ms |
| Error Rate | < 0.1% |
| Alert Processing | < 30s end-to-end |
