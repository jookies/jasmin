package core_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/pumpitspace/jasmin/internal/core"
	"github.com/pumpitspace/jasmin/internal/core/billing"
	"github.com/pumpitspace/jasmin/internal/core/interceptor"
	"github.com/pumpitspace/jasmin/internal/core/routingfilter"
	"github.com/pumpitspace/jasmin/internal/core/routingtable"
	"github.com/pumpitspace/jasmin/internal/core/segmentation"
	"github.com/pumpitspace/jasmin/internal/transport/amqpcompat"
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
		CustomTLVs:  map[uint16][]byte{0x1400: {0x01, 0x02}},
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

func emptyInterceptors() *interceptor.Table {
	return interceptor.NewTableBuilder().Build()
}
