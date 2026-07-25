package core

import (
	"context"
	"errors"
	"time"

	"github.com/pumpitspace/jasmin/internal/core/tlv"
)

var (
	ErrAuthentication   = errors.New("authentication failed")
	ErrNoLiveConnector  = errors.New("no live connector")
	ErrQuotaExceeded    = errors.New("quota exceeded")
	ErrFilterRejected   = errors.New("request rejected by filters")
	ErrInvalidParameter = errors.New("invalid parameter")
)

// Authenticator verifies that a user and its group are enabled and that the
// supplied credentials are valid.
type Authenticator interface {
	Authenticate(ctx context.Context, username, password string) error
}

// BalanceSnapshot keeps decimal values as strings so the core never loses
// precision. A nil value represents the legacy unlimited quota.
type BalanceSnapshot struct {
	Balance  *string
	SMSCount *string
}

type BalanceReader interface {
	Balance(ctx context.Context, username string) (BalanceSnapshot, error)
}

type RateQuote struct {
	UnitRate      float64
	SubmitSMCount int
}

type RateReader interface {
	Rate(ctx context.Context, username, destination string) (RateQuote, error)
}

type SubmitRequest struct {
	Username       string
	Password       string
	Destination    string
	Content        string
	HexContent     string
	From           string
	Coding         int
	Priority       int
	SDT            *time.Time
	ValidityPeriod *time.Duration
	DLR            bool
	DLRUrl         string
	DLRLevel       int
	DLRMethod      string
	Tags           []string
	// CustomTLVs is the tlv.Normalize output for the request's custom_tlvs
	// argument, in caller order (wire order). Types stay unresolved here;
	// connector rules apply them at submit time, matching the legacy listener.
	CustomTLVs []tlv.TLV
	// SourceConnector names the ingress: "httpapi" (default when empty) or
	// "smppsapi". It flows to the AMQP envelope's source_connector header and
	// the DLR record's sc field, so a receipt correlates back to the right leg.
	SourceConnector string
}

type Submitter interface {
	Submit(ctx context.Context, request SubmitRequest) (messageID string, err error)
}
