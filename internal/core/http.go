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
	// ErrThroughputExceeded is the user's per-second submit ceiling being hit.
	// Distinct from ErrQuotaExceeded (balance / submit_sm_count) because the
	// two map to different front-door responses and different counters.
	ErrThroughputExceeded = errors.New("user throughput exceeded")
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
	// SMPPSOrigin is set only for a submit that arrived over an SMPP bind. It
	// carries what the DLR record needs to route a later receipt back to that
	// bind; nil on the HTTP path, which registers its callback URL instead.
	SMPPSOrigin *SMPPSOrigin
}

// SMPPSOrigin is the submitting bind's identity and addressing, as the ESME
// sent them. These do not come from the connector defaults: a receipt has to be
// addressed the way the original submit was, not the way the outbound leg was.
type SMPPSOrigin struct {
	SystemID string
	// TON/NPI are the legacy enum *names* ("AddrTon.INTERNATIONAL"), not the
	// numeric values, because that is what the frozen Redis record stores.
	SourceAddrTON      string
	SourceAddrNPI      string
	DestinationAddrTON string
	DestinationAddrNPI string
	// RegisteredDelivery is the RegisteredDeliveryReceipt enum name the ESME
	// asked for; legacy stores it as rd_receipt.
	RegisteredDelivery string
}

type Submitter interface {
	Submit(ctx context.Context, request SubmitRequest) (messageID string, err error)
}
