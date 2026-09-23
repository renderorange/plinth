package provision

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
	"plinth/internal/config"
)

func TestVLLMServiceProvisionerName(t *testing.T) {
	p := NewVLLMServiceProvisioner("/tmp")
	if p.Name() != "vllm-service" {
		t.Errorf("Name() = %q, want %q", p.Name(), "vllm-service")
	}
	if p.Description() == "" {
		t.Error("Description() is empty")
	}
}

func TestVLLMServiceProvisionerProvision(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "vllm.service"), []byte("[Unit]\nDescription=test"), 0644); err != nil {
		t.Fatalf("writing service file: %v", err)
	}

	addr, hostKey, cleanup, commands := startRecordingMockSSHServer(t)
	defer cleanup()

	host, portStr, _ := net.SplitHostPort(addr)
	port := mustAtoi(t, portStr)

	client, err := NewSSHClient(host, SSHConfig{
		KeyPath: writeClientKey(t),
		User:    "test",
		Port:    port,
		HostKey: ssh.FingerprintSHA256(hostKey),
	})
	if err != nil {
		t.Fatalf("NewSSHClient() = %v", err)
	}
	defer client.Close()

	p := NewVLLMServiceProvisioner(dir)
	node := config.NodeConfig{Name: "test-node", IP: host}
	if err := p.Provision(context.Background(), node, client); err != nil {
		t.Fatalf("Provision() = %v, want nil", err)
	}

	cmds := commands()
	expected := []string{
		"cat > /etc/systemd/system/vllm.service",
		"systemctl daemon-reload",
		"systemctl enable vllm",
		"systemctl restart vllm",
	}
	for _, want := range expected {
		found := false
		for _, cmd := range cmds {
			if strings.Contains(cmd, want) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected command %q not found in %v", want, cmds)
		}
	}
}

func TestVLLMServiceProvisionerMissingServiceFile(t *testing.T) {
	p := NewVLLMServiceProvisioner("/nonexistent")
	node := config.NodeConfig{Name: "test-node", IP: "127.0.0.1"}

	addr, hostKey, cleanup := startMockSSHServer(t)
	defer cleanup()

	host, portStr, _ := net.SplitHostPort(addr)
	port := mustAtoi(t, portStr)

	client, err := NewSSHClient(host, SSHConfig{
		KeyPath: writeClientKey(t),
		User:    "test",
		Port:    port,
		HostKey: ssh.FingerprintSHA256(hostKey),
	})
	if err != nil {
		t.Fatalf("NewSSHClient() = %v", err)
	}
	defer client.Close()

	err = p.Provision(context.Background(), node, client)
	if err == nil {
		t.Fatal("Provision() with missing service file = nil, want error")
	}
	if !strings.Contains(err.Error(), "vllm.service") {
		t.Errorf("error = %q, want mention of vllm.service", err)
	}
}

func mustAtoi(t *testing.T, s string) int {
	t.Helper()
	var n int
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil {
		t.Fatalf("parsing %q: %v", s, err)
	}
	return n
}
