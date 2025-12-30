# Alert Format

Complete reference for the alert data structure.

## Alert Object

```json
{
  "header": ":rotating_light: High CPU Usage",
  "text": "Server web-01 CPU at 95%",
  "footer": "Source: Prometheus | Region: us-east-1",
  "fallbackText": "High CPU Usage on web-01",
  "severity": "error",
  "correlationId": "cpu-alert-web-01",
  "slackChannelId": "C0123456789",
  "routeKey": "production",
  "type": "prometheus",
  "iconEmoji": ":rotating_light:",
  "username": "Alert Bot",
  "autoResolveSeconds": 3600,
  "archivingDelaySeconds": 86400,
  "issueFollowUpEnabled": true,
  "failOnRateLimitError": false,
  "ignoreIfTextContains": ["test", "debug"],
  "webhooks": [...]
}
```

## Field Reference

### Required Fields

| Field | Type | Description |
|-------|------|-------------|
| `header` | string | Main alert title, displayed prominently |
| `correlationId` | string | Unique ID for grouping related alerts |

### Display Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `text` | string | - | Detailed alert description |
| `footer` | string | - | Footer text (source, links, etc.) |
| `fallbackText` | string | - | Plain text for notifications |
| `iconEmoji` | string | - | Slack emoji for bot avatar |
| `username` | string | - | Display name for bot |

### Routing Fields

| Field | Type | Description |
|-------|------|-------------|
| `slackChannelId` | string | Target Slack channel ID |
| `routeKey` | string | Key for routing rule matching |
| `type` | string | Alert type for filtering |

### Behavior Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `severity` | string | `info` | Alert severity level |
| `autoResolveSeconds` | int | 0 | Auto-resolve after N seconds (0 = disabled) |
| `archivingDelaySeconds` | int | 86400 | Archive after resolution (default: 24h) |
| `issueFollowUpEnabled` | bool | false | Enable issue tracking |
| `failOnRateLimitError` | bool | false | Fail instead of dropping on rate limit |
| `ignoreIfTextContains` | []string | - | Ignore if text contains any string |

## Severity Levels

| Severity | Description | Typical Color |
|----------|-------------|---------------|
| `info` | Informational alert | Blue |
| `warning` | Warning condition | Yellow |
| `error` | Error condition | Red |
| `resolved` | Issue resolved | Green |
| `panic` | Critical/panic | Dark Red |

```json
{
  "severity": "error"
}
```

## Correlation ID

The `correlationId` groups related alerts into a single issue:

```json
// These alerts create/update the same issue
{"correlationId": "cpu-high-web01", "header": "CPU at 90%"}
{"correlationId": "cpu-high-web01", "header": "CPU at 95%"}
{"correlationId": "cpu-high-web01", "header": "CPU at 85%"}

// This creates a separate issue
{"correlationId": "cpu-high-web02", "header": "CPU at 90%"}
```

### Best Practices

- Use consistent, predictable IDs
- Include relevant context (host, service, metric)
- Keep IDs reasonably short

Examples:
```
cpu-high-web01
disk-full-db-primary
api-error-rate-production
deployment-failed-myservice-v1.2.3
```

## Channel Resolution

The API determines the target channel in this order:

1. **URL Parameter**: `/alert/C0123456789`
2. **Body Field**: `{"slackChannelId": "C0123456789"}`
3. **Route Key Match**: `{"routeKey": "production"}` → routing rules
4. **Error**: If none match, returns 400

## Auto-Resolution

Configure automatic resolution for transient alerts:

```json
{
  "header": "Deployment in progress",
  "correlationId": "deploy-123",
  "autoResolveSeconds": 1800,
  "issueFollowUpEnabled": true
}
```

After 30 minutes (1800 seconds), the alert automatically resolves.

## Ignoring Alerts

Filter out noisy alerts:

```json
{
  "header": "Test Alert",
  "text": "This is a test alert from the staging environment",
  "ignoreIfTextContains": ["test", "staging", "debug"]
}
```

This alert will be ignored because `text` contains "test" and "staging".

## Webhooks

Attach webhooks for interactive actions:

```json
{
  "header": "Deployment Approval Required",
  "webhooks": [
    {
      "id": "approve",
      "url": "https://api.example.com/deployments/123/approve",
      "buttonText": "Approve",
      "buttonStyle": "primary",
      "confirmationText": "Are you sure you want to approve?"
    },
    {
      "id": "reject",
      "url": "https://api.example.com/deployments/123/reject",
      "buttonText": "Reject",
      "buttonStyle": "danger"
    }
  ]
}
```

### Webhook Fields

| Field | Type | Description |
|-------|------|-------------|
| `id` | string | Unique webhook identifier |
| `url` | string | Target URL for webhook call |
| `buttonText` | string | Button label |
| `buttonStyle` | string | `primary`, `danger`, or default |
| `confirmationText` | string | Confirmation dialog text |
| `accessLevel` | string | Who can trigger (`everyone`, `admins`) |
| `payload` | object | Additional payload data |

## Complete Example

```json
{
  "header": ":fire: Production Database CPU Critical",
  "text": "*Host*: db-primary-01\n*CPU*: 98%\n*Duration*: 5 minutes",
  "footer": "<https://grafana.example.com/d/abc123|View Dashboard> | Region: us-east-1",
  "fallbackText": "Production database CPU at 98%",
  "severity": "error",
  "correlationId": "db-cpu-critical-primary",
  "slackChannelId": "C0INCIDENTS",
  "routeKey": "production-database",
  "type": "prometheus",
  "iconEmoji": ":database:",
  "username": "Database Alerts",
  "autoResolveSeconds": 0,
  "archivingDelaySeconds": 172800,
  "issueFollowUpEnabled": true,
  "webhooks": [
    {
      "id": "runbook",
      "url": "https://wiki.example.com/runbooks/db-cpu",
      "buttonText": "View Runbook",
      "buttonStyle": "primary"
    }
  ]
}
```
