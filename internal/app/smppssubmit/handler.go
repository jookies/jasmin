// Package smppssubmit adapts a bound ESME's submit_sm into the MT pipeline: it
// runs the smpps credential validation and delegates to the core submit
// service with source_connector=smppsapi, mapping the outcome to an SMPP
// command_status. It is the inbound half of the SMPPS server.
package smppssubmit

import (
	"context"
	"errors"

	"github.com/pumpitspace/jasmin/internal/core"
	"github.com/pumpitspace/jasmin/internal/core/dlr"
	"github.com/pumpitspace/jasmin/internal/core/mtcredential"
	"github.com/pumpitspace/jasmin/internal/core/smpps"
	"github.com/pumpitspace/jasmin/internal/transport/smppwire"
)

// SMPP command_status values used by the submit path.
const (
	statusROK             uint32 = 0x00000000 // ESME_ROK
	statusInvalidDestAddr uint32 = 0x0000000B // ESME_RINVDSTADR
	statusSubmitFailed    uint32 = 0x00000045 // ESME_RSUBMITFAIL
	statusThrottled       uint32 = 0x00000058 // ESME_RTHROTTLED
	statusSystemError     uint32 = 0x00000008 // ESME_RSYSERR
	// nonDefaultRDelivery is the SMSC-receipt bits of registered_delivery; any
	// set bit means the ESME asked for a receipt (DLRRequested).
	nonDefaultRDelivery byte = 0x03
)

// CredentialResolver projects a bound system_id into its MT messaging
// credential for validation. A missing user returns ok=false (the session
// only ingests submits from bound users, so this should not normally miss).
type CredentialResolver interface {
	ResolveCredential(systemID string) (*mtcredential.Credential, bool)
}

// Handler implements smpps.SubmitHandler over the core submit pipeline.
type Handler struct {
	credentials CredentialResolver
	submitter   core.Submitter
}

func NewHandler(credentials CredentialResolver, submitter core.Submitter) (*Handler, error) {
	if credentials == nil {
		return nil, errors.New("smppssubmit: nil credential resolver")
	}
	if submitter == nil {
		return nil, errors.New("smppssubmit: nil submitter")
	}
	return &Handler{credentials: credentials, submitter: submitter}, nil
}

// HandleSubmit validates and ingests one submit_sm. It mirrors the legacy
// submit_sm_event: reject an empty destination, run SmppsCredentialValidator,
// then route/bill/publish via the submit service.
func (h *Handler) HandleSubmit(ctx context.Context, systemID string, sm *smppwire.SMBody) (string, uint32) {
	if sm == nil || len(sm.DestinationAddress) == 0 {
		// The legacy factory rejects a submit_sm with no destination_addr.
		return "", statusInvalidDestAddr
	}
	credential, ok := h.credentials.ResolveCredential(systemID)
	if !ok {
		return "", statusSystemError
	}
	validation := mtcredential.SubmitRequest{
		DestinationAddr: sm.DestinationAddress,
		SourceAddr:      sm.SourceAddress,
		PriorityFlag:    sm.PriorityFlag,
		ShortMessage:    sm.ShortMessage,
		DLRRequested:    sm.RegisteredDelivery&nonDefaultRDelivery != 0,
	}
	if err := mtcredential.ValidateSubmit(credential, validation); err != nil {
		// A credential rejection is a submit failure to the ESME.
		return "", statusSubmitFailed
	}

	request := core.SubmitRequest{
		Username:        systemID,
		Destination:     string(sm.DestinationAddress),
		From:            string(sm.SourceAddress),
		Content:         string(sm.ShortMessage),
		Coding:          int(sm.DataCoding),
		Priority:        int(sm.PriorityFlag),
		DLR:             sm.RegisteredDelivery&nonDefaultRDelivery != 0,
		SourceConnector: "smppsapi",
	}
	// Carry the bind's identity and the ESME's own addressing so the submit
	// path can register the dlr:<msgid> record. Legacy writes it only when a
	// receipt was actually asked for (managers/clients.py:618); without it the
	// correlation legs have nothing and every receipt for this message is
	// dropped as DLRMapNotFound.
	if request.DLR {
		request.SMPPSOrigin = &core.SMPPSOrigin{
			SystemID:           systemID,
			SourceAddrTON:      dlr.FormatAddrTON(sm.SourceAddressTON),
			SourceAddrNPI:      dlr.FormatAddrNPI(sm.SourceAddressNPI),
			DestinationAddrTON: dlr.FormatAddrTON(sm.DestinationAddressTON),
			DestinationAddrNPI: dlr.FormatAddrNPI(sm.DestinationAddressNPI),
			RegisteredDelivery: dlr.FormatRegisteredDeliveryReceipt(sm.RegisteredDelivery),
		}
	}
	messageID, err := h.submitter.Submit(ctx, request)
	if err != nil {
		return "", mapSubmitError(err)
	}
	return messageID, statusROK
}

// mapSubmitError maps a submit-pipeline error to an SMPP command_status,
// mirroring the legacy submit_sm_event error handling: no route / no bound
// connector is a submit failure, a quota/throttle failure throttles, an
// invalid parameter is a submit failure, everything else is a system error.
func mapSubmitError(err error) uint32 {
	switch {
	case errors.Is(err, core.ErrNoRouteMatched), errors.Is(err, core.ErrNoLiveConnector):
		return statusSubmitFailed
	case errors.Is(err, core.ErrQuotaExceeded), errors.Is(err, core.ErrThroughputExceeded):
		// SubmitSmThroughputExceededError carries ESME_RTHROTTLED and is a
		// "no shutdown" error: the bind survives, only this PDU is refused
		// (jasmin/protocols/smpp/error.py:78-84).
		return statusThrottled
	case errors.Is(err, core.ErrFilterRejected), errors.Is(err, core.ErrInvalidParameter):
		return statusSubmitFailed
	case errors.Is(err, core.ErrAuthentication):
		return statusSystemError
	default:
		return statusSystemError
	}
}

// Ensure the adapter satisfies the session's ingestion seam.
var _ smpps.SubmitHandler = (*Handler)(nil)
