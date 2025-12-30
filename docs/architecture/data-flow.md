# Data Flow

This document describes how data flows through the Slack Manager system.

## Alert Processing Flow

```mermaid
sequenceDiagram
    participant E as External System
    participant A as API Server
    participant Q as Alert Queue
    participant M as Manager
    participant C as Coordinator
    participant CM as Channel Manager
    participant S as Slack
    participant D as Database

    E->>A: POST /alert
    A->>A: Validate & Rate Limit
    A->>Q: Enqueue alert
    A-->>E: 204 No Content

    Q->>M: Receive alert
    M->>C: Route to coordinator
    C->>CM: Dispatch to channel
    CM->>CM: Find/create issue
    CM->>D: Save issue
    CM->>S: Post/update message
    S-->>CM: Message ID
    CM->>D: Update message ID
```

## Alert Correlation

Alerts are grouped into issues using correlation IDs:

```mermaid
graph LR
    subgraph Incoming Alerts
        A1[Alert: cpu-high-web01]
        A2[Alert: cpu-high-web01]
        A3[Alert: disk-full-db01]
    end

    subgraph Issues
        I1[Issue: cpu-high-web01]
        I2[Issue: disk-full-db01]
    end

    A1 --> I1
    A2 --> I1
    A3 --> I2
```

### Correlation Logic

1. Extract `correlationId` from alert
2. Search for existing issue with matching ID
3. If found: Add alert to existing issue
4. If not found: Create new issue

## Issue State Transitions

```mermaid
stateDiagram-v2
    [*] --> Firing: New alert
    Firing --> Acknowledged: :eyes: reaction
    Acknowledged --> Firing: Remove :eyes:
    Firing --> Resolved: :white_check_mark: reaction
    Acknowledged --> Resolved: :white_check_mark:
    Resolved --> Firing: New alert (same correlation)
    Resolved --> Archived: Archive delay elapsed
    Archived --> [*]
```

### State Descriptions

| State | Description | Slack Indicator |
|-------|-------------|-----------------|
| Firing | Active issue, needs attention | Red status |
| Acknowledged | Someone is looking at it | :eyes: reaction |
| Resolved | Issue is fixed | :white_check_mark: reaction |
| Archived | Issue cleaned up | Message deleted/archived |

## Slack Event Flow

```mermaid
sequenceDiagram
    participant U as User
    participant S as Slack
    participant SM as Socket Mode
    participant C as Coordinator
    participant CM as Channel Manager
    participant D as Database

    U->>S: Add :eyes: reaction
    S->>SM: reaction_added event
    SM->>C: Route event
    C->>CM: Handle reaction
    CM->>CM: Update issue state
    CM->>D: Save issue
    CM->>S: Update message
```

## Message Queue Patterns

### Alert Queue

- **Type**: FIFO (First-In-First-Out)
- **Deduplication**: By correlation ID + channel
- **Visibility Timeout**: Extended during processing
- **Dead Letter Queue**: For failed messages

### Command Queue

- **Type**: FIFO
- **Purpose**: Inter-component commands
- **Examples**: Force refresh, bulk operations

## Data Structures

### Alert

```json
{
  "correlationId": "cpu-high-web01",
  "header": ":rotating_light: High CPU",
  "text": "Server web-01 CPU at 95%",
  "severity": "error",
  "slackChannelId": "C0123456789",
  "routeKey": "production",
  "createdAt": "2024-01-15T10:30:00Z"
}
```

### Issue

```json
{
  "id": "issue-123",
  "correlationId": "cpu-high-web01",
  "channelId": "C0123456789",
  "messageTs": "1705315800.123456",
  "state": "firing",
  "alerts": [...],
  "createdAt": "2024-01-15T10:30:00Z",
  "updatedAt": "2024-01-15T10:35:00Z"
}
```

## Caching Strategy

```mermaid
graph TB
    subgraph Read Path
        R[Request] --> C{Cache Hit?}
        C -->|Yes| CR[Return Cached]
        C -->|No| S[Fetch from Slack]
        S --> U[Update Cache]
        U --> CR
    end
```

### Cached Data

| Data | TTL | Purpose |
|------|-----|---------|
| Channel Info | 5 min | Validate channel exists |
| User Info | 5 min | Display names in messages |
| Bot Info | 1 hour | Self-identification |

## Error Handling

### Retry Strategy

```mermaid
graph TD
    A[Process Alert] --> B{Success?}
    B -->|Yes| C[Acknowledge]
    B -->|No| D{Retryable?}
    D -->|Yes| E[Return to Queue]
    D -->|No| F[Dead Letter Queue]
    E --> G[Exponential Backoff]
    G --> A
```

### Error Categories

| Category | Action | Example |
|----------|--------|---------|
| Transient | Retry | Network timeout |
| Rate Limit | Wait & Retry | Slack rate limit |
| Permanent | DLQ | Invalid channel |
