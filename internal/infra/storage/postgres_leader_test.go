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
