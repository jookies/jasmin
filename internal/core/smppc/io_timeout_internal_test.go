package smppc

import (
	"context"
	"net"
	"testing"
	"time"
)

func TestConnectAndBindUsesSessionInitTimeout(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	address := listener.Addr().(*net.TCPAddr)
	config := Config{
		CID: "bind-timeout", Host: address.IP.String(), Port: address.Port,
		SystemID: "client", TrxTimeout: 0.6, SessionInitTimeout: 0.05,
	}
	connector, err := NewConnector(config, "")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		server, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer server.Close()
		buffer := make([]byte, 1024)
		_, _ = server.Read(buffer)
		time.Sleep(time.Second)
	}()

	started := time.Now()
	if _, err := connector.connectAndBind(context.Background()); err == nil {
		t.Fatal("silent SMSC did not time out the bind")
	}
	if elapsed := time.Since(started); elapsed > 300*time.Millisecond {
		t.Fatalf("bind timed out after %s, want sessionInitTimeout near 50ms", elapsed)
	}
}
