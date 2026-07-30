package dlr

import (
	"context"
	"fmt"

	"github.com/pumpitspace/synevyr/internal/state/rediscompat"
)

// hashWriter is the rediscompat seam the request store needs.
type hashWriter interface {
	WriteHashRecord(ctx context.Context, record rediscompat.HashRecord) error
}

// RequestStore writes the submit-side DLR request record — the legacy
// SMPPClientManagerPB.perspective_submit_sm Redis write (managers/clients.py):
// dlr:<queue-msgid> holding the callback state OnSubmitResp/OnDeliverReceipt
// later read. Without this record the correlation legs find nothing and every
// receipt is dropped as DLRMapNotFound.
type RequestStore struct {
	redis hashWriter
}

// NewRequestStore builds the store over a rediscompat client.
func NewRequestStore(redis hashWriter) (*RequestStore, error) {
	if redis == nil {
		return nil, fmt.Errorf("dlr: nil redis client")
	}
	return &RequestStore{redis: redis}, nil
}

// HTTPDLRRequest is the httpapi callback state for one submit.
type HTTPDLRRequest struct {
	URL           string
	Level         int
	Method        string
	Connector     string // routed connector cid (legacy dlr_connector)
	ExpirySeconds int64  // connector dlr_expiry (default 86400)
}

// SMPPSDLRRequest is the smppsapi callback state for one submit: enough of the
// ESME's submit_sm to route a later receipt back to the bind that sent it.
type SMPPSDLRRequest struct {
	SystemID           string
	SourceAddrTON      string
	SourceAddrNPI      string
	SourceAddress      string
	DestinationAddrTON string
	DestinationAddrNPI string
	DestinationAddress string
	SubmissionDate     string
	RegisteredDelivery string // legacy rd_receipt, the RegisteredDeliveryReceipt name
	ExpirySeconds      int64
}

// StoreSMPPSDLRRequest writes dlr:<msgID> for a submit that arrived over an
// SMPP bind, mirroring the legacy SMPPServerProtocol branch of
// SMPPClientManagerPB.perspective_submit_sm (managers/clients.py:635-646).
//
// Without this record every receipt for an SMPP-originated message is dropped
// as DLRMapNotFound: the correlation legs have nothing mapping the SMSC's
// message id back to the bind that submitted it. The egress plumbing was
// already complete, so this write is the whole difference between an ESME
// getting all of its receipts and none of them.
func (s *RequestStore) StoreSMPPSDLRRequest(ctx context.Context, msgID string, request SMPPSDLRRequest) error {
	key, err := rediscompat.BuildDLRKey(msgID)
	if err != nil {
		return fmt.Errorf("dlr: request key: %w", err)
	}
	record, err := rediscompat.NewSMPPSDLRRecord(key, rediscompat.SMPPSDLRRequest{
		SystemID:                  request.SystemID,
		SourceAddrTON:             request.SourceAddrTON,
		SourceAddrNPI:             request.SourceAddrNPI,
		SourceAddress:             request.SourceAddress,
		DestinationAddrTON:        request.DestinationAddrTON,
		DestinationAddrNPI:        request.DestinationAddrNPI,
		DestinationAddress:        request.DestinationAddress,
		SubmissionDate:            request.SubmissionDate,
		RegisteredDeliveryReceipt: request.RegisteredDelivery,
		ExpirySeconds:             request.ExpirySeconds,
	})
	if err != nil {
		return fmt.Errorf("dlr: smpps request record: %w", err)
	}
	return s.redis.WriteHashRecord(ctx, record)
}

// StoreHTTPDLRRequest writes dlr:<msgID> for an httpapi submit. It mirrors the
// legacy hmset+expire: the record TTL is the connector's dlr_expiry.
func (s *RequestStore) StoreHTTPDLRRequest(ctx context.Context, msgID string, request HTTPDLRRequest) error {
	key, err := rediscompat.BuildDLRKey(msgID)
	if err != nil {
		return fmt.Errorf("dlr: request key: %w", err)
	}
	record, err := rediscompat.NewHTTPDLRRecord(key, rediscompat.HTTPDLRRequest{
		URL:           request.URL,
		Level:         int64(request.Level),
		Method:        request.Method,
		Connector:     request.Connector,
		ExpirySeconds: request.ExpirySeconds,
	})
	if err != nil {
		return fmt.Errorf("dlr: request record: %w", err)
	}
	return s.redis.WriteHashRecord(ctx, record)
}
