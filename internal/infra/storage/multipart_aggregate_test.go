package storage

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/pumpitspace/jasmin/internal/core/submittransaction"
	"github.com/pumpitspace/jasmin/internal/transport/amqpcompat"
)

func TestMultipartAggregateStatusAcrossRecoveryAndResults(t *testing.T) {
	repository, _ := newSubmitStore(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 22, 2, 0, 0, 0, time.UTC)
	service, err := submittransaction.NewService(repository, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	envelopes := make([]amqpcompat.Envelope, 2)
	for index := range envelopes {
		partNumber := index + 1
		properties, propErr := amqpcompat.NewProperties(fmt.Sprintf("aggregate-status/%06d", partNumber), map[string]amqpcompat.Field{
			"aggregate-message-id": amqpcompat.StringField("aggregate-status"),
			"part-number":          amqpcompat.IntegerField(int64(partNumber)),
			"part-count":           amqpcompat.IntegerField(2),
		})
		if propErr != nil {
			t.Fatal(propErr)
		}
		envelopes[index], err = amqpcompat.NewEnvelope("submit.sm.connector-a", properties, []byte{0x80, 2, byte(index + 1)})
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := service.AdmitSubmit(ctx, envelopes); err != nil {
		t.Fatal(err)
	}
	assertAggregateStatus(t, service, submittransaction.PartPending, 2, 2, 0, 0, 0)

	if _, _, err := service.BeginAttempt(ctx, "aggregate-status/000001"); err != nil {
		t.Fatal(err)
	}
	if recovered, err := service.Recover(ctx); err != nil || recovered != 1 {
		t.Fatalf("Recover=(%d,%v)", recovered, err)
	}
	assertAggregateStatus(t, service, submittransaction.PartUnknownAfterSend, 2, 1, 0, 1, 0)

	commitSuccess := func(partKey string) {
		attempt, committed, beginErr := service.BeginAttempt(ctx, partKey)
		if beginErr != nil || committed {
			t.Fatalf("BeginAttempt(%s)=(%+v,%v,%v)", partKey, attempt, committed, beginErr)
		}
		fresh, commitErr := service.CommitResponse(ctx, submittransaction.Result{
			PartKey: partKey, AttemptID: attempt.ID, Kind: submittransaction.ResultSuccess,
		})
		if commitErr != nil || !fresh {
			t.Fatalf("CommitResponse(%s)=(%v,%v)", partKey, fresh, commitErr)
		}
	}
	commitSuccess("aggregate-status/000001")
	assertAggregateStatus(t, service, submittransaction.PartPending, 2, 1, 0, 0, 1)
	commitSuccess("aggregate-status/000002")
	assertAggregateStatus(t, service, submittransaction.PartResultCommitted, 2, 0, 0, 0, 2)
}

func assertAggregateStatus(t *testing.T, service *submittransaction.Service, state submittransaction.PartState, total, pending, attempting, unknown, committed int) {
	t.Helper()
	status, err := service.AggregateStatus(context.Background(), "aggregate-status")
	if err != nil {
		t.Fatal(err)
	}
	if status.State != state || status.TotalParts != total || status.Pending != pending || status.Attempting != attempting || status.UnknownAfterSend != unknown || status.ResultCommitted != committed {
		t.Fatalf("aggregate=%+v want state=%s total=%d counts=(%d,%d,%d,%d)", status, state, total, pending, attempting, unknown, committed)
	}
}
