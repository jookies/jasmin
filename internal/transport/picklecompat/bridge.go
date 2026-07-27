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

// decodeResponse makes bridge reads context-cancellable. A timed-out request
// kills the subprocess before releasing the serialization mutex, so no later
// request can consume an abandoned response.
func (b *Bridge) decodeResponse(ctx context.Context, response *bridgeResponse) error {
	done := make(chan error, 1)
	go func() { done <- b.stdout.Decode(response) }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		if b.cmd != nil && b.cmd.Process != nil {
			_ = b.cmd.Process.Kill()
		}
		<-done
		return ctx.Err()
	}
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
	if err := b.decodeResponse(ctx, &res); err != nil {
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
	if err := b.decodeResponse(ctx, &res); err != nil {
		return nil, err
	}

	if res.Status != "ok" {
		return nil, fmt.Errorf("bridge error: %s", res.Message)
	}

	return base64.StdEncoding.DecodeString(res.Data)
}

// Ping round-trips a no-op action through the subprocess, proving the bridge
// loop is alive and serving requests — not merely that the process exists.
// It shares the request mutex, so a hung in-flight request delays it; callers
// probing health should bound the wait themselves and treat a timeout as
// unhealthy rather than assume Ping returns promptly.
func (b *Bridge) Ping(ctx context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := json.NewEncoder(b.stdin).Encode(bridgeRequest{Action: "ping"}); err != nil {
		return fmt.Errorf("send bridge ping: %w", err)
	}
	var response bridgeResponse
	if err := b.decodeResponse(ctx, &response); err != nil {
		return fmt.Errorf("read bridge ping response: %w", err)
	}
	if response.Status != "ok" {
		return fmt.Errorf("bridge ping error: %s", response.Message)
	}
	return nil
}

// EncodeSubmitSMResponse creates the exact protocol-2 SubmitSMResp object used
// by the legacy response listener. The action is fixed; callers cannot choose a
// Python class or arbitrary constructor.
func (b *Bridge) EncodeSubmitSMResponse(ctx context.Context, commandStatus, sequence uint32, messageID []byte) ([]byte, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	request := bridgeRequest{Action: "encode_submit_sm_resp", Result: map[string]any{
		"command_status": commandStatus,
		"sequence":       sequence,
		"message_id":     base64.StdEncoding.EncodeToString(messageID),
	}}
	if err := json.NewEncoder(b.stdin).Encode(request); err != nil {
		return nil, fmt.Errorf("send SubmitSMResp bridge request: %w", err)
	}
	var response bridgeResponse
	if err := b.decodeResponse(ctx, &response); err != nil {
		return nil, fmt.Errorf("read SubmitSMResp bridge response: %w", err)
	}
	if response.Status != "ok" {
		return nil, fmt.Errorf("SubmitSMResp bridge error: %s", response.Message)
	}
	data, err := base64.StdEncoding.DecodeString(response.Data)
	if err != nil {
		return nil, fmt.Errorf("decode SubmitSMResp body: %w", err)
	}
	return data, nil
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
	if err := b.decodeResponse(ctx, &response); err != nil {
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
