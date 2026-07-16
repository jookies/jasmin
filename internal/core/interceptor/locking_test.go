package interceptor_test

import (
	"context"
	"testing"
	"time"

	"github.com/pumpitspace/jasmin/internal/core/interceptor"
	"github.com/pumpitspace/jasmin/internal/core/routingfilter"
)

func TestPythonRunnerLocking(t *testing.T) {
	runner := &interceptor.PythonRunner{}
	
	r, _ := routingfilter.NewRoutable(routingfilter.RoutableInput{
		Direction:  routingfilter.MT,
		SourceAddr: routingfilter.BytesField{Present: true, Value: []byte("original")},
		Timestamp:  time.Now(),
	})

	// Lock source_addr
	r.Lock("source_addr")

	// Script tries to modify locked field
	script := interceptor.Script{
		IDValue: "lock_test",
		PyCode:  "routable.pdu.params['source_addr'] = 'modified'",
	}

	_, err := runner.Run(context.Background(), script, interceptor.Context{Routable: r})
	if err == nil {
		t.Fatal("expected error from python runner when modifying locked field, got nil")
	}

	// Verify the error message contains 'locked'
	expectedErr := "source_addr field is locked"
	if err.Error() != expectedErr {
		t.Fatalf("error=%q want=%q", err.Error(), expectedErr)
	}

	// Script tries to add tag when tags are locked
	r2, _ := routingfilter.NewRoutable(routingfilter.RoutableInput{
		Direction: routingfilter.MT,
		Timestamp: time.Now(),
	})
	r2.Lock("tags")
	script2 := interceptor.Script{
		IDValue: "tag_lock_test",
		PyCode:  "routable.addTag('new_tag')",
	}
	_, err = runner.Run(context.Background(), script2, interceptor.Context{Routable: r2})
	if err == nil {
		t.Fatal("expected error from python runner when adding tag to locked tags, got nil")
	}
	if err.Error() != "tags field is locked" {
		t.Fatalf("error=%q want=%q", err.Error(), "tags field is locked")
	}
}
