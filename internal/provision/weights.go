package provision

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"plinth/internal/config"
	"plinth/internal/log"
)

type WeightManager struct {
	weightsDir string
}

func NewWeightManager(weightsDir string) *WeightManager {
	return &WeightManager{weightsDir: weightsDir}
}

func (wm *WeightManager) ModelPath(modelName string) string {
	return filepath.Join(wm.weightsDir, modelName)
}

var downloadWeights = func(ctx context.Context, modelName, localDir string) error {
	cmd := exec.CommandContext(ctx, "huggingface-cli", "download", modelName, "--local-dir", localDir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func validateModelName(modelName string) error {
	if modelName == "" {
		return fmt.Errorf("invalid model name: empty")
	}
	if strings.HasPrefix(modelName, "/") || strings.HasSuffix(modelName, "/") {
		return fmt.Errorf("invalid model name %q: must be a relative path", modelName)
	}
	for _, elem := range strings.Split(modelName, "/") {
		if elem == "" || elem == "." || elem == ".." {
			return fmt.Errorf("invalid model name %q: path elements must not be empty, '.', or '..'", modelName)
		}
	}
	for _, r := range modelName {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') ||
			r == '.' || r == '_' || r == '-' || r == '/' {
			continue
		}
		return fmt.Errorf("invalid model name %q: character %q not allowed", modelName, r)
	}
	return nil
}

func (wm *WeightManager) Pull(ctx context.Context, modelName string) error {
	if err := validateModelName(modelName); err != nil {
		return err
	}

	modelPath := wm.ModelPath(modelName)

	if _, err := os.Stat(modelPath); err == nil {
		log.Info("weights already exist", "model", modelName, "path", modelPath)
		return nil
	}

	parentDir := filepath.Dir(modelPath)
	if err := os.MkdirAll(parentDir, 0755); err != nil {
		return fmt.Errorf("creating weights directory: %w", err)
	}

	tmpDir, err := os.MkdirTemp(parentDir, ".partial-*")
	if err != nil {
		return fmt.Errorf("creating download directory: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	log.Info("downloading weights", "model", modelName, "path", modelPath)

	if err := downloadWeights(ctx, modelName, tmpDir); err != nil {
		return fmt.Errorf("downloading weights: %w", err)
	}

	if err := os.Rename(tmpDir, modelPath); err != nil {
		if _, statErr := os.Stat(modelPath); statErr == nil {
			log.Info("weights already exist", "model", modelName, "path", modelPath)
			return nil
		}
		return fmt.Errorf("finalizing download: %w", err)
	}

	return nil
}

func (wm *WeightManager) Push(ctx context.Context, modelName string, node config.NodeConfig, sshCfg SSHConfig) error {
	if err := validateModelName(modelName); err != nil {
		return err
	}

	modelPath := wm.ModelPath(modelName)

	if _, err := os.Stat(modelPath); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("weights not found at %s; run 'plinth weights pull %s' first", modelPath, modelName)
		}
		return fmt.Errorf("checking weights: %w", err)
	}

	sshClient, err := NewSSHClient(node.IP, sshCfg)
	if err != nil {
		return fmt.Errorf("SSH connection: %w", err)
	}
	defer sshClient.Close()

	remotePath := fmt.Sprintf("/opt/models/%s", modelName)
	mkdirCmd := fmt.Sprintf("mkdir -p '%s'", remotePath)
	if _, err := sshClient.Run(ctx, mkdirCmd); err != nil {
		return fmt.Errorf("creating remote directory: %w", err)
	}

	sshArgs := fmt.Sprintf("ssh -i %s -l %s", sshCfg.KeyPath, sshCfg.User)
	rsyncArgs := []string{
		"-avz", "--progress",
		"-e", sshArgs,
		modelPath + "/",
		fmt.Sprintf("%s@%s:%s/", sshCfg.User, node.IP, remotePath),
	}
	log.Info("pushing weights", "model", modelName, "node", node.Name, "command", strings.Join(append([]string{"rsync"}, rsyncArgs...), " "))

	cmd := exec.CommandContext(ctx, "rsync", rsyncArgs...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("rsync failed: %w", err)
	}

	log.Info("weights pushed", "model", modelName, "node", node.Name)
	return nil
}
