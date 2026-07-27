package picklecompat

import "github.com/pumpitspace/jasmin/internal/transport/gopickle"

// submitSmBill builds a loadable SubmitSmBill pickle. The Go billing path reads
// the late-bill amount from the AMQP header (session.go), never this pickle, so
// it does not reproduce the full User mt_credential/smpps_credential/value_filters
// graph — only what a legacy (shadow) billing consumer reads: the bid, the user
// uid/username, the amounts, and the actions. It loads as a real SubmitSmBill
// (NEWOBJ+BUILD sets __dict__; no __init__ runs).
func submitSmBill(request SubmitSMEncodeRequest) gopickle.Object {
	group := gopickle.Object{
		Class: gopickle.Global{Module: "jasmin.routing.jasminApi", Name: "Group"},
		State: gopickle.Dict{
			{Key: gopickle.Str("gid"), Value: gopickle.Str("runtime")},
			{Key: gopickle.Str("enabled"), Value: gopickle.Bool(true)},
		},
	}
	user := gopickle.Object{
		Class: gopickle.Global{Module: "jasmin.routing.jasminApi", Name: "User"},
		State: gopickle.Dict{
			{Key: gopickle.Str("uid"), Value: gopickle.Str(request.UserID)},
			{Key: gopickle.Str("group"), Value: group},
			{Key: gopickle.Str("username"), Value: gopickle.Str(request.Username)},
			{Key: gopickle.Str("password"), Value: gopickle.Bytes(nil)},
		},
	}
	return gopickle.Object{
		Class: gopickle.Global{Module: "jasmin.routing.Bills", Name: "SubmitSmBill"},
		State: gopickle.Dict{
			{Key: gopickle.Str("bid"), Value: gopickle.Str(request.BillID)},
			{Key: gopickle.Str("user"), Value: user},
			{Key: gopickle.Str("amounts"), Value: gopickle.Dict{
				{Key: gopickle.Str("submit_sm"), Value: gopickle.Float(request.SubmitSMAmount)},
				{Key: gopickle.Str("submit_sm_resp"), Value: gopickle.Float(request.SubmitSMRespAmount)},
			}},
			{Key: gopickle.Str("actions"), Value: gopickle.Dict{
				{Key: gopickle.Str("decrement_submit_sm_count"), Value: gopickle.Int(int64(request.DecrementSubmitSMCount))},
			}},
		},
	}
}
