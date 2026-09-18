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
# Build both binaries
make all

# Or build individually
make build-gateway
make build-gpu-exporter

# Or use go directly
go build -o gateway ./cmd/gateway
go build -o gpu-exporter ./cmd/gpu-exporter
```

The Makefile also provides: `test`, `vet`, `fmt`, `cover`, `clean`, and versioned build targets (`build-gateway-versioned`, `build-gpu-exporter-versioned`) that inject git commit hash and build time.

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
| `[[nodes]]` | `ring` | Ring this node belongs to (empty for standalone) | — |
| `[models]` | `default` | Default model name | — |
| `[[models.available]]` | `name` | Model identifier | — |
| `[[models.available]]` | `pipeline_stages` | Informational metadata only | `1` |
| `[[models.available]]` | `ring` | Ring whose nodes serve this model (empty for standalone) | — |

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

## CI/CD

GitHub Actions workflows in `.github/workflows/`:

- **PR checks** (`pr.yml`) — Runs `go vet`, tests with race detector, and builds gateway + gpu-exporter on linux/amd64 and linux/arm64
- **Release** (`release.yml`) — On merge to main: runs tests, builds versioned binaries, creates a GitHub release with commit hash version, and attaches all binaries

## Project Structure

```
plinth/
├── cmd/
│   ├── gateway/         # Gateway binary
│   └── gpu-exporter/    # GPU metrics exporter
├── config/              # Configuration files
├── docs/                # Detailed documentation
├── .github/workflows/   # CI/CD (PR checks + release)
├── internal/
│   ├── api/             # HTTP handlers + reverse proxy
│   ├── balancer/        # Round-robin load balancer
│   ├── config/          # TOML config loader
│   ├── gpumetrics/      # NVML collector + Prometheus exporter
│   ├── health/          # Node health monitoring
│   ├── log/             # Structured logging
│   ├── metrics/         # Prometheus gateway metrics
│   └── version/         # Build version info
├── Makefile             # Build, test, and utility targets
└── scripts/             # Deployment scripts
```

## Limitations

- **No runtime config reload** — Config changes require a restart. SIGHUP is not supported because the monitor, handler, and servers do not support runtime config propagation.
- **No per-node model awareness** — Routing uses ring membership from config: ring models round-robin among that ring's nodes, all other models round-robin among ring-less nodes. The gateway does not verify which models each node actually serves.
- **No streaming support** — The proxy buffers full responses; SSE streaming is not yet implemented.
- **Multi-GPU support** — The gpu-exporter now collects metrics from all NVIDIA GPUs on a node, exposing them with UUID-based labels in Prometheus format.
- **No retry on proxy failure** — If a selected node fails mid-request, the request is not retried on another healthy node.

## License and Copyright

`plinth` is Copyright (c) 2026 Blaine Motsinger under the MIT license.
