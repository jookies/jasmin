package config

import (
	"bytes"
	"encoding/hex"
	"testing"
)

func TestLoadSMPPServerPBDefaults(t *testing.T) {
	admin, err := LoadSMPPServerPB(mustParse(t, "[smpp-server-pb]\n"))
	if err != nil {
		t.Fatal(err)
	}
	wantPW, _ := hex.DecodeString(defaultSMPPServerPBAdminPWHex)
	if admin.Bind != "0.0.0.0" || admin.Port != 14000 || !admin.Authentication ||
		admin.AdminUsername != "smppsadmin" || !bytes.Equal(admin.AdminPassword, wantPW) {
		t.Fatalf("smpp-server-pb defaults diverge: %+v", admin)
	}
	if got := admin.BindAddr(); got != "0.0.0.0:14000" {
		t.Fatalf("BindAddr = %q", got)
	}
}

func TestLoadJCliDefaults(t *testing.T) {
	admin, err := LoadJCli(mustParse(t, "[jcli]\n"))
	if err != nil {
		t.Fatal(err)
	}
	wantPW, _ := hex.DecodeString(defaultJCliAdminPWHex)
	// jcli binds loopback by default, unlike the other PB admin sections.
	if admin.Bind != "127.0.0.1" || admin.Port != 8990 || !admin.Authentication ||
		admin.AdminUsername != "jcliadmin" || !bytes.Equal(admin.AdminPassword, wantPW) {
		t.Fatalf("jcli defaults diverge: %+v", admin)
	}
}

func TestLoadInterceptorDefaults(t *testing.T) {
	interceptor, err := LoadInterceptor(mustParse(t, "[interceptor]\n"))
	if err != nil {
		t.Fatal(err)
	}
	wantPW, _ := hex.DecodeString(defaultInterceptorAdminPWHex)
	if interceptor.Bind != "0.0.0.0" || interceptor.Port != 8987 || !interceptor.Authentication ||
		interceptor.AdminUsername != "iadmin" || !bytes.Equal(interceptor.AdminPassword, wantPW) ||
		interceptor.LogSlowScript != 1 {
		t.Fatalf("interceptor defaults diverge: %+v", interceptor)
	}
}

func TestLoadPBClientsDefaults(t *testing.T) {
	sc, err := LoadSMPPServerPBClient(mustParse(t, "[smpp-server-pb-client]\n"))
	if err != nil {
		t.Fatal(err)
	}
	if sc.Host != "127.0.0.1" || sc.Port != 14000 || sc.Username != "smppsadmin" || sc.Password != "smppspwd" {
		t.Fatalf("smpp-server-pb-client defaults diverge: %+v", sc)
	}
	if got := sc.Addr(); got != "127.0.0.1:14000" {
		t.Fatalf("Addr = %q", got)
	}
	ic, err := LoadInterceptorClient(mustParse(t, "[interceptor-client]\n"))
	if err != nil {
		t.Fatal(err)
	}
	if ic.Host != "127.0.0.1" || ic.Port != 8987 || ic.Username != "iadmin" || ic.Password != "ipwd" {
		t.Fatalf("interceptor-client defaults diverge: %+v", ic)
	}
}

func TestLoadPBSectionsOverrides(t *testing.T) {
	interceptor, err := LoadInterceptor(mustParse(t, "[interceptor]\nbind = 10.0.0.1\nport = 9000\n"+
		"authentication = no\nadmin_username = ix\nadmin_password = 00ff\nlog_slow_script = 5\n"))
	if err != nil {
		t.Fatal(err)
	}
	if interceptor.Bind != "10.0.0.1" || interceptor.Port != 9000 || interceptor.Authentication ||
		interceptor.AdminUsername != "ix" || !bytes.Equal(interceptor.AdminPassword, []byte{0x00, 0xff}) ||
		interceptor.LogSlowScript != 5 {
		t.Fatalf("interceptor overrides diverge: %+v", interceptor)
	}
	ic, err := LoadInterceptorClient(mustParse(t, "[interceptor-client]\nhost = pbhost\nport = 9001\n"+
		"username = u\npassword = p\n"))
	if err != nil {
		t.Fatal(err)
	}
	if ic.Host != "pbhost" || ic.Port != 9001 || ic.Username != "u" || ic.Password != "p" {
		t.Fatalf("interceptor-client overrides diverge: %+v", ic)
	}
}

func TestLoadPBSectionsErrors(t *testing.T) {
	if _, err := LoadJCli(mustParse(t, "[jcli]\nadmin_password = zz\n")); err == nil {
		t.Fatal("non-hex admin_password should error")
	}
	if _, err := LoadInterceptor(mustParse(t, "[interceptor]\nlog_slow_script = soon\n")); err == nil {
		t.Fatal("non-integer log_slow_script should error")
	}
	if _, err := LoadSMPPServerPBClient(mustParse(t, "[smpp-server-pb-client]\nport = notint\n")); err == nil {
		t.Fatal("non-integer port should error")
	}
}
