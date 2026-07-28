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

	https := runtimeConfig.HTTPS
	server := &http.Server{
		Addr:              runtimeConfig.Outbound.ListenAddress,
		Handler:           runtime.Handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	errCh := make(chan error, 2)
	serve(server, https, errCh)
	if https != nil {
		log.Printf("jasmin-go-httpapi listening on %s (TLS)", runtimeConfig.Outbound.ListenAddress)
	} else {
		log.Printf("jasmin-go-httpapi listening on %s", runtimeConfig.Outbound.ListenAddress)
	}

	// The admin web UI, when configured, runs on its own listener so it is not
	// exposed on the public sendsms port; it shares the TLS cert if HTTPS is set.
	var webServer *http.Server
	if runtime.WebListenAddress != "" {
		webServer = &http.Server{
			Addr:              runtime.WebListenAddress,
			Handler:           runtime.WebHandler,
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       30 * time.Second,
			WriteTimeout:      30 * time.Second,
			IdleTimeout:       60 * time.Second,
		}
		serve(webServer, https, errCh)
		log.Printf("jasmin-go-httpapi admin UI listening on %s", runtime.WebListenAddress)
	}

	select {
	case <-lifetime.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		err := server.Shutdown(shutdownContext)
		if err != nil {
			err = fmt.Errorf("graceful HTTP shutdown: %w", err)
		}
		if webServer != nil {
			if webErr := webServer.Shutdown(shutdownContext); webErr != nil && err == nil {
				err = fmt.Errorf("graceful admin UI shutdown: %w", webErr)
			}
		}
		return err
	case err := <-errCh:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	}
}

// serve starts an HTTP server in a goroutine, using TLS when https is set, and
// reports its terminal error on errCh.
func serve(server *http.Server, https *gateway.HTTPSConfig, errCh chan<- error) {
	go func() {
		if https != nil {
			errCh <- server.ListenAndServeTLS(https.CertFile, https.KeyFile)
		} else {
			errCh <- server.ListenAndServe()
		}
	}()
}
