package httpcompat

import (
	"strings"
	"testing"

	"github.com/pumpitspace/synevyr/internal/core"
	"github.com/pumpitspace/synevyr/internal/core/mtcredential"
)

type fixedCredential struct{ credential *mtcredential.Credential }

func (f fixedCredential) ResolveCredential(string) (*mtcredential.Credential, bool) {
	return f.credential, true
}

// TestLongContentAuthorizationCountsHexPayload is the regression for a
// credential an operator could set and no client had to respect.
//
// http_long_content was decided from len(req.Content) alone. A hex-content
// submit leaves Content empty, so the same payload that was refused as `content`
// sailed through as `hex-content` — and was then segmented, delivered and billed
// per part. The cost control the operator withheld was unenforceable against
// anyone willing to hex-encode.
func TestLongContentAuthorizationCountsHexPayload(t *testing.T) {
	credential := mtcredential.New(true)
	credential.SetAuthorization(mtcredential.AuthHTTPLongContent, false)
	h := &handler{dependencies: Dependencies{Credentials: fixedCredential{credential}}}

	// 600 GSM-7 characters is well past the 160-byte single-part limit.
	long := strings.Repeat("a", 600)
	hex := strings.Repeat("61", 600) // the same 600 bytes, hex-encoded

	for _, testCase := range []struct {
		name    string
		request core.SubmitRequest
		args    map[string]string
	}{
		{"as content", core.SubmitRequest{Content: long}, map[string]string{"content": long}},
		{"as hex-content", core.SubmitRequest{HexContent: hex}, map[string]string{"hex-content": hex}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			request := testCase.request
			message, refused := h.checkSendCredentials("alice", testCase.args, &request)
			if !refused {
				t.Fatal("long content accepted for a user without http_long_content")
			}
			if !strings.Contains(message, "Long content not authorized") {
				t.Errorf("rejection = %q, want the long-content message", message)
			}
		})
	}

	// A payload that fits one part is still allowed through either field.
	short := core.SubmitRequest{HexContent: strings.Repeat("61", 10)}
	if _, refused := h.checkSendCredentials("alice",
		map[string]string{"hex-content": short.HexContent}, &short); refused {
		t.Error("a single-part hex payload was refused as long content")
	}
}
