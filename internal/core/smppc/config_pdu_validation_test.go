package smppc

import "testing"

// TestConfigRejectsInvalidPDUDefaultBytes proves the connector config's PDU
// defaults carry SMPP WIRE bytes and are range-checked at load: an out-of-range
// TON/NPI/protocol_id/replace flag is rejected here rather than silently
// truncated by uint8() or poisoning every submit at bridge encode time.
func TestConfigRejectsInvalidPDUDefaultBytes(t *testing.T) {
	base := func() Config { return Config{CID: "c", Host: "h", Port: 1, SystemID: "s"} }

	valid := map[string]func(*Config){
		"max ton / defaulted ton":  func(c *Config) { c.SrcTON = 6; c.DstTON = 0 },
		"npi set members":          func(c *Config) { c.SrcNPI = 18; c.DstNPI = 14 },
		"max protocol + msg id":    func(c *Config) { c.ProtocolID = 255; c.SmDefaultMsgID = 255 },
		"replace do-not / replace": func(c *Config) { c.ReplaceIfPresentFlag = 1 },
	}
	for name, mutate := range valid {
		t.Run("valid/"+name, func(t *testing.T) {
			c := base()
			mutate(&c)
			if err := c.Validate(); err != nil {
				t.Fatalf("valid config rejected: %v", err)
			}
		})
	}

	invalid := map[string]func(*Config){
		"ton above 6":         func(c *Config) { c.SrcTON = 7 },
		"ton out of byte":     func(c *Config) { c.DstTON = 300 },
		"ton negative":        func(c *Config) { c.SrcTON = -1 },
		"npi not in set":      func(c *Config) { c.SrcNPI = 2 },
		"npi out of byte":     func(c *Config) { c.DstNPI = 256 },
		"protocol_id too big": func(c *Config) { c.ProtocolID = 256 },
		"sm_default too big":  func(c *Config) { c.SmDefaultMsgID = 256 },
		"replace above 1":     func(c *Config) { c.ReplaceIfPresentFlag = 2 },
	}
	for name, mutate := range invalid {
		t.Run("invalid/"+name, func(t *testing.T) {
			c := base()
			mutate(&c)
			if err := c.Validate(); err == nil {
				t.Fatalf("invalid config accepted (%s)", name)
			}
		})
	}
}
