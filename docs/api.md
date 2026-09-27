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
|------|------|-------------|
| `status` | string | `ok` / `degraded` / `error` — health only (unchanged) |
| `nodes` | int | node count (unchanged) |
| `healthy` | int | healthy count (unchanged) |
| `models_ok` | int | nodes whose model discovery is `known` or `known_empty` |
| `details` | array | per-node status + `models` object |
| `details[].models.state` | string | `untried` / `known` / `known_empty` / `degraded` / `expired` |
| `details[].models.names` | array\|null | last-known-good model ids |
| `details[].models.age_seconds` | number\|null | seconds since last successful fetch |
| `details[].models.fails` | int | consecutive listing failures |
| `details[].models.last_error` | string | last listing error text |

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
| 502 | Connection to the node lost after the response started (response truncated) |
| 503 | `no healthy node available` (pool empty) or `no reachable node available` (all proxy attempts failed) |

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
| 502 | Connection to the node lost after the response started (response truncated) |
| 503 | `no healthy node available` (pool empty) or `no reachable node available` (all proxy attempts failed) |

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
| `gateway_proxy_attempts_total` | Counter | Proxy attempts by model and observed status |
| `gateway_response_client_write_failures_total` | Counter | Committed responses whose write to the client failed |
| `gateway_response_passthrough_total` | Counter | Responses committed early and passed through to the client |
| `gateway_model_filter_exclusions_total` | Counter | Nodes dropped from the candidate pool by the model filter (`model`, `reason`) |
| `gateway_node_models_ok` | Gauge | Per-node model discovery settled flag (`node`) |
| `gateway_model_discovery_total` | Counter | Model discovery fetch results (`node`, `result`) |
| `health_check_panics_total` | Counter | Total health probe panics recovered by the monitor |

**Labels for `gateway_requests_total`:**

- `model` — Model name from request
- `status` — HTTP status code (e.g., `"200"`, `"503"`)

**Labels for `gateway_proxy_attempts_total`:**

- `model` — Model name from request
- `status` — HTTP status code observed on the attempt

**Labels for `gateway_response_client_write_failures_total`:**

- `model` — Model name from request

**Labels for `gateway_response_passthrough_total`:**

- `model` — Model name from request
- `reason` — Why the response was passed through instead of buffered: `size_limit` (buffered response exceeded `max_buffered_response_bytes`) or `content_length` (upstream `Content-Length` already exceeded the limit)

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
| 502 | Connection to the node lost after the response started (response truncated) |
| 503 | No healthy node available (pool empty) or no reachable node available (all proxy attempts failed) |
