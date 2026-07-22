package picklecompat_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/pumpitspace/jasmin/internal/transport/picklecompat"
)

func TestDecodeSubmitSMRejectsNonProtocol2TrailingAndInvalidSAR(t *testing.T) {
	pythonPath := os.Getenv("PYTHON_PATH")
	if pythonPath == "" {
		t.Skip("PYTHON_PATH is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	bridge, err := picklecompat.NewBridge(ctx, pythonPath)
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Close()

	valid, err := bridge.EncodeSubmitSM(ctx, picklecompat.SubmitSMEncodeRequest{
		Sequence: 1, DestinationAddr: picklecompat.Bytes("15551234567"),
		ShortMessage: picklecompat.Bytes("hello"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(valid.Body) < 2 || valid.Body[0] != 0x80 || valid.Body[1] != 0x02 {
		t.Fatalf("encoder did not produce protocol 2: %x", valid.Body[:2])
	}

	protocol4 := append([]byte(nil), valid.Body...)
	protocol4[1] = 0x04 // protocol-2 opcodes are a valid subset under PROTO 4.
	for name, body := range map[string][]byte{
		"protocol4": protocol4,
		"trailing":  append(append([]byte(nil), valid.Body...), 0x00),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := bridge.DecodeSubmitSM(ctx, body)
			if !errors.Is(err, picklecompat.ErrSubmitSMPoison) {
				t.Fatalf("error=%v, want poison", err)
			}
		})
	}

	invalidSAR, err := bridge.EncodeSubmitSM(ctx, picklecompat.SubmitSMEncodeRequest{
		Sequence: 1, DestinationAddr: picklecompat.Bytes("15551234567"),
		ShortMessage: picklecompat.Bytes("hello"),
		SAR:          &picklecompat.SubmitSMSAR{Reference: 7, Total: 0, Sequence: 0},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = bridge.DecodeSubmitSM(ctx, invalidSAR.Body)
	if !errors.Is(err, picklecompat.ErrSubmitSMPoison) {
		t.Fatalf("invalid SAR error=%v, want poison", err)
	}
}

func TestEncodeSubmitSMResponseUsesProtocol2(t *testing.T) {
	pythonPath := os.Getenv("PYTHON_PATH")
	if pythonPath == "" {
		t.Skip("PYTHON_PATH is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	bridge, err := picklecompat.NewBridge(ctx, pythonPath)
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Close()
	body, err := bridge.EncodeSubmitSMResponse(ctx, 0, 7, []byte("smsc-1"))
	if err != nil {
		t.Fatal(err)
	}
	if len(body) < 2 || body[0] != 0x80 || body[1] != 0x02 {
		t.Fatalf("response is not protocol 2: %x", body[:2])
	}
}
