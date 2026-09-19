package provision

import (
	"context"
	"net"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
	"plinth/internal/config"
)

func TestProvisionerCommandsUseNoninteractiveFrontend(t *testing.T) {
	addr, hostKey, cleanup, commands := startRecordingMockSSHServer(t)
	defer cleanup()

	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("splitting mock addr: %v", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("parsing mock port: %v", err)
	}

	client, err := NewSSHClient(host, SSHConfig{
		KeyPath: writeClientKey(t),
		User:    "test",
		Port:    port,
		HostKey: ssh.FingerprintSHA256(hostKey),
	})
	if err != nil {
		t.Fatalf("connecting to mock server: %v", err)
	}
	defer client.Close()

	registry := NewRegistry()
	registry.Add(&DriversProvisioner{})
	registry.Add(&PythonProvisioner{})

	node := config.NodeConfig{Name: "test-node", IP: host}
	if err := registry.RunAll(context.Background(), node, client); err != nil {
		t.Fatalf("RunAll() = %v, want nil", err)
	}

	cmds := commands()
	aptCount := 0
	for _, c := range cmds {
		if c == "export DEBIAN_FRONTEND=noninteractive" {
			t.Error("standalone export command does not affect subsequent SSH sessions")
		}
		if strings.Contains(c, "apt-get install") {
			aptCount++
			if !strings.HasPrefix(c, "DEBIAN_FRONTEND=noninteractive ") {
				t.Errorf("apt-get command missing inline DEBIAN_FRONTEND: %q", c)
			}
			if !strings.Contains(c, "-y") {
				t.Errorf("apt-get command missing -y: %q", c)
			}
		}
	}
	if aptCount != 2 {
		t.Errorf("got %d apt-get install commands, want 2 (drivers + python)", aptCount)
	}
}

func TestFullProvisionPipeline(t *testing.T) {
	registry := NewRegistry()

	registry.Add(&DriversProvisioner{})
	registry.Add(&PythonProvisioner{})
	registry.Add(&VLLMProvisioner{})
	registry.Add(&UserProvisioner{})
	registry.Add(&ModelWeightsProvisioner{})

	if registry.Len() != 5 {
		t.Errorf("registry.Len() = %d, want 5", registry.Len())
	}

	names := make([]string, 0)
	for _, p := range registry.List() {
		names = append(names, p.Name())
	}

	expected := []string{"drivers", "python", "vllm", "user", "models"}
	if len(names) != len(expected) {
		t.Errorf("provisioner count = %d, want %d", len(names), len(expected))
	}
	for i, name := range names {
		if name != expected[i] {
			t.Errorf("provisioner[%d].Name() = %q, want %q", i, name, expected[i])
		}
	}
}

func TestFullProvisionPipelineExecution(t *testing.T) {
	addr, _, cleanup := startMockSSHServer(t)
	defer cleanup()

	host, _, _ := net.SplitHostPort(addr)

	netConn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	sshConn, chans, reqs, err := ssh.NewClientConn(netConn, addr, &ssh.ClientConfig{
		User:            "test",
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	})
	if err != nil {
		t.Fatalf("ssh connect: %v", err)
	}
	client := &SSHClient{
		client: ssh.NewClient(sshConn, chans, reqs),
		config: SSHConfig{User: "test"},
	}
	defer client.Close()

	registry := NewRegistry()
	registry.Add(&DriversProvisioner{})
	registry.Add(&PythonProvisioner{})
	registry.Add(&VLLMProvisioner{})
	registry.Add(&UserProvisioner{})

	node := config.NodeConfig{
		Name: "test-node",
		IP:   host,
	}

	ctx := context.Background()
	err = registry.RunAll(ctx, node, client)
	if err != nil {
		t.Errorf("RunAll() error = %v, want nil", err)
	}
}

func TestFullProvisionPipelineExecutionError(t *testing.T) {
	addr, _, cleanup := startMockSSHServer(t)
	defer cleanup()

	host, _, _ := net.SplitHostPort(addr)

	netConn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	sshConn, chans, reqs, err := ssh.NewClientConn(netConn, addr, &ssh.ClientConfig{
		User:            "test",
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	})
	if err != nil {
		t.Fatalf("ssh connect: %v", err)
	}
	client := &SSHClient{
		client: ssh.NewClient(sshConn, chans, reqs),
		config: SSHConfig{User: "test"},
	}
	defer client.Close()

	registry := NewRegistry()
	registry.Add(&mockProvisioner{name: "step1", fail: false})
	registry.Add(&mockProvisioner{name: "step2", fail: true})
	registry.Add(&mockProvisioner{name: "step3", fail: false})

	node := config.NodeConfig{Name: "test-node", IP: host}
	err = registry.RunAll(context.Background(), node, client)
	if err == nil {
		t.Error("RunAll() error = nil, want error")
	}

	if !strings.Contains(err.Error(), "step2") {
		t.Errorf("error = %q, want mention of failing provisioner step2", err)
	}
}
