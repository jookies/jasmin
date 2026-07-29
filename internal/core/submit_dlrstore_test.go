package core_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/pumpitspace/jasmin/internal/app/outbound"
	"github.com/pumpitspace/jasmin/internal/core"
	"github.com/pumpitspace/jasmin/internal/core/billing"
	"github.com/pumpitspace/jasmin/internal/core/dlr"
	"github.com/pumpitspace/jasmin/internal/core/smppc"
	"github.com/pumpitspace/jasmin/internal/core/submittransaction"
	"github.com/pumpitspace/jasmin/internal/state/rediscompat"
	"github.com/pumpitspace/jasmin/internal/transport/amqpcompat"
	"github.com/pumpitspace/jasmin/internal/transport/picklecompat"
)

type recordingDLRStore struct {
	calls        int
	msgID        string
	request      dlr.HTTPDLRRequest
	smppsCalls   int
	smppsMsgID   string
	smppsRequest dlr.SMPPSDLRRequest
}

func (s *recordingDLRStore) StoreHTTPDLRRequest(_ context.Context, msgID string, request dlr.HTTPDLRRequest) error {
	s.calls++
	s.msgID = msgID
	s.request = request
	return nil
}

func (s *recordingDLRStore) StoreSMPPSDLRRequest(_ context.Context, msgID string, request dlr.SMPPSDLRRequest) error {
	s.smppsCalls++
	s.smppsMsgID = msgID
	s.smppsRequest = request
	return nil
}

type fixedSubmitEncoder struct{}

func (fixedSubmitEncoder) EncodeSubmitSM(_ context.Context, request picklecompat.SubmitSMEncodeRequest) (picklecompat.SubmitSMEncodeResult, error) {
	return picklecompat.SubmitSMEncodeResult{
		Body: []byte{0x80, 0x02, byte(request.Sequence)},
		Bill: []byte{0x80, 0x02, 0x42},
	}, nil
}

type envelopeCapturePublisher struct {
	envelopes []amqpcompat.Envelope
}

func (p *envelopeCapturePublisher) Publish(_ context.Context, _, _ string, envelope amqpcompat.Envelope) error {
	p.envelopes = append(p.envelopes, envelope)
	return nil
}

type dlrCapturePublisher struct {
	forwards []dlr.Forward
}

func (p *dlrCapturePublisher) PublishDLR(_ context.Context, forward dlr.Forward) error {
	p.forwards = append(p.forwards, forward)
	return nil
}

type responseCaptureRepository struct {
	submittransaction.Repository
	commit submittransaction.ResultCommit
}

func (r *responseCaptureRepository) CommitResult(_ context.Context, commit submittransaction.ResultCommit) (bool, error) {
	r.commit = commit
	return true, nil
}

func submitRespDLRPublication(t *testing.T, envelope amqpcompat.Envelope, status, smscMessageID string) amqpcompat.Envelope {
	t.Helper()
	repository := &responseCaptureRepository{}
	transactions, err := submittransaction.NewService(repository, nil)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := smppc.NewErrorRetryPolicy(smppc.DefaultErrorRetryRules())
	if err != nil {
		t.Fatal(err)
	}
	lifecycle, err := smppc.NewDurableResponseLifecycle(transactions, retry, nil)
	if err != nil {
		t.Fatal(err)
	}
	messageID := envelope.Properties().MessageID()
	if _, err := lifecycle.Commit(context.Background(), smppc.DurableResponseInput{
		PartKey:       messageID,
		AttemptID:     1,
		Status:        status,
		SMSCMessageID: smscMessageID,
		MessageID:     messageID,
		RetryAttempt:  1,
		RetryEnvelope: &envelope,
	}); err != nil {
		t.Fatal(err)
	}
	for _, event := range repository.commit.Events {
		if event.Kind != submittransaction.EventDLRState {
			continue
		}
		publication, err := submittransaction.RestoreEnvelope(event.Payload)
		if err != nil {
			t.Fatal(err)
		}
		return publication
	}
	t.Fatal("submit_sm_resp produced no DLR publication")
	return amqpcompat.Envelope{}
}

func submitRespEvent(t *testing.T, publication amqpcompat.Envelope) dlr.SubmitRespEvent {
	t.Helper()
	event := dlr.SubmitRespEvent{
		QueueMsgID: publication.Properties().MessageID(),
		Status:     string(publication.Body()),
	}
	if field, ok := publication.Properties().Headers()["smpp_msgid"]; ok {
		smppMessageID, ok := field.String()
		if !ok {
			t.Fatal("DLR publication smpp_msgid is not a string")
		}
		event.SMPPMsgID = smppMessageID
	}
	return event
}

type memoryRedis struct {
	redis.Cmdable
	hashes map[string]map[string]string
}

func newMemoryRedis() *memoryRedis {
	return &memoryRedis{hashes: make(map[string]map[string]string)}
}

func (r *memoryRedis) TxPipelined(ctx context.Context, fn func(redis.Pipeliner) error) ([]redis.Cmder, error) {
	pipeline := &memoryRedisPipeline{redis: r}
	if err := fn(pipeline); err != nil {
		return nil, err
	}
	return pipeline.commands, nil
}

func (r *memoryRedis) HGetAll(ctx context.Context, key string) *redis.MapStringStringCmd {
	command := redis.NewMapStringStringCmd(ctx)
	fields := make(map[string]string, len(r.hashes[key]))
	for name, value := range r.hashes[key] {
		fields[name] = value
	}
	command.SetVal(fields)
	return command
}

func (r *memoryRedis) Del(ctx context.Context, keys ...string) *redis.IntCmd {
	command := redis.NewIntCmd(ctx)
	var deleted int64
	for _, key := range keys {
		if _, ok := r.hashes[key]; ok {
			delete(r.hashes, key)
			deleted++
		}
	}
	command.SetVal(deleted)
	return command
}

type memoryRedisPipeline struct {
	redis.Pipeliner
	redis    *memoryRedis
	commands []redis.Cmder
}

func (p *memoryRedisPipeline) HSet(ctx context.Context, key string, values ...interface{}) *redis.IntCmd {
	command := redis.NewIntCmd(ctx)
	fields := p.redis.hashes[key]
	if fields == nil {
		fields = make(map[string]string)
		p.redis.hashes[key] = fields
	}
	var added int64
	for _, value := range values {
		for name, field := range value.(map[string]any) {
			if _, ok := fields[name]; !ok {
				added++
			}
			fields[name] = fmt.Sprint(field)
		}
	}
	command.SetVal(added)
	p.commands = append(p.commands, command)
	return command
}

func (p *memoryRedisPipeline) Expire(ctx context.Context, key string, _ time.Duration) *redis.BoolCmd {
	command := redis.NewBoolCmd(ctx)
	_, ok := p.redis.hashes[key]
	command.SetVal(ok)
	p.commands = append(p.commands, command)
	return command
}

func newSubmitServiceWithDLR(t *testing.T, store core.DLRRequestStore, expiry func(string) int64) (*core.SubmitService, *recordingPublisher) {
	t.Helper()
	user := billing.NewUser(7)
	if err := user.SetBalance(100); err != nil {
		t.Fatal(err)
	}
	users := billing.NewManager()
	if err := users.AddUserWithID("alice", "user-opaque", user); err != nil {
		t.Fatal(err)
	}
	publisher := &recordingPublisher{}
	routes := routeTable(t, true)
	service, err := core.NewSubmitService(core.SubmitServiceDependencies{
		InterceptorTable:   emptyInterceptors(),
		InterceptorRunner:  fixedRunner{},
		RoutingTable:       &routes,
		BillingUsers:       users,
		EnvelopeBuilder:    &recordingBuilder{},
		Publisher:          publisher,
		DLRRequestStore:    store,
		ConnectorDLRExpiry: expiry,
		NewMessageID:       func() (string, error) { return "11111111-1111-4111-8111-111111111111", nil },
		NewReference:       func() (uint16, error) { return 41, nil },
		Now:                func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) },
	})
	if err != nil {
		t.Fatal(err)
	}
	return service, publisher
}

func newMultipartDLRService(t *testing.T, store core.DLRRequestStore, publisher core.AMQPPublisher) *core.SubmitService {
	t.Helper()
	builder, err := outbound.NewSubmitEnvelopeBuilder(fixedSubmitEncoder{})
	if err != nil {
		t.Fatal(err)
	}
	user := billing.NewUser(7)
	if err := user.SetBalance(100); err != nil {
		t.Fatal(err)
	}
	users := billing.NewManager()
	if err := users.AddUserWithID("alice", "user-opaque", user); err != nil {
		t.Fatal(err)
	}
	routes := routeTable(t, true)
	service, err := core.NewSubmitService(core.SubmitServiceDependencies{
		InterceptorTable:  emptyInterceptors(),
		InterceptorRunner: fixedRunner{},
		RoutingTable:      &routes,
		BillingUsers:      users,
		EnvelopeBuilder:   builder,
		Publisher:         publisher,
		DLRRequestStore:   store,
		NewMessageID: func() (string, error) {
			return "11111111-1111-4111-8111-111111111111", nil
		},
		NewBillID: func() (string, error) { return "bill-1", nil },
		NewReference: func() (uint16, error) {
			return 41, nil
		},
		Now: func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) },
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func TestSubmitWritesDLRRequestForLevel2HTTP(t *testing.T) {
	store := &recordingDLRStore{}
	service, _ := newSubmitServiceWithDLR(t, store, func(string) int64 { return 3600 })

	_, err := service.Submit(context.Background(), core.SubmitRequest{
		Username:    "alice",
		Destination: "15551230000",
		Content:     "hi",
		DLR:         true,
		DLRUrl:      "http://sink.example/dlr",
		DLRLevel:    2,
		DLRMethod:   "GET",
	})
	if err != nil {
		t.Fatal(err)
	}
	if store.calls != 1 {
		t.Fatalf("store calls=%d want 1", store.calls)
	}
	if store.msgID != "11111111-1111-4111-8111-111111111111" {
		t.Fatalf("stored msgid=%q", store.msgID)
	}
	want := dlr.HTTPDLRRequest{URL: "http://sink.example/dlr", Level: 2, Method: "GET", Connector: "connector-a", ExpirySeconds: 3600}
	if store.request != want {
		t.Fatalf("stored request=%+v want %+v", store.request, want)
	}
}

func TestMultipartSubmitDLRCorrelatesLastPartToAggregateMessage(t *testing.T) {
	redisClient := rediscompat.NewClient(newMemoryRedis())
	store, err := dlr.NewRequestStore(redisClient)
	if err != nil {
		t.Fatal(err)
	}
	publisher := &envelopeCapturePublisher{}
	service := newMultipartDLRService(t, store, publisher)

	messageID, err := service.Submit(context.Background(), core.SubmitRequest{
		Username:    "alice",
		Destination: "15551230000",
		Content:     strings.Repeat("A", 161),
		DLR:         true,
		DLRUrl:      "http://sink.example/dlr",
		DLRLevel:    3,
		DLRMethod:   "POST",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(publisher.envelopes) != 2 {
		t.Fatalf("published parts=%d want 2", len(publisher.envelopes))
	}
	firstPartID := publisher.envelopes[0].Properties().MessageID()
	lastPartID := publisher.envelopes[1].Properties().MessageID()
	if firstPartID != messageID+"/000001" || lastPartID != messageID+"/000002" {
		t.Fatalf("part ids=(%q,%q) aggregate=%q", firstPartID, lastPartID, messageID)
	}

	dlrPublisher := &dlrCapturePublisher{}
	correlator := dlr.NewCorrelator(redisClient, dlrPublisher, dlr.Config{})
	firstPublication := submitRespDLRPublication(t, publisher.envelopes[0], "ESME_RSUBMITFAIL", "")
	err = correlator.OnSubmitResp(context.Background(), submitRespEvent(t, firstPublication))
	if !errors.Is(err, dlr.ErrDLRMapNotFound) {
		t.Fatalf("non-DLR part submit_sm_resp error=%v want ErrDLRMapNotFound", err)
	}
	if len(dlrPublisher.forwards) != 0 {
		t.Fatalf("non-DLR part forwarded receipts=%d want 0", len(dlrPublisher.forwards))
	}

	lastPublication := submitRespDLRPublication(t, publisher.envelopes[1], "ESME_ROK", "000ABC2")
	if err := correlator.OnSubmitResp(context.Background(), submitRespEvent(t, lastPublication)); err != nil {
		t.Fatalf("DLR-bearing part submit_sm_resp: %v", err)
	}
	if len(dlrPublisher.forwards) != 1 {
		t.Fatalf("level-1 forwards=%d want 1", len(dlrPublisher.forwards))
	}
	if forward := dlrPublisher.forwards[0]; forward.QueueMsgID != messageID || forward.Level != 1 {
		t.Fatalf("level-1 forward=%+v want aggregate id %q", forward, messageID)
	}

	if err := correlator.OnDeliverReceipt(context.Background(), dlr.DeliverReceiptEvent{
		RawDLRID:    "000abc2",
		Base:        dlr.MsgIDBaseSame,
		ConnectorID: "connector-a",
		Status:      "DELIVRD",
		Sub:         "001",
		Dlvrd:       "001",
		SubmitDate:  "2601020304",
		DoneDate:    "2601020305",
		Err:         "000",
		Text:        "delivered",
	}); err != nil {
		t.Fatalf("terminal receipt: %v", err)
	}
	if len(dlrPublisher.forwards) != 2 {
		t.Fatalf("total forwards=%d want level 1 and level 2", len(dlrPublisher.forwards))
	}
	if forward := dlrPublisher.forwards[1]; forward.QueueMsgID != messageID || forward.Level != 2 {
		t.Fatalf("level-2 forward=%+v want aggregate id %q", forward, messageID)
	}
}

func TestMultipartSubmitLastPartFailureForwardsOneAggregateDLR(t *testing.T) {
	redisClient := rediscompat.NewClient(newMemoryRedis())
	store, err := dlr.NewRequestStore(redisClient)
	if err != nil {
		t.Fatal(err)
	}
	publisher := &envelopeCapturePublisher{}
	service := newMultipartDLRService(t, store, publisher)
	messageID, err := service.Submit(context.Background(), core.SubmitRequest{
		Username:    "alice",
		Destination: "15551230000",
		Content:     strings.Repeat("A", 161),
		DLR:         true,
		DLRUrl:      "http://sink.example/dlr",
		DLRLevel:    3,
		DLRMethod:   "POST",
	})
	if err != nil {
		t.Fatal(err)
	}

	dlrPublisher := &dlrCapturePublisher{}
	correlator := dlr.NewCorrelator(redisClient, dlrPublisher, dlr.Config{})
	firstPublication := submitRespDLRPublication(t, publisher.envelopes[0], "ESME_ROK", "000ABC1")
	if err := correlator.OnSubmitResp(context.Background(), submitRespEvent(t, firstPublication)); !errors.Is(err, dlr.ErrDLRMapNotFound) {
		t.Fatalf("non-DLR part submit_sm_resp error=%v want ErrDLRMapNotFound", err)
	}
	lastPublication := submitRespDLRPublication(t, publisher.envelopes[1], "ESME_RSUBMITFAIL", "")
	if err := correlator.OnSubmitResp(context.Background(), submitRespEvent(t, lastPublication)); err != nil {
		t.Fatalf("DLR-bearing part submit_sm_resp: %v", err)
	}
	if len(dlrPublisher.forwards) != 1 {
		t.Fatalf("forwards=%d want one aggregate failure", len(dlrPublisher.forwards))
	}
	if forward := dlrPublisher.forwards[0]; forward.QueueMsgID != messageID ||
		forward.Level != 1 || forward.Status != "ESME_RSUBMITFAIL" {
		t.Fatalf("failure forward=%+v want aggregate id %q", forward, messageID)
	}
	if err := correlator.OnDeliverReceipt(context.Background(), dlr.DeliverReceiptEvent{
		RawDLRID: "000abc2",
		Base:     dlr.MsgIDBaseSame,
		Status:   "DELIVRD",
	}); !errors.Is(err, dlr.ErrDLRMapNotFound) {
		t.Fatalf("terminal receipt after submit failure error=%v want ErrDLRMapNotFound", err)
	}
}

func TestMultipartSMPPSDLRCorrelatesLastPartToAggregateMessage(t *testing.T) {
	redisClient := rediscompat.NewClient(newMemoryRedis())
	store, err := dlr.NewRequestStore(redisClient)
	if err != nil {
		t.Fatal(err)
	}
	publisher := &envelopeCapturePublisher{}
	service := newMultipartDLRService(t, store, publisher)

	messageID, err := service.Submit(context.Background(), core.SubmitRequest{
		Username:        "alice",
		Destination:     "15551230000",
		From:            "ACME",
		Content:         strings.Repeat("A", 161),
		DLR:             true,
		SourceConnector: "smppsapi",
		SMPPSOrigin: &core.SMPPSOrigin{
			SystemID:           "client-a",
			SourceAddrTON:      "AddrTon.ALPHANUMERIC",
			SourceAddrNPI:      "AddrNpi.UNKNOWN",
			DestinationAddrTON: "AddrTon.INTERNATIONAL",
			DestinationAddrNPI: "AddrNpi.ISDN",
			RegisteredDelivery: "RegisteredDeliveryReceipt.SMSC_DELIVERY_RECEIPT_REQUESTED",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(publisher.envelopes) != 2 {
		t.Fatalf("published parts=%d want 2", len(publisher.envelopes))
	}
	firstPartID := publisher.envelopes[0].Properties().MessageID()
	lastPartID := publisher.envelopes[1].Properties().MessageID()
	if firstPartID != messageID+"/000001" || lastPartID != messageID+"/000002" {
		t.Fatalf("part ids=(%q,%q) aggregate=%q", firstPartID, lastPartID, messageID)
	}

	dlrPublisher := &dlrCapturePublisher{}
	correlator := dlr.NewCorrelator(redisClient, dlrPublisher, dlr.Config{})
	firstPublication := submitRespDLRPublication(t, publisher.envelopes[0], "ESME_ROK", "smsc-part-1")
	err = correlator.OnSubmitResp(context.Background(), submitRespEvent(t, firstPublication))
	if !errors.Is(err, dlr.ErrDLRMapNotFound) {
		t.Fatalf("non-DLR part submit_sm_resp error=%v want ErrDLRMapNotFound", err)
	}
	lastPublication := submitRespDLRPublication(t, publisher.envelopes[1], "ESME_ROK", "000ABC2")
	if err := correlator.OnSubmitResp(context.Background(), submitRespEvent(t, lastPublication)); err != nil {
		t.Fatalf("DLR-bearing part submit_sm_resp: %v", err)
	}
	if len(dlrPublisher.forwards) != 0 {
		t.Fatalf("success submit_sm_resp forwards=%d want 0 with default config", len(dlrPublisher.forwards))
	}

	if err := correlator.OnDeliverReceipt(context.Background(), dlr.DeliverReceiptEvent{
		RawDLRID:    "000abc2",
		Base:        dlr.MsgIDBaseSame,
		ConnectorID: "connector-a",
		Status:      "DELIVRD",
		Err:         "000",
	}); err != nil {
		t.Fatalf("terminal receipt: %v", err)
	}
	if len(dlrPublisher.forwards) != 1 {
		t.Fatalf("terminal forwards=%d want 1", len(dlrPublisher.forwards))
	}
	if forward := dlrPublisher.forwards[0]; forward.Target != dlr.ForwardSMPPS || forward.QueueMsgID != messageID {
		t.Fatalf("terminal forward=%+v want aggregate id %q", forward, messageID)
	}
}

func TestSubmitDLRExpiryFallsBackToDefault(t *testing.T) {
	store := &recordingDLRStore{}
	// Nil expiry provider → the legacy 86400 default.
	service, _ := newSubmitServiceWithDLR(t, store, nil)

	_, err := service.Submit(context.Background(), core.SubmitRequest{
		Username: "alice", Destination: "15551230000", Content: "hi",
		DLR: true, DLRUrl: "http://sink/dlr", DLRLevel: 3, DLRMethod: "POST",
	})
	if err != nil {
		t.Fatal(err)
	}
	if store.request.ExpirySeconds != core.DefaultDLRExpirySeconds {
		t.Fatalf("expiry=%d want %d", store.request.ExpirySeconds, core.DefaultDLRExpirySeconds)
	}
}

func TestSubmitSkipsDLRStoreWhenNotRequested(t *testing.T) {
	store := &recordingDLRStore{}
	service, _ := newSubmitServiceWithDLR(t, store, func(string) int64 { return 3600 })

	// No dlr-url → no record (a bare submit).
	_, err := service.Submit(context.Background(), core.SubmitRequest{
		Username: "alice", Destination: "15551230000", Content: "hi",
	})
	if err != nil {
		t.Fatal(err)
	}
	if store.calls != 0 {
		t.Fatalf("store calls=%d want 0 (DLR not requested)", store.calls)
	}
}

// TestSubmitRegistersSMPPSDLRRecord covers the gap that silently cost every
// SMPP bind customer 100% of their delivery receipts. The egress plumbing was
// complete, but nothing ever wrote the sc=smppsapi dlr:<msgid> record, so the
// correlation legs had nothing to resolve and every receipt was dropped as
// DLRMapNotFound. An ESME reads that as total delivery failure and re-sends.
func TestSubmitRegistersSMPPSDLRRecord(t *testing.T) {
	store := &recordingDLRStore{}
	service, _ := newSubmitServiceWithDLR(t, store, nil)

	request := core.SubmitRequest{
		Username:        "alice",
		Destination:     "447700900000",
		From:            "ACME",
		Content:         "hi",
		DLR:             true,
		SourceConnector: "smppsapi",
		SMPPSOrigin: &core.SMPPSOrigin{
			SystemID:           "client-a",
			SourceAddrTON:      "AddrTon.ALPHANUMERIC",
			SourceAddrNPI:      "AddrNpi.UNKNOWN",
			DestinationAddrTON: "AddrTon.INTERNATIONAL",
			DestinationAddrNPI: "AddrNpi.ISDN",
			RegisteredDelivery: "RegisteredDeliveryReceipt.SMSC_DELIVERY_RECEIPT_REQUESTED",
		},
	}
	messageID, err := service.Submit(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if store.smppsCalls != 1 {
		t.Fatalf("smpps DLR registrations = %d, want 1", store.smppsCalls)
	}
	if store.calls != 0 {
		t.Fatalf("an SMPPs submit wrote the httpapi DLR record %d time(s)", store.calls)
	}
	if store.smppsMsgID != messageID {
		t.Fatalf("record keyed on %q, want the returned message id %q", store.smppsMsgID, messageID)
	}
	got := store.smppsRequest
	if got.SystemID != "client-a" {
		t.Fatalf("system_id = %q — a receipt would not find the bind that submitted", got.SystemID)
	}
	// The addressing must be the ESME's own, not the outbound connector's: the
	// receipt has to be addressed the way the original submit was.
	if got.SourceAddress != "ACME" || got.DestinationAddress != "447700900000" {
		t.Fatalf("addresses = %q -> %q, want ACME -> 447700900000", got.SourceAddress, got.DestinationAddress)
	}
	if got.SourceAddrTON != "AddrTon.ALPHANUMERIC" || got.DestinationAddrNPI != "AddrNpi.ISDN" {
		t.Fatalf("TON/NPI not carried through: %+v", got)
	}
	if got.ExpirySeconds != core.DefaultDLRExpirySeconds {
		t.Fatalf("expiry = %d, want the legacy default %d", got.ExpirySeconds, core.DefaultDLRExpirySeconds)
	}
	if got.SubmissionDate == "" {
		t.Fatal("sub_date is empty; the receipt renders it verbatim")
	}
}

// TestSubmitSkipsSMPPSDLRRecordWhenNoReceiptRequested pins the legacy gate: the
// record is written only when the ESME actually asked for a receipt
// (managers/clients.py:618), not on every SMPP submit.
func TestSubmitSkipsSMPPSDLRRecordWhenNoReceiptRequested(t *testing.T) {
	store := &recordingDLRStore{}
	service, _ := newSubmitServiceWithDLR(t, store, nil)

	if _, err := service.Submit(context.Background(), core.SubmitRequest{
		Username: "alice", Destination: "447700900000", From: "ACME", Content: "hi",
		SourceConnector: "smppsapi",
	}); err != nil {
		t.Fatal(err)
	}
	if store.smppsCalls != 0 {
		t.Fatalf("wrote a DLR record for a submit that requested no receipt (%d)", store.smppsCalls)
	}
}
