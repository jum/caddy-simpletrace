# Caddy SimpleTrace Plugin

A lightweight Caddy module for W3C Trace Context propagation and structured logging without the overhead of full OpenTelemetry integration.

## Features

- Parses incoming `traceparent` HTTP headers (W3C Trace Context specification)
- Generates new trace IDs when none exist
- Creates unique span IDs for each request
- Propagates `traceparent` headers to upstream/proxied requests
- Adds trace context to Caddy’s structured logs
- Supports Google Cloud Logging (Stackdriver) format
- Respects and propagates sampling flags
- Zero external dependencies beyond Caddy

## Why SimpleTrace?

Caddy’s built-in `tracing` directive includes the full OpenTelemetry stack, which can be heavy if you only need trace context in logs. SimpleTrace provides just the essentials:

- Trace ID propagation across service boundaries
- Structured logging with trace context
- Cloud logging platform integration
- Minimal performance overhead

## Installation

### Using xcaddy

```bash
xcaddy build --with github.com/yourusername/caddy-simpletrace
```

### Using Docker

```dockerfile
FROM caddy:builder AS builder
RUN xcaddy build \
    --with github.com/yourusername/caddy-simpletrace

FROM caddy:latest
COPY --from=builder /usr/bin/caddy /usr/bin/caddy
```

## Usage

### Basic Configuration

```caddyfile
example.com {
    simpletrace
    reverse_proxy backend:8080
}
```

This will:

1. Parse or generate trace IDs
1. Add trace context to logs in standard format
1. Propagate `traceparent` headers to backend

**Log output:**

```json
{
  "level": "info",
  "ts": 1702123456.789,
  "msg": "handled request",
  "trace_id": "4bf92f3577b34da6a3ce929d0e0e4736",
  "span_id": "00f067aa0ba902b7",
  "trace_sampled": true
}
```

### Google Cloud Logging (Stackdriver) Format

```caddyfile
example.com {
    simpletrace {
        stackdriver your-gcp-project-id
    }
    reverse_proxy backend:8080
}
```

**Log output:**

```json
{
  "level": "info",
  "ts": 1702123456.789,
  "msg": "handled request",
  "logging.googleapis.com/trace": "projects/your-gcp-project-id/traces/4bf92f3577b34da6a3ce929d0e0e4736",
  "logging.googleapis.com/spanId": "00f067aa0ba902b7",
  "logging.googleapis.com/trace_sampled": true
}
```

Benefits with Stackdriver format:

- Automatic trace correlation in Google Cloud Logging
- Integration with Cloud Trace (for sampled traces)
- Clickable trace links in GCP Console

## Configuration Options

### Standard Format

```caddyfile
simpletrace
```

Adds these fields to logs:

- `trace_id` - 32-character hex trace identifier
- `span_id` - 16-character hex span identifier
- `trace_sampled` - boolean indicating if trace should be recorded
- `parent_span_id` - (optional) parent span ID from incoming request

### Stackdriver Format

```caddyfile
simpletrace {
    stackdriver <project-id>
}
```

**Parameters:**

- `project-id` (optional) - Your GCP project ID. When provided, trace field includes full resource path: `projects/{project-id}/traces/{trace-id}`

## How It Works

### Trace Context Flow

1. **Incoming Request**
- If `traceparent` header exists: parse trace ID, parent span ID, and flags
- If no header: generate new trace ID with random sampling
1. **Request Processing**
- Generate new span ID for this request
- Add trace context fields to Caddy’s log context
- Create new `traceparent` header with current span as parent
1. **Outgoing Request**
- Propagate `traceparent` header to upstream services
- Downstream services can continue the trace
1. **Logging**
- All Caddy access logs for this request include trace context
- Enables correlation across service boundaries

### Traceparent Header Format

Follows [W3C Trace Context](https://www.w3.org/TR/trace-context/) specification:

```
traceparent: 00-{trace-id}-{parent-span-id}-{flags}
```

Example:

```
traceparent: 00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01
```

- `00` - Version
- `4bf92f3577b34da6a3ce929d0e0e4736` - Trace ID (32 hex chars)
- `00f067aa0ba902b7` - Parent Span ID (16 hex chars)
- `01` - Flags (01 = sampled, 00 = not sampled)

### Sampling Flag

The least significant bit of the flags byte indicates sampling:

- `01` - **Sampled**: Trace should be recorded by tracing backends
- `00` - **Not sampled**: Trace context propagated but not recorded

When using Google Cloud Logging, the `trace_sampled` field controls whether traces are sent to Cloud Trace for analysis.

## Example Configurations

### Multi-service Setup

```caddyfile
# Frontend service
frontend.example.com {
    simpletrace {
        stackdriver my-project
    }
    reverse_proxy frontend-app:3000
}

# API service
api.example.com {
    simpletrace {
        stackdriver my-project
    }
    reverse_proxy api-app:8080
}
```

All services in your architecture can use SimpleTrace to maintain trace context across the entire request flow.

### Development Setup

```caddyfile
localhost:8080 {
    simpletrace
    log {
        output stdout
        format json
    }
    reverse_proxy localhost:3000
}
```

### Production with Cloud Logging

```caddyfile
example.com {
    simpletrace {
        stackdriver production-project-123
    }
    
    log {
        output stdout
        format json
    }
    
    reverse_proxy backend:8080
}
```

When running on Google Cloud (GKE, Cloud Run, etc.), logs are automatically ingested by Cloud Logging and traces are correlated.

## Comparison with Built-in Tracing

|Feature                 |SimpleTrace |Built-in `tracing`|
|------------------------|------------|------------------|
|Trace ID propagation    |✅           |✅                 |
|Log augmentation        |✅           |✅                 |
|OpenTelemetry SDK       |❌           |✅                 |
|OTLP export             |❌           |✅                 |
|Span export             |❌           |✅                 |
|Performance overhead    |Minimal     |Moderate          |
|Configuration complexity|Simple      |Complex           |
|Use case                |Logging only|Full observability|

Use SimpleTrace when you only need trace context in logs. Use the built-in `tracing` directive when you need full distributed tracing with span export to collectors like Jaeger, Zipkin, or Cloud Trace.

## Troubleshooting

### Logs don’t include trace fields

Ensure you’re using JSON log format:

```caddyfile
log {
    format json
}
```

### Traces not appearing in Cloud Trace

1. Verify `stackdriver` directive includes your project ID
1. Check that `trace_sampled` is `true` in logs
1. Ensure Cloud Logging API is enabled
1. Verify service account has `roles/cloudtrace.agent` permission

### Invalid traceparent headers

SimpleTrace validates incoming headers. Invalid formats are rejected and new trace IDs are generated. Check logs for trace ID generation patterns - consistent new traces may indicate malformed incoming headers.

## Resources

- [W3C Trace Context Specification](https://www.w3.org/TR/trace-context/)
- [Google Cloud Logging Structured Logging](https://cloud.google.com/logging/docs/structured-logging)
- [Caddy Documentation](https://caddyserver.com/docs/)
- [Caddy Module Development](https://caddyserver.com/docs/extending-caddy)
