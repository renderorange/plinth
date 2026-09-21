# Configuration Reference

Plinth uses TOML for configuration. The config file path is passed via the `-config` flag (default: `config/gateway.toml`). The same file is shared by the gateway and `plinth-provision`.

## Example Configuration

```toml
[cluster]
name = "production-cluster"

[gateway]
listen = ":8000"
metrics_listen = ":9090"
health_interval = "3s"
health_fail_threshold = 3

# Standalone nodes — each independently serves non-ring models
[[nodes]]
ip = "192.168.1.100"
name = "gpu-node-1"
vllm_port = 8000
metrics_port = 9100

[[nodes]]
ip = "192.168.1.101"
name = "gpu-node-2"
vllm_port = 8000
metrics_port = 9100

# Ring nodes — each independently serves the ring model
[[nodes]]
ip = "192.168.1.102"
name = "gpu-node-3"
ring = "ring-a"

[[nodes]]
ip = "192.168.1.103"
name = "gpu-node-4"
ring = "ring-a"

[models]
default = "Qwen/Qwen2.5-7B-Instruct"

# Ring model — routed round-robin among the nodes in ring-a
[[models.available]]
name = "Qwen/Qwen2.5-32B-Instruct-Q4"
pipeline_stages = 2 # informational metadata; not enforced
ring = "ring-a"

# Standalone model — routed round-robin among nodes without a ring
[[models.available]]
name = "Qwen/Qwen2.5-7B-Instruct"
pipeline_stages = 1

[provision]
weights_dir = "/var/lib/plinth/weights"
ssh_key = "~/.ssh/id_rsa"
# ssh_user = "root"
# ssh_port = 22
# ssh_host_key = "SHA256:..." # required for provision/weights push
```

## Sections

### `[cluster]`

Cluster-level settings.

| Key | Type | Required | Description |
|-----|------|----------|-------------|
| `name` | string | Yes | Cluster identifier used in logs |

### `[gateway]`

Gateway server settings.

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `listen` | string | No | `:8000` | Address for the API server |
| `metrics_listen` | string | No | `:9090` | Address for the Prometheus metrics server |
| `health_interval` | string | No | `3s` | Interval between health checks (Go duration format) |
| `health_fail_threshold` | int | No | `3` | Consecutive failures before marking a node as dead |

#### Duration Format

`health_interval` accepts Go duration strings:

- `"3s"` — 3 seconds
- `"500ms"` — 500 milliseconds
- `"1m30s"` — 1 minute 30 seconds

### `[[nodes]]`

GPU node definitions. Add one `[[nodes]]` section per node.

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `ip` | string | Yes | — | Node IP address |
| `name` | string | Yes | — | Human-readable node name (used in logs) |
| `vllm_port` | int | No | `8000` | Port where vLLM listens |
| `metrics_port` | int | No | `9100` | Port where gpu-exporter listens |
| `ring` | string | No | — | Ring this node belongs to. Ring nodes serve only ring models; all other models round-robin among nodes without a ring |

### `[models]`

Model configuration.

| Key | Type | Required | Description |
|-----|------|----------|-------------|
| `default` | string | No | Default model name |

### `[[models.available]]`

Available models. Add one `[[models.available]]` section per model.

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `name` | string | Yes | — | Model identifier (matches vLLM model name) |
| `pipeline_stages` | int | No | `1` | Informational metadata only; not enforced by the gateway |
| `ring` | string | No | — | Ring whose nodes serve this model. Requests round-robin among that ring's nodes. Empty routes to nodes without a ring |

### `[provision]`

Settings used by `plinth-provision` for SSH access and model weight transfer.

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `weights_dir` | string | No | `/var/lib/plinth/weights` | Local directory where model weights are downloaded (`weights pull`) and read from (`weights push`, `provision`) |
| `ssh_key` | string | No | `~/.ssh/id_rsa` | Path to the SSH private key for node access |
| `ssh_user` | string | No | `root` | SSH login user |
| `ssh_port` | int | No | `22` | SSH port (must be 1–65535 when set) |
| `ssh_host_key` | string | No | — | `SHA256` fingerprint of the node's host key, verified on every connection. **Required** for `provision` and `weights push`. Obtain with `ssh-keyscan -t ed25519 <node-ip>` |

Notes:

- `~` at the start of `weights_dir` and `ssh_key` is expanded to the current user's home directory at load time.
- Connections are refused unless the node's presented host key matches `ssh_host_key`; there is no opt-out.
- If `[provision]` is omitted, the defaults below are applied at load; the gateway ignores them, and `plinth-provision` refuses SSH connections until `ssh_host_key` is set.

## Environment Variables

The gpu-exporter supports one environment variable:

| Variable | Description | Default |
|----------|-------------|---------|
| `METRICS_PORT` | Override the metrics listen port | `9100` |

## Config Reload

Send `SIGHUP` to reload the config file without restarting the gateway:

- The whole config is re-read, validated, and swapped atomically. In-flight requests finish against the previous config; new requests use the new one immediately.
- If the new config fails to parse or validate, the gateway keeps serving the current config and logs the error. Retry once the file is fixed.
- Nodes present in both configs keep their health status and failure counts. Newly added nodes, and existing nodes whose IP changed, start healthy.
- Removed nodes stop receiving health checks; requests already routed to them complete normally.
- Changes to `gateway.listen` or `gateway.metrics_listen` are logged as warnings and left running on the old addresses — listeners bind at startup, so address changes require a restart.
- A SIGHUP received while a reload is in progress is not processed concurrently: reloads always run sequentially, and each delivered SIGHUP triggers a fresh re-read and swap of the config file once the in-flight reload finishes.

## Validation

The gateway validates at startup:

- Config file exists and is parseable TOML
- `health_interval` is a valid, positive Go duration (defaults to `3s` when omitted)
- `health_fail_threshold` is greater than zero (defaults to `3` when omitted)
- At least one node is configured
- `ssh_port` is between 1 and 65535 when set (defaults to `22`)
- Every model ring references a ring that at least one node belongs to
- Every model without a ring has at least one ring-less node to serve it

Unset values fall back to their defaults. Individual nodes and models are not otherwise validated; an unreachable node is marked degraded/dead by the health monitor at runtime.

## Routing

The model name in a request (or `models.default` when omitted) picks the routing pool:

- Ring model → round-robin among healthy nodes in that ring
- Any other model → round-robin among healthy nodes without a ring

When a request omits `model`, the default model name is injected into the forwarded body so the backend receives a complete payload. `pipeline_stages` is informational metadata and is not used for routing or validation.
