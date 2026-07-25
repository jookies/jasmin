package config_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/pumpitspace/jasmin/internal/config"
)

// pbOracleScript builds the real PB admin and client config classes and dumps
// the fields the Go parsers reproduce. admin_password is a bytes digest,
// hex-encoded here so it survives JSON.
const pbOracleScript = `
import json, sys, tempfile, os, binascii
from jasmin.protocols.smpp.configs import SMPPServerPBConfig, SMPPServerPBClientConfig
from jasmin.protocols.cli.configs import JCliConfig
from jasmin.interceptor.configs import InterceptorPBConfig, InterceptorPBClientConfig
text = sys.stdin.read()
path = tempfile.mktemp(suffix=".cfg")
open(path, "w").write(text)
sp = SMPPServerPBConfig(path)
jc = JCliConfig(path)
it = InterceptorPBConfig(path)
spc = SMPPServerPBClientConfig(path)
itc = InterceptorPBClientConfig(path)
os.remove(path)

def admin(c):
    return {"bind": c.bind, "port": c.port, "authentication": c.authentication,
            "admin_username": c.admin_username,
            "admin_password": binascii.hexlify(c.admin_password).decode()}

def client(c):
    return {"host": c.host, "port": c.port, "username": c.username, "password": c.password}

sp_d = admin(sp)
jc_d = admin(jc)
it_d = admin(it); it_d["log_slow_script"] = it.log_slow_script
print(json.dumps({"smpp_server_pb": sp_d, "jcli": jc_d, "interceptor": it_d,
                  "smpp_server_pb_client": client(spc), "interceptor_client": client(itc)}))
`

func TestPBSectionsDifferentialAgainstLegacy(t *testing.T) {
	pythonPath := os.Getenv("PYTHON_PATH")
	if pythonPath == "" {
		t.Skip("PYTHON_PATH is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Mix overridden and defaulted fields across every section.
	text := "[smpp-server-pb]\nport = 15000\n" +
		"[jcli]\nadmin_username = jx\n" +
		"[interceptor]\nlog_slow_script = 3\n" +
		"[smpp-server-pb-client]\nhost = pb1\n" +
		"[interceptor-client]\npassword = secret\n"

	command := exec.CommandContext(ctx, pythonPath, "-c", pbOracleScript)
	command.Env = append(os.Environ(), "PYTHONPATH=../..")
	command.Stdin = bytes.NewReader([]byte(text))
	output, err := command.Output()
	if err != nil {
		t.Fatalf("oracle: %v (%s)", err, output)
	}
	type adminSection struct {
		Bind          string `json:"bind"`
		Port          int    `json:"port"`
		Auth          bool   `json:"authentication"`
		AdminUsername string `json:"admin_username"`
		AdminPassword string `json:"admin_password"` // hex
		LogSlowScript int    `json:"log_slow_script"`
	}
	type clientSection struct {
		Host     string `json:"host"`
		Port     int    `json:"port"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	var oracle struct {
		SMPPServerPB       adminSection  `json:"smpp_server_pb"`
		JCli               adminSection  `json:"jcli"`
		Interceptor        adminSection  `json:"interceptor"`
		SMPPServerPBClient clientSection `json:"smpp_server_pb_client"`
		InterceptorClient  clientSection `json:"interceptor_client"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(output), &oracle); err != nil {
		t.Fatalf("oracle output %q: %v", output, err)
	}

	file, err := config.ParseString(text)
	if err != nil {
		t.Fatal(err)
	}

	assertAdmin := func(name string, got config.PBAdmin, want adminSection) {
		t.Helper()
		if got.Bind != want.Bind || got.Port != want.Port || got.Authentication != want.Auth ||
			got.AdminUsername != want.AdminUsername ||
			hex.EncodeToString(got.AdminPassword) != want.AdminPassword {
			t.Fatalf("%s diverges:\n  go %+v (pw %x)\n  py %+v", name, got, got.AdminPassword, want)
		}
	}
	assertClient := func(name string, got config.PBClient, want clientSection) {
		t.Helper()
		if got.Host != want.Host || got.Port != want.Port ||
			got.Username != want.Username || got.Password != want.Password {
			t.Fatalf("%s diverges:\n  go %+v\n  py %+v", name, got, want)
		}
	}

	sp, err := config.LoadSMPPServerPB(file)
	if err != nil {
		t.Fatal(err)
	}
	assertAdmin("smpp-server-pb", sp, oracle.SMPPServerPB)

	jc, err := config.LoadJCli(file)
	if err != nil {
		t.Fatal(err)
	}
	assertAdmin("jcli", jc, oracle.JCli)

	it, err := config.LoadInterceptor(file)
	if err != nil {
		t.Fatal(err)
	}
	assertAdmin("interceptor", it.PBAdmin, oracle.Interceptor)
	if it.LogSlowScript != oracle.Interceptor.LogSlowScript {
		t.Fatalf("interceptor log_slow_script: go=%d py=%d", it.LogSlowScript, oracle.Interceptor.LogSlowScript)
	}

	spc, err := config.LoadSMPPServerPBClient(file)
	if err != nil {
		t.Fatal(err)
	}
	assertClient("smpp-server-pb-client", spc, oracle.SMPPServerPBClient)

	itc, err := config.LoadInterceptorClient(file)
	if err != nil {
		t.Fatal(err)
	}
	assertClient("interceptor-client", itc, oracle.InterceptorClient)
}
