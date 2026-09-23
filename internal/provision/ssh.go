package provision

import (
	"context"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"plinth/internal/config"
)

type SSHConfig struct {
	KeyPath string
	User    string
	Timeout time.Duration
	Port    int
	HostKey string
}

func SSHConfigFrom(cfg config.ProvisionConfig) SSHConfig {
	return SSHConfig{
		KeyPath: cfg.SSHKeyPath,
		User:    cfg.SSHUser,
		Port:    cfg.SSHPort,
		HostKey: cfg.SSHHostKey,
	}
}

func (c SSHConfig) withDefaults() SSHConfig {
	if c.User == "" {
		c.User = "root"
	}
	if c.Port == 0 {
		c.Port = 22
	}
	if c.Timeout == 0 {
		c.Timeout = 30 * time.Second
	}
	return c
}

type SSHClient struct {
	client *ssh.Client
	config SSHConfig
}

func NewSSHClient(host string, cfg SSHConfig) (*SSHClient, error) {
	cfg = cfg.withDefaults()

	expected, err := normalizeHostKeyFingerprint(cfg.HostKey)
	if err != nil {
		return nil, err
	}
	if expected == "" {
		return nil, fmt.Errorf("SSH host key fingerprint required; set [provision] ssh_host_key to the value printed by 'ssh-keyscan -t ed25519 %s'", host)
	}

	key, err := os.ReadFile(cfg.KeyPath)
	if err != nil {
		return nil, fmt.Errorf("reading SSH key: %w", err)
	}

	signer, err := ssh.ParsePrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("parsing SSH key: %w", err)
	}

	sshCfg := &ssh.ClientConfig{
		User: cfg.User,
		Auth: []ssh.AuthMethod{
			ssh.PublicKeys(signer),
		},
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			got := ssh.FingerprintSHA256(key)
			if !strings.EqualFold(got, expected) {
				return fmt.Errorf("host key fingerprint mismatch: got %s, want %s", got, expected)
			}
			return nil
		},
		Timeout: cfg.Timeout,
	}

	addr := net.JoinHostPort(host, strconv.Itoa(cfg.Port))
	client, err := ssh.Dial("tcp", addr, sshCfg)
	if err != nil {
		return nil, fmt.Errorf("SSH dial: %w", err)
	}

	return &SSHClient{
		client: client,
		config: cfg,
	}, nil
}

func normalizeHostKeyFingerprint(fp string) (string, error) {
	fp = strings.TrimSpace(fp)
	if fp == "" {
		return "", nil
	}
	if len(fp) >= 7 && strings.EqualFold(fp[:7], "SHA256:") {
		fp = fp[7:]
	}
	fp = strings.TrimRight(fp, "=")
	if fp == "" {
		return "", fmt.Errorf("invalid ssh_host_key: expected base64 after SHA256: prefix")
	}
	return "SHA256:" + strings.ToUpper(fp), nil
}

func (c *SSHClient) Run(ctx context.Context, command string) (string, error) {
	session, err := c.client.NewSession()
	if err != nil {
		return "", fmt.Errorf("creating session: %w", err)
	}
	defer session.Close()

	done := make(chan struct{})
	var output []byte
	var runErr error

	go func() {
		output, runErr = session.CombinedOutput(command)
		close(done)
	}()

	select {
	case <-ctx.Done():
		session.Signal(ssh.SIGKILL)
		<-done
		return "", ctx.Err()
	case <-done:
		if runErr != nil {
			return string(output), fmt.Errorf("running command: %w", runErr)
		}
		return string(output), nil
	}
}

func (c *SSHClient) Close() error {
	return c.client.Close()
}

func (c *SSHClient) PutFile(ctx context.Context, content []byte, remotePath string, perm os.FileMode) error {
	session, err := c.client.NewSession()
	if err != nil {
		return fmt.Errorf("creating session: %w", err)
	}
	defer session.Close()

	stdin, err := session.StdinPipe()
	if err != nil {
		return fmt.Errorf("opening stdin pipe: %w", err)
	}

	cmd := fmt.Sprintf("cat > %s", remotePath)
	if err := session.Start(cmd); err != nil {
		return fmt.Errorf("starting command: %w", err)
	}

	if _, err := stdin.Write(content); err != nil {
		return fmt.Errorf("writing content: %w", err)
	}
	if err := stdin.Close(); err != nil {
		return fmt.Errorf("closing stdin: %w", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- session.Wait()
	}()

	select {
	case <-ctx.Done():
		session.Signal(ssh.SIGKILL)
		<-done
		return ctx.Err()
	case err := <-done:
		if err != nil {
			return fmt.Errorf("putting file: %w", err)
		}
	}

	chmodCmd := fmt.Sprintf("chmod %04o %s", perm, remotePath)
	if _, err := c.Run(ctx, chmodCmd); err != nil {
		return fmt.Errorf("setting permissions: %w", err)
	}

	return nil
}
