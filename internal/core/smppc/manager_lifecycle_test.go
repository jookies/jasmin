package smppc_test

import (
	"testing"

	"github.com/pumpitspace/jasmin/internal/core/smppc"
)

func TestManagerDesiredObservedLifecycleAndUpdate(t *testing.T) {
	manager := smppc.NewManager("")
	config := smppc.Config{CID: "managed", Host: "127.0.0.1", Port: 1, SystemID: "system", ConFailDelay: 0.01}
	if err := manager.Add(config); err != nil {
		t.Fatal(err)
	}
	if stats := manager.Stats(); stats.Total != 1 || stats.Desired != 0 || stats.Disconnected != 1 {
		t.Fatalf("initial stats=%+v", stats)
	}
	if err := manager.Start("managed"); err != nil {
		t.Fatal(err)
	}
	status, err := manager.Status("managed")
	if err != nil {
		t.Fatal(err)
	}
	if !status.Desired {
		t.Fatalf("status=%+v", status)
	}
	updated := config
	updated.Port = 2
	if err := manager.Update(updated); err != nil {
		t.Fatal(err)
	}
	status, err = manager.Status("managed")
	if err != nil {
		t.Fatal(err)
	}
	if !status.Desired || status.Config.Port != 2 {
		t.Fatalf("updated status=%+v", status)
	}
	if err := manager.Stop("managed"); err != nil {
		t.Fatal(err)
	}
	status, err = manager.Status("managed")
	if err != nil {
		t.Fatal(err)
	}
	if status.Desired || status.Observed != smppc.StatusDisconnected {
		t.Fatalf("stopped status=%+v", status)
	}
	if err := manager.Remove("managed"); err != nil {
		t.Fatal(err)
	}
}
