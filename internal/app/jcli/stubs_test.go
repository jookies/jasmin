package jcli

import (
	"context"

	"github.com/pumpitspace/jasmin/internal/app/admin"
	"github.com/pumpitspace/jasmin/internal/core/smppc"
)

// stubManager is a no-op ConnectorManager: the console tests care about what the
// admin services report, not about real SMPP sessions.
type stubManager struct {
	added   map[string]smppc.Config
	started map[string]bool
}

func newStubManager() *stubManager {
	return &stubManager{added: map[string]smppc.Config{}, started: map[string]bool{}}
}

func (m *stubManager) Add(cfg smppc.Config) error    { m.added[cfg.CID] = cfg; return nil }
func (m *stubManager) Update(cfg smppc.Config) error { m.added[cfg.CID] = cfg; return nil }
func (m *stubManager) Remove(cid string) error {
	delete(m.added, cid)
	delete(m.started, cid)
	return nil
}
func (m *stubManager) Start(cid string) error { m.started[cid] = true; return nil }
func (m *stubManager) Stop(cid string) error  { m.started[cid] = false; return nil }
func (m *stubManager) Status(cid string) (smppc.ManagedStatus, error) {
	return smppc.ManagedStatus{CID: cid, Desired: m.started[cid], Observed: smppc.StatusDisconnected}, nil
}

type stubProvisioner struct{}

func (stubProvisioner) ApplyRoutes(context.Context, []string) error { return nil }

type stubMOProvisioner struct{}

func (stubMOProvisioner) ApplyMORoutes(context.Context, []string) error { return nil }

type stubInterceptorProvisioner struct{}

func (stubInterceptorProvisioner) ApplyInterceptors(context.Context, admin.InterceptorDirection, []string) error {
	return nil
}

type stubGroupProvisioner struct{}

func (stubGroupProvisioner) AddGroup(string, string, int64) error { return nil }
func (stubGroupProvisioner) RemoveGroup(string) error             { return nil }
func (stubGroupProvisioner) ConfigGroupFloor() int64              { return 0 }

type stubUserProvisioner struct{}

func (stubUserProvisioner) AddUser(string, string, int64) error { return nil }
func (stubUserProvisioner) RemoveUser(string) error             { return nil }
func (stubUserProvisioner) ConfigUserFloor() int64              { return 0 }

// stubConfig is a minimal valid connector config for console listing tests.
func stubConfig(cid string) smppc.Config {
	return smppc.Config{
		CID:      cid,
		Host:     "smsc.example.com",
		Port:     2775,
		SystemID: "jasmin",
		Password: "secret-bind-pw",
		Bind:     smppc.BindTransceiver,
	}
}
