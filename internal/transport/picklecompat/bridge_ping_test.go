package picklecompat_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/pumpitspace/jasmin/internal/transport/picklecompat"
)

// TestBridgePing proves the ping action round-trips through the live
// subprocess loop — the /health bridge-liveness probe depends on it.
func TestBridgePing(t *testing.T) {
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

	for i := 0; i < 3; i++ {
		if err := bridge.Ping(ctx); err != nil {
			t.Fatalf("ping %d: %v", i, err)
		}
	}
}
