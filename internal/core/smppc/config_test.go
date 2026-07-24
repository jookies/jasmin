package smppc

import "testing"

func TestConfigCustomTLVRules(t *testing.T) {
	length := 32
	cfg := Config{
		CID: "c", Host: "h", Port: 2775, SystemID: "s",
		CustomTLVs: []CustomTLVRule{
			{Tag: 0x11400, Type: "Int8", Required: true},
			{Tag: 0x1401, Type: "COctetString", Length: &length},
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	rules := cfg.ConnectorTLVRules()
	// Legacy rule maps mask tags with int(tag) & 0xFFFF.
	if len(rules) != 2 || rules[0].Tag != 0x1400 || !rules[0].Required || rules[1].Tag != 0x1401 {
		t.Fatalf("rules = %+v", rules)
	}
	if rules[1].Length == nil || *rules[1].Length != 32 || rules[0].Length != nil {
		t.Fatalf("lengths = %+v", rules)
	}

	bad := cfg.Clone()
	bad.CustomTLVs[0].Type = "Int9"
	if err := bad.Validate(); err == nil {
		t.Fatal("invalid type must fail validation")
	}
	zero := 0
	bad2 := cfg.Clone()
	bad2.CustomTLVs[1].Length = &zero
	if err := bad2.Validate(); err == nil {
		t.Fatal("non-positive length must fail validation")
	}
	// Clone must not share rule slices or length pointers.
	clone := cfg.Clone()
	*clone.CustomTLVs[1].Length = 99
	clone.CustomTLVs[0].Type = "Int1"
	if *cfg.CustomTLVs[1].Length != 32 || cfg.CustomTLVs[0].Type != "Int8" {
		t.Fatalf("Clone shares custom TLV state: %+v", cfg.CustomTLVs)
	}
}
