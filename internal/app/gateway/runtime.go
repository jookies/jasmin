package gateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/pumpitspace/jasmin/internal/app/outbound"
	"github.com/pumpitspace/jasmin/internal/core/smppc"
)

type ManagerFactory func(string) *smppc.Manager

type Runtime struct {
	Handler   http.Handler
	manager   *smppc.Manager
	outbound  *outbound.Runtime
	closeOnce sync.Once
	closeErr  error
}

func NewRuntime(ctx context.Context, config Config) (*Runtime, error) {
	return newRuntime(ctx, config, smppc.NewManager, outbound.NewRuntime)
}

type outboundFactory func(context.Context, outbound.Config) (*outbound.Runtime, error)

func newRuntime(ctx context.Context, config Config, managers ManagerFactory, outbounds outboundFactory) (_ *Runtime, resultErr error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ValidateConfig(config); err != nil {
		return nil, err
	}
	outboundRuntime, err := outbounds(ctx, config.Outbound)
	if err != nil {
		return nil, fmt.Errorf("start outbound runtime: %w", err)
	}
	runtime := &Runtime{Handler: outboundRuntime.Handler, outbound: outboundRuntime}
	defer func() {
		if resultErr != nil {
			_ = runtime.Close()
		}
	}()
	manager := managers(config.Outbound.AMQPURL)
	if manager == nil {
		return nil, errors.New("gateway manager factory returned nil")
	}
	runtime.manager = manager
	for _, connector := range config.Connectors {
		if err := manager.Add(connector); err != nil {
			return nil, fmt.Errorf("add connector %q: %w", connector.CID, err)
		}
	}
	if err := manager.StartAll(); err != nil {
		return nil, fmt.Errorf("start connectors: %w", err)
	}
	if err := waitRequiredBound(ctx, manager, config.RequiredConnectors(), config.BindTimeout()); err != nil {
		return nil, err
	}
	return runtime, nil
}

func (runtime *Runtime) Manager() *smppc.Manager {
	if runtime == nil {
		return nil
	}
	return runtime.manager
}

func (runtime *Runtime) Close() error {
	if runtime == nil {
		return nil
	}
	runtime.closeOnce.Do(func() {
		var errs []error
		if runtime.manager != nil {
			if err := runtime.manager.StopAll(); err != nil {
				errs = append(errs, err)
			}
		}
		if runtime.outbound != nil {
			if err := runtime.outbound.Close(); err != nil {
				errs = append(errs, err)
			}
		}
		runtime.closeErr = errors.Join(errs...)
	})
	return runtime.closeErr
}

func waitRequiredBound(parent context.Context, manager *smppc.Manager, required []string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		pending := make([]string, 0, len(required))
		for _, cid := range required {
			status, err := manager.Status(cid)
			if err != nil {
				return fmt.Errorf("read connector %q status: %w", cid, err)
			}
			if status.Observed != smppc.StatusBound {
				pending = append(pending, fmt.Sprintf("%s=%s", cid, status.Observed))
			}
		}
		if len(pending) == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			sort.Strings(pending)
			return fmt.Errorf("required SMPPc connectors not bound (%v): %w", pending, ctx.Err())
		case <-ticker.C:
		}
	}
}
