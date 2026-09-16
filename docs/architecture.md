# Architecture

This document describes the high-level architecture of plinth.

## System Overview

Plinth is a distributed VRAM gateway that sits between LLM clients and GPU nodes running vLLM. It provides load balancing, health monitoring, and metrics collection.

## Components

### Gateway (`cmd/gateway/`)

The main gateway process that:

1. Listens for incoming API requests (default `:8000`)
2. Health-checks GPU nodes periodically
3. Load-balances requests across healthy nodes
4. Exposes Prometheus metrics (default `:9090`)

### gpu-exporter (`cmd/gpu-exporter/`)

A standalone process that runs on each GPU node and:

1. Reads NVIDIA GPU metrics via NVML
2. Exposes metrics in Prometheus format (default `:9100`)
3. Provides a `/health` endpoint for liveness checks

## Internal Packages

### `internal/api/`

HTTP handlers and reverse proxy:

- `handler.go` — Routes requests to `/health`, `/v1/models`, `/v1/chat/completions`, `/v1/completions`
- `proxy.go` — Reverse proxies requests to selected vLLM instances

### `internal/balancer/`

Round-robin load balancer:

- Maintains an atomic counter for fair distribution
- Prefers healthy nodes, falls back to degraded nodes
- Returns `ErrNoHealthyNode` when no nodes are available

### `internal/config/`

TOML configuration loader:

- Parses `gateway.toml` into typed structs
- Supports duration strings (e.g., `"3s"`, `"1m"`)

### `internal/gpumetrics/`

NVML collector and Prometheus exporter:

- `nvml.go` — NVML wrapper for reading GPU stats
- `collector.go` — Collects memory, utilization, temperature
- `exporter.go` — HTTP handler for `/metrics` endpoint

### `internal/health/`

Node health monitoring:

- Periodically pings each node's `/health` endpoint
- Scrapes GPU metrics from each node's gpu-exporter
- Tracks consecutive failures and computes health state

### `internal/log/`

Structured logging with key-value pairs.

### `internal/metrics/`

Prometheus metrics for the gateway:

- `cluster_nodes_healthy` — Gauge for healthy node count
- `cluster_nodes_degraded` — Gauge for degraded node count
- `cluster_nodes_dead` — Gauge for dead node count
- `gateway_request_duration_seconds` — Histogram for request latency
- `gateway_requests_total` — Counter for requests by model and status

## Request Flow

```
Client → Gateway → Balancer.Select() → Health Monitor
                ↓
         Selected Node (vLLM)
                ↓
         Response → Client
```

1. Client sends request to gateway
2. Gateway extracts model from request body
3. Balancer selects a node based on health state
4. Gateway reverse-proxies request to selected vLLM instance
5. Response is streamed back to client
6. Request duration and status are recorded in metrics

## Health States

| State | Description |
|-------|-------------|
| `Healthy` | Node responding to health checks |
| `Degraded` | Node failing health checks (below threshold) |
| `Dead` | Node exceeded failure threshold |

The balancer prefers healthy nodes. If none are available, it falls back to degraded nodes. Dead nodes are excluded from load balancing.

## Data Flow

```
┌─────────────┐     ┌─────────────┐     ┌─────────────┐
│   Client    │────▶│   Gateway   │────▶│  vLLM Node  │
└─────────────┘     └──────┬──────┘     └─────────────┘
                           │
                    ┌──────▼──────┐
                    │   Health    │◀──── gpu-exporter
                    │   Monitor   │      (NVML metrics)
                    └─────────────┘
```

## Metrics Collection

The gateway collects two types of metrics:

1. **Gateway metrics** — Request latency, counts, node health gauges (exposed on `:9090`)
2. **GPU metrics** — Memory, utilization, temperature from each node (scraped from `:9100`)

GPU metrics are scraped during health checks and stored in the node state. They are not re-exported by the gateway; use Prometheus to scrape both the gateway and gpu-exporters directly.
