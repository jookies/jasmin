package smppc

import "testing"

func TestConfigPDUDefaultsAppliesLegacyTONNPIDefaults(t *testing.T) {
	// An unset connector resolves the legacy SMPPClientConfig TON/NPI defaults.
	c := Config{CID: "c", Host: "h", Port: 1, SystemID: "s"}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	d := c.PDUDefaults()
	if d.SourceAddrTON != 2 || d.SourceAddrNPI != 1 || d.DestAddrTON != 1 || d.DestAddrNPI != 1 {
		t.Fatalf("defaults = src %d/%d dst %d/%d, want 2/1 1/1", d.SourceAddrTON, d.SourceAddrNPI, d.DestAddrTON, d.DestAddrNPI)
	}
	// An explicit connector value survives.
	c2 := Config{CID: "c", Host: "h", Port: 1, SystemID: "s", DstTON: 4, ServiceType: "CMT", ProtocolID: 0x34}
	if err := c2.Validate(); err != nil {
		t.Fatal(err)
	}
	d2 := c2.PDUDefaults()
	if d2.DestAddrTON != 4 || d2.ServiceType != "CMT" || d2.ProtocolID != 0x34 {
		t.Fatalf("explicit values lost: %+v", d2)
	}
}
