# API Overview

The Slack Manager API is a REST API that accepts alerts from external systems and routes them to Slack channels.

## Base URL

```
http://localhost:8080
```

## Authentication

The API does not require authentication by default. For production deployments, implement authentication at the infrastructure level (API Gateway, reverse proxy, etc.).

## Content Type

All endpoints accept and return `application/json`.

## Endpoints Summary

| Method | Endpoint | Description |
|--------|----------|-------------|
| `POST` | `/alert` | Submit one or more alerts |
| `POST` | `/alert/:channelId` | Submit alerts to a specific channel |
| `POST` | `/alerts` | Alias for `/alert` |
| `POST` | `/alerts/:channelId` | Alias for `/alert/:channelId` |
| `POST` | `/prometheus-alert` | Prometheus AlertManager webhook |
| `POST` | `/prometheus-alert/:channelId` | Prometheus webhook to specific channel |
| `POST` | `/alerts-test` | Test endpoint (logs only, no queuing) |
| `GET` | `/mappings` | List routing rules |
| `GET` | `/channels` | List managed channels |
| `GET` | `/ping` | Health check |

## Response Codes

| Code | Description |
|------|-------------|
| `200 OK` | Successful GET request |
| `204 No Content` | Alert accepted successfully |
| `400 Bad Request` | Invalid request (validation error) |
| `429 Too Many Requests` | Rate limit exceeded |
| `500 Internal Server Error` | Server error |

## Rate Limiting

Each Slack channel has its own rate limit:

- **Default rate**: 1 alert per second
- **Burst size**: 10 alerts

When rate limited, the API returns `429 Too Many Requests`:

```json
{
  "error": "rate limit exceeded for 5 alerts in channel C0123456789"
}
```

## Quick Examples

### Send a Single Alert

```bash
curl -X POST http://localhost:8080/alert/C0123456789 \
  -H "Content-Type: application/json" \
  -d '{
    "header": ":rotating_light: High CPU Usage",
    "text": "Server web-01 CPU at 95%",
    "severity": "error",
    "correlationId": "cpu-alert-web-01"
  }'
```

### Send Multiple Alerts

```bash
curl -X POST http://localhost:8080/alerts \
  -H "Content-Type: application/json" \
  -d '[
    {
      "header": ":rotating_light: Alert 1",
      "slackChannelId": "C0123456789",
      "correlationId": "alert-1"
    },
    {
      "header": ":warning: Alert 2",
      "slackChannelId": "C0123456789",
      "correlationId": "alert-2"
    }
  ]'
```

### Health Check

```bash
curl http://localhost:8080/ping
# Response: pong
```

### List Routing Rules

```bash
curl http://localhost:8080/mappings
```

Response:
```json
[
  {
    "name": "production",
    "channel": "C0123456789",
    "hasPrefix": ["prod-"]
  }
]
```
