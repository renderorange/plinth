package provision

import (
	"context"

	"plinth/internal/config"
)

type VLLMServiceProvisioner struct {
	serviceFilesDir string
}

func NewVLLMServiceProvisioner(serviceFilesDir string) *VLLMServiceProvisioner {
	return &VLLMServiceProvisioner{serviceFilesDir: serviceFilesDir}
}

func (p *VLLMServiceProvisioner) Name() string        { return "vllm-service" }
func (p *VLLMServiceProvisioner) Description() string { return "Install and enable vllm.service" }

func (p *VLLMServiceProvisioner) Provision(ctx context.Context, node config.NodeConfig, ssh *SSHClient) error {
	return provisionSystemService(ctx, ssh, p.serviceFilesDir, "vllm")
}
