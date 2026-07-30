package smppc_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/pumpitspace/synevyr/internal/core/smppc"
	"github.com/pumpitspace/synevyr/internal/transport/amqpcompat"
	"github.com/pumpitspace/synevyr/internal/transport/smppwire"
)

func TestSessionTimeoutHonorsRequeueDelay(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()
	retry, _ := smppc.NewErrorRetryPolicy(smppc.DefaultErrorRetryRules())
	readiness, _ := smppc.NewReadinessPolicy(smppc.DefaultReadinessConfig())
	session := smppc.NewSession(client, smppc.Config{
		CID: "delayed-requeue", ResTimeout: 0.01, RequeueDelay: 0.2,
	}, retry, readiness, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go session.Run(ctx)

	frameRead := make(chan struct{})
	go func() {
		_, _ = smppwire.Read(server, 1024)
		close(frameRead)
	}()
	props, _ := amqpcompat.NewProperties("delayed-1", nil)
	envelope, _ := amqpcompat.NewEnvelope("submit.sm.delayed-requeue", props, []byte("hello"))
	settled := make(chan bool, 1)
	started := time.Now()
	if err := session.Submit(ctx, injectDelivery(envelope, func(requeue bool) { settled <- requeue })); err != nil {
		t.Fatal(err)
	}
	<-frameRead

	select {
	case requeue := <-settled:
		elapsed := time.Since(started)
		t.Fatalf("delivery settled after %s (requeue=%v), before configured 200ms requeue delay", elapsed, requeue)
	case <-time.After(150 * time.Millisecond):
	}
	select {
	case requeue := <-settled:
		if !requeue {
			t.Fatal("timed-out submit was rejected without requeue")
		}
	case <-time.After(time.Second):
		t.Fatal("timed-out submit was not requeued after configured delay")
	}
}
