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

// adminOracleScript builds the real SMPPClientPBConfig ('client-management') and
// RouterPBConfig ('router') and dumps the fields the Go parsers reproduce.
// admin_password is a bytes digest, hex-encoded here so it survives JSON.
const adminOracleScript = `
import json, sys, tempfile, os, binascii
from jasmin.managers.configs import SMPPClientPBConfig
from jasmin.routing.configs import RouterPBConfig
text = sys.stdin.read()
path = tempfile.mktemp(suffix=".cfg")
open(path, "w").write(text)
c = SMPPClientPBConfig(path)
r = RouterPBConfig(path)
os.remove(path)
print(json.dumps({
    "cm": {"bind": c.bind, "port": c.port, "authentication": c.authentication,
           "admin_username": c.admin_username,
           "admin_password": binascii.hexlify(c.admin_password).decode(),
           "pickle_protocol": c.pickle_protocol},
    "router": {"bind": r.bind, "port": r.port, "authentication": r.authentication,
               "admin_username": r.admin_username,
               "admin_password": binascii.hexlify(r.admin_password).decode(),
               "pickle_protocol": r.pickle_protocol,
               "persistence_timer_secs": r.persistence_timer_secs},
}))
`

func TestAdminSectionsDifferentialAgainstLegacy(t *testing.T) {
	pythonPath := os.Getenv("PYTHON_PATH")
	if pythonPath == "" {
		t.Skip("PYTHON_PATH is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Mix overridden and defaulted fields so both the override and the default
	// (including the hex-decoded admin_password default) are checked at once.
	text := "[client-management]\nport = 9100\nadmin_username = ops\n" +
		"[router]\npersistence_timer_secs = 120\n"

	command := exec.CommandContext(ctx, pythonPath, "-c", adminOracleScript)
	command.Env = append(os.Environ(), "PYTHONPATH=../..")
	command.Stdin = bytes.NewReader([]byte(text))
	output, err := command.Output()
	if err != nil {
		t.Fatalf("oracle: %v (%s)", err, output)
	}
	type pbSection struct {
		Bind           string `json:"bind"`
		Port           int    `json:"port"`
		Authentication bool   `json:"authentication"`
		AdminUsername  string `json:"admin_username"`
		AdminPassword  string `json:"admin_password"` // hex
		PickleProtocol int    `json:"pickle_protocol"`
		Persistence    int    `json:"persistence_timer_secs"`
	}
	var oracle struct {
		CM     pbSection `json:"cm"`
		Router pbSection `json:"router"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(output), &oracle); err != nil {
		t.Fatalf("oracle output %q: %v", output, err)
	}

	file, err := config.ParseString(text)
	if err != nil {
		t.Fatal(err)
	}
	cm, err := config.LoadClientManagement(file)
	if err != nil {
		t.Fatal(err)
	}
	if cm.Bind != oracle.CM.Bind || cm.Port != oracle.CM.Port ||
		cm.Authentication != oracle.CM.Authentication || cm.AdminUsername != oracle.CM.AdminUsername ||
		hex.EncodeToString(cm.AdminPassword) != oracle.CM.AdminPassword ||
		cm.PickleProtocol != oracle.CM.PickleProtocol {
		t.Fatalf("client-management diverges:\n  go %+v (pw %x)\n  py %+v", cm, cm.AdminPassword, oracle.CM)
	}
	r, err := config.LoadRouter(file)
	if err != nil {
		t.Fatal(err)
	}
	if r.Bind != oracle.Router.Bind || r.Port != oracle.Router.Port ||
		r.Authentication != oracle.Router.Authentication || r.AdminUsername != oracle.Router.AdminUsername ||
		hex.EncodeToString(r.AdminPassword) != oracle.Router.AdminPassword ||
		r.PickleProtocol != oracle.Router.PickleProtocol ||
		r.PersistenceTimerSecs != oracle.Router.Persistence {
		t.Fatalf("router diverges:\n  go %+v (pw %x)\n  py %+v", r, r.AdminPassword, oracle.Router)
	}
}
