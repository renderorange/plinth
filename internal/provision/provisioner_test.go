package provision

import (
	"context"
	"fmt"
	"testing"

	"plinth/internal/config"
)

type mockProvisioner struct {
	name        string
	description string
	called      bool
	fail        bool
}

func (m *mockProvisioner) Name() string        { return m.name }
func (m *mockProvisioner) Description() string { return m.description }
func (m *mockProvisioner) Provision(ctx context.Context, node config.NodeConfig, ssh *SSHClient) error {
	m.called = true
	if m.fail {
		return fmt.Errorf("mock error")
	}
	return nil
}

func TestRegistryAddAndList(t *testing.T) {
	reg := NewRegistry()
	p1 := &mockProvisioner{name: "step1", description: "Step 1"}
	p2 := &mockProvisioner{name: "step2", description: "Step 2"}

	reg.Add(p1)
	reg.Add(p2)

	provisioners := reg.List()
	if len(provisioners) != 2 {
		t.Errorf("List() = %d, want 2", len(provisioners))
	}
	if provisioners[0].Name() != "step1" {
		t.Errorf("provisioners[0].Name() = %q, want step1", provisioners[0].Name())
	}
}

func TestRegistryListIsolation(t *testing.T) {
	reg := NewRegistry()
	reg.Add(&mockProvisioner{name: "step-1"})
	reg.Add(&mockProvisioner{name: "step-2"})

	list := reg.List()
	list[0] = &mockProvisioner{name: "intruder"}
	list = append(list, &mockProvisioner{name: "extra"})

	if reg.Len() != 2 {
		t.Errorf("Len() = %d after mutating List() result, want 2", reg.Len())
	}
	if reg.provisioners[0].Name() != "step-1" {
		t.Errorf("provisioners[0] = %q after mutating List() result, want step-1", reg.provisioners[0].Name())
	}
}

func TestRegistryRunAll(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		reg := NewRegistry()
		p1 := &mockProvisioner{name: "step1", description: "Step 1"}
		p2 := &mockProvisioner{name: "step2", description: "Step 2"}

		reg.Add(p1)
		reg.Add(p2)

		if reg.Len() != 2 {
			t.Errorf("Len() = %d, want 2", reg.Len())
		}

		node := config.NodeConfig{Name: "test-node"}
		err := reg.RunAll(context.Background(), node, nil)
		if err != nil {
			t.Errorf("RunAll() error = %v, want nil", err)
		}
		if !p1.called {
			t.Error("p1.Provision() not called")
		}
		if !p2.called {
			t.Error("p2.Provision() not called")
		}
	})

	t.Run("error short-circuits", func(t *testing.T) {
		reg := NewRegistry()
		p1 := &mockProvisioner{name: "step1", description: "Step 1", fail: true}
		p2 := &mockProvisioner{name: "step2", description: "Step 2"}

		reg.Add(p1)
		reg.Add(p2)

		node := config.NodeConfig{Name: "test-node"}
		err := reg.RunAll(context.Background(), node, nil)
		if err == nil {
			t.Error("RunAll() error = nil, want error")
		}
		if !p1.called {
			t.Error("p1.Provision() not called")
		}
		if p2.called {
			t.Error("p2.Provision() called after p1 failure")
		}
	})
}
