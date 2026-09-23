package provision

import (
	"bytes"
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
	"plinth/internal/config"
)

func TestGPUExporterServiceProvisionerName(t *testing.T) {
	p := NewGPUExporterServiceProvisioner("/tmp/gpu-exporter", "/tmp")
	if p.Name() != "gpu-exporter-service" {
		t.Errorf("Name() = %q, want %q", p.Name(), "gpu-exporter-service")
	}
	if p.Description() == "" {
		t.Error("Description() is empty")
	}
}

func TestGPUExporterServiceProvisionerProvision(t *testing.T) {
	dir := t.TempDir()

	binContent := []byte("fake-binary")
	binPath := filepath.Join(dir, "gpu-exporter")
	if err := os.WriteFile(binPath, binContent, 0755); err != nil {
		t.Fatalf("writing fake binary: %v", err)
	}

	serviceContent := []byte("[Unit]\nDescription=test")
	if err := os.WriteFile(filepath.Join(dir, "gpu-exporter.service"), serviceContent, 0644); err != nil {
		t.Fatalf("writing service file: %v", err)
	}

	addr, hostKey, cleanup, commands, contents := startRecordingMockSSHServer(t)
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

	p := NewGPUExporterServiceProvisioner(binPath, dir)
	node := config.NodeConfig{Name: "test-node", IP: host}
	if err := p.Provision(context.Background(), node, client); err != nil {
		t.Fatalf("Provision() = %v, want nil", err)
	}

	cmds := commands()
	expected := []string{
		"id -u vram-exporter",
		"usermod -aG video vram-exporter",
		"/usr/local/bin/gpu-exporter.tmp",
		"/etc/systemd/system/gpu-exporter.service.tmp",
		"systemctl daemon-reload",
		"systemctl enable gpu-exporter",
		"systemctl restart gpu-exporter",
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

	fileContents := contents()
	var foundBinContent, foundServiceContent bool
	for _, c := range fileContents {
		if bytes.Contains(c, binContent) {
			foundBinContent = true
		}
		if bytes.Contains(c, serviceContent) {
			foundServiceContent = true
		}
	}
	if !foundBinContent {
		t.Errorf("binary content %q not found in transferred data", binContent)
	}
	if !foundServiceContent {
		t.Errorf("service content %q not found in transferred data", serviceContent)
	}
}

func TestGPUExporterServiceProvisionerMissingBinary(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "gpu-exporter.service"), []byte("[Unit]"), 0644); err != nil {
		t.Fatal(err)
	}

	p := NewGPUExporterServiceProvisioner("/nonexistent/gpu-exporter", dir)
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
		t.Fatal("Provision() with missing binary = nil, want error")
	}
	if !strings.Contains(err.Error(), "gpu-exporter") {
		t.Errorf("error = %q, want mention of gpu-exporter", err)
	}
}
