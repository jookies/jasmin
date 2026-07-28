package httpcompat

import (
	"testing"

	"github.com/pumpitspace/jasmin/internal/core/mtcredential"
)

// TestCredentialRejectionMatchesTheOracle pins the rejection text. It is
// contract: scripts match on it, and the parenthesised detail is how an
// operator tells "not allowed to send at all" from "your destination filter
// rejected this number". The strings come from
// jasmin/protocols/http/validation.py.
func TestCredentialRejectionMatchesTheOracle(t *testing.T) {
	for _, testCase := range []struct {
		name string
		err  error
		want string
	}{
		{
			"http_send",
			&mtcredential.ValidationError{Reason: mtcredential.ReasonAuthorization, Key: mtcredential.AuthHTTPSend},
			"Authorization failed for user [alice] (Cannot send MT messages).",
		},
		{
			"long content",
			&mtcredential.ValidationError{Reason: mtcredential.ReasonAuthorization, Key: mtcredential.AuthHTTPLongContent},
			"Authorization failed for user [alice] (Long content not authorized).",
		},
		{
			"dlr level",
			&mtcredential.ValidationError{Reason: mtcredential.ReasonAuthorization, Key: mtcredential.AuthSetDLRLevel},
			"Authorization failed for user [alice] (Setting dlr level not authorized).",
		},
		{
			"source address",
			&mtcredential.ValidationError{Reason: mtcredential.ReasonAuthorization, Key: mtcredential.AuthSetSourceAddress},
			"Authorization failed for user [alice] (Setting source address not authorized).",
		},
		{
			"schedule delivery time",
			&mtcredential.ValidationError{Reason: mtcredential.ReasonAuthorization, Key: mtcredential.AuthSetScheduleDeliveryTime},
			"Authorization failed for user [alice] (Setting schedule delivery time not authorized).",
		},
		{
			"destination filter",
			&mtcredential.ValidationError{Reason: mtcredential.ReasonValueFilter, Key: mtcredential.FilterDestinationAddress},
			"Value filter failed for user [alice] (destination_address filter mismatch).",
		},
		{
			"content filter",
			&mtcredential.ValidationError{Reason: mtcredential.ReasonValueFilter, Key: mtcredential.FilterContent},
			"Value filter failed for user [alice] (content filter mismatch).",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			got, ok := credentialRejection("alice", testCase.err)
			if !ok {
				t.Fatal("rejection not recognised")
			}
			if got != testCase.want {
				t.Errorf("\n got %q\nwant %q", got, testCase.want)
			}
		})
	}
}
