package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"plinth/internal/api"
	"plinth/internal/balancer"
	"plinth/internal/config"
	"plinth/internal/health"
	"plinth/internal/log"
	"plinth/internal/metrics"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	configPath := flag.String("config", "config/gateway.toml", "path to config file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	log.Info("starting gateway", "cluster", cfg.Cluster.Name)
	log.Info("config loaded", "nodes", fmt.Sprintf("%d", len(cfg.Nodes)), "models", fmt.Sprintf("%d", len(cfg.Models.Available)))

	var standalone []string
	ringMembers := make(map[string][]string)
	for _, n := range cfg.Nodes {
		if n.Ring == "" {
			standalone = append(standalone, n.Name)
		} else {
			ringMembers[n.Ring] = append(ringMembers[n.Ring], n.Name)
		}
	}
	if len(standalone) > 0 {
		log.Info("standalone nodes serving non-ring models", "nodes", strings.Join(standalone, ","))
	}
	for ring, members := range ringMembers {
		log.Info("ring nodes", "ring", ring, "nodes", strings.Join(members, ","))
	}
	for _, m := range cfg.Models.Available {
		if m.Ring != "" {
			log.Info("model routed to ring", "model", m.Name, "ring", m.Ring)
		}
	}

	mon := health.NewMonitor(cfg)
	bal := balancer.New()
	handler := api.NewHandler(cfg, mon, bal)

	mon.Start()
	defer mon.Stop()

	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			states := mon.GetNodeStates()
			var healthy, degraded, dead int
			for _, s := range states {
				switch s.Status {
				case health.Healthy:
					healthy++
				case health.Degraded:
					degraded++
				case health.Dead:
					dead++
				}
			}
			metrics.NodesHealthy.Set(float64(healthy))
			metrics.NodesDegraded.Set(float64(degraded))
			metrics.NodesDead.Set(float64(dead))
		}
	}()

	apiServer := &http.Server{
		Addr:    cfg.Gateway.Listen,
		Handler: handler,
	}

	metricsMux := http.NewServeMux()
	metricsMux.Handle("/metrics", promhttp.Handler())
	metricsServer := &http.Server{
		Addr:    cfg.Gateway.MetricsListen,
		Handler: metricsMux,
	}

	// NOTE: Config changes require a restart. SIGHUP is not handled because the
	// monitor, handler, and servers do not support runtime config propagation.

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		<-sig
		log.Info("shutting down")
		cancel()
	}()

	serveErr := make(chan error, 2)
	go func() {
		log.Info("api server listening", "addr", cfg.Gateway.Listen)
		serveErr <- apiServer.ListenAndServe()
	}()
	go func() {
		log.Info("metrics server listening", "addr", cfg.Gateway.MetricsListen)
		serveErr <- metricsServer.ListenAndServe()
	}()

	var serveFailed error
	select {
	case <-ctx.Done():
	case err := <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("server error", "error", err.Error())
			serveFailed = err
			cancel()
		}
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := apiServer.Shutdown(shutdownCtx); err != nil {
		log.Error("api server shutdown error", "error", err.Error())
	}
	if err := metricsServer.Shutdown(shutdownCtx); err != nil {
		log.Error("metrics server shutdown error", "error", err.Error())
	}
	log.Info("gateway stopped")

	return serveFailed
}
