package provision

import (
	"context"
	"fmt"

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
