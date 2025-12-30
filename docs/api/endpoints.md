# API Endpoints

Detailed documentation for all API endpoints.

## POST /alert

Submit one or more alerts for processing.

### Request

**URL**: `/alert` or `/alert/:channelId`

**Method**: `POST`

**Content-Type**: `application/json`

**URL Parameters**:

| Parameter | Type | Description |
|-----------|------|-------------|
| `channelId` | string | Optional. Slack channel ID (e.g., `C0123456789`) |

**Body**: Single alert object or array of alerts

### Single Alert

```json
{
  "header": ":rotating_light: High CPU Usage",
  "text": "Server web-01 CPU at 95%",
  "footer": "Source: Prometheus",
  "severity": "error",
  "correlationId": "cpu-alert-web-01",
  "slackChannelId": "C0123456789",
  "routeKey": "production",
  "iconEmoji": ":alert:",
  "username": "Alert Bot",
  "autoResolveSeconds": 3600,
  "ignoreIfTextContains": ["test", "debug"]
}
```

### Array of Alerts

```json
[
  {"header": "Alert 1", "correlationId": "a1", "slackChannelId": "C123"},
  {"header": "Alert 2", "correlationId": "a2", "slackChannelId": "C123"}
]
```

### Wrapped Array Format

```json
{
  "alerts": [
    {"header": "Alert 1", "correlationId": "a1"},
    {"header": "Alert 2", "correlationId": "a2"}
  ]
}
```

### Response

**Success**: `204 No Content`

**Error**: `400 Bad Request` with error message

```
Missing POST body
```

### Examples

=== "curl"

    ```bash
    curl -X POST http://localhost:8080/alert/C0123456789 \
      -H "Content-Type: application/json" \
      -d '{
        "header": ":fire: Server Down",
        "text": "web-01 is not responding",
        "severity": "error",
        "correlationId": "server-down-web01"
      }'
    ```

=== "Go"

    ```go
    alert := &common.Alert{
        Header:        ":fire: Server Down",
        Text:          "web-01 is not responding",
        Severity:      common.AlertError,
        CorrelationID: "server-down-web01",
    }

    body, _ := json.Marshal(alert)
    resp, err := http.Post(
        "http://localhost:8080/alert/C0123456789",
        "application/json",
        bytes.NewReader(body),
    )
    ```

=== "Python"

    ```python
    import requests

    alert = {
        "header": ":fire: Server Down",
        "text": "web-01 is not responding",
        "severity": "error",
        "correlationId": "server-down-web01"
    }

    response = requests.post(
        "http://localhost:8080/alert/C0123456789",
        json=alert
    )
    ```

---

## POST /prometheus-alert

Receive webhooks from Prometheus AlertManager.

### Request

**URL**: `/prometheus-alert` or `/prometheus-alert/:channelId`

**Method**: `POST`

**Content-Type**: `application/json`

**Body**: Prometheus AlertManager webhook payload

```json
{
  "version": "4",
  "groupKey": "alertname:HighCPU",
  "status": "firing",
  "receiver": "slack-manager",
  "groupLabels": {
    "alertname": "HighCPU"
  },
  "commonLabels": {
    "alertname": "HighCPU",
    "severity": "error"
  },
  "commonAnnotations": {
    "summary": "High CPU usage detected"
  },
  "externalURL": "http://alertmanager:9093",
  "alerts": [
    {
      "status": "firing",
      "labels": {
        "alertname": "HighCPU",
        "instance": "web-01:9090",
        "severity": "error"
      },
      "annotations": {
        "summary": "CPU at 95%",
        "description": "Server web-01 CPU usage is very high"
      },
      "startsAt": "2024-01-15T10:30:00.000Z",
      "endsAt": "0001-01-01T00:00:00Z",
      "generatorURL": "http://prometheus:9090/graph"
    }
  ]
}
```

### Response

**Success**: `204 No Content`

**Error**: `400 Bad Request`

### Label to Alert Mapping

| Prometheus Label | Alert Field |
|-----------------|-------------|
| `alertname` | Header prefix |
| `severity` | Severity |
| `summary` | Header |
| `description` | Text |
| `correlationid` | Correlation ID |
| `runbook_url` | Footer link |

---

## GET /mappings

List all configured routing rules.

### Request

**URL**: `/mappings`

**Method**: `GET`

### Response

**Success**: `200 OK`

```json
[
  {
    "name": "production",
    "description": "Production alerts",
    "channel": "C0123456789",
    "hasPrefix": ["prod-", "production-"]
  },
  {
    "name": "staging",
    "channel": "C9876543210",
    "equals": ["staging", "stage"]
  },
  {
    "name": "default",
    "channel": "C0DEFAULT00",
    "matchAll": true
  }
]
```

---

## GET /channels

List all channels managed by Slack Manager.

### Request

**URL**: `/channels`

**Method**: `GET`

### Response

**Success**: `200 OK`

```json
{
  "alerts": "C0123456789",
  "incidents": "C9876543210",
  "notifications": "C1111111111"
}
```

---

## GET /ping

Health check endpoint.

### Request

**URL**: `/ping`

**Method**: `GET`

### Response

**Success**: `200 OK`

```
pong
```

---

## POST /alerts-test

Test endpoint that logs alerts without queuing them.

### Request

Same as `/alert` endpoint.

### Response

**Success**: `204 No Content`

### Use Case

Use this endpoint to test alert formatting without creating actual alerts:

```bash
curl -X POST http://localhost:8080/alerts-test/C0123456789 \
  -H "Content-Type: application/json" \
  -d '{"header": "Test Alert", "correlationId": "test-1"}'
```

The alert will be logged but not sent to Slack.
