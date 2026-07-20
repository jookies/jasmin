package picklecompat

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
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

	scriptPath, rootDir, err := locateBridgeScript()
	if err != nil {
		return nil, err
	}

	cmd := exec.CommandContext(ctx, pythonPath, scriptPath)
	cmd.Dir = rootDir
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

func locateBridgeScript() (scriptPath, rootDir string, err error) {
	current, err := os.Getwd()
	if err != nil {
		return "", "", fmt.Errorf("resolve working directory: %w", err)
	}
	for {
		candidate := filepath.Join(current, "scripts", "pickle_bridge.py")
		if info, statErr := os.Stat(candidate); statErr == nil && !info.IsDir() {
			absolute, absErr := filepath.Abs(candidate)
			if absErr != nil {
				return "", "", absErr
			}
			return absolute, current, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	return "", "", fmt.Errorf("could not find scripts/pickle_bridge.py from working directory")
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

// EncodeSubmitSM invokes the bridge's fixed allowlisted protocol-2 encoder.
// Unlike Encode, callers cannot select an arbitrary Python class.
func (b *Bridge) EncodeSubmitSM(ctx context.Context, request SubmitSMEncodeRequest) (SubmitSMEncodeResult, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return SubmitSMEncodeResult{}, err
	}
	if err := json.NewEncoder(b.stdin).Encode(bridgeRequest{Action: "encode_submit_sm", Result: request}); err != nil {
		return SubmitSMEncodeResult{}, err
	}
	var response bridgeResponse
	if err := b.stdout.Decode(&response); err != nil {
		return SubmitSMEncodeResult{}, err
	}
	if response.Status != "ok" {
		return SubmitSMEncodeResult{}, fmt.Errorf("bridge error: %s", response.Message)
	}
	var wire struct {
		Body string  `json:"body"`
		Bill *string `json:"bill"`
	}
	if err := json.Unmarshal(response.Result, &wire); err != nil {
		return SubmitSMEncodeResult{}, fmt.Errorf("decode SubmitSM bridge response: %w", err)
	}
	body, err := base64.StdEncoding.DecodeString(wire.Body)
	if err != nil {
		return SubmitSMEncodeResult{}, fmt.Errorf("decode SubmitSM body: %w", err)
	}
	result := SubmitSMEncodeResult{Body: body}
	if wire.Bill != nil {
		result.Bill, err = base64.StdEncoding.DecodeString(*wire.Bill)
		if err != nil {
			return SubmitSMEncodeResult{}, fmt.Errorf("decode SubmitSM bill: %w", err)
		}
	}
	return result, nil
}
