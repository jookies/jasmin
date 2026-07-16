package picklecompat
 
 import (
 	"context"
 	"encoding/base64"
 	"encoding/json"
 	"fmt"
 	"io"
 	"os"
 	"os/exec"
 	"sync"
 )

type Bridge struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *json.Decoder
	mu     sync.Mutex
}

func NewBridge(ctx context.Context, pythonPath string) (*Bridge, error) {
	if pythonPath == "" {
		pythonPath = "python3"
	}

	// Attempt to find script in common locations
	scriptPath := "scripts/pickle_bridge.py"
	if _, err := os.Stat(scriptPath); os.IsNotExist(err) {
		// Try parent directories (for tests)
		scriptPath = "../../../scripts/pickle_bridge.py"
		if _, err := os.Stat(scriptPath); os.IsNotExist(err) {
			return nil, fmt.Errorf("could not find pickle_bridge.py")
		}
	}

	cmd := exec.CommandContext(ctx, pythonPath, scriptPath)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	return &Bridge{
		cmd:    cmd,
		stdin:  stdin,
		stdout: json.NewDecoder(stdoutPipe),
	}, nil
}

func (b *Bridge) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.stdin.Close()
	return b.cmd.Wait()
}

type bridgeRequest struct {
	Action string `json:"action"`
	Data   string `json:"data,omitempty"`
	Result any    `json:"result,omitempty"`
}

type bridgeResponse struct {
	Status  string          `json:"status"`
	Message string          `json:"message,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Data    string          `json:"data,omitempty"`
}

func (b *Bridge) Decode(ctx context.Context, data []byte) (json.RawMessage, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	req := bridgeRequest{
		Action: "decode",
		Data:   base64.StdEncoding.EncodeToString(data),
	}
	if err := json.NewEncoder(b.stdin).Encode(req); err != nil {
		return nil, err
	}

	var res bridgeResponse
	if err := b.stdout.Decode(&res); err != nil {
		return nil, err
	}

	if res.Status != "ok" {
		return nil, fmt.Errorf("bridge error: %s", res.Message)
	}

	return res.Result, nil
}

func (b *Bridge) Encode(ctx context.Context, obj any) ([]byte, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	req := bridgeRequest{
		Action: "encode",
		Result: obj,
	}
	if err := json.NewEncoder(b.stdin).Encode(req); err != nil {
		return nil, err
	}

	var res bridgeResponse
	if err := b.stdout.Decode(&res); err != nil {
		return nil, err
	}

	if res.Status != "ok" {
		return nil, fmt.Errorf("bridge error: %s", res.Message)
	}

	return base64.StdEncoding.DecodeString(res.Data)
}
