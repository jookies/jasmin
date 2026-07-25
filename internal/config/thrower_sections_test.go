package config

import "testing"

func TestLoadDeliverSMThrowerDefaults(t *testing.T) {
	file := mustParse(t, "[deliversm-thrower]\n")
	th, err := LoadDeliverSMThrower(file)
	if err != nil {
		t.Fatal(err)
	}
	if th.TimeoutSecs != 30 || th.RetryDelaySecs != 30 || th.MaxRetries != 3 || th.DLRPDU != "" {
		t.Fatalf("deliversm-thrower defaults diverge: %+v", th)
	}
}

func TestLoadDLRThrowerDefaults(t *testing.T) {
	file := mustParse(t, "[dlr-thrower]\n")
	th, err := LoadDLRThrower(file)
	if err != nil {
		t.Fatal(err)
	}
	if th.TimeoutSecs != 30 || th.RetryDelaySecs != 30 || th.MaxRetries != 3 || th.DLRPDU != "deliver_sm" {
		t.Fatalf("dlr-thrower defaults diverge: %+v", th)
	}
}

func TestLoadThrowersOverrides(t *testing.T) {
	file := mustParse(t, "[deliversm-thrower]\nhttp_timeout = 10\nretry_delay = 5\nmax_retries = 7\n"+
		"[dlr-thrower]\nhttp_timeout = 20\nretry_delay = 15\nmax_retries = 1\ndlr_pdu = data_sm\n")
	ds, err := LoadDeliverSMThrower(file)
	if err != nil {
		t.Fatal(err)
	}
	if ds.TimeoutSecs != 10 || ds.RetryDelaySecs != 5 || ds.MaxRetries != 7 {
		t.Fatalf("deliversm-thrower overrides diverge: %+v", ds)
	}
	dlr, err := LoadDLRThrower(file)
	if err != nil {
		t.Fatal(err)
	}
	if dlr.TimeoutSecs != 20 || dlr.RetryDelaySecs != 15 || dlr.MaxRetries != 1 || dlr.DLRPDU != "data_sm" {
		t.Fatalf("dlr-thrower overrides diverge: %+v", dlr)
	}
}

func TestLoadThrowerInvalidIntErrors(t *testing.T) {
	file := mustParse(t, "[dlr-thrower]\nmax_retries = lots\n")
	if _, err := LoadDLRThrower(file); err == nil {
		t.Fatal("non-integer max_retries should error")
	}
}
