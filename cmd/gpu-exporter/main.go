package main

import (
	"fmt"
	"log"
	"os"

	"plinth/internal/gpumetrics"
)

func main() {
	addr := ":9100"
	if v := os.Getenv("METRICS_PORT"); v != "" {
		addr = ":" + v
	}

	nvmlCollector, err := gpumetrics.NewNVMLCollector()
	if err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
	defer nvmlCollector.Close()

	exporter := gpumetrics.NewExporter(nvmlCollector)
	log.Printf("gpu-exporter listening on %s", addr)
	if err := exporter.ListenAndServe(addr); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}
