package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"distributed-vram/internal/api"
	"distributed-vram/internal/balancer"
	"distributed-vram/internal/config"
	"distributed-vram/internal/health"
	"distributed-vram/internal/log"
	"distributed-vram/internal/metrics"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func main() {
	configPath := flag.String("config", "config/gateway.toml", "path to config file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading config: %v\n", err)
		os.Exit(1)
	}

	log.Info("starting gateway", "cluster", cfg.Cluster.Name)
	log.Info("config loaded", "nodes", fmt.Sprintf("%d", len(cfg.Nodes)), "models", fmt.Sprintf("%d", len(cfg.Models.Available)))

	mon := health.NewMonitor(cfg)
	bal := balancer.New()
	handler := api.NewHandler(cfg, mon, bal)

	mon.Start()

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

	go func() {
		log.Info("api server listening", "addr", cfg.Gateway.Listen)
		if err := apiServer.ListenAndServe(); err != http.ErrServerClosed {
			log.Error("api server error", "error", err.Error())
		}
	}()

	go func() {
		log.Info("metrics server listening", "addr", cfg.Gateway.MetricsListen)
		if err := metricsServer.ListenAndServe(); err != http.ErrServerClosed {
			log.Error("metrics server error", "error", err.Error())
		}
	}()

	<-ctx.Done()
	mon.Stop()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	apiServer.Shutdown(shutdownCtx)
	metricsServer.Shutdown(shutdownCtx)
	log.Info("gateway stopped")
}
