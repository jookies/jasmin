package dlr

import (
	"context"
	"fmt"

	"github.com/pumpitspace/jasmin/internal/state/rediscompat"
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
