package smppc

import (
	"context"
	"fmt"

	"github.com/pumpitspace/synevyr/internal/transport/smppwire"
)

// MOInterceptor optionally rewrites or drops an inbound MO deliver_sm before it
// is published to the router. It is the smppc-side seam for MO-direction
// interception; the app layer implements it over the interceptor engine so this
// core package stays decoupled from the Python script runtime. Nil disables it.
type MOInterceptor interface {
	// InterceptMO runs the MO interception scripts against the message. A reject
	// result drops the MO (the caller acks with ESME_ROK and does not publish).
	// Otherwise the returned fields (possibly mutated) replace the originals
	// before the deliver_sm is encoded and published.
	InterceptMO(ctx context.Context, in MOInterceptData) (MOInterceptResult, error)
}

// MOInterceptData is one inbound MO presented to the interceptor.
type MOInterceptData struct {
	ConnectorID     string
	SourceAddr      []byte
	DestinationAddr []byte
	ShortMessage    []byte
	MessagePayload  []byte
}

// MOInterceptResult carries the (possibly mutated) fields back. Reject drops the
// message. SourceAddr/DestinationAddr/ShortMessage always carry the current
// values (equal to the originals when the script did not touch them).
type MOInterceptResult struct {
	SourceAddr      []byte
	DestinationAddr []byte
	ShortMessage    []byte
	Reject          bool
}

// SetMOInterceptor sets the MO-direction interceptor applied on the deliver
// path. Nil (the default) disables MO interception. Call before Run.
func (s *Session) SetMOInterceptor(moInterceptor MOInterceptor) {
	s.moInterceptor = moInterceptor
}

// interceptMO applies MO interception in place on body and returns the resolved
// content to publish. dropped=true means the script rejected the message (the
// caller acks ESME_ROK without publishing); errStatus != 0 means the interceptor
// itself failed (the caller returns that SMPP status). A nil interceptor is a
// no-op that just resolves the content, so both call sites route through here.
func (s *Session) interceptMO(ctx context.Context, body *smppwire.SMBody, msgID string) (content []byte, dropped bool, errStatus uint32) {
	if s.moInterceptor == nil {
		return deliverMessageContent(body), false, 0
	}
	result, err := s.moInterceptor.InterceptMO(ctx, MOInterceptData{
		ConnectorID:     s.cfg.CID,
		SourceAddr:      body.SourceAddress,
		DestinationAddr: body.DestinationAddress,
		ShortMessage:    body.ShortMessage,
		MessagePayload:  body.Optional.MessagePayload,
	})
	if err != nil {
		s.logDeliverError(fmt.Sprintf("mo interceptor [queue-msgid:%s]: %v", msgID, err))
		return nil, false, smppStatusUnknownError
	}
	if result.Reject {
		s.logMOInterceptDrop(msgID)
		return nil, true, 0
	}
	// Write the resolved fields back so the re-encode carries any mutation. The
	// adapter returns the current values for untouched fields, so this is a
	// no-op when the script did not mutate.
	body.SourceAddress = result.SourceAddr
	body.DestinationAddress = result.DestinationAddr
	body.ShortMessage = result.ShortMessage
	return deliverMessageContent(body), false, 0
}

// logMOInterceptDrop records an interceptor-rejected MO drop, mirroring the
// legacy interceptor "message thrown/rejected" behaviour on the MO path.
func (s *Session) logMOInterceptDrop(msgID string) {
	if s.auditLogger == nil {
		return
	}
	s.auditLogger.Info(fmt.Sprintf("MO message [queue-msgid:%s] rejected by interceptor, not routed", msgID))
}
