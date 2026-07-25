package config

import (
	"bytes"
	"encoding/hex"
	"testing"
)

func TestLoadClientManagementDefaults(t *testing.T) {
	file := mustParse(t, "[client-management]\n")
	cm, err := LoadClientManagement(file)
	if err != nil {
		t.Fatal(err)
	}
	wantPW, _ := hex.DecodeString(defaultClientMgmtAdminPWHex)
	if cm.Bind != "0.0.0.0" || cm.Port != 8989 || !cm.Authentication ||
		cm.AdminUsername != "cmadmin" || cm.PickleProtocol != 2 ||
		cm.PersistenceTimerSecs != 0 || !bytes.Equal(cm.AdminPassword, wantPW) {
		t.Fatalf("client-management defaults diverge: %+v (pw %x)", cm, cm.AdminPassword)
	}
	if got := cm.BindAddr(); got != "0.0.0.0:8989" {
		t.Fatalf("BindAddr = %q", got)
	}
}

func TestLoadRouterDefaults(t *testing.T) {
	file := mustParse(t, "[router]\n")
	r, err := LoadRouter(file)
	if err != nil {
		t.Fatal(err)
	}
	wantPW, _ := hex.DecodeString(defaultRouterAdminPWHex)
	if r.Bind != "0.0.0.0" || r.Port != 8988 || !r.Authentication ||
		r.AdminUsername != "radmin" || r.PickleProtocol != 2 ||
		r.PersistenceTimerSecs != 60 || !bytes.Equal(r.AdminPassword, wantPW) {
		t.Fatalf("router defaults diverge: %+v (pw %x)", r, r.AdminPassword)
	}
	if got := r.BindAddr(); got != "0.0.0.0:8988" {
		t.Fatalf("BindAddr = %q", got)
	}
}

func TestLoadAdminSectionsExplicitOverrides(t *testing.T) {
	file := mustParse(t, "[client-management]\nbind = 127.0.0.1\nport = 9100\n"+
		"authentication = no\nadmin_username = ops\nadmin_password = 00ff\n"+
		"pickle_protocol = 4\nstore_path = /var/store\n")
	cm, err := LoadClientManagement(file)
	if err != nil {
		t.Fatal(err)
	}
	if cm.Bind != "127.0.0.1" || cm.Port != 9100 || cm.Authentication ||
		cm.AdminUsername != "ops" || cm.PickleProtocol != 4 || cm.StorePath != "/var/store" ||
		!bytes.Equal(cm.AdminPassword, []byte{0x00, 0xff}) {
		t.Fatalf("client-management overrides diverge: %+v", cm)
	}

	file = mustParse(t, "[router]\npersistence_timer_secs = 120\nport = 9200\n")
	r, err := LoadRouter(file)
	if err != nil {
		t.Fatal(err)
	}
	if r.PersistenceTimerSecs != 120 || r.Port != 9200 {
		t.Fatalf("router overrides diverge: %+v", r)
	}
}

func TestLoadAdminSectionsInvalidHexPasswordErrors(t *testing.T) {
	// binascii.unhexlify raises on non-hex or odd-length input.
	for _, bad := range []string{"xyz", "abc"} {
		file := mustParse(t, "[client-management]\nadmin_password = "+bad+"\n")
		if _, err := LoadClientManagement(file); err == nil {
			t.Fatalf("admin_password %q should error", bad)
		}
	}
}

func TestLoadAdminSectionsInvalidPortErrors(t *testing.T) {
	file := mustParse(t, "[router]\nport = notanint\n")
	if _, err := LoadRouter(file); err == nil {
		t.Fatal("non-integer port should error")
	}
}
