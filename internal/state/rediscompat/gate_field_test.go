package rediscompat

import "testing"

// TestDLRRecordOmitsGateFieldsWhenUnset is the parity guard for the golden Redis
// fixture: a submit with no registry gate must produce exactly the field set the
// frozen baseline records, so the feature is invisible to every deployment that
// does not use it.
func TestDLRRecordOmitsGateFieldsWhenUnset(t *testing.T) {
	key, err := BuildDLRKey("q1")
	if err != nil {
		t.Fatalf("BuildDLRKey: %v", err)
	}
	record, err := NewHTTPDLRRecord(key, HTTPDLRRequest{
		URL: "http://cb/dlr", Level: 2, Method: "POST", Connector: "smpp-01", ExpirySeconds: 86400,
	})
	if err != nil {
		t.Fatalf("NewHTTPDLRRecord: %v", err)
	}
	for _, field := range []string{"gate_stat", "gate_err"} {
		if _, present := record.Fields()[field]; present {
			t.Fatalf("ungated record carries %q", field)
		}
	}
	if len(record.Fields()) != 6 {
		t.Fatalf("ungated record has %d fields, want the frozen 6", len(record.Fields()))
	}
}

func TestDLRRecordCarriesGateFieldsWhenSet(t *testing.T) {
	key, _ := BuildDLRKey("q1")
	record, err := NewSMPPSDLRRecord(key, SMPPSDLRRequest{
		SystemID: "demoesme", SourceAddrTON: "AddrTon.INTERNATIONAL", SourceAddrNPI: "AddrNpi.ISDN",
		SourceAddress: "12345", DestinationAddrTON: "AddrTon.INTERNATIONAL", DestinationAddrNPI: "AddrNpi.ISDN",
		DestinationAddress: "380930242105", SubmissionDate: "2101011200",
		RegisteredDeliveryReceipt: "RegisteredDeliveryReceipt.SMSC_DELIVERY_RECEIPT_REQUESTED",
		ExpirySeconds:             3600,
		Gate:                      GateOverride{Status: "REJECTD", Error: "008"},
	})
	if err != nil {
		t.Fatalf("NewSMPPSDLRRecord: %v", err)
	}
	status, ok := record.Fields()["gate_stat"].String()
	if !ok || status != "REJECTD" {
		t.Fatalf("gate_stat = %q (string %v), want REJECTD", status, ok)
	}
	gateErr, ok := record.Fields()["gate_err"].String()
	if !ok || gateErr != "008" {
		t.Fatalf("gate_err = %q (string %v), want 008", gateErr, ok)
	}
}
