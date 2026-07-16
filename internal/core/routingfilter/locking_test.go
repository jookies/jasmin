package routingfilter_test

import (
	"errors"
	"testing"
	"time"

	"github.com/pumpitspace/jasmin/internal/core/routingfilter"
)

func TestRoutableLocking(t *testing.T) {
	routable, err := routingfilter.NewRoutable(routingfilter.RoutableInput{
		Direction:       routingfilter.MT,
		SourceAddr:      routingfilter.BytesField{Present: true, Value: []byte("original_src")},
		DestinationAddr: routingfilter.BytesField{Present: true, Value: []byte("original_dest")},
		ShortMessage:    routingfilter.BytesField{Present: true, Value: []byte("original_msg")},
		Tags:            []string{"tag1"},
		Timestamp:       time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}

	// Lock source_addr
	routable.Lock("source_addr")
	if !routable.IsLocked("source_addr") {
		t.Fatal("source_addr should be locked")
	}

	// Try to mutate locked field
	err = routable.SetSourceAddr([]byte("new_src"))
	if !errors.Is(err, routingfilter.ErrFieldLocked) {
		t.Fatalf("expected ErrFieldLocked, got %v", err)
	}

	// Try to mutate unlocked field
	err = routable.SetDestinationAddr([]byte("new_dest"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Lock tags
	routable.Lock("tags")
	err = routable.AddTag("tag2")
	if !errors.Is(err, routingfilter.ErrFieldLocked) {
		t.Fatalf("expected ErrFieldLocked for AddTag, got %v", err)
	}
	err = routable.RemoveTag("tag1")
	if !errors.Is(err, routingfilter.ErrFieldLocked) {
		t.Fatalf("expected ErrFieldLocked for RemoveTag, got %v", err)
	}

	// Unlock and mutate
	routable.Unlock("source_addr")
	if routable.IsLocked("source_addr") {
		t.Fatal("source_addr should be unlocked")
	}
	err = routable.SetSourceAddr([]byte("new_src"))
	if err != nil {
		t.Fatalf("unexpected error after unlock: %v", err)
	}

	// Verify locked list
	locked := routable.Locked()
	if len(locked) != 1 || locked[0] != "tags" {
		t.Fatalf("unexpected locked list: %v", locked)
	}
}
