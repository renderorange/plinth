package provision

import (
	"context"
	"fmt"

	"plinth/internal/config"
)

type VLLMProvisioner struct{}

func (p *VLLMProvisioner) Name() string        { return "vllm" }
func (p *VLLMProvisioner) Description() string { return "Create venv, install vLLM" }

func (p *VLLMProvisioner) Provision(ctx context.Context, node config.NodeConfig, ssh *SSHClient) error {
	commands := []string{
		"mkdir -p /opt/vllm",
		"python3 -m venv /opt/vllm/.venv",
		". /opt/vllm/.venv/bin/activate && python3 -m pip install --upgrade pip",
		". /opt/vllm/.venv/bin/activate && python3 -m pip install vllm",
	}
	for _, cmd := range commands {
		if _, err := ssh.Run(ctx, cmd); err != nil {
			return fmt.Errorf("vllm: %w", err)
		}
	}
	return nil
}
