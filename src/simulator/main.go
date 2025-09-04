package main

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"simulator/internal/config"
	"simulator/internal/csvreader"
	"simulator/internal/generator"
	"simulator/internal/httpclient"
	"simulator/internal/metrics"
)

func main() {
	log.Println("Starting Ticket Simulator Service...")

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	// Print configuration for debugging
	cfgJSON, _ := json.MarshalIndent(cfg, "", "  ")
	log.Printf("Configuration loaded:\n%s", cfgJSON)

	// Initialize CSV reader
	csvReader, err := csvreader.New(cfg.CSVFilePath)
	if err != nil {
		log.Fatalf("Failed to initialize CSV reader: %v", err)
	}

	// Initialize HTTP client
	httpClient := httpclient.New(cfg.HTTPClient)

	// Initialize metrics
	metricsCollector := metrics.New()

	// Initialize generator
	gen := generator.New(cfg, csvReader, httpClient, metricsCollector)

	// Create context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start metrics reporting
	go metricsCollector.StartReporting(ctx, cfg.Metrics.ReportInterval)

	// Start the generator
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := gen.Start(ctx); err != nil {
			log.Printf("Generator error: %v", err)
		}
	}()

	// Wait for interrupt signal
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)

	log.Println("Simulator service started. Press Ctrl+C to stop...")
	<-c

	log.Println("Shutting down simulator service...")
	cancel()

	// Wait for all goroutines to finish with timeout
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		log.Println("All goroutines finished")
	case <-time.After(30 * time.Second):
		log.Println("Timeout waiting for goroutines to finish")
	}

	log.Println("Simulator service stopped")
}
