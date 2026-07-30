package httpcompat_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/pumpitspace/synevyr/internal/core"
	"github.com/pumpitspace/synevyr/internal/transport/httpcompat"
)

type fixtureFile struct {
	Cases []fixtureCase `json:"cases"`
}

type fixtureCase struct {
	ID       string          `json:"id"`
	Request  fixtureRequest  `json:"request"`
	Response fixtureResponse `json:"response"`
}

type fixtureRequest struct {
	Arguments map[string]string `json:"arguments"`
	JSON      map[string]any    `json:"json"`
	Method    string            `json:"method"`
	Path      string            `json:"path"`
}

type fixtureResponse struct {
	BodyBase64 string              `json:"body_base64"`
	BodyUTF8   string              `json:"body_utf8"`
	Headers    map[string][]string `json:"headers"`
	Status     int                 `json:"status"`
}

type authStub struct{ err error }

func (s authStub) Authenticate(context.Context, string, string) error { return s.err }

type balanceStub struct{ result core.BalanceSnapshot }

func (s balanceStub) Balance(context.Context, string) (core.BalanceSnapshot, error) {
	return s.result, nil
}

type rateStub struct{ result core.RateQuote }

func (s rateStub) Rate(context.Context, string, string) (core.RateQuote, error) {
	return s.result, nil
}

type submitStub struct {
	id  string
	err error
}

func (s submitStub) Submit(context.Context, core.SubmitRequest) (string, error) {
	if s.err != nil {
		return "", s.err
	}
	return s.id, nil
}

func TestGoldenHTTPCompatibility(t *testing.T) {
	fixture := loadFixture(t)
	if len(fixture.Cases) != 11 {
		t.Fatalf("fixture cases = %d, want 11", len(fixture.Cases))
	}

	for _, tc := range fixture.Cases {
		t.Run(tc.ID, func(t *testing.T) {
			handler := httpcompat.NewHandler(dependenciesFor(tc.ID))
			req := requestFor(t, tc.Request)
			recorder := httptest.NewRecorder()

			handler.ServeHTTP(recorder, req)
			response := recorder.Result()
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatal(err)
			}

			wantBody, err := base64.StdEncoding.DecodeString(tc.Response.BodyBase64)
			if err != nil {
				t.Fatalf("decode fixture body: %v", err)
			}
			if response.StatusCode != tc.Response.Status {
				t.Errorf("status = %d, want %d", response.StatusCode, tc.Response.Status)
			}
			if !bytes.Equal(body, wantBody) {
				t.Errorf("body = %q, want %q", body, wantBody)
			}
			if tc.Response.BodyUTF8 != "" && string(body) != tc.Response.BodyUTF8 {
				t.Errorf("body UTF-8 = %q, want %q", body, tc.Response.BodyUTF8)
			}
			if got := nonEmptyHeaders(response.Header); !reflect.DeepEqual(got, tc.Response.Headers) {
				t.Errorf("headers = %#v, want %#v", got, tc.Response.Headers)
			}
		})
	}
}

func dependenciesFor(id string) httpcompat.Dependencies {
	deps := httpcompat.Dependencies{
		Authenticator: authStub{},
		BalanceReader: balanceStub{result: core.BalanceSnapshot{}},
		RateReader:    rateStub{result: core.RateQuote{UnitRate: 0, SubmitSMCount: 1}},
		Submitter:     submitStub{id: "test-msg-id"},
	}
	switch id {
	case "send_bad_password":
		deps.Authenticator = authStub{err: core.ErrAuthentication}
	case "send_no_live_connector", "send_all_optional", "send_json_valid":
		deps.Submitter = submitStub{err: core.ErrNoLiveConnector}
	case "send_filter_dest_mismatch":
		deps.Submitter = submitStub{err: fmt.Errorf("%w: Value filter failed for user [nathalie] (destination_address filter mismatch).", core.ErrFilterRejected)}
	case "send_filter_src_mismatch":
		deps.Submitter = submitStub{err: fmt.Errorf("%w: Value filter failed for user [nathalie] (source_address filter mismatch).", core.ErrFilterRejected)}
	case "send_auth_src_addr_forbidden":
		deps.Submitter = submitStub{err: fmt.Errorf("%w: Authorization failed for user [nathalie] (Setting source address not authorized).", core.ErrAuthentication)}
	case "send_insufficient_balance":
		deps.Submitter = submitStub{err: core.ErrQuotaExceeded}
	}
	return deps
}

func requestFor(t *testing.T, fixture fixtureRequest) *http.Request {
	t.Helper()
	var body io.Reader
	requestURL := fixture.Path
	header := ""
	if fixture.JSON != nil {
		encoded, err := json.Marshal(fixture.JSON)
		if err != nil {
			t.Fatal(err)
		}
		body = bytes.NewReader(encoded)
		header = "application/json"
	} else {
		values := make(url.Values, len(fixture.Arguments))
		for key, value := range fixture.Arguments {
			values.Set(key, value)
		}
		if fixture.Method == http.MethodGet {
			if query := values.Encode(); query != "" {
				requestURL += "?" + query
			}
		} else {
			body = strings.NewReader(values.Encode())
			header = "application/x-www-form-urlencoded"
		}
	}
	req := httptest.NewRequest(fixture.Method, requestURL, body)
	if header != "" {
		req.Header.Set("Content-Type", header)
	}
	return req
}

func loadFixture(t *testing.T) fixtureFile {
	t.Helper()
	path := filepath.Join("..", "..", "..", "compat", "fixtures", "http", "baseline.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var fixture fixtureFile
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func nonEmptyHeaders(header http.Header) map[string][]string {
	result := map[string][]string{}
	for key, values := range header {
		if len(values) == 0 {
			continue
		}
		lower := strings.ToLower(key)
		result[lower] = append([]string(nil), values...)
	}
	return result
}
