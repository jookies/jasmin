package core_test

import (
	"bytes"
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/pumpitspace/jasmin/internal/core"
	"github.com/pumpitspace/jasmin/internal/core/billing"
	"github.com/pumpitspace/jasmin/internal/core/interceptor"
	"github.com/pumpitspace/jasmin/internal/core/routingfilter"
	"github.com/pumpitspace/jasmin/internal/core/routingtable"
	"github.com/pumpitspace/jasmin/internal/core/segmentation"
	"github.com/pumpitspace/jasmin/internal/core/tlv"
	"github.com/pumpitspace/jasmin/internal/transport/amqpcompat"
	"github.com/pumpitspace/jasmin/internal/transport/smppwire"
	"math/big"
)

var errBuild = errors.New("build failed")
var errPublish = errors.New("publish failed")
var errIntercept = errors.New("intercept failed")

type recordingBuilder struct {
	request   core.SubmitEnvelopeRequest
	sequences []uint8
	errAt     int
	hook      func()
}

func (builder *recordingBuilder) BuildSubmitEnvelope(_ context.Context, request core.SubmitEnvelopeRequest, part segmentation.Part) (amqpcompat.Envelope, error) {
	builder.request = request
	builder.sequences = append(builder.sequences, part.Sequence())
	if builder.hook != nil {
		hook := builder.hook
		builder.hook = nil
		hook()
	}
	if builder.errAt > 0 && len(builder.sequences) == builder.errAt {
		return amqpcompat.Envelope{}, errBuild
	}
	properties, err := amqpcompat.NewProperties(request.MessageID, nil)
	if err != nil {
		return amqpcompat.Envelope{}, err
	}
	return amqpcompat.NewEnvelope(
		"submit.sm."+request.ConnectorID,
		properties,
		[]byte{part.Sequence()},
	)
}

type recordingPublisher struct {
	exchanges   []string
	routingKeys []string
	bodies      [][]byte
	failAt      int
}

func (publisher *recordingPublisher) Publish(_ context.Context, exchange, routingKey string, message amqpcompat.Envelope) error {
	publisher.exchanges = append(publisher.exchanges, exchange)
	publisher.routingKeys = append(publisher.routingKeys, routingKey)
	publisher.bodies = append(publisher.bodies, message.Body())
	if publisher.failAt > 0 && len(publisher.bodies) == publisher.failAt {
		return errPublish
	}
	return nil
}

type fixedRunner struct {
	action interceptor.Action
	err    error
}

func (runner fixedRunner) Run(_ context.Context, _ interceptor.Script, request interceptor.Context) (interceptor.Result, error) {
	if runner.err != nil {
		return interceptor.Result{}, runner.err
	}
	return interceptor.Result{Routable: request.Routable, Action: runner.action}, nil
}

func TestSubmitServiceMultipartBuildChargeAndPublish(t *testing.T) {
	user := billing.NewUser(7)
	if err := user.SetBalance(10); err != nil {
		t.Fatal(err)
	}
	if err := user.SetEarlyDecrementPercent(50); err != nil {
		t.Fatal(err)
	}
	user.SetSubmitSmCountQuota(5)
	builder := &recordingBuilder{}
	publisher := &recordingPublisher{}
	service := newSubmitService(t, user, routeTable(t, true), emptyInterceptors(), fixedRunner{}, builder, publisher)

	messageID, err := service.Submit(context.Background(), core.SubmitRequest{
		Username:    "alice",
		Destination: "15551230000",
		Content:     strings.Repeat("A", 161),
		Coding:      0,
		Priority:    2,
		CustomTLVs:  []tlv.TLV{{Tag: big.NewInt(0x1400), Value: "12"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if messageID != "11111111-1111-4111-8111-111111111111" {
		t.Fatalf("message ID=%q", messageID)
	}
	if len(builder.request.Parts) != 2 {
		t.Fatalf("parts=%d want=2", len(builder.request.Parts))
	}
	if len(builder.sequences) != 2 || builder.sequences[0] != 1 || builder.sequences[1] != 2 {
		t.Fatalf("builder sequences=%v want=[1 2]", builder.sequences)
	}
	if builder.request.Bill.SubmitSmAmount != 0.5 || builder.request.Bill.SubmitSmRespAmount != 0.5 || builder.request.Bill.DecrementSubmitSmCount != 1 {
		t.Fatalf("per-part bill=%+v", builder.request.Bill)
	}
	if builder.request.UserID != "user-opaque" {
		t.Fatalf("external user ID=%q want=user-opaque", builder.request.UserID)
	}
	if len(publisher.bodies) != 2 || publisher.bodies[0][0] != 1 || publisher.bodies[1][0] != 2 {
		t.Fatalf("published bodies=%v", publisher.bodies)
	}
	for index := range publisher.bodies {
		if publisher.exchanges[index] != "messaging" || publisher.routingKeys[index] != "submit.sm.connector-a" {
			t.Fatalf("publication %d = %s/%s", index, publisher.exchanges[index], publisher.routingKeys[index])
		}
	}
	state := user.GetState()
	if state.Balance == nil || *state.Balance != 9 {
		t.Fatalf("balance=%v want=9", state.Balance)
	}
	if state.SubmitSmCountQuota == nil || *state.SubmitSmCountQuota != 3 {
		t.Fatalf("count=%v want=3", state.SubmitSmCountQuota)
	}
}

func TestSubmitServicePreservesLegacyFloatOrderAcrossAdmissionAndPerPartProjection(t *testing.T) {
	user := billing.NewUser(7)
	balance := math.Float64frombits(0x3f9eb851eb851eb7)
	if err := user.SetBalance(balance); err != nil {
		t.Fatal(err)
	}
	if err := user.SetEarlyDecrementPercent(7); err != nil {
		t.Fatal(err)
	}
	user.SetSubmitSmCountQuota(3)
	builder := &recordingBuilder{}
	publisher := &recordingPublisher{}
	service := newSubmitService(t, user, routeTableWithRate(t, 0.01), emptyInterceptors(), fixedRunner{}, builder, publisher)

	if _, err := service.Submit(context.Background(), core.SubmitRequest{
		Username: "alice", Destination: "15551230000", Content: strings.Repeat("A", 307), Coding: 0,
	}); err != nil {
		t.Fatalf("legacy equality balance rejected: %v", err)
	}
	if len(builder.request.Parts) != 3 || len(publisher.bodies) != 3 {
		t.Fatalf("parts=%d published=%d want=3", len(builder.request.Parts), len(publisher.bodies))
	}
	if got := math.Float64bits(builder.request.Bill.SubmitSmAmount); got != 0x3f46f0068db8bac8 {
		t.Fatalf("unit early bits=%016x", got)
	}
	if got := math.Float64bits(builder.request.Bill.SubmitSmRespAmount); got != 0x3f830be0ded288ce {
		t.Fatalf("unit late bits=%016x", got)
	}
	wantBalance := balance - math.Float64frombits(0x3f613404ea4a8c16)
	if got := math.Float64bits(user.Balance()); got != math.Float64bits(wantBalance) {
		t.Fatalf("post-debit bits=%016x want=%016x", got, math.Float64bits(wantBalance))
	}
}

func TestSubmitServiceGSM0338ExtensionAffectsMultipartBoundary(t *testing.T) {
	user := fundedUser(t)
	builder := &recordingBuilder{}
	service := newSubmitService(t, user, routeTable(t, true), emptyInterceptors(), fixedRunner{}, builder, &recordingPublisher{})
	if _, err := service.Submit(context.Background(), core.SubmitRequest{
		Username: "alice", Destination: "15551230000", Content: strings.Repeat("^", 81), Coding: 0,
	}); err != nil {
		t.Fatal(err)
	}
	if len(builder.request.Parts) != 2 {
		t.Fatalf("parts=%d want=2 for 162 GSM septets", len(builder.request.Parts))
	}
	first := builder.request.Parts[0].Payload()
	if len(first) != 153 || first[0] != 0x1b || first[1] != 0x14 {
		t.Fatalf("first GSM part len/prefix=(%d,%x)", len(first), first[:min(2, len(first))])
	}
}

func TestSubmitServiceGSM0338ReplacesUnsupportedRune(t *testing.T) {
	user := fundedUser(t)
	builder := &recordingBuilder{}
	service := newSubmitService(t, user, routeTable(t, true), emptyInterceptors(), fixedRunner{}, builder, &recordingPublisher{})
	if _, err := service.Submit(context.Background(), core.SubmitRequest{
		Username: "alice", Destination: "15551230000", Content: "A🙂B", Coding: 0,
	}); err != nil {
		t.Fatal(err)
	}
	payload := builder.request.Parts[0].Payload()
	if string(payload) != "A?B" {
		t.Fatalf("GSM replacement payload=%x", payload)
	}
}

func TestSubmitServicePreservesSMPPsCodingZeroOctets(t *testing.T) {
	user := fundedUser(t)
	builder := &recordingBuilder{}
	service := newSubmitService(t, user, routeTable(t, true), emptyInterceptors(), fixedRunner{}, builder, &recordingPublisher{})
	payload := []byte{0x1b, 0x65, 0xff, 0x00, 0x7f}
	if _, err := service.Submit(context.Background(), core.SubmitRequest{
		Username: "alice", Destination: "15551230000", Content: string(payload), Coding: 0,
		SourceConnector: "smppsapi",
		SMPPSubmit: &smppwire.SubmitSMBody{
			DestinationAddress: []byte("15551230000"),
			DataCoding:         0,
			ShortMessage:       append([]byte(nil), payload...),
		},
	}); err != nil {
		t.Fatal(err)
	}
	if len(builder.request.Parts) != 1 {
		t.Fatalf("parts=%d want 1", len(builder.request.Parts))
	}
	if got := builder.request.Parts[0].Payload(); !bytes.Equal(got, payload) {
		t.Fatalf("SMPPs coding-zero payload changed: got=%x want=%x", got, payload)
	}
	if builder.request.SMPPSubmit == nil || !bytes.Equal(builder.request.SMPPSubmit.ShortMessage, payload) {
		t.Fatalf("raw SMPPs PDU changed: %+v", builder.request.SMPPSubmit)
	}
}

func TestSubmitServiceBuildFailureDoesNotCharge(t *testing.T) {
	user := fundedUser(t)
	builder := &recordingBuilder{errAt: 1}
	service := newSubmitService(t, user, routeTable(t, true), emptyInterceptors(), fixedRunner{}, builder, &recordingPublisher{})
	_, err := service.Submit(context.Background(), core.SubmitRequest{Username: "alice", Destination: "1", Content: "hello"})
	if !errors.Is(err, errBuild) {
		t.Fatalf("error=%v want build failure", err)
	}
	if got := user.Balance(); got != 10 {
		t.Fatalf("balance=%v want=10", got)
	}
}

func TestSubmitServicePublishFailureKeepsLegacyEarlyCharge(t *testing.T) {
	user := fundedUser(t)
	builder := &recordingBuilder{}
	publisher := &recordingPublisher{failAt: 1}
	service := newSubmitService(t, user, routeTable(t, true), emptyInterceptors(), fixedRunner{}, builder, publisher)
	_, err := service.Submit(context.Background(), core.SubmitRequest{Username: "alice", Destination: "1", Content: "hello"})
	if !errors.Is(err, errPublish) {
		t.Fatalf("error=%v want publish failure", err)
	}
	if got := user.Balance(); got != 9 {
		t.Fatalf("balance=%v want=9 (legacy has no refund)", got)
	}
}

func TestSubmitServiceNoRouteDoesNotBuildOrCharge(t *testing.T) {
	user := fundedUser(t)
	builder := &recordingBuilder{}
	service := newSubmitService(t, user, routeTable(t, false), emptyInterceptors(), fixedRunner{}, builder, &recordingPublisher{})
	_, err := service.Submit(context.Background(), core.SubmitRequest{Username: "alice", Destination: "1", Content: "hello"})
	if !errors.Is(err, core.ErrNoRouteMatched) {
		t.Fatalf("error=%v want no route", err)
	}
	if builder.request.MessageID != "" || user.Balance() != 10 {
		t.Fatal("no-route path reached build or billing")
	}
}

func TestSubmitServiceInterceptorRejectDoesNotRouteOrCharge(t *testing.T) {
	user := fundedUser(t)
	tableBuilder := interceptor.NewTableBuilder()
	script := interceptor.Script{IDValue: "reject", PyCode: "action = 'reject'"}
	entry, err := interceptor.NewInterceptor(script)
	if err != nil {
		t.Fatal(err)
	}
	if err := tableBuilder.Add(1, entry); err != nil {
		t.Fatal(err)
	}
	table := tableBuilder.Build()
	builder := &recordingBuilder{}
	service := newSubmitService(t, user, routeTable(t, true), table, fixedRunner{action: interceptor.ActionReject}, builder, &recordingPublisher{})
	_, err = service.Submit(context.Background(), core.SubmitRequest{Username: "alice", Destination: "1", Content: "hello"})
	if !errors.Is(err, core.ErrFilterRejected) {
		t.Fatalf("error=%v want filter rejection", err)
	}
	if builder.request.MessageID != "" || user.Balance() != 10 {
		t.Fatal("rejected path reached build or billing")
	}
}

func TestSubmitServiceInterceptorErrorDoesNotBuildOrCharge(t *testing.T) {
	user := fundedUser(t)
	tableBuilder := interceptor.NewTableBuilder()
	entry, err := interceptor.NewInterceptor(interceptor.Script{IDValue: "error", PyCode: "raise RuntimeError('boom')"})
	if err != nil {
		t.Fatal(err)
	}
	if err := tableBuilder.Add(1, entry); err != nil {
		t.Fatal(err)
	}
	builder := &recordingBuilder{}
	service := newSubmitService(t, user, routeTable(t, true), tableBuilder.Build(), fixedRunner{err: errIntercept}, builder, &recordingPublisher{})
	_, err = service.Submit(context.Background(), core.SubmitRequest{Username: "alice", Destination: "1", Content: "hello"})
	if !errors.Is(err, errIntercept) {
		t.Fatalf("error=%v want interceptor failure", err)
	}
	if builder.request.MessageID != "" || user.Balance() != 10 {
		t.Fatal("interceptor-error path reached build or billing")
	}
}

func TestSubmitServiceQuotaFailureDoesNotPublishOrMutate(t *testing.T) {
	user := billing.NewUser(1)
	if err := user.SetBalance(0.5); err != nil {
		t.Fatal(err)
	}
	builder := &recordingBuilder{}
	publisher := &recordingPublisher{}
	service := newSubmitService(t, user, routeTable(t, true), emptyInterceptors(), fixedRunner{}, builder, publisher)
	_, err := service.Submit(context.Background(), core.SubmitRequest{Username: "alice", Destination: "1", Content: "hello"})
	if !errors.Is(err, core.ErrQuotaExceeded) {
		t.Fatalf("error=%v want quota failure", err)
	}
	if len(publisher.bodies) != 0 || user.Balance() != 0.5 {
		t.Fatalf("quota failure published=%d balance=%v", len(publisher.bodies), user.Balance())
	}
}

func TestSubmitServiceInvalidEnvelopeRouteDoesNotCharge(t *testing.T) {
	user := fundedUser(t)
	builder := wrongRouteBuilder{}
	service := newSubmitService(t, user, routeTable(t, true), emptyInterceptors(), fixedRunner{}, builder, &recordingPublisher{})
	_, err := service.Submit(context.Background(), core.SubmitRequest{Username: "alice", Destination: "1", Content: "hello"})
	if !errors.Is(err, core.ErrInvalidEnvelopeSet) {
		t.Fatalf("error=%v want invalid envelope", err)
	}
	if user.Balance() != 10 {
		t.Fatal("invalid envelope charged user")
	}
}

func TestSubmitServiceRejectsBillStaleAfterEnvelopeBuild(t *testing.T) {
	user := billing.NewUser(1)
	builder := &recordingBuilder{hook: func() {
		if err := user.SetBalance(0); err != nil {
			t.Fatal(err)
		}
		user.SetSubmitSmCountQuota(0)
	}}
	publisher := &recordingPublisher{}
	service := newSubmitService(t, user, routeTable(t, true), emptyInterceptors(), fixedRunner{}, builder, publisher)

	_, err := service.Submit(context.Background(), core.SubmitRequest{Username: "alice", Destination: "1", Content: "hello"})
	if !errors.Is(err, core.ErrQuotaExceeded) {
		t.Fatalf("error=%v want stale-bill quota failure", err)
	}
	if len(publisher.bodies) != 0 || user.Balance() != 0 {
		t.Fatalf("stale bill published=%d balance=%v", len(publisher.bodies), user.Balance())
	}
}

type wrongRouteBuilder struct{}

func (wrongRouteBuilder) BuildSubmitEnvelope(_ context.Context, request core.SubmitEnvelopeRequest, _ segmentation.Part) (amqpcompat.Envelope, error) {
	properties, _ := amqpcompat.NewProperties(request.MessageID, nil)
	envelope, _ := amqpcompat.NewEnvelope("submit.sm.other", properties, []byte("x"))
	return envelope, nil
}

type stableSubmitBoundary struct {
	exists bool
}

func (boundary *stableSubmitBoundary) SubmissionExists(context.Context, string) (bool, error) {
	return boundary.exists, nil
}

func (boundary *stableSubmitBoundary) AdmitSubmit(context.Context, []amqpcompat.Envelope) error {
	return nil
}

func TestStableRecoveredSubmitReturnsBeforeRoutingOrCharging(t *testing.T) {
	user := fundedUser(t)
	users := billing.NewManager()
	if err := users.AddUserWithID("alice", "user-opaque", user); err != nil {
		t.Fatal(err)
	}
	builder := &recordingBuilder{}
	boundary := &stableSubmitBoundary{exists: true}
	routes := routeTable(t, true)
	service, err := core.NewSubmitService(core.SubmitServiceDependencies{
		InterceptorTable: emptyInterceptors(), RoutingTable: &routes,
		BillingUsers: users, EnvelopeBuilder: builder, Transaction: boundary,
	})
	if err != nil {
		t.Fatal(err)
	}
	id, err := service.Submit(context.Background(), core.SubmitRequest{
		MessageID: "rest-task-stable-id", Username: "alice",
		Destination: "15551234567", Content: "hi",
	})
	if err != nil {
		t.Fatal(err)
	}
	if id != "rest-task-stable-id" || len(builder.sequences) != 0 || user.Balance() != 10 {
		t.Fatalf("id=%q builds=%v balance=%v", id, builder.sequences, user.Balance())
	}
}

func TestTrustedManagerSubmitHonorsConnectorBillAndDLRWithoutDoubleCharging(t *testing.T) {
	user := billing.NewUser(7)
	if err := user.SetBalance(10); err != nil {
		t.Fatal(err)
	}
	user.SetSubmitSmCountQuota(5)
	users := billing.NewManager()
	if err := users.AddUserWithID("alice", "user-opaque", user); err != nil {
		t.Fatal(err)
	}
	builder := &recordingBuilder{}
	publisher := &recordingPublisher{}
	store := &recordingDLRStore{}
	routes := routeTable(t, false) // trusted manager submits are already routed
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
		NewReference: func() (uint16, error) { return 41, nil },
		Now:          func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) },
	})
	if err != nil {
		t.Fatal(err)
	}
	suppliedBill := billing.Bill{
		SubmitSmAmount:         0.25,
		SubmitSmRespAmount:     0.75,
		DecrementSubmitSmCount: 1,
		AuthorizationAmount:    1,
	}
	firstPDU := &smppwire.SubmitSMBody{
		SourceAddress: []byte("123"), DestinationAddress: []byte("15551230000"),
		ShortMessage: []byte("part-1"), PriorityFlag: 1,
	}
	secondPDU := &smppwire.SubmitSMBody{
		SourceAddress: []byte("123"), DestinationAddress: []byte("15551230000"),
		ShortMessage: []byte("part-2"), PriorityFlag: 1,
	}
	messageID, err := service.Submit(context.Background(), core.SubmitRequest{
		Username:    "alice",
		Destination: "15551230000",
		HexContent:  "706172742d31",
		Priority:    3,
		DLR:         true,
		DLRUrl:      "https://example.test/dlr",
		DLRLevel:    3,
		DLRMethod:   "POST",
		TrustedManagerSubmit: &core.TrustedManagerSubmit{
			ConnectorID:    "forced-cid",
			BillID:         "legacy-bill-id",
			Bill:           suppliedBill,
			HasBill:        true,
			DLRConnector:   "receipt-cid",
			ValidityPeriod: "2026-07-29 12:34:56.123456",
			SubmitSMChain:  []*smppwire.SubmitSMBody{firstPDU, secondPDU},
		},
		SMPPSubmit: firstPDU,
	})
	if err != nil {
		t.Fatal(err)
	}
	if messageID != "11111111-1111-4111-8111-111111111111" {
		t.Fatalf("message ID=%q", messageID)
	}
	if len(publisher.routingKeys) != 2 || publisher.routingKeys[0] != "submit.sm.forced-cid" ||
		publisher.routingKeys[1] != "submit.sm.forced-cid" {
		t.Fatalf("routing keys=%v", publisher.routingKeys)
	}
	if builder.request.ConnectorID != "forced-cid" ||
		builder.request.BillID != "legacy-bill-id" ||
		builder.request.Bill != suppliedBill ||
		builder.request.Priority != 3 ||
		builder.request.Expiration != "2026-07-29 12:34:56.123456" ||
		len(builder.request.SMPPSubmits) != 2 ||
		string(builder.request.SMPPSubmits[0].ShortMessage) != "part-1" ||
		string(builder.request.SMPPSubmits[1].ShortMessage) != "part-2" ||
		len(builder.sequences) != 2 {
		t.Fatalf("trusted envelope request=%+v", builder.request)
	}
	state := user.GetState()
	if state.Balance == nil || *state.Balance != 10 ||
		state.SubmitSmCountQuota == nil || *state.SubmitSmCountQuota != 5 {
		t.Fatalf("trusted manager submit double charged user: %+v", state)
	}
	if store.calls != 1 || store.request.Connector != "receipt-cid" {
		t.Fatalf("DLR store calls=%d request=%+v", store.calls, store.request)
	}
}

func newSubmitService(
	t *testing.T,
	user *billing.User,
	routes routingtable.Table,
	interceptors *interceptor.Table,
	runner interceptor.Runner,
	builder core.SubmitEnvelopeBuilder,
	publisher core.AMQPPublisher,
) *core.SubmitService {
	t.Helper()
	users := billing.NewManager()
	if err := users.AddUserWithID("alice", "user-opaque", user); err != nil {
		t.Fatal(err)
	}
	service, err := core.NewSubmitService(core.SubmitServiceDependencies{
		InterceptorTable:  interceptors,
		InterceptorRunner: runner,
		RoutingTable:      &routes,
		BillingUsers:      users,
		EnvelopeBuilder:   builder,
		Publisher:         publisher,
		NewMessageID: func() (string, error) {
			return "11111111-1111-4111-8111-111111111111", nil
		},
		NewReference: func() (uint16, error) { return 41, nil },
		Now:          func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) },
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func fundedUser(t *testing.T) *billing.User {
	t.Helper()
	user := billing.NewUser(1)
	if err := user.SetBalance(10); err != nil {
		t.Fatal(err)
	}
	return user
}

func routeTable(t *testing.T, withRoute bool) routingtable.Table {
	t.Helper()
	builder, err := routingtable.NewBuilder(routingfilter.MT)
	if err != nil {
		t.Fatal(err)
	}
	if withRoute {
		route, err := routingtable.NewDefaultRoute(
			routingtable.Connector{IDValue: "connector-a", TypeValue: routingtable.SMPPC},
			1,
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := builder.Add(0, route); err != nil {
			t.Fatal(err)
		}
	}
	return builder.Build()
}

func routeTableWithRate(t *testing.T, rate float64) routingtable.Table {
	t.Helper()
	builder, err := routingtable.NewBuilder(routingfilter.MT)
	if err != nil {
		t.Fatal(err)
	}
	route, err := routingtable.NewDefaultRoute(
		routingtable.Connector{IDValue: "connector-a", TypeValue: routingtable.SMPPC}, rate,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := builder.Add(0, route); err != nil {
		t.Fatal(err)
	}
	return builder.Build()
}

func emptyInterceptors() *interceptor.Table {
	return interceptor.NewTableBuilder().Build()
}
