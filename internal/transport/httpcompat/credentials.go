package httpcompat

import (
	"fmt"

	"github.com/pumpitspace/synevyr/internal/core/mtcredential"
)

// The MtMessagingCredential gate on /send.
//
// internal/core/mtcredential ports Jasmin's HttpAPICredentialValidator and has
// been unit-tested since it was written, but nothing called it: the front door
// checked the password and let everything else through. So a user with
// `http_send False`, a `dst_addr` value filter or a default source address got
// none of it enforced, while the console and the API reported all three as set.
//
// CredentialResolver is the seam. A nil resolver keeps the old behaviour rather
// than failing closed, because a gateway whose directory does not supply
// credentials must not start refusing every message on upgrade.

// CredentialResolver returns a user's MT credential.
type CredentialResolver interface {
	ResolveCredential(username string) (*mtcredential.Credential, bool)
}

// credentialRejection renders Jasmin's exact rejection text. The message is
// contract: scripts match on it, and the parenthesised detail is how an
// operator tells "not allowed to send at all" from "your destination filter
// rejected this number".
func credentialRejection(username string, err error) (string, bool) {
	validation, ok := err.(*mtcredential.ValidationError)
	if !ok {
		return "", false
	}
	switch validation.Reason {
	case mtcredential.ReasonAuthorization:
		return fmt.Sprintf("Authorization failed for user [%s] (%s).",
			username, authorizationDetail(validation.Key)), true
	case mtcredential.ReasonValueFilter:
		return fmt.Sprintf("Value filter failed for user [%s] (%s filter mismatch).",
			username, validation.Key), true
	default:
		return "", false
	}
}

// authorizationDetail maps an authorization key to the phrase the oracle uses.
// These are transcribed from jasmin/protocols/http/validation.py, where each
// check raises its own sentence rather than naming the key.
func authorizationDetail(key string) string {
	switch key {
	case mtcredential.AuthHTTPSend:
		return "Cannot send MT messages"
	case mtcredential.AuthHTTPLongContent:
		return "Long content not authorized"
	case mtcredential.AuthSetDLRLevel:
		return "Setting dlr level not authorized"
	case mtcredential.AuthHTTPSetDLRMethod:
		return "Setting dlr method not authorized"
	case mtcredential.AuthSetSourceAddress:
		return "Setting source address not authorized"
	case mtcredential.AuthSetPriority:
		return "Setting priority not authorized"
	case mtcredential.AuthSetValidityPeriod:
		return "Setting validity period not authorized"
	case mtcredential.AuthSetHexContent:
		return "Setting hex content not authorized"
	case mtcredential.AuthSetScheduleDeliveryTime:
		return "Setting schedule delivery time not authorized"
	case mtcredential.AuthHTTPBalance:
		return "Cannot check balance"
	case mtcredential.AuthHTTPRate:
		return "Cannot check rate"
	default:
		return "Not authorized"
	}
}

// sendRequestProjection builds the validator's view of the request. The Has*
// flags mirror argument *presence* in the legacy request.args, not whether a
// value is non-empty: `from=` with an empty value still counts as "the user set
// a source address" and needs the authorization.
func sendRequestProjection(arguments map[string]string, present map[string]bool, longContent bool) mtcredential.SendRequest {
	return mtcredential.SendRequest{
		Destination:       []byte(arguments["to"]),
		Source:            []byte(arguments["from"]),
		HasSource:         present["from"],
		Priority:          []byte(arguments["priority"]),
		HasPriority:       present["priority"],
		ValidityPeriod:    []byte(arguments["validity-period"]),
		HasValidityPeriod: present["validity-period"],
		Content:           []byte(arguments["content"]),
		HasContent:        present["content"],
		HasDLRLevel:       present["dlr-level"],
		HasDLRMethod:      present["dlr-method"],
		HasHexContent:     present["hex-content"],
		HasSDT:            present["sdt"],
		IsLongContent:     longContent,
	}
}
