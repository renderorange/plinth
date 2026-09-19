package provision

import (
	"context"
	"fmt"

	"plinth/internal/config"
)

type ModelWeightsProvisioner struct {
	weightManager *WeightManager
	sshConfig     SSHConfig
	models        []config.ModelConfig
}

func NewModelWeightsProvisioner(wm *WeightManager, sshCfg SSHConfig, models []config.ModelConfig) *ModelWeightsProvisioner {
	return &ModelWeightsProvisioner{
		weightManager: wm,
		sshConfig:     sshCfg,
		models:        models,
	}
}

func (p *ModelWeightsProvisioner) Name() string        { return "models" }
func (p *ModelWeightsProvisioner) Description() string { return "rsync model weights from controller" }

func (p *ModelWeightsProvisioner) ModelForNode(node config.NodeConfig) (string, error) {
	for _, m := range p.models {
		if m.Ring == node.Ring {
			return m.Name, nil
		}
	}
	if node.Ring == "" {
		return "", fmt.Errorf("no model configured for standalone nodes")
	}
	return "", fmt.Errorf("no model configured for ring %q", node.Ring)
}

func (p *ModelWeightsProvisioner) Provision(ctx context.Context, node config.NodeConfig, ssh *SSHClient) error {
	modelName, err := p.ModelForNode(node)
	if err != nil {
		return err
	}

	if err := p.weightManager.Push(ctx, modelName, node, p.sshConfig); err != nil {
		return fmt.Errorf("pushing weights: %w", err)
	}

	return nil
}
