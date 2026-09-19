package provision

import (
	"context"
	"fmt"

	"plinth/internal/config"
)

type PythonProvisioner struct{}

func (p *PythonProvisioner) Name() string        { return "python" }
func (p *PythonProvisioner) Description() string { return "Install Python, pip, venv" }

func (p *PythonProvisioner) Provision(ctx context.Context, node config.NodeConfig, ssh *SSHClient) error {
	commands := []string{
		"DEBIAN_FRONTEND=noninteractive apt-get install -y python3 python3-venv python3-pip",
	}
	for _, cmd := range commands {
		if _, err := ssh.Run(ctx, cmd); err != nil {
			return fmt.Errorf("python: %w", err)
		}
	}
	return nil
}
