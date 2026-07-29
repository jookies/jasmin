package storage

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

func TestGatewayAdvisoryLockIDIsStableAndNamespaced(t *testing.T) {
	first := GatewayAdvisoryLockID("production")
	if first != GatewayAdvisoryLockID("production") {
		t.Fatal("same namespace produced different advisory lock IDs")
	}
	if first == GatewayAdvisoryLockID("staging") {
		t.Fatal("independent namespaces share an advisory lock ID")
	}
}

func TestPostgresLeaderLeaseRejectsInvalidConfiguration(t *testing.T) {
	if _, err := OpenPostgresLeaderLease(context.Background(), "", "production"); err == nil {
		t.Fatal("empty DSN was accepted")
	}
	if _, err := OpenPostgresLeaderLease(context.Background(), "postgres://unused", ""); err == nil {
		t.Fatal("empty namespace was accepted")
	}
	if _, err := openPostgresLeaderLease(context.Background(), "postgres://unused", "production", 0); err == nil {
		t.Fatal("non-positive probe interval was accepted")
	}
}

func TestWaitPostgresLeaderLeaseRetriesContentionAndStopsOnOtherErrors(t *testing.T) {
	var attempts int
	expected := &PostgresLeaderLease{}
	lease, err := waitPostgresLeaderLease(context.Background(), time.Millisecond, func(context.Context) (*PostgresLeaderLease, error) {
		attempts++
		if attempts < 3 {
			return nil, ErrLeadershipHeld
		}
		return expected, nil
	})
	if err != nil || lease != expected || attempts != 3 {
		t.Fatalf("wait=(%p,%v,%d) want=(%p,nil,3)", lease, err, attempts, expected)
	}

	sentinel := errors.New("database unavailable")
	attempts = 0
	if _, err = waitPostgresLeaderLease(context.Background(), time.Millisecond, func(context.Context) (*PostgresLeaderLease, error) {
		attempts++
		return nil, sentinel
	}); !errors.Is(err, sentinel) || attempts != 1 {
		t.Fatalf("non-contention wait=(%v,%d)", err, attempts)
	}
}

func TestWaitPostgresLeaderLeaseHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := waitPostgresLeaderLease(ctx, time.Hour, func(context.Context) (*PostgresLeaderLease, error) {
		return nil, ErrLeadershipHeld
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v want context.Canceled", err)
	}
}

func TestPostgresLeaderLeaseFencesSecondNodeAndPermitsTakeover(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	namespace := "storage-test-leader-takeover"

	leader, err := openPostgresLeaderLease(ctx, dsn, namespace, 25*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = OpenPostgresLeaderLease(ctx, dsn, namespace); !errors.Is(err, ErrLeadershipHeld) {
		_ = leader.Close()
		t.Fatalf("second node error=%v want ErrLeadershipHeld", err)
	}
	if err = leader.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-leader.Lost():
		if !errors.Is(leader.Err(), ErrLeadershipClosed) {
			t.Fatalf("closed lease error=%v", leader.Err())
		}
	default:
		t.Fatal("closed lease did not close Lost")
	}

	takeover, err := OpenPostgresLeaderLease(ctx, dsn, namespace)
	if err != nil {
		t.Fatalf("standby could not acquire released fence: %v", err)
	}
	if err = takeover.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresWaitingStandbyPromotesInProcess(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	leader, err := openPostgresLeaderLease(ctx, dsn, "storage-test-waiting-standby", 25*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		lease *PostgresLeaderLease
		err   error
	}
	promoted := make(chan result, 1)
	go func() {
		lease, waitErr := WaitPostgresLeaderLease(
			ctx,
			dsn,
			"storage-test-waiting-standby",
			20*time.Millisecond,
		)
		promoted <- result{lease: lease, err: waitErr}
	}()

	select {
	case early := <-promoted:
		if early.lease != nil {
			_ = early.lease.Close()
		}
		_ = leader.Close()
		t.Fatalf("standby promoted while leader held the fence: %v", early.err)
	case <-time.After(100 * time.Millisecond):
	}
	if err = leader.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case promotion := <-promoted:
		if promotion.err != nil {
			t.Fatalf("promote standby: %v", promotion.err)
		}
		if promotion.lease == nil {
			t.Fatal("promotion returned a nil lease")
		}
		if err = promotion.lease.Close(); err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatalf("standby did not promote: %v", ctx.Err())
	}
}
