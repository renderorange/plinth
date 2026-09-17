# plinth

A distributed VRAM gateway for load-balancing LLM inference across GPU nodes running vLLM.

## Overview

Plinth sits in front of multiple vLLM instances and provides:

- **Load balancing** — Round-robin request distribution across healthy GPU nodes
- **Health monitoring** — Continuous health checks with configurable failure thresholds
- **GPU metrics collection** — Scrapes NVIDIA GPU metrics (memory, utilization, temperature) via NVML
- **Prometheus metrics** — Exposes request latency, counts, and node health gauges
- **OpenAI-compatible API** — Proxies `/v1/chat/completions` and `/v1/completions` requests

## Architecture

```
┌─────────────────┐
│   Client (LLM)  │
└────────┬────────┘
         │
┌────────▼────────┐
│     Gateway     │  ← Load balancer + health monitor
│    (plinth)     │
└────────┬────────┘
         │
    ┌────┴────┐
    │         │
┌───▼───┐ ┌──▼────┐
│ Node1 │ │ Node2 │   ← vLLM + gpu-exporter
│ :8000 │ │ :8000 │
│ :9100 │ │ :9100 │
└───────┘ └───────┘
```

Each GPU node runs:
- **vLLM** — LLM inference server (default port 8000)
- **gpu-exporter** — Prometheus exporter for NVIDIA GPU metrics (default port 9100)

## Quick Start

### Prerequisites

- Go 1.22+
- NVIDIA GPU drivers + NVML (for gpu-exporter)
- vLLM instances running on GPU nodes

### Build

```bash
# Build gateway
go build -o gateway ./cmd/gateway

# Build gpu-exporter
go build -o gpu-exporter ./cmd/gpu-exporter
```

### Configure

```bash
cp config/gateway.toml.example config/gateway.toml
# Edit config/gateway.toml with your cluster settings
```

### Run

```bash
# Start gateway
./gateway -config config/gateway.toml

# Start gpu-exporter on each GPU node
./gpu-exporter
```

## Configuration

See [config/gateway.toml.example](config/gateway.toml.example) for a complete example.

| Section | Key | Description | Default |
|---------|-----|-------------|---------|
| `[cluster]` | `name` | Cluster identifier | — |
| `[gateway]` | `listen` | API listen address | `:8000` |
| `[gateway]` | `metrics_listen` | Prometheus metrics address | `:9090` |
| `[gateway]` | `health_interval` | Health check interval | `3s` |
| `[gateway]` | `health_fail_threshold` | Failures before marking dead | `3` |
| `[[nodes]]` | `ip` | Node IP address | — |
| `[[nodes]]` | `name` | Node display name | — |
| `[[nodes]]` | `vllm_port` | vLLM API port | `8000` |
| `[[nodes]]` | `metrics_port` | gpu-exporter port | `9100` |
| `[models]` | `default` | Default model name | — |
| `[[models.available]]` | `name` | Model identifier | — |
| `[[models.available]]` | `pipeline_stages` | Pipeline parallelism stages | `1` |

## API Endpoints

### Gateway (port 8000)

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/health` | Cluster health status |
| `GET` | `/v1/models` | List available models |
| `POST` | `/v1/chat/completions` | Chat completion (proxied to vLLM) |
| `POST` | `/v1/completions` | Text completion (proxied to vLLM) |

### Metrics (port 9090)

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/metrics` | Prometheus metrics |

### gpu-exporter (port 9100)

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/metrics` | GPU metrics (memory, utilization, temperature) |

## Health States

| State | Description |
|-------|-------------|
| `Healthy` | Node responding to health checks |
| `Degraded` | Node failing health checks (below threshold) |
| `Dead` | Node exceeded failure threshold |

The gateway prefers healthy nodes. If none are available, it falls back to degraded nodes. Dead nodes are excluded from load balancing.

## Deployment

Deployment scripts are provided in `scripts/`:

- `provision-node.sh` — Set up a GPU node (vLLM + gpu-exporter)
- `setup-gateway.sh` — Set up the gateway node
- `setup-keepalived.sh` — Configure keepalived for HA gateway
- `vllm.service` — systemd unit for vLLM
- `gpu-exporter.service` — systemd unit for gpu-exporter
- `keepalived-primary.conf` / `keepalived-secondary.conf` — keepalived configs

## Project Structure

```
plinth/
├── cmd/
│   ├── gateway/         # Gateway binary
│   └── gpu-exporter/    # GPU metrics exporter
├── config/              # Configuration files
├── internal/
│   ├── api/             # HTTP handlers + reverse proxy
│   ├── balancer/        # Round-robin load balancer
│   ├── config/          # TOML config loader
│   ├── gpumetrics/      # NVML collector + Prometheus exporter
│   ├── health/          # Node health monitoring
│   └── log/             # Structured logging
└── scripts/             # Deployment scripts
```

## Limitations

- **No runtime config reload** — Config changes require a restart. SIGHUP is not supported because the monitor, handler, and servers do not support runtime config propagation.
- **No model-based routing** — All nodes are assumed to serve all models. The balancer selects based on health, not model availability.
- **No streaming support** — The proxy buffers full responses; SSE streaming is not yet implemented.
- **Single-GPU metrics** — The gpu-exporter collects only the first GPU (device index 0); multi-GPU nodes are not fully reported.
- **No retry on proxy failure** — If a selected node fails mid-request, the request is not retried on another healthy node.

## License and Copyright

`plinth` is Copyright (c) 2026 Blaine Motsinger under the MIT license.
