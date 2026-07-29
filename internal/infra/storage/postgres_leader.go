package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

var (
	// ErrLeadershipHeld means another live PostgreSQL session already owns the
	// cluster's active-passive gateway fence.
	ErrLeadershipHeld = errors.New("gateway leadership is held by another node")
	// ErrLeadershipClosed distinguishes an orderly release from a database
	// connection failure on Lost/Err.
	ErrLeadershipClosed = errors.New("gateway leadership lease closed")
)

const defaultLeadershipProbeInterval = 2 * time.Second

// PostgresLeaderLease holds a session-level PostgreSQL advisory lock on one
// dedicated connection. Losing that connection atomically loses the lock, so
// Lost is a fencing signal: a runtime must stop admitting traffic when it
// closes. This is the first active-passive HA primitive; it does not make the
// node-local SQLite control plane multi-writer.
type PostgresLeaderLease struct {
	db       *sql.DB
	conn     *sql.Conn
	lockID   int64
	interval time.Duration

	cancel context.CancelFunc
	wg     sync.WaitGroup

	lostOnce sync.Once
	lost     chan struct{}
	errMu    sync.RWMutex
	err      error

	closeOnce sync.Once
	closeErr  error
}

// GatewayAdvisoryLockID derives a stable signed PostgreSQL advisory-lock key
// from the deployment namespace. Namespaces allow independent Jasmin clusters
// sharing one database to elect leaders independently.
func GatewayAdvisoryLockID(namespace string) int64 {
	sum := sha256.Sum256([]byte("jasmin-go/gateway-leader/v1\x00" + namespace))
	return int64(binary.BigEndian.Uint64(sum[:8]))
}

// OpenPostgresLeaderLease attempts to acquire the active-passive fence. It
// returns ErrLeadershipHeld immediately instead of waiting, allowing an
// orchestrator to keep the standby process healthy without exposing traffic.
func OpenPostgresLeaderLease(ctx context.Context, dsn, namespace string) (*PostgresLeaderLease, error) {
	return openPostgresLeaderLease(ctx, dsn, namespace, defaultLeadershipProbeInterval)
}

func openPostgresLeaderLease(ctx context.Context, dsn, namespace string, probeInterval time.Duration) (*PostgresLeaderLease, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if dsn == "" {
		return nil, errors.New("empty PostgreSQL DSN")
	}
	if namespace == "" {
		return nil, errors.New("empty gateway HA namespace")
	}
	if probeInterval <= 0 {
		return nil, errors.New("gateway leadership probe interval must be positive")
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("open gateway leadership database: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	conn, err := db.Conn(ctx)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("connect gateway leadership database: %w", err)
	}
	lockID := GatewayAdvisoryLockID(namespace)
	var acquired bool
	if err = conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock($1)`, lockID).Scan(&acquired); err != nil {
		_ = conn.Close()
		_ = db.Close()
		return nil, fmt.Errorf("acquire gateway leadership: %w", err)
	}
	if !acquired {
		_ = conn.Close()
		_ = db.Close()
		return nil, fmt.Errorf("%w: namespace %q", ErrLeadershipHeld, namespace)
	}

	monitorCtx, cancel := context.WithCancel(context.Background())
	lease := &PostgresLeaderLease{
		db:       db,
		conn:     conn,
		lockID:   lockID,
		interval: probeInterval,
		cancel:   cancel,
		lost:     make(chan struct{}),
	}
	lease.wg.Add(1)
	go lease.monitor(monitorCtx)
	return lease, nil
}

// Lost closes when the dedicated session fails or Close releases the fence.
func (lease *PostgresLeaderLease) Lost() <-chan struct{} {
	if lease == nil {
		closed := make(chan struct{})
		close(closed)
		return closed
	}
	return lease.lost
}

// Err reports why Lost closed.
func (lease *PostgresLeaderLease) Err() error {
	if lease == nil {
		return ErrLeadershipClosed
	}
	lease.errMu.RLock()
	defer lease.errMu.RUnlock()
	return lease.err
}

func (lease *PostgresLeaderLease) monitor(ctx context.Context) {
	defer lease.wg.Done()
	ticker := time.NewTicker(lease.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			probeCtx, cancel := context.WithTimeout(ctx, lease.interval)
			err := lease.conn.PingContext(probeCtx)
			cancel()
			if err != nil {
				lease.markLost(fmt.Errorf("gateway leadership connection lost: %w", err))
				return
			}
		}
	}
}

func (lease *PostgresLeaderLease) markLost(err error) {
	lease.lostOnce.Do(func() {
		lease.errMu.Lock()
		lease.err = err
		lease.errMu.Unlock()
		close(lease.lost)
	})
}

// Close releases the advisory lock and its dedicated database session.
func (lease *PostgresLeaderLease) Close() error {
	if lease == nil {
		return nil
	}
	lease.closeOnce.Do(func() {
		lease.cancel()
		lease.wg.Wait()

		var errs []error
		unlockCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		var unlocked bool
		err := lease.conn.QueryRowContext(unlockCtx, `SELECT pg_advisory_unlock($1)`, lease.lockID).Scan(&unlocked)
		cancel()
		if err != nil && lease.Err() == nil {
			errs = append(errs, fmt.Errorf("release gateway leadership: %w", err))
		} else if err == nil && !unlocked {
			errs = append(errs, errors.New("release gateway leadership: advisory lock was not held"))
		}
		if err = lease.conn.Close(); err != nil {
			errs = append(errs, err)
		}
		if err = lease.db.Close(); err != nil {
			errs = append(errs, err)
		}
		lease.markLost(ErrLeadershipClosed)
		lease.closeErr = errors.Join(errs...)
	})
	return lease.closeErr
}
