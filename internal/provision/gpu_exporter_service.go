package provision

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"plinth/internal/config"
)

type GPUExporterServiceProvisioner struct {
	gpuExporterBin  string
	serviceFilesDir string
}

func NewGPUExporterServiceProvisioner(gpuExporterBin string, serviceFilesDir string) *GPUExporterServiceProvisioner {
	return &GPUExporterServiceProvisioner{
		gpuExporterBin:  gpuExporterBin,
		serviceFilesDir: serviceFilesDir,
	}
}

func (p *GPUExporterServiceProvisioner) Name() string { return "gpu-exporter-service" }
func (p *GPUExporterServiceProvisioner) Description() string {
	return "Install gpu-exporter binary, create user, enable service"
}

func (p *GPUExporterServiceProvisioner) Provision(ctx context.Context, node config.NodeConfig, ssh *SSHClient) error {
	// Create vram-exporter user
	userCommands := []string{
		"id -u vram-exporter &>/dev/null || useradd -r -s /bin/false vram-exporter",
		"usermod -aG video vram-exporter",
	}
	for _, cmd := range userCommands {
		if _, err := ssh.Run(ctx, cmd); err != nil {
			return fmt.Errorf("gpu-exporter-service user setup: %w", err)
		}
	}

	// Upload binary
	binContent, err := os.ReadFile(p.gpuExporterBin)
	if err != nil {
		return fmt.Errorf("reading gpu-exporter binary: %w", err)
	}
	if err := ssh.PutFile(ctx, binContent, "/usr/local/bin/gpu-exporter", 0755); err != nil {
		return fmt.Errorf("uploading gpu-exporter binary: %w", err)
	}

	// Upload service file
	serviceContent, err := os.ReadFile(filepath.Join(p.serviceFilesDir, "gpu-exporter.service"))
	if err != nil {
		return fmt.Errorf("reading gpu-exporter.service: %w", err)
	}
	if err := ssh.PutFile(ctx, serviceContent, "/etc/systemd/system/gpu-exporter.service", 0644); err != nil {
		return fmt.Errorf("uploading gpu-exporter.service: %w", err)
	}

	// Enable and start
	commands := []string{
		"systemctl daemon-reload",
		"systemctl enable gpu-exporter",
		"systemctl restart gpu-exporter",
	}
	for _, cmd := range commands {
		if _, err := ssh.Run(ctx, cmd); err != nil {
			return fmt.Errorf("gpu-exporter-service: %w", err)
		}
	}
	return nil
}
