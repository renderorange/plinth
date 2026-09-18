# API Reference

This document describes the plinth API endpoints.

## Gateway API (default port 8000)

### Health Check

```
GET /health
```

Returns cluster health status.

**Response:**

```json
{
  "status": "ok",
  "nodes": 3,
  "healthy": 2
}
```

| Field | Type | Description |
|-------|------|-------------|
| `status` | string | `"ok"` (all nodes healthy), `"degraded"` (some unhealthy), or `"error"` (no healthy nodes) |
| `nodes` | int | Total number of configured nodes |
| `healthy` | int | Number of healthy nodes |

### List Models

```
GET /v1/models
```

Returns available models in OpenAI-compatible format.

**Response:**

```json
{
  "object": "list",
  "data": [
    {
      "id": "Qwen/Qwen2.5-32B-Instruct-Q4",
      "object": "model",
      "owned_by": "cluster"
    }
  ]
}
```

### Chat Completions

```
POST /v1/chat/completions
```

Proxies chat completion requests to a healthy vLLM node.

**Request Body:**

```json
{
  "model": "Qwen/Qwen2.5-32B-Instruct-Q4",
  "messages": [
    {"role": "user", "content": "Hello"}
  ]
}
```

The `model` field picks the routing pool: ring models round-robin among their ring's nodes; anything else round-robins among ring-less nodes. If omitted, the configured default model is used for routing and injected into the forwarded request.

**Response:** Standard OpenAI chat completion response (proxied from vLLM).

**Errors:**

| Status | Description |
|--------|-------------|
| 400 | Invalid request body, or `model` omitted with no default configured |
| 503 | No healthy node available |

### Text Completions

```
POST /v1/completions
```

Proxies text completion requests to a healthy vLLM node.

**Request Body:**

```json
{
  "model": "Qwen/Qwen2.5-32B-Instruct-Q4",
  "prompt": "Once upon a time"
}
```

The `model` field is optional. If omitted, the configured default model is used for routing and injected into the forwarded request.

**Response:** Standard OpenAI completion response (proxied from vLLM).

**Errors:**

| Status | Description |
|--------|-------------|
| 400 | Invalid request body, or `model` omitted with no default configured |
| 503 | No healthy node available |

## Metrics API (default port 9090)

### Prometheus Metrics

```
GET /metrics
```

Returns Prometheus metrics for the gateway.

**Metrics:**

| Metric | Type | Description |
|--------|------|-------------|
| `cluster_nodes_healthy` | Gauge | Number of healthy nodes |
| `cluster_nodes_degraded` | Gauge | Number of degraded nodes |
| `cluster_nodes_dead` | Gauge | Number of dead nodes |
| `gateway_request_duration_seconds` | Histogram | Request duration |
| `gateway_requests_total` | Counter | Total requests by model and status |

**Labels for `gateway_requests_total`:**

- `model` — Model name from request
- `status` — HTTP status code (e.g., `"200"`, `"503"`)

## gpu-exporter API (default port 9100)

### GPU Metrics

```
GET /metrics
```

Returns GPU metrics in Prometheus text format.

**Metrics:**

| Metric | Type | Description |
|--------|------|-------------|
| `gpu_memory_used_bytes` | Gauge | GPU memory used |
| `gpu_memory_total_bytes` | Gauge | Total GPU memory |
| `gpu_utilization_percent` | Gauge | GPU utilization percentage |
| `gpu_temperature_celsius` | Gauge | GPU temperature |

### Health Check

```
GET /health
```

Returns liveness status.

**Response:**

```
ok
```

## Request Flow

```
Client
  │
  ▼
POST /v1/chat/completions
  │
  ▼
Extract model from body (for metrics)
  │
  ▼
Balancer.Select(states)
  │
  ▼
Proxy to http://<node>:<port>/v1/chat/completions
  │
  ▼
Response returned to client (buffered; no streaming)
```

## Error Responses

Errors are returned as plain text (via `http.Error`) with the appropriate HTTP status code.

Common HTTP status codes:

| Code | Description |
|------|-------------|
| 200 | Success |
| 400 | Bad request (invalid body) |
| 413 | Request body too large |
| 500 | Internal server error |
| 503 | No healthy node available |
