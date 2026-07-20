package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/pumpitspace/jasmin/internal/app/outbound"
)

func main() {
	if err := run(); err != nil {
		log.Printf("jasmin-go-httpapi: %v", err)
		os.Exit(1)
	}
}

func run() error {
	configPath := flag.String("config", "", "path to outbound runtime JSON configuration")
	checkConfig := flag.Bool("check-config", false, "validate configuration and exit")
	flag.Parse()

	config, err := outbound.LoadConfig(*configPath)
	if err != nil {
		return err
	}
	if err := outbound.ValidateConfig(config); err != nil {
		return err
	}
	if *checkConfig {
		fmt.Println("configuration: ok")
		return nil
	}

	lifetime, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	runtime, err := outbound.NewRuntime(lifetime, config)
	if err != nil {
		return err
	}
	defer runtime.Close()

	server := &http.Server{
		Addr:              config.ListenAddress,
		Handler:           runtime.Handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.ListenAndServe()
	}()
	log.Printf("jasmin-go-httpapi listening on %s", config.ListenAddress)

	select {
	case <-lifetime.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownContext); err != nil {
			return fmt.Errorf("graceful HTTP shutdown: %w", err)
		}
		return nil
	case err := <-errCh:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	}
}
