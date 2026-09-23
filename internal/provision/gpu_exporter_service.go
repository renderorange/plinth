package provision

import (
	"context"
	"fmt"
	"os"

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
	userCommands := []string{
		"id -u vram-exporter &>/dev/null || useradd -r -s /bin/false vram-exporter",
		"usermod -aG video vram-exporter",
	}
	for _, cmd := range userCommands {
		if _, err := ssh.Run(ctx, cmd); err != nil {
			return fmt.Errorf("gpu-exporter-service user setup: %w", err)
		}
	}

	binContent, err := os.ReadFile(p.gpuExporterBin)
	if err != nil {
		return fmt.Errorf("reading gpu-exporter binary: %w", err)
	}
	if err := ssh.PutFile(ctx, binContent, "/usr/local/bin/gpu-exporter", 0755); err != nil {
		return fmt.Errorf("uploading gpu-exporter binary: %w", err)
	}

	if err := provisionSystemService(ctx, ssh, p.serviceFilesDir, "gpu-exporter"); err != nil {
		return err
	}

	return nil
}
