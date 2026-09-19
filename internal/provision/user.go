package provision

import (
	"context"
	"fmt"

	"plinth/internal/config"
)

type UserProvisioner struct{}

func (p *UserProvisioner) Name() string        { return "user" }
func (p *UserProvisioner) Description() string { return "Create vllm service user, set permissions" }

func (p *UserProvisioner) Provision(ctx context.Context, node config.NodeConfig, ssh *SSHClient) error {
	commands := []string{
		"groupadd -f video",
		"id -u vllm &>/dev/null || useradd -r -s /bin/false vllm",
		"usermod -aG video vllm",
		"chown -R vllm:vllm /opt/vllm",
	}
	for _, cmd := range commands {
		if _, err := ssh.Run(ctx, cmd); err != nil {
			return fmt.Errorf("user: %w", err)
		}
	}
	return nil
}
