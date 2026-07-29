package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
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
	var (
		standbyServer   *http.Server
		standbyListener net.Listener
	)
	if runtimeConfig.HA != nil && runtimeConfig.HA.StandbyListenAddress != "" {
		standbyServer, standbyListener, err = newStandbyHealthServer(runtimeConfig.HA.StandbyListenAddress)
		if err != nil {
			return err
		}
		defer standbyServer.Close()
		go func() {
			if serveErr := standbyServer.Serve(standbyListener); serveErr != nil && serveErr != http.ErrServerClosed {
				log.Printf("jasmin-go-httpapi standby health: %v", serveErr)
			}
		}()
		log.Printf("jasmin-go-httpapi waiting for leadership; standby health listening on %s",
			runtimeConfig.HA.StandbyListenAddress)
	}
	runtime, err := gateway.NewRuntime(lifetime, runtimeConfig)
	if err != nil {
		return err
	}
	defer runtime.Close()
	if standbyServer != nil {
		shutdownContext, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = standbyServer.Shutdown(shutdownContext)
		cancel()
	}

	https := runtimeConfig.HTTPS
	server := &http.Server{
		Addr:              runtimeConfig.Outbound.ListenAddress,
		Handler:           runtime.Handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	errCh := make(chan error, 4)
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
	// The admin REST API (/admin/) gets the same treatment when
	// admin.api_listen_address is set. Otherwise it stays on the public sendsms
	// mux, which the runtime warns about at startup.
	var adminAPIServer *http.Server
	if runtime.AdminAPIListenAddress != "" {
		adminAPIServer = &http.Server{
			Addr:              runtime.AdminAPIListenAddress,
			Handler:           runtime.AdminAPIHandler,
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       30 * time.Second,
			WriteTimeout:      30 * time.Second,
			IdleTimeout:       60 * time.Second,
		}
		serve(adminAPIServer, https, errCh)
		log.Printf("jasmin-go-httpapi admin API listening on %s", runtime.AdminAPIListenAddress)
	}
	var pbServer *http.Server
	if runtime.PBListenAddress != "" {
		pbServer = &http.Server{
			Addr:              runtime.PBListenAddress,
			Handler:           runtime.PBHandler,
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       30 * time.Second,
			WriteTimeout:      30 * time.Second,
			IdleTimeout:       60 * time.Second,
		}
		serve(pbServer, https, errCh)
		log.Printf("jasmin-go-httpapi PB compatibility facade listening on %s", runtime.PBListenAddress)
	}
	var restServer *http.Server
	if runtime.RESTListenAddress != "" {
		restServer = &http.Server{
			Addr:              runtime.RESTListenAddress,
			Handler:           runtime.RESTHandler,
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       30 * time.Second,
			WriteTimeout:      30 * time.Second,
			IdleTimeout:       60 * time.Second,
		}
		serve(restServer, https, errCh)
		log.Printf("jasmin-go-httpapi REST compatibility daemon listening on %s", runtime.RESTListenAddress)
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
		if adminAPIServer != nil {
			if apiErr := adminAPIServer.Shutdown(shutdownContext); apiErr != nil && err == nil {
				err = fmt.Errorf("graceful admin API shutdown: %w", apiErr)
			}
		}
		if pbServer != nil {
			if pbErr := pbServer.Shutdown(shutdownContext); pbErr != nil && err == nil {
				err = fmt.Errorf("graceful PB facade shutdown: %w", pbErr)
			}
		}
		if restServer != nil {
			if restErr := restServer.Shutdown(shutdownContext); restErr != nil && err == nil {
				err = fmt.Errorf("graceful REST daemon shutdown: %w", restErr)
			}
		}
		return err
	case <-runtime.LeadershipLost():
		// A lost database session means the advisory fence no longer belongs to
		// this process. Stop admission immediately; graceful draining here could
		// overlap a newly elected node and double-spend in-memory quotas.
		_ = server.Close()
		if webServer != nil {
			_ = webServer.Close()
		}
		if adminAPIServer != nil {
			_ = adminAPIServer.Close()
		}
		if pbServer != nil {
			_ = pbServer.Close()
		}
		if restServer != nil {
			_ = restServer.Close()
		}
		_ = runtime.Close()
		return fmt.Errorf("active-passive gateway fence lost: %w", runtime.LeadershipError())
	case err := <-errCh:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	}
}

func newStandbyHealthServer(address string) (*http.Server, net.Listener, error) {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, nil, fmt.Errorf("listen for standby health on %s: %w", address, err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/live", func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			writer.Header().Set("Allow", http.MethodGet)
			http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte(`{"status":"standby","live":true,"ready":false}`))
	})
	mux.HandleFunc("/ready", func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			writer.Header().Set("Allow", http.MethodGet)
			http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusServiceUnavailable)
		_, _ = writer.Write([]byte(`{"status":"standby","live":true,"ready":false}`))
	})
	return &http.Server{
		Addr:              address,
		Handler:           mux,
		ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      5 * time.Second,
		IdleTimeout:       30 * time.Second,
	}, listener, nil
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
