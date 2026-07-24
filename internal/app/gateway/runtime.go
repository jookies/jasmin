package gateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/pumpitspace/jasmin/internal/app/dlrlookup"
	"github.com/pumpitspace/jasmin/internal/app/dlrthrower"
	"github.com/pumpitspace/jasmin/internal/app/outbound"
	"github.com/pumpitspace/jasmin/internal/core/smppc"
	"github.com/pumpitspace/jasmin/internal/core/submittransaction"
	"github.com/pumpitspace/jasmin/internal/infra/storage"
	"github.com/pumpitspace/jasmin/internal/transport/picklecompat"
)

type ManagerFactory func(string) *smppc.Manager

type Runtime struct {
	Handler      http.Handler
	manager      *smppc.Manager
	outbound     *outbound.Runtime
	bridge       *picklecompat.Bridge
	store        *storage.PostgresSubmitTransactionRepository
	dlrLookup    *dlrlookup.Service
	dlrThrower   *dlrthrower.Service
	workerCancel context.CancelFunc
	closeOnce    sync.Once
	closeErr     error
}

func NewRuntime(ctx context.Context, config Config) (_ *Runtime, resultErr error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ValidateConfig(config); err != nil {
		return nil, err
	}
	workerCtx, workerCancel := context.WithCancel(context.Background())
	repository, err := storage.OpenPostgresSubmitTransactionRepository(ctx, config.Outbound.PostgresDSN)
	if err != nil {
		workerCancel()
		return nil, err
	}
	runtime := &Runtime{store: repository, workerCancel: workerCancel}
	defer func() {
		if resultErr != nil {
			_ = runtime.Close()
		}
	}()
	if err = repository.Migrate(ctx); err != nil {
		return nil, fmt.Errorf("migrate submit transaction store: %w", err)
	}
	transactions, err := submittransaction.NewProductionService(repository, nil)
	if err != nil {
		return nil, err
	}
	if _, err = transactions.Recover(ctx); err != nil {
		return nil, fmt.Errorf("recover unresolved submit attempts: %w", err)
	}
	bridge, err := picklecompat.NewBridge(workerCtx, config.Outbound.PythonPath)
	if err != nil {
		return nil, fmt.Errorf("start trusted pickle bridge: %w", err)
	}
	runtime.bridge = bridge
	manager := smppc.NewManagerWithFactory(config.Outbound.AMQPURL, func(connectorConfig smppc.Config, amqpURL string) (*smppc.Connector, error) {
		connector, connectorErr := smppc.NewConnectorWithDecoder(connectorConfig, amqpURL, bridge)
		if connectorErr != nil {
			return nil, connectorErr
		}
		if connectorErr = connector.ConfigureDurability(transactions); connectorErr != nil {
			return nil, connectorErr
		}
		return connector, nil
	})
	runtime.manager = manager
	for _, connector := range config.Connectors {
		if err := manager.Add(connector); err != nil {
			return nil, fmt.Errorf("add connector %q: %w", connector.CID, err)
		}
	}
	outboundRuntime, err := outbound.NewRuntimeWithDependencies(workerCtx, config.Outbound, outbound.RuntimeDependencies{
		Bridge: bridge, Transactions: transactions, Repository: repository, ConnectorAvailable: manager.Available,
	})
	if err != nil {
		return nil, fmt.Errorf("start outbound runtime: %w", err)
	}
	runtime.Handler = outboundRuntime.Handler
	runtime.outbound = outboundRuntime
	if config.DLRLookup != nil {
		lookupConfig := *config.DLRLookup
		if lookupConfig.AMQPURL == "" {
			lookupConfig.AMQPURL = config.Outbound.AMQPURL
		}
		lookupService, lookupErr := dlrlookup.NewService(lookupConfig)
		if lookupErr != nil {
			return nil, fmt.Errorf("start DLR lookup worker: %w", lookupErr)
		}
		runtime.dlrLookup = lookupService
		go func() { _ = lookupService.Run(workerCtx) }()
	}
	if config.DLRThrower != nil {
		throwerConfig := *config.DLRThrower
		if throwerConfig.AMQPURL == "" {
			throwerConfig.AMQPURL = config.Outbound.AMQPURL
		}
		throwerService, throwerErr := dlrthrower.NewService(throwerConfig)
		if throwerErr != nil {
			return nil, fmt.Errorf("start DLR thrower worker: %w", throwerErr)
		}
		runtime.dlrThrower = throwerService
		go func() { _ = throwerService.Run(workerCtx) }()
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
		// Admission is stopped by the executable before Runtime.Close. Stop the
		// outbox/consumers and confirming publisher before fencing connectors.
		if runtime.outbound != nil {
			if err := runtime.outbound.Close(); err != nil {
				errs = append(errs, err)
			}
		}
		if runtime.manager != nil {
			if err := runtime.manager.StopAll(); err != nil {
				errs = append(errs, err)
			}
		}
		if runtime.workerCancel != nil {
			runtime.workerCancel()
		}
		if runtime.dlrLookup != nil {
			if err := runtime.dlrLookup.Close(); err != nil {
				errs = append(errs, err)
			}
		}
		if runtime.bridge != nil {
			if err := runtime.bridge.Close(); err != nil {
				errs = append(errs, err)
			}
		}
		if runtime.store != nil {
			if err := runtime.store.Close(); err != nil {
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
