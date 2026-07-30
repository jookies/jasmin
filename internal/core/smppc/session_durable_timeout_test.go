package smppc_test

import (
	"context"
	"database/sql"
	"net"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/pumpitspace/synevyr/internal/core/smppc"
	"github.com/pumpitspace/synevyr/internal/core/submittransaction"
	"github.com/pumpitspace/synevyr/internal/core/tlv"
	"github.com/pumpitspace/synevyr/internal/infra/storage"
	"github.com/pumpitspace/synevyr/internal/transport/amqpcompat"
	"github.com/pumpitspace/synevyr/internal/transport/smppwire"
)

type staticSubmitDecoder struct{}

func (staticSubmitDecoder) DecodeSubmitSM(context.Context, []byte) (smppwire.SubmitSMBody, []tlv.TLV, error) {
	return smppwire.SubmitSMBody{DestinationAddress: []byte("15551230000"), ShortMessage: []byte("timeout")}, nil, nil
}

func TestDurableSessionTimeoutClosesAttemptBeforeRedelivery(t *testing.T) {
	db, err := sql.Open("sqlite3", "file:durable-session-timeout?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	repository, err := storage.NewSQLiteSubmitTransactionRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	transactions, err := submittransaction.NewService(repository, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	properties, _ := amqpcompat.NewProperties("timeout-message", nil)
	envelope, _ := amqpcompat.NewEnvelope("submit.sm.connector-a", properties, []byte("pickle"))
	if err := transactions.AdmitSubmit(context.Background(), []amqpcompat.Envelope{envelope}); err != nil {
		t.Fatal(err)
	}

	client, server := net.Pipe()
	t.Cleanup(func() { _ = client.Close(); _ = server.Close() })
	retry, _ := smppc.NewErrorRetryPolicy(smppc.DefaultErrorRetryRules())
	readiness, _ := smppc.NewReadinessPolicy(smppc.DefaultReadinessConfig())
	session := smppc.NewSessionWithDurability(client, smppc.Config{CID: "timeout", ResTimeout: 0.05}, retry, readiness, staticSubmitDecoder{}, transactions, nil)
	settled := make(chan bool, 1)
	delivery := injectDelivery(envelope, func(requeue bool) { settled <- requeue })
	readResult := make(chan error, 1)
	go func() {
		_, err := smppwire.Read(server, smppwire.DefaultMaxSize)
		readResult <- err
	}()
	if err := session.Submit(context.Background(), delivery); err != nil {
		t.Fatal(err)
	}
	if err := <-readResult; err != nil {
		t.Fatalf("read submit_sm: %v", err)
	}
	select {
	case requeue := <-settled:
		if !requeue {
			t.Fatal("timeout discarded delivery")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout did not settle delivery")
	}
	second, committed, err := transactions.BeginAttempt(context.Background(), "timeout-message/000001")
	if err != nil || committed || second.Number != 2 {
		t.Fatalf("redelivery attempt=%+v committed=%v err=%v", second, committed, err)
	}
}
