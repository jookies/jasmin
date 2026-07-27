package smppsserver

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pumpitspace/jasmin/internal/core/smpps"
	"github.com/pumpitspace/jasmin/internal/transport/smppwire"
)

// selfSignedKeypair writes a throwaway localhost certificate for the listener.
func selfSignedKeypair(t *testing.T) (certFile, keyFile string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certFile = filepath.Join(dir, "cert.pem")
	keyFile = filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	return certFile, keyFile
}

// TestServiceBindOverTLS proves an SMPPS-over-TLS bind: TLS handshake on the
// wrapped listener, then a normal bind_transceiver -> ESME_ROK.
func TestServiceBindOverTLS(t *testing.T) {
	certFile, keyFile := selfSignedKeypair(t)
	submitter := &fakeSubmitter{id: "msg-tls"}
	service, err := NewService(Config{
		BindAddr:    "127.0.0.1:0",
		Users:       []UserConfig{{SystemID: "alice", Password: "secret"}},
		TLSCertFile: certFile,
		TLSKeyFile:  keyFile,
	}, submitter)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- service.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		_ = service.Close()
		<-done
	})

	pool := x509.NewCertPool()
	certPEM, err := os.ReadFile(certFile)
	if err != nil {
		t.Fatal(err)
	}
	if !pool.AppendCertsFromPEM(certPEM) {
		t.Fatal("failed to trust test certificate")
	}
	conn, err := tls.Dial("tcp", service.Addr().String(), &tls.Config{
		RootCAs:    pool,
		ServerName: "127.0.0.1",
		MinVersion: tls.VersionTLS12,
	})
	if err != nil {
		t.Fatalf("TLS dial: %v", err)
	}
	defer conn.Close()

	writePDU(t, conn, smppwire.PDU{
		Header: smppwire.Header{CommandID: smpps.CommandBindTransceiver, SequenceNumber: 1},
		Bind:   &smppwire.BindBody{SystemID: []byte("alice"), Password: []byte("secret"), SystemType: []byte(""), InterfaceVersion: 0x34},
	})
	bindResp := readPDU(t, conn)
	if bindResp.Header.CommandStatus != smpps.StatusROK {
		t.Fatalf("bind over TLS status = %#x", bindResp.Header.CommandStatus)
	}
}

func TestValidateConfigTLSPairing(t *testing.T) {
	base := Config{BindAddr: "127.0.0.1:0", Users: []UserConfig{{SystemID: "u", Password: "p"}}}
	certOnly := base
	certOnly.TLSCertFile = "cert.pem"
	if err := ValidateConfig(certOnly); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("cert without key: err=%v", err)
	}
	keyOnly := base
	keyOnly.TLSKeyFile = "key.pem"
	if err := ValidateConfig(keyOnly); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("key without cert: err=%v", err)
	}
}
