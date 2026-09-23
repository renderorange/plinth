package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"plinth/internal/config"
	"plinth/internal/log"
	"plinth/internal/provision"
	"plinth/internal/version"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	showVersion := flag.Bool("version", false, "print version and exit")
	configPath := flag.String("config", "config/gateway.toml", "path to config file")
	flag.Parse()

	if *showVersion {
		fmt.Println(version.String())
		return nil
	}

	args := flag.Args()
	if len(args) < 1 {
		return fmt.Errorf("usage: plinth-provision <command> [options]\ncommands:\n  provision  Provision GPU nodes\n  weights    Manage model weights")
	}

	command := args[0]
	commandArgs := args[1:]

	switch command {
	case "provision":
		return runProvision(*configPath, commandArgs)
	case "weights":
		return runWeights(*configPath, commandArgs)
	default:
		return fmt.Errorf("unknown command: %s", command)
	}
}

func runProvision(configPath string, args []string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	var nodes []config.NodeConfig
	all := false

	for _, arg := range args {
		if arg == "--all" {
			all = true
			continue
		}
		node := findNode(cfg, arg)
		if node == nil {
			return fmt.Errorf("node %q not found in config", arg)
		}
		nodes = append(nodes, *node)
	}

	if all && len(nodes) > 0 {
		return fmt.Errorf("cannot combine --all with named nodes")
	}

	if all {
		nodes = cfg.Nodes
	}

	if len(nodes) == 0 {
		return fmt.Errorf("no nodes specified; use --all or specify node names")
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	sshCfg := provision.SSHConfigFrom(cfg.Provision)
	weightMgr := provision.NewWeightManager(cfg.Provision.WeightsDir)

	for _, node := range nodes {
		log.Info("provisioning node", "name", node.Name, "ip", node.IP)

		sshClient, err := provision.NewSSHClient(node.IP, sshCfg)
		if err != nil {
			return fmt.Errorf("SSH to %s: %w", node.IP, err)
		}

		registry := provision.NewRegistry()
		registry.Add(&provision.DriversProvisioner{})
		registry.Add(&provision.PythonProvisioner{})
		registry.Add(&provision.VLLMProvisioner{})
		registry.Add(&provision.UserProvisioner{})
		registry.Add(provision.NewModelWeightsProvisioner(weightMgr, sshCfg, cfg.Models.Available))
		registry.Add(provision.NewVLLMServiceProvisioner(cfg.Provision.ServiceFilesDir))
		registry.Add(provision.NewGPUExporterServiceProvisioner(cfg.Provision.GPUExporterBin, cfg.Provision.ServiceFilesDir))

		if err := registry.RunAll(ctx, node, sshClient); err != nil {
			sshClient.Close()
			return fmt.Errorf("provisioning %s: %w", node.Name, err)
		}

		sshClient.Close()
		log.Info("node provisioned", "name", node.Name)
	}

	log.Info("provisioning complete", "nodes", fmt.Sprintf("%d", len(nodes)))
	return nil
}

func pushTargets(cfg *config.Config, modelName string, all bool, nodeNames []string) ([]config.NodeConfig, error) {
	if all && len(nodeNames) > 0 {
		return nil, fmt.Errorf("cannot combine --all with named nodes")
	}

	if !all {
		var nodes []config.NodeConfig
		for _, name := range nodeNames {
			node := findNode(cfg, name)
			if node == nil {
				return nil, fmt.Errorf("node %q not found in config", name)
			}
			nodes = append(nodes, *node)
		}
		return nodes, nil
	}

	modelRing := ""
	found := false
	for _, m := range cfg.Models.Available {
		if m.Name == modelName {
			modelRing = m.Ring
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("model %q not found in config", modelName)
	}

	return cfg.NodesInRing(modelRing), nil
}

func runWeights(configPath string, args []string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	if len(args) < 2 {
		return fmt.Errorf("usage: plinth-provision weights <pull|push> <model> [nodes...]")
	}

	action := args[0]
	modelName := args[1]
	weightMgr := provision.NewWeightManager(cfg.Provision.WeightsDir)

	switch action {
	case "pull":
		return weightMgr.Pull(context.Background(), modelName)
	case "push":
		if len(args) < 3 {
			return fmt.Errorf("usage: plinth-provision weights push <model> [--all|node...]")
		}
		all := false
		var nodeNames []string
		for _, arg := range args[2:] {
			if arg == "--all" {
				all = true
				continue
			}
			nodeNames = append(nodeNames, arg)
		}

		nodes, err := pushTargets(cfg, modelName, all, nodeNames)
		if err != nil {
			return err
		}
		if len(nodes) == 0 {
			return fmt.Errorf("no nodes specified; use --all or specify node names")
		}

		sshCfg := provision.SSHConfigFrom(cfg.Provision)

		for _, node := range nodes {
			if err := weightMgr.Push(context.Background(), modelName, node, sshCfg); err != nil {
				return fmt.Errorf("pushing to %s: %w", node.Name, err)
			}
		}
		return nil
	default:
		return fmt.Errorf("unknown weights action: %s (use pull or push)", action)
	}
}

func findNode(cfg *config.Config, name string) *config.NodeConfig {
	for _, n := range cfg.Nodes {
		if n.Name == name {
			return &n
		}
	}
	return nil
}
