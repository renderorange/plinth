package provision

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"plinth/internal/config"
	"plinth/internal/log"
)

type Provisioner interface {
	Name() string
	Description() string
	Provision(ctx context.Context, node config.NodeConfig, ssh *SSHClient) error
}

type Registry struct {
	provisioners []Provisioner
}

func NewRegistry() *Registry {
	return &Registry{}
}

func (r *Registry) Add(p Provisioner) {
	r.provisioners = append(r.provisioners, p)
}

func (r *Registry) List() []Provisioner {
	return append([]Provisioner(nil), r.provisioners...)
}

func (r *Registry) Len() int {
	return len(r.provisioners)
}

func (r *Registry) RunAll(ctx context.Context, node config.NodeConfig, ssh *SSHClient) error {
	for i, p := range r.provisioners {
		log.Info("provisioning step", "step", fmt.Sprintf("%d/%d", i+1, len(r.provisioners)), "name", p.Name(), "node", node.Name)

		if err := p.Provision(ctx, node, ssh); err != nil {
			return fmt.Errorf("provisioner %q failed: %w", p.Name(), err)
		}

		log.Info("provisioning step complete", "name", p.Name(), "node", node.Name)
	}
	return nil
}

// provisionSystemService uploads a systemd service file and enables/restarts the service.
// Each step is idempotent: re-running after a partial failure is safe because PutFile uses
// atomic writes (temp file + mv) and systemctl enable/restart succeed on already-enabled
// services. However, provision is not atomic — a failure mid-way leaves the node partially
// provisioned (e.g., binary uploaded but service not enabled). Callers should re-run the
// full provision pipeline to recover from partial failures.
func provisionSystemService(ctx context.Context, ssh *SSHClient, serviceFilesDir, serviceName string) error {
	localPath := filepath.Join(serviceFilesDir, serviceName+".service")
	content, err := os.ReadFile(localPath)
	if err != nil {
		return fmt.Errorf("reading %s: %w", serviceName+".service", err)
	}

	remotePath := "/etc/systemd/system/" + serviceName + ".service"
	if err := ssh.PutFile(ctx, content, remotePath, 0644); err != nil {
		return fmt.Errorf("uploading %s: %w", serviceName+".service", err)
	}

	commands := []string{
		"systemctl daemon-reload",
		"systemctl enable " + serviceName,
		"systemctl restart " + serviceName,
	}
	for _, cmd := range commands {
		if _, err := ssh.Run(ctx, cmd); err != nil {
			return fmt.Errorf("%s: %w", serviceName, err)
		}
	}
	return nil
}
