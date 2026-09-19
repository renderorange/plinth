package provision

import (
	"context"
	"fmt"

	"plinth/internal/config"
)

type DriversProvisioner struct{}

func (p *DriversProvisioner) Name() string        { return "drivers" }
func (p *DriversProvisioner) Description() string { return "Install NVIDIA drivers and CUDA toolkit" }

func (p *DriversProvisioner) Provision(ctx context.Context, node config.NodeConfig, ssh *SSHClient) error {
	commands := []string{
		"apt-get update",
		"DEBIAN_FRONTEND=noninteractive apt-get install -y nvidia-driver nvidia-cuda-toolkit",
	}
	for _, cmd := range commands {
		if _, err := ssh.Run(ctx, cmd); err != nil {
			return fmt.Errorf("drivers: %w", err)
		}
	}
	return nil
}
