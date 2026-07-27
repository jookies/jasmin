package picklecompat

import (
	"context"
	"fmt"

	"github.com/pumpitspace/jasmin/internal/transport/gopickle"
)

// EncodeSubmitSMResponse builds the pickled smpp.pdu SubmitSMResp — the native
// counterpart of Bridge.EncodeSubmitSMResponse. The status wire code maps to its
// CommandStatus ordinal (the pickled Enum value); on ESME_ROK the message_id
// rides in params, otherwise message_id is None (the SubmitSMResp default).
func (c *NativeCodec) EncodeSubmitSMResponse(ctx context.Context, commandStatus, sequence uint32, messageID []byte) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if sequence < 1 || sequence > 0x7fffffff {
		return nil, fmt.Errorf("%w: sequence %d out of SMPP range", ErrNativeCodec, sequence)
	}
	statusOrdinal, ok := commandStatusWireToOrdinal[commandStatus]
	if !ok {
		return nil, fmt.Errorf("%w: unknown command_status %#x", ErrNativeCodec, commandStatus)
	}
	var messageIDValue gopickle.Value = gopickle.None{}
	if commandStatus == 0 {
		messageIDValue = gopickle.Bytes(messageID)
	}
	obj := gopickle.Object{
		Class: gopickle.Global{Module: "smpp.pdu.operations", Name: "SubmitSMResp"},
		State: gopickle.Dict{
			{Key: gopickle.Str("id"), Value: smppEnum("CommandId", commandIDSubmitSMResp)},
			{Key: gopickle.Str("seqNum"), Value: gopickle.Int(int64(sequence))},
			{Key: gopickle.Str("status"), Value: smppEnum("CommandStatus", statusOrdinal)},
			{Key: gopickle.Str("custom_tlvs"), Value: gopickle.List{}},
			{Key: gopickle.Str("params"), Value: gopickle.Dict{
				{Key: gopickle.Str("message_id"), Value: messageIDValue},
			}},
		},
	}
	return gopickle.Dump(obj)
}
