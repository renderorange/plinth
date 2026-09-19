# Provision Command

## Overview

The `plinth provision` command sets up GPU nodes for running vLLM inference. It connects via SSH and runs a series of provisioners to install drivers, Python, vLLM, and copy model weights. Systemd service installation is handled separately by `scripts/provision-node.sh`.

## Commands

### `plinth provision`

Full node provisioning:

```bash
plinth provision <node-name> [<node-name>...]   # specific nodes
plinth provision --all                           # all configured nodes
```

### `plinth weights`

Model weight management:

```bash
plinth weights pull <model>                          # download to controller
plinth weights push <model> [<node-name>...]         # push to specific nodes
plinth weights push <model> --all                    # push to all nodes serving that model
```

## Provisioner Pipeline

Per node, executed sequentially:

1. **drivers** — Install NVIDIA drivers and CUDA toolkit
2. **python** — Install Python, pip, venv
3. **vllm** — Create venv, install vLLM
4. **user** — Create vllm service user, set permissions
5. **models** — rsync model weights from controller (ring models go to ring nodes, ring-less models to standalone nodes)

Service installation and verification are not yet part of the pipeline; use `scripts/provision-node.sh` for now.

## Configuration

Add to `config/gateway.toml`:

```toml
[provision]
weights_dir = "/var/lib/plinth/weights"
ssh_key = "~/.ssh/id_rsa"
# ssh_user = "root"
# ssh_port = 22
# ssh_host_key = "SHA256:..."   # required for provision/weights push; run ssh-keyscan -t ed25519 <node>
```

## Requirements

- SSH key-based access to nodes
- `rsync` installed on controller and nodes
- Nodes running Debian/Ubuntu with apt
- `ssh_host_key` set to each node's `SHA256` fingerprint (from `ssh-keyscan -t ed25519 <node>`) so hosts are verified
