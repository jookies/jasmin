package interceptor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"time"
)

type PythonRunner struct {
	PythonPath string
}

type pyRequest struct {
	Script     string                 `json:"script"`
	Routable   pyRoutable             `json:"routable"`
	SMPPStatus int                    `json:"smpp_status"`
	HTTPStatus int                    `json:"http_status"`
}

type pyRoutable struct {
	SourceAddr      string   `json:"source_addr"`
	DestinationAddr string   `json:"destination_addr"`
	ShortMessage    string   `json:"short_message"`
	Tags            []string `json:"tags"`
	Locked          []string `json:"locked"`
}

type pyResponse struct {
	Routable   pyRoutable `json:"routable"`
	SMPPStatus int        `json:"smpp_status"`
	HTTPStatus int        `json:"http_status"`
	Action     string     `json:"action"`
	Error      string     `json:"error"`
}

const pyWrapper = `
import sys
import json

def run():
    try:
        data = json.load(sys.stdin)
        script = data['script']
        
        # Mocking the Jasmin Routable environment for the script
        class Routable:
            def __init__(self, data):
                locked = list(data.get('locked', []))
                class Params(dict):
                    def __init__(self, d, locked):
                        super().__init__(d)
                        self._locked = locked
                    def __setitem__(self, key, value):
                        if key in self._locked:
                            raise Exception('%s field is locked' % key)
                        super().__setitem__(key, value)
                
                self.pdu = type('PDU', (), {'params': Params({
                    'source_addr': data['source_addr'],
                    'destination_addr': data['destination_addr'],
                    'short_message': data['short_message']
                }, locked)})
                self._tags = list(data['tags'])
                self._locked = locked
            def addTag(self, tag):
                if 'tags' in self._locked:
                    raise Exception('tags field is locked')
                if tag not in self._tags:
                    self._tags.append(str(tag))
        
        routable = Routable(data['routable'])
        gl = {
            'routable': routable,
            'smpp_status': data['smpp_status'],
            'http_status': data['http_status'],
            'action': 'continue',
        }
        
        exec(script, gl)
        
        print(json.dumps({
            'routable': {
                'source_addr': routable.pdu.params['source_addr'],
                'destination_addr': routable.pdu.params['destination_addr'],
                'short_message': routable.pdu.params['short_message'],
                'tags': routable._tags
            },
            'smpp_status': gl['smpp_status'],
            'http_status': gl['http_status'],
            'action': gl['action']
        }))
    except Exception as e:
        print(json.dumps({'error': str(e)}))

if __name__ == "__main__":
    run()
`

func (r *PythonRunner) Run(ctx context.Context, script Script, req Context) (Result, error) {
	pyReq := pyRequest{
		Script: script.PyCode,
		Routable: pyRoutable{
			SourceAddr:      string(req.Routable.SourceAddr().Value),
			DestinationAddr: string(req.Routable.DestinationAddr().Value),
			ShortMessage:    string(req.Routable.ShortMessage().Value),
			Tags:            req.Routable.Tags(),
			Locked:          req.Routable.Locked(),
		},
		SMPPStatus: req.SMPPStatus,
		HTTPStatus: req.HTTPStatus,
	}

	input, err := json.Marshal(pyReq)
	if err != nil {
		return Result{}, err
	}

	path := r.PythonPath
	if path == "" {
		path = "python3"
	}

	cmd := exec.CommandContext(ctx, path, "-c", pyWrapper)
	cmd.Stdin = bytes.NewReader(input)
	var out bytes.Buffer
	cmd.Stderr = &out // Capture stderr too just in case
	
	start := time.Now()
	output, err := cmd.Output()
	duration := time.Since(start)

	if err != nil {
		return Result{}, fmt.Errorf("python execution failed: %v (stderr: %s)", err, out.String())
	}

	var pyRes pyResponse
	if err := json.Unmarshal(output, &pyRes); err != nil {
		return Result{}, fmt.Errorf("failed to parse python output: %v (output: %s)", err, string(output))
	}

	if pyRes.Error != "" {
		return Result{}, errors.New(pyRes.Error)
	}

	// Update routable
	newR := req.Routable.Clone()
	_ = newR.SetSourceAddr([]byte(pyRes.Routable.SourceAddr))
	_ = newR.SetDestinationAddr([]byte(pyRes.Routable.DestinationAddr))
	_ = newR.SetShortMessage([]byte(pyRes.Routable.ShortMessage))
	// Tags - clear and add back (simplest for now)
	for _, t := range req.Routable.Tags() {
		newR.RemoveTag(t)
	}
	for _, t := range pyRes.Routable.Tags {
		_ = newR.AddTag(t)
	}

	action := ActionContinue
	if pyRes.Action != "" {
		action = Action(pyRes.Action)
	}

	return Result{
		Routable:    newR,
		SMPPStatus:  pyRes.SMPPStatus,
		HTTPStatus:  pyRes.HTTPStatus,
		Action:      action,
		ExecutionMS: duration.Milliseconds(),
	}, nil
}
