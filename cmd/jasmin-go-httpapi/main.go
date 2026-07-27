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

	"github.com/pumpitspace/jasmin/internal/app/gateway"
	"github.com/pumpitspace/jasmin/internal/config"
)

func main() {
	if err := run(); err != nil {
		log.Printf("jasmin-go-httpapi: %v", err)
		os.Exit(1)
	}
}

func run() error {
	configPath := flag.String("config", "", "path to outbound runtime JSON configuration")
	jasminConfigPath := flag.String("jasmin-cfg", "", "optional jasmin.cfg supplying infrastructure settings (broker, redis, listeners, thrower/dlr/smpps policy); connectors and routes still come from --config")
	checkConfig := flag.Bool("check-config", false, "validate configuration and exit")
	flag.Parse()

	runtimeConfig, err := gateway.LoadConfig(*configPath)
	if err != nil {
		return err
	}
	// An opt-in jasmin.cfg overlays the infrastructure fields before validation;
	// a JSON-only run skips this entirely and is unaffected.
	if *jasminConfigPath != "" {
		jasminConfig, err := config.LoadJasmin(*jasminConfigPath)
		if err != nil {
			return err
		}
		gateway.ApplyJasmin(&runtimeConfig, jasminConfig)
	}
	if err := gateway.ValidateConfig(runtimeConfig); err != nil {
		return err
	}
	if *checkConfig {
		fmt.Println("configuration: ok")
		return nil
	}

	lifetime, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	runtime, err := gateway.NewRuntime(lifetime, runtimeConfig)
	if err != nil {
		return err
	}
	defer runtime.Close()

	server := &http.Server{
		Addr:              runtimeConfig.Outbound.ListenAddress,
		Handler:           runtime.Handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	errCh := make(chan error, 1)
	if https := runtimeConfig.HTTPS; https != nil {
		go func() {
			errCh <- server.ListenAndServeTLS(https.CertFile, https.KeyFile)
		}()
		log.Printf("jasmin-go-httpapi listening on %s (TLS)", runtimeConfig.Outbound.ListenAddress)
	} else {
		go func() {
			errCh <- server.ListenAndServe()
		}()
		log.Printf("jasmin-go-httpapi listening on %s", runtimeConfig.Outbound.ListenAddress)
	}

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
