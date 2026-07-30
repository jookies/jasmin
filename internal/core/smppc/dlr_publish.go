package smppc

import (
	"fmt"
	"strings"

	"github.com/pumpitspace/synevyr/internal/transport/amqpcompat"
)

// dlrSubmitRespRoutingKey is the route the legacy SMPPClientSMListener publishes
// every final submit_sm_resp DLR to, for DLRLookup to fire level-1 callbacks and
// write the smpp_msgid -> msgid Redis mapping that later receipt correlation
// depends on.
const dlrSubmitRespRoutingKey = "dlr.submit_sm_resp"

// newDLRSubmitRespPublication builds the dlr.submit_sm_resp envelope the legacy
// managers/content.py DLR produces: the body is the command_status name, the
// message-id is the queue msgid, and the headers carry type=submit_sm_resp plus,
// for ESME_ROK only, the SMSC message id normalized exactly as the legacy does
// (smpp_msgid.decode().upper().lstrip('0')). A non-ROK response carries no
// smpp_msgid, and an ROK response without an SMSC message id is an error, both
// mirroring the legacy DLR content contract.
func newDLRSubmitRespPublication(msgID, status, smscMessageID string) (amqpcompat.Envelope, error) {
	headers := map[string]amqpcompat.Field{
		"type": amqpcompat.StringField("submit_sm_resp"),
	}
	if status == "ESME_ROK" {
		if smscMessageID == "" {
			return amqpcompat.Envelope{}, fmt.Errorf("%w: ESME_ROK dlr requires an SMSC message id", ErrInvalidSubmitResponsePublication)
		}
		headers["smpp_msgid"] = amqpcompat.StringField(normalizeSMPPMsgID(smscMessageID))
	}
	properties, err := amqpcompat.NewProperties(msgID, headers)
	if err != nil {
		return amqpcompat.Envelope{}, fmt.Errorf("%w: %w", ErrInvalidSubmitResponsePublication, err)
	}
	return amqpcompat.NewEnvelope(dlrSubmitRespRoutingKey, properties, []byte(status))
}

// normalizeSMPPMsgID mirrors the legacy smpp_msgid.decode().upper().lstrip('0'):
// upper-case, then strip leading ASCII zeros. An all-zero id normalizes to "".
func normalizeSMPPMsgID(id string) string {
	return strings.TrimLeft(strings.ToUpper(id), "0")
}
