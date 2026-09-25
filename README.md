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
- **gpu-exporter** — Per-GPU Prometheus metrics (memory, utilization, temperature) for all NVIDIA GPUs, UUID-labeled (default port 9100)

## Quick Start

### Prerequisites

- Go 1.22+
- NVIDIA GPU drivers + NVML (for gpu-exporter)
- vLLM instances running on GPU nodes

### Build

```bash
# Build all binaries (gateway, gpu-exporter, plinth-provision)
make all

# Or build individually
make build-gateway
make build-gpu-exporter
make build-provision

# Or use go directly
go build -o gateway ./cmd/gateway
go build -o gpu-exporter ./cmd/gpu-exporter
go build -o plinth-provision ./cmd/provision
```

The Makefile also provides: `test`, `vet`, `fmt`, `cover`, `clean`, and versioned build targets (`build-gateway-versioned`, `build-gpu-exporter-versioned`, `build-provision-versioned`) that inject git commit hash and build time.

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

### Provision Nodes

Set `[provision] ssh_host_key` in `config/gateway.toml` first (the `SHA256` fingerprint printed by `ssh-keyscan -t ed25519 <node-ip>`). Then:

```bash
# Provision a specific node
./plinth-provision -config config/gateway.toml provision gpu-node-1

# Provision all nodes
./plinth-provision -config config/gateway.toml provision --all

# Download model weights to controller
./plinth-provision -config config/gateway.toml weights pull Qwen/Qwen2.5-7B-Instruct

# Push weights to specific nodes
./plinth-provision -config config/gateway.toml weights push Qwen/Qwen2.5-7B-Instruct gpu-node-1

# Push weights to all nodes serving that model
./plinth-provision -config config/gateway.toml weights push Qwen/Qwen2.5-7B-Instruct --all
```

Node provisioning installs drivers, Python, vLLM, the service user, and model weights. Run `weights pull <model>` before `provision` so the models step has local weights to push. `weights push --all` requires the model to be listed in `[[models.available]]`. Systemd service installation is handled separately by `scripts/provision-node.sh`.

## Configuration

See [config/gateway.toml.example](config/gateway.toml.example) for a complete example.

| Section | Key | Description | Default |
|---------|-----|-------------|---------|
| `[cluster]` | `name` | Cluster identifier | — |
| `[gateway]` | `listen` | API listen address | `:8000` |
| `[gateway]` | `metrics_listen` | Prometheus metrics address | `:9090` |
| `[gateway]` | `health_interval` | Health check interval | `3s` |
| `[gateway]` | `health_fail_threshold` | Failures before marking dead | `3` |
| `[gateway]` | `max_buffered_response_bytes` | Max buffered non-streaming response bytes | `8388608` |
| `[[nodes]]` | `ip` | Node IP address | — |
| `[[nodes]]` | `name` | Node display name | — |
| `[[nodes]]` | `vllm_port` | vLLM API port | `8000` |
| `[[nodes]]` | `metrics_port` | gpu-exporter port | `9100` |
| `[[nodes]]` | `ring` | Ring this node belongs to (empty for standalone) | — |
| `[models]` | `default` | Default model name | — |
| `[[models.available]]` | `name` | Model identifier | — |
| `[[models.available]]` | `pipeline_stages` | Informational metadata only | `1` |
| `[[models.available]]` | `ring` | Ring whose nodes serve this model (empty for standalone) | — |
| `[provision]` | `weights_dir` | Local path for model weights | `/var/lib/plinth/weights` |
| `[provision]` | `ssh_key` | Path to SSH private key | `~/.ssh/id_rsa` |
| `[provision]` | `ssh_user` | SSH login user | `root` |
| `[provision]` | `ssh_port` | SSH port | `22` |
| `[provision]` | `ssh_host_key` | Node host key fingerprint (required for provision/weights push) | — |

Node IPs must be unique — the gateway identifies nodes by IP.

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
- **Release** (`release.yml`) — On merge to main: runs tests, builds versioned gateway + gpu-exporter binaries, creates a GitHub release with commit hash version, and attaches the built binaries

## Project Structure

```
plinth/
├── cmd/
│   ├── gateway/         # Gateway binary
│   ├── gpu-exporter/    # GPU metrics exporter
│   └── provision/       # Node provisioning + model weights CLI
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
│   ├── provision/       # SSH client, provisioners, weight manager
│   └── version/         # Build version info
├── Makefile             # Build, test, and utility targets
└── scripts/             # Deployment scripts
```

## Limitations

- **Config reload** — `SIGHUP` reloads nodes, models, rings, and health settings at runtime. In-flight requests finish against the old config; health state carries over for unchanged nodes; an invalid config is rejected with the current config kept. Listen address changes require a restart.
- **No per-node model awareness** — Routing uses ring membership from config: ring models round-robin among that ring's nodes, all other models round-robin among ring-less nodes. The gateway does not verify which models each node actually serves.
- **Streaming** — `stream: true` completion requests are streamed chunk-by-chunk to the client. Retry is allowed until the first chunk is flushed; once committed, an upstream failure terminates the stream (clean EOF, no fabricated `[DONE]`, never a 502 after commit). Stalled streams without a first chunk are abandoned and retried after a fixed 10s deadline. Non-streaming requests are buffered up to max_buffered_response_bytes (default 8 MiB) so the attempt stays retryable; larger responses are committed early and passed through to the client.
- **Connection-level retry only** — Requests whose proxy attempt fails to establish a connection (or times out before any response) are retried on other nodes in the pool. HTTP errors from vLLM are never retried (completions are not idempotent), and errors after response headers start return 502 unless the response has already been committed. Retrying on a *response-header timeout* carries a small duplicate-generation risk. For streaming requests, failures before the first chunk reach the client are retried; failures after commit terminate the stream instead of returning 502. Once a response is committed (buffered complete, or oversized and passed through), an upstream failure yields a truncated body and never a 502.
- **Provisioning** — `plinth provision` installs drivers, Python, vLLM, the service user, and model weights, but not systemd services; service setup remains in `scripts/provision-node.sh`.

## License and Copyright

`plinth` is Copyright (c) 2026 Blaine Motsinger under the MIT license.
