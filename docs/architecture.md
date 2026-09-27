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

1. Reads NVIDIA GPU metrics via NVML (all GPUs, UUID-labeled)
2. Exposes metrics in Prometheus format (default `:9100`)
3. Provides a `/health` endpoint for liveness checks

### plinth-provision (`cmd/provision/`)

A CLI that runs on the controller to set up GPU nodes over SSH:

1. `provision` — Runs the provisioner pipeline per node: drivers, python, vllm, user, models
2. `weights pull` — Downloads model weights to the controller
3. `weights push` — rsyncs weights to named nodes, or to all nodes serving a model when `--all` is used

Connections verify the node's host key against `[provision] ssh_host_key` before running anything. Systemd service installation is handled by `scripts/provision-node.sh`, not by this CLI.

## Internal Packages

### `internal/api/`

HTTP handlers and reverse proxy:

- `handler.go` — Routes requests to `/health`, `/v1/models`, `/v1/chat/completions`, `/v1/completions`; filters candidate nodes by ring membership before balancing
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
- `gateway_model_discovery_total` — Counter for model discovery fetch results (`node`, `result`)
- `gateway_model_filter_exclusions_total` — Counter for nodes dropped from the candidate pool by the model filter (`model`, `reason`)
- `gateway_node_models_ok` — Gauge for per-node model discovery settled flag (`node`)
- `gateway_proxy_attempts_total` — Counter for proxy attempts by model and observed status
- `gateway_request_duration_seconds` — Histogram for request latency
- `gateway_requests_total` — Counter for requests by model and status
- `gateway_response_client_write_failures_total` — Counter for committed responses whose write to the client failed, labeled by model
- `gateway_response_passthrough_total` — Counter for responses committed early and passed through to the client, labeled by model and reason (`size_limit`, `content_length`)
- `health_check_panics_total` — Counter for health probe panics recovered by the monitor

### `internal/provision/`

Node provisioning support for `plinth-provision`:

- `ssh.go` — SSH client with key auth, fingerprint-pinned host key verification, and context-cancelable command execution
- `provisioner.go` — Provisioner interface and registry; `RunAll` executes steps in order and short-circuits on the first error
- `drivers.go`, `python.go`, `vllm.go`, `user.go` — Package install steps run over SSH
- `models.go` — Picks the model whose ring matches the node's ring and pushes it
- `weights.go` — Weight manager: `huggingface-cli` downloads into `weights_dir`, atomic on completion; rsync push to nodes

### `internal/version/`

Build version information:

- Stores version, commit hash, and build time
- Injected via `-ldflags` at build time
- Exposed via `--version` flag on all binaries

## Request Flow

```
Client → Gateway → Balancer.Select() → Health Monitor
                ↓
         Selected Node (vLLM)
                ↓
         Response → Client
```

1. Client sends request to gateway
2. Gateway extracts the model from the request body (applying the configured default when omitted) and picks the routing pool: the model's ring nodes, or the ring-less nodes for non-ring models. Candidate nodes are then filtered by `ModelDiscovery.Allows` (exact model id match from each node's `/v1/models`); listing runs in the monitor discovery loop and never affects health status.
3. Balancer selects a node within that pool based on health state
4. Gateway reverse-proxies request to selected vLLM instance
5. Response is buffered up to max_buffered_response_bytes and returned to client; oversized responses are committed early and passed through (no SSE on this path)
6. Request duration and status are recorded in metrics

## Retry and Failover

Each proxy attempt is buffered in memory and committed to the client exactly once, only when an attempt succeeds or the buffered response exceeds `max_buffered_response_bytes`. Connection-level failures are classified in two tiers and retried on the next node in the pool:

- **Tier 1** — Dial failures (connection refused, no route to host). The request provably never reached vLLM, so retrying is safe.
- **Tier 2** — Timeouts before a response arrives, including the response-header timeout. Retrying carries a small duplicate-generation risk, since the first node may already have started generating.

Buffered responses are also a commit gate. While an attempt stays under `max_buffered_response_bytes` nothing reaches the client, so a connection failure can retry the next node. If the response exceeds that limit, the buffered prefix is committed to the client and the remainder is passed through; from that point the attempt is not retried and an upstream failure produces a truncated body rather than a 502.

Nodes with failed attempts are skipped for 10 seconds (offline skip-set), and each failure triggers `Monitor.Recheck` so the node's health is re-checked immediately.

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
