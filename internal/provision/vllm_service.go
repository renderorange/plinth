package provision

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

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
	localPath := filepath.Join(p.serviceFilesDir, "vllm.service")
	content, err := os.ReadFile(localPath)
	if err != nil {
		return fmt.Errorf("reading vllm.service: %w", err)
	}

	remotePath := "/etc/systemd/system/vllm.service"
	if err := ssh.PutFile(ctx, content, remotePath, 0644); err != nil {
		return fmt.Errorf("uploading vllm.service: %w", err)
	}

	commands := []string{
		"systemctl daemon-reload",
		"systemctl enable vllm",
		"systemctl restart vllm",
	}
	for _, cmd := range commands {
		if _, err := ssh.Run(ctx, cmd); err != nil {
			return fmt.Errorf("vllm-service: %w", err)
		}
	}
	return nil
}
