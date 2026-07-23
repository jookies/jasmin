package dlr

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// httpDLRUserAgent mirrors jasmin's DLRThrower User-Agent ('Jasmin gateway/1.0 %s' % name).
const httpDLRUserAgent = "Jasmin gateway/1.0 DLRThrower"

// maxDLRResponseBytes bounds the response read when checking the ACK reply.
const maxDLRResponseBytes = 4 << 10

var (
	// ErrDLRHTTPStatus reports a >= 400 response from the callback endpoint.
	ErrDLRHTTPStatus = errors.New("dlr: callback returned HTTP error status")
	// ErrDLRNotAcknowledged reports a 2xx/3xx response whose body was not "ACK/Jasmin".
	ErrDLRNotAcknowledged = errors.New("dlr: callback did not reply ACK/Jasmin")
)

// HTTPDoer is the minimal HTTP client the thrower needs; *http.Client satisfies it.
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// HTTPDLRCallback is a delivery receipt to POST/GET to an httpapi user's callback URL.
type HTTPDLRCallback struct {
	MsgID         string
	URL           string
	Method        string // "GET" or "POST"
	Level         int    // 1, 2, or 3
	MessageStatus string
	Connector     string
	// Level 2/3 receipt fields.
	IDSMSC   string
	Sub      string
	Dlvrd    string
	SubDate  string
	DoneDate string
	Err      string
	Text     string
}

// HTTPDLRCallbackFromForward projects a ForwardHTTP into a callback. Level-1 forwards
// (submit_sm_resp leg) carry only the mandatory fields; level-2 forwards (deliver_sm leg)
// add the receipt fields.
func HTTPDLRCallbackFromForward(f Forward) (HTTPDLRCallback, error) {
	if f.Target != ForwardHTTP {
		return HTTPDLRCallback{}, fmt.Errorf("dlr: forward target is not http")
	}
	return HTTPDLRCallback{
		MsgID: f.QueueMsgID, URL: f.URL, Method: f.Method, Level: f.Level,
		MessageStatus: f.Status, Connector: f.Connector,
		IDSMSC: f.IDSMSC, Sub: f.Sub, Dlvrd: f.Dlvrd,
		SubDate: f.SubmitDate, DoneDate: f.DoneDate, Err: f.Err, Text: f.Text,
	}, nil
}

// args builds the callback parameters, matching jasmin's DLRThrower.http_dlr_callback:
// the mandatory set for every level plus the receipt fields for levels 2 and 3.
func (cb HTTPDLRCallback) args() url.Values {
	v := url.Values{}
	v.Set("id", cb.MsgID)
	v.Set("level", strconv.Itoa(cb.Level))
	v.Set("message_status", cb.MessageStatus)
	v.Set("connector", cb.Connector)
	if cb.Level == 2 || cb.Level == 3 {
		v.Set("id_smsc", cb.IDSMSC)
		v.Set("sub", cb.Sub)
		v.Set("dlvrd", cb.Dlvrd)
		v.Set("subdate", cb.SubDate)
		v.Set("donedate", cb.DoneDate)
		v.Set("err", cb.Err)
		v.Set("text", cb.Text)
	}
	return v
}

// SendHTTPDLR delivers a DLR to an httpapi callback, matching jasmin's
// DLRThrower.http_dlr_callback: GET sends the args as the query string, any other method
// (POST) sends them as an x-www-form-urlencoded body. Success requires an HTTP status
// below 400 and a body that trims to exactly "ACK/Jasmin".
func SendHTTPDLR(ctx context.Context, client HTTPDoer, cb HTTPDLRCallback) error {
	args := cb.args()
	method := strings.ToUpper(strings.TrimSpace(cb.Method))

	var req *http.Request
	var err error
	if method == "GET" {
		target, buildErr := mergeQuery(cb.URL, args)
		if buildErr != nil {
			return fmt.Errorf("dlr: build GET url: %w", buildErr)
		}
		req, err = http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	} else {
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, cb.URL, strings.NewReader(args.Encode()))
	}
	if err != nil {
		return fmt.Errorf("dlr: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "text/plain")
	req.Header.Set("User-Agent", httpDLRUserAgent)

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("dlr: callback request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxDLRResponseBytes))
	if resp.StatusCode >= 400 {
		return fmt.Errorf("%w: %d", ErrDLRHTTPStatus, resp.StatusCode)
	}
	if strings.TrimSpace(string(body)) != "ACK/Jasmin" {
		return fmt.Errorf("%w: %q", ErrDLRNotAcknowledged, strings.TrimSpace(string(body)))
	}
	return nil
}

// mergeQuery adds args to the base URL's existing query, matching treq params= semantics.
func mergeQuery(base string, args url.Values) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	q := u.Query()
	for key, values := range args {
		for _, value := range values {
			q.Set(key, value)
		}
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}
