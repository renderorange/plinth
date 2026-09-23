package provision

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"plinth/internal/config"
)

func writeClientKey(t *testing.T) string {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generating client key: %v", err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatalf("marshalling client key: %v", err)
	}
	path := filepath.Join(t.TempDir(), "id_ed25519")
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0600); err != nil {
		t.Fatalf("writing client key: %v", err)
	}
	return path
}

func TestSSHConfigDefaults(t *testing.T) {
	got := (SSHConfig{}).withDefaults()
	if got.User != "root" {
		t.Errorf("default User = %q, want %q", got.User, "root")
	}
	if got.Port != 22 {
		t.Errorf("default Port = %d, want 22", got.Port)
	}
	if got.Timeout != 30*time.Second {
		t.Errorf("default Timeout = %v, want 30s", got.Timeout)
	}

	custom := (SSHConfig{User: "admin", Port: 2222, Timeout: time.Minute}).withDefaults()
	if custom.User != "admin" || custom.Port != 2222 || custom.Timeout != time.Minute {
		t.Errorf("withDefaults() overrode explicit values: %+v", custom)
	}
}

func TestNewSSHClientRequiresHostKey(t *testing.T) {
	_, err := NewSSHClient("example.com", SSHConfig{KeyPath: "/nonexistent/key"})
	if err == nil {
		t.Fatal("NewSSHClient() without host key fingerprint = nil, want error")
	}
	if !strings.Contains(err.Error(), "fingerprint") {
		t.Errorf("error = %q, want mention of required fingerprint", err)
	}
}

func TestNewSSHClientVerifiesFingerprint(t *testing.T) {
	addr, hostKey, cleanup := startMockSSHServer(t)
	defer cleanup()

	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("splitting mock addr: %v", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("parsing mock port: %v", err)
	}

	fingerprint := ssh.FingerprintSHA256(hostKey)
	client, err := NewSSHClient(host, SSHConfig{
		KeyPath: writeClientKey(t),
		User:    "test",
		Port:    port,
		HostKey: strings.ToLower(fingerprint),
	})
	if err != nil {
		t.Fatalf("NewSSHClient() = %v, want nil", err)
	}
	defer client.Close()

	out, err := client.Run(context.Background(), "echo hi")
	if err != nil {
		t.Fatalf("Run() = %v, want nil", err)
	}
	if !strings.Contains(out, "ok") {
		t.Errorf("Run() output = %q, want mock reply", out)
	}
}

func TestNormalizeHostKeyFingerprint(t *testing.T) {
	tests := []struct {
		input   string
		want    string
		wantErr bool
	}{
		{"", "", false},
		{"  ", "", false},
		{"SHA256:abcd1234EFGH==", "SHA256:ABCD1234EFGH", false},
		{"sha256:abcd1234EFGH==", "SHA256:ABCD1234EFGH", false},
		{"abcd1234EFGH", "SHA256:ABCD1234EFGH", false},
		{"SHA256:", "", true},
		{"SHA256:===", "", true},
		{"  SHA256:xyz  ", "SHA256:XYZ", false},
	}
	for _, tt := range tests {
		got, err := normalizeHostKeyFingerprint(tt.input)
		if (err != nil) != tt.wantErr {
			t.Errorf("normalizeHostKeyFingerprint(%q) err = %v, wantErr %v", tt.input, err, tt.wantErr)
			continue
		}
		if got != tt.want {
			t.Errorf("normalizeHostKeyFingerprint(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestNewSSHClientRejectsWrongFingerprint(t *testing.T) {
	addr, hostKey, cleanup := startMockSSHServer(t)
	defer cleanup()

	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("splitting mock addr: %v", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("parsing mock port: %v", err)
	}

	_ = ssh.FingerprintSHA256(hostKey)
	client, err := NewSSHClient(host, SSHConfig{
		KeyPath: writeClientKey(t),
		User:    "test",
		Port:    port,
		HostKey: "SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
	})
	if err == nil {
		client.Close()
		t.Fatal("NewSSHClient() with wrong fingerprint = nil, want error")
	}
	if !strings.Contains(err.Error(), "mismatch") {
		t.Errorf("error = %q, want mention of fingerprint mismatch", err)
	}
}

func TestSSHClientPutFile(t *testing.T) {
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
		t.Fatalf("NewSSHClient() = %v", err)
	}
	defer client.Close()

	content := []byte("hello world\n")
	err = client.PutFile(context.Background(), content, "/tmp/test.txt", 0644)
	if err != nil {
		t.Fatalf("PutFile() = %v, want nil", err)
	}

	cmds := commands()
	var foundCat, foundChmod bool
	for _, cmd := range cmds {
		if strings.Contains(cmd, "cat > /tmp/test.txt") {
			foundCat = true
		}
		if strings.Contains(cmd, "chmod 0644 /tmp/test.txt") {
			foundChmod = true
		}
	}
	if !foundCat {
		t.Errorf("PutFile did not execute cat command; commands = %v", cmds)
	}
	if !foundChmod {
		t.Errorf("PutFile did not execute chmod command; commands = %v", cmds)
	}
}

func TestSSHConfigFrom(t *testing.T) {
	cfg := config.ProvisionConfig{
		SSHKeyPath: "/home/test/.ssh/id_ed25519",
		SSHUser:    "ubuntu",
		SSHPort:    2222,
		SSHHostKey: "SHA256:ABCD1234",
	}
	got := SSHConfigFrom(cfg)
	if got.KeyPath != cfg.SSHKeyPath {
		t.Errorf("KeyPath = %q, want %q", got.KeyPath, cfg.SSHKeyPath)
	}
	if got.User != cfg.SSHUser {
		t.Errorf("User = %q, want %q", got.User, cfg.SSHUser)
	}
	if got.Port != cfg.SSHPort {
		t.Errorf("Port = %d, want %d", got.Port, cfg.SSHPort)
	}
	if got.HostKey != cfg.SSHHostKey {
		t.Errorf("HostKey = %q, want %q", got.HostKey, cfg.SSHHostKey)
	}
	if got.Timeout != 0 {
		t.Errorf("Timeout = %v, want 0 (withDefaults applies on connect)", got.Timeout)
	}
}
