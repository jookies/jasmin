package smppc

import (
	"context"
	"encoding/pem"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestDialSMPPVerifiesConfiguredTLSCAAndServerName(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	server.StartTLS()
	defer server.Close()
	host, portText, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := net.LookupPort("tcp", portText)
	if err != nil {
		t.Fatal(err)
	}
	certificate := server.Certificate()
	caFile := t.TempDir() + "/ca.pem"
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Raw})
	if err := os.WriteFile(caFile, pemBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	serverName := certificate.DNSNames[0]
	cfg := Config{Host: host, Port: port, TLSEnabled: true, TLSServerName: serverName, TLSCAFile: caFile}
	connection, err := dialSMPP(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	_ = connection.Close()

	cfg.TLSServerName = "wrong.invalid"
	if connection, err = dialSMPP(context.Background(), cfg); err == nil {
		_ = connection.Close()
		t.Fatal("TLS hostname mismatch accepted")
	}
}
