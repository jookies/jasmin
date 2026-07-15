package httpcompat_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/pumpitspace/jasmin/internal/core"
	"github.com/pumpitspace/jasmin/internal/transport/httpcompat"
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

type submitStub struct{ err error }

func (s submitStub) Submit(context.Context, core.SubmitRequest) (string, error) {
	return "", s.err
}

func TestGoldenHTTPCompatibility(t *testing.T) {
	fixture := loadFixture(t)
	if len(fixture.Cases) != 9 {
		t.Fatalf("fixture cases = %d, want 9", len(fixture.Cases))
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
			if string(body) != tc.Response.BodyUTF8 {
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
		Submitter:     submitStub{err: core.ErrNoLiveConnector},
	}
	switch id {
	case "send_bad_password", "send_json_bad_password", "rate_disabled_user", "balance_disabled_group":
		deps.Authenticator = authStub{err: core.ErrAuthentication}
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
