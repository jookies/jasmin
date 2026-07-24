// Package mo implements Jasmin's mobile-originated (MO) message flow. This first unit is
// the HTTP delivery mechanism — the deliverSmThrower's http callback that forwards a
// routed deliver_sm to an HttpConnector.
package mo

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// httpMOUserAgent mirrors jasmin's deliverSmThrower User-Agent.
const httpMOUserAgent = "Jasmin gateway/1.0 deliverSmHttpThrower"

const maxMOResponseBytes = 4 << 10

var (
	// ErrMOHTTPStatus reports a >= 400 response from the MO callback endpoint.
	ErrMOHTTPStatus = errors.New("mo: callback returned HTTP error status")
	// ErrMONotAcknowledged reports a 2xx/3xx response whose body was not "ACK/Jasmin".
	ErrMONotAcknowledged = errors.New("mo: callback did not reply ACK/Jasmin")
)

// HTTPDoer is the minimal HTTP client the thrower needs; *http.Client satisfies it.
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// Delivery is a routed MO message to forward to an HttpConnector. Content is the already
// selected message body (short_message or message_payload — the caller chooses per Jasmin's
// precedence). Priority and Coding are omitted when nil; Validity when empty.
//
// The fork's tlv_params / custom_tlvs forwarding is a separate follow-on and is not built
// here; this covers the mandatory MO arguments plus the simple optionals.
type Delivery struct {
	MsgID           string
	From            string // source_addr
	To              string // destination_addr
	OriginConnector string // src-connector-id
	Content         []byte
	Priority        *byte  // priority_flag value
	Coding          *byte  // data_coding value
	Validity        string // validity_period
	URL             string
	Method          string
}

// args builds the MO callback parameters, matching jasmin's deliverSmThrower
// http_deliver_sm_callback: mandatory id/from/to/origin-connector, the content and its hex
// binary, and the present optionals.
func (d Delivery) args() url.Values {
	v := url.Values{}
	v.Set("id", d.MsgID)
	v.Set("from", d.From)
	v.Set("to", d.To)
	v.Set("origin-connector", d.OriginConnector)
	v.Set("content", string(d.Content))
	v.Set("binary", hex.EncodeToString(d.Content))
	if d.Priority != nil {
		v.Set("priority", strconv.Itoa(int(*d.Priority)))
	}
	if d.Coding != nil {
		v.Set("coding", strconv.Itoa(int(*d.Coding)))
	}
	if d.Validity != "" {
		v.Set("validity", d.Validity)
	}
	return v
}

// SendMO forwards a routed MO message to an HttpConnector, matching jasmin's
// deliverSmThrower: GET sends the args as the query string, POST as an
// x-www-form-urlencoded body. Success requires an HTTP status below 400 and a body that
// trims to exactly "ACK/Jasmin".
func SendMO(ctx context.Context, client HTTPDoer, d Delivery) error {
	args := d.args()
	method := strings.ToUpper(strings.TrimSpace(d.Method))

	var req *http.Request
	var err error
	if method == "GET" {
		target, buildErr := mergeQuery(d.URL, args)
		if buildErr != nil {
			return fmt.Errorf("mo: build GET url: %w", buildErr)
		}
		req, err = http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	} else {
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, d.URL, strings.NewReader(args.Encode()))
	}
	if err != nil {
		return fmt.Errorf("mo: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "text/plain")
	req.Header.Set("User-Agent", httpMOUserAgent)

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("mo: callback request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxMOResponseBytes))
	if resp.StatusCode >= 400 {
		return fmt.Errorf("%w: %d", ErrMOHTTPStatus, resp.StatusCode)
	}
	if strings.TrimSpace(string(body)) != "ACK/Jasmin" {
		return fmt.Errorf("%w: %q", ErrMONotAcknowledged, strings.TrimSpace(string(body)))
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
