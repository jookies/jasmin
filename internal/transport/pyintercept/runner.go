// Package pyintercept runs jasmin interception scripts (Python, by the legacy
// contract) in a subprocess and adapts them to the core interceptor.Runner
// seam. It mirrors picklecompat's bridge lifecycle: a long-lived
// scripts/interceptor_runner.py process spoken to over JSON lines, with
// context-cancellable reads.
package pyintercept

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
	"time"

	"github.com/pumpitspace/synevyr/internal/core/interceptor"
	"github.com/pumpitspace/synevyr/internal/core/routingfilter"
)

// Runner is a live interceptor-script subprocess. Safe for concurrent Run
// calls: each is serialised on the request mutex.
type Runner struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *json.Decoder
	mu     sync.Mutex
	// pythonPath and scriptPath are retained so a killed subprocess can be
	// respawned. Without them a single cancelled request would end interception
	// for the process lifetime.
	pythonPath string
	scriptPath string
	rootDir    string
	// closed marks a deliberate shutdown, so Run does not resurrect the
	// subprocess after Close.
	closed bool
}

// NewRunner starts the Python interceptor runner. pythonPath defaults to
// "python3"; the script is discovered by walking up from the working directory
// like the pickle bridge.
func NewRunner(ctx context.Context, pythonPath string) (*Runner, error) {
	if pythonPath == "" {
		pythonPath = "python3"
	}
	scriptPath, rootDir, err := locateRunnerScript()
	if err != nil {
		return nil, err
	}
	runner := &Runner{pythonPath: pythonPath, scriptPath: scriptPath, rootDir: rootDir}
	if err := runner.spawn(ctx); err != nil {
		return nil, err
	}
	return runner, nil
}

// spawn starts (or restarts) the subprocess. Callers hold r.mu, except
// NewRunner, which has not published the Runner yet.
func (r *Runner) spawn(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// The caller's context bounds startup, not the subprocess lifetime. Using
	// CommandContext here tied a healthy long-lived runner to a transient
	// request/startup context and could kill it after NewRunner had returned.
	cmd := exec.Command(r.pythonPath, r.scriptPath)
	cmd.Dir = r.rootDir
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	r.cmd, r.stdin, r.stdout = cmd, stdin, json.NewDecoder(stdout)
	return nil
}

// ensureProcess starts a missing worker. Callers hold r.mu.
func (r *Runner) ensureProcess(ctx context.Context) error {
	if r.closed {
		return fmt.Errorf("interceptor runner is closed")
	}
	if r.cmd != nil {
		return nil
	}
	if err := r.spawn(ctx); err != nil {
		return fmt.Errorf("restart interceptor runner: %w", err)
	}
	return nil
}

// invalidateProcess kills, reaps, and clears a worker after a transport or
// protocol failure. A failed write may have been partial and a failed decode
// leaves framing ambiguous, so the current request is never replayed
// automatically; the next independent request starts a clean worker.
// Callers hold r.mu.
func (r *Runner) invalidateProcess() {
	if r.cmd == nil {
		r.stdin, r.stdout = nil, nil
		return
	}
	if r.stdin != nil {
		_ = r.stdin.Close()
	}
	if r.cmd.Process != nil {
		_ = r.cmd.Process.Kill()
	}
	_ = r.cmd.Wait()
	r.cmd, r.stdin, r.stdout = nil, nil, nil
}

func locateRunnerScript() (scriptPath, rootDir string, err error) {
	current, err := os.Getwd()
	if err != nil {
		return "", "", fmt.Errorf("resolve working directory: %w", err)
	}
	for {
		candidate := filepath.Join(current, "scripts", "interceptor_runner.py")
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
	return "", "", fmt.Errorf("could not find scripts/interceptor_runner.py from working directory")
}

// Close stops the subprocess and prevents any further respawn.
func (r *Runner) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
	if r.cmd == nil {
		// Already reaped by a cancelled request; nothing to wait for.
		return nil
	}
	_ = r.stdin.Close()
	err := r.cmd.Wait()
	r.cmd, r.stdin, r.stdout = nil, nil, nil
	return err
}

type runRequest struct {
	Action     string       `json:"action"`
	PyCode     string       `json:"py_code"`
	Routable   routableWire `json:"routable"`
	SMPPStatus *int         `json:"smpp_status"`
	HTTPStatus *int         `json:"http_status"`
}

type routableWire struct {
	SourceAddr      string   `json:"source_addr"`
	DestinationAddr string   `json:"destination_addr"`
	ShortMessage    string   `json:"short_message"`
	Tags            []string `json:"tags"`
}

type runResponse struct {
	Status     string       `json:"status"`
	Message    string       `json:"message"`
	Action     string       `json:"action"`
	SMPPStatus *int         `json:"smpp_status"`
	HTTPStatus *int         `json:"http_status"`
	Routable   routableWire `json:"routable"`
	DelayMS    int64        `json:"delay_ms"`
}

// Run executes one interceptor script against the routable, mapping the
// mutated fields and status back into a Result. It satisfies
// interceptor.Runner.
func (r *Runner) Run(ctx context.Context, script interceptor.Script, req interceptor.Context) (interceptor.Result, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return interceptor.Result{}, err
	}
	// A previous cancellation, EOF, crash, broken pipe, or malformed response
	// clears the subprocess to resynchronise the protocol. Bring it back before
	// writing so one failed script cannot disable interception process-wide.
	if err := r.ensureProcess(ctx); err != nil {
		return interceptor.Result{}, err
	}

	source := req.Routable.SourceAddr()
	destination := req.Routable.DestinationAddr()
	message := req.Routable.ShortMessage()
	request := runRequest{
		Action: "run",
		PyCode: script.PyCode,
		Routable: routableWire{
			SourceAddr:      encodeField(source),
			DestinationAddr: encodeField(destination),
			ShortMessage:    encodeField(message),
			Tags:            req.Routable.Tags(),
		},
		SMPPStatus: statusPtr(req.SMPPStatus),
		HTTPStatus: statusPtr(req.HTTPStatus),
	}
	if err := json.NewEncoder(r.stdin).Encode(request); err != nil {
		r.invalidateProcess()
		return interceptor.Result{}, fmt.Errorf("send interceptor request: %w", err)
	}
	var response runResponse
	if err := r.decode(ctx, &response); err != nil {
		r.invalidateProcess()
		return interceptor.Result{}, err
	}
	if response.Status != "ok" {
		return interceptor.Result{}, fmt.Errorf("interceptor script error: %s", response.Message)
	}

	mutated, err := rebuildRoutable(req.Routable, response.Routable)
	if err != nil {
		return interceptor.Result{}, err
	}
	action := interceptor.ActionContinue
	if response.Action == "reject" {
		action = interceptor.ActionReject
	}
	return interceptor.Result{
		Routable:    mutated,
		SMPPStatus:  derefStatus(response.SMPPStatus),
		HTTPStatus:  derefStatus(response.HTTPStatus),
		Action:      action,
		ExecutionMS: response.DelayMS,
	}, nil
}

// decode reads the response, cancellable via ctx; a timed-out read kills the
// subprocess so a later request can't consume an abandoned response.
func (r *Runner) decode(ctx context.Context, response *runResponse) error {
	done := make(chan error, 1)
	go func() { done <- r.stdout.Decode(response) }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		if r.cmd != nil && r.cmd.Process != nil {
			_ = r.cmd.Process.Kill()
		}
		<-done
		return ctx.Err()
	}
}

// rebuildRoutable reconstructs the routable with the script-mutated
// source/destination/short_message/tags, preserving the untouched fields
// (direction, user, group, timestamp, message_payload).
func rebuildRoutable(original routingfilter.Routable, mutated routableWire) (routingfilter.Routable, error) {
	source, err := decodeField(mutated.SourceAddr)
	if err != nil {
		return routingfilter.Routable{}, fmt.Errorf("interceptor source_addr: %w", err)
	}
	destination, err := decodeField(mutated.DestinationAddr)
	if err != nil {
		return routingfilter.Routable{}, fmt.Errorf("interceptor destination_addr: %w", err)
	}
	message, err := decodeField(mutated.ShortMessage)
	if err != nil {
		return routingfilter.Routable{}, fmt.Errorf("interceptor short_message: %w", err)
	}
	originalMessage := original.ShortMessage()
	return routingfilter.NewRoutable(routingfilter.RoutableInput{
		Direction:       original.Direction(),
		ConnectorID:     original.ConnectorID(),
		UserID:          original.UserID(),
		GroupID:         original.GroupID(),
		SourceAddr:      routingfilter.BytesField{Present: original.SourceAddr().Present, Value: source},
		DestinationAddr: routingfilter.BytesField{Present: true, Value: destination},
		ShortMessage:    routingfilter.BytesField{Present: originalMessage.Present, Value: message},
		MessagePayload:  original.MessagePayload(),
		Timestamp:       original.Timestamp(),
		Tags:            mutated.Tags,
	})
}

func encodeField(field routingfilter.BytesField) string {
	if !field.Present {
		return ""
	}
	return base64.StdEncoding.EncodeToString(field.Value)
}

func decodeField(encoded string) ([]byte, error) {
	if encoded == "" {
		return nil, nil
	}
	return base64.StdEncoding.DecodeString(encoded)
}

// statusPtr maps the interceptor's 0-means-unset int to a nullable wire value
// (the script contract distinguishes None from 0).
func statusPtr(status int) *int {
	if status == 0 {
		return nil
	}
	value := status
	return &value
}

func derefStatus(status *int) int {
	if status == nil {
		return 0
	}
	return *status
}

var _ interceptor.Runner = (*Runner)(nil)

// pingTimeout bounds the liveness probe.
const pingTimeout = 3 * time.Second

// Ping round-trips a no-op through the subprocess to prove it is serving.
func (r *Runner) Ping(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	probeCtx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()
	if err := r.ensureProcess(probeCtx); err != nil {
		return err
	}
	if err := json.NewEncoder(r.stdin).Encode(map[string]string{"action": "ping"}); err != nil {
		r.invalidateProcess()
		return fmt.Errorf("send interceptor ping: %w", err)
	}
	var response runResponse
	if err := r.decode(probeCtx, &response); err != nil {
		r.invalidateProcess()
		return err
	}
	if response.Status != "ok" {
		return fmt.Errorf("interceptor ping error: %s", response.Message)
	}
	return nil
}
