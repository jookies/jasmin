package picklecompat_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/pumpitspace/jasmin/internal/core/smppc"
	"github.com/pumpitspace/jasmin/internal/transport/picklecompat"
	"github.com/pumpitspace/jasmin/internal/transport/smppwire"
)

// sendPathOracleScript builds a submit_sm through the FULL legacy front-door
// send path — SMPPOperationFactory(config).SubmitSM(...) then the send.py
// update_submit_sm_pdu(routable, config) re-application and the protocol.py
// preSubmitSm steps (default source_addr from config, data_coding int->object)
// — and prints the encoded wire body (offset 16+, header stripped). This is the
// exact byte sequence a strict SMSC receives for a Go front-door submit; the Go
// side must reproduce it from the routed connector's PDUDefaults (GAP 4).
//
// The spec (argv[1]) carries only the connector's *overrides*; unset fields are
// null so SMPPClientConfig applies its own legacy defaults (source_addr_ton
// NATIONAL/2, source_addr_npi ISDN/1, dest_addr_ton INTERNATIONAL/1,
// dest_addr_npi ISDN/1, service_type None, protocol_id None, sm_default_msg_id
// 0), mirroring how an unset smppc.Config resolves via Validate()+PDUDefaults().
const sendPathOracleScript = `
import sys, json, binascii
from jasmin.protocols.smpp.configs import SMPPClientConfig
from jasmin.protocols.smpp.operations import SMPPOperationFactory
from jasmin.protocols.http.endpoints.send import update_submit_sm_pdu
from jasmin.routing.Routables import RoutableSubmitSm
from jasmin.routing.jasminApi import User, Group
from io import BytesIO
from smpp.pdu.pdu_encoding import PDUEncoder, AddrTonEncoder, AddrNpiEncoder
from smpp.pdu.constants import data_coding_default_value_map
from smpp.pdu.pdu_types import DataCoding, DataCodingDefault

# Interpret spec TON/NPI as WIRE bytes decoded like the bridge does — the
# AddrTon/AddrNpi enums are 1-indexed (NATIONAL=3) and do NOT equal the wire
# byte (NATIONAL->wire 2), so enum-by-value would misencode.
_ton = lambda v: AddrTonEncoder().decode(BytesIO(bytes([v])))
_npi = lambda v: AddrNpiEncoder().decode(BytesIO(bytes([v])))

spec = json.loads(sys.argv[1])
c = spec["config"]
ck = {"id": "oracletest"}
if c.get("src_ton") is not None: ck["source_addr_ton"] = _ton(c["src_ton"])
if c.get("src_npi") is not None: ck["source_addr_npi"] = _npi(c["src_npi"])
if c.get("dst_ton") is not None: ck["dest_addr_ton"] = _ton(c["dst_ton"])
if c.get("dst_npi") is not None: ck["dest_addr_npi"] = _npi(c["dst_npi"])
if c.get("service_type"): ck["service_type"] = c["service_type"]
if c.get("protocol_id"): ck["protocol_id"] = c["protocol_id"]
if c.get("sm_default_msg_id"): ck["sm_default_msg_id"] = c["sm_default_msg_id"]
if c.get("source_addr"): ck["source_addr"] = c["source_addr"].encode("latin1")
config = SMPPClientConfig(**ck)

m = spec["msg"]
sk = {"destination_addr": m["destination_addr"].encode("latin1"),
      "short_message": m["short_message"].encode("latin1"),
      "data_coding": m["data_coding"]}
if m.get("source_addr"):
    sk["source_addr"] = m["source_addr"].encode("latin1")
pdu = SMPPOperationFactory(config).SubmitSM(**sk)

# Full legacy send path: wrap in a routable and re-apply the connector param
# list exactly like jasmin.protocols.http.endpoints.send.update_submit_sm_pdu.
routable = RoutableSubmitSm(pdu, User("1", Group("g"), "u", "p"))
update_submit_sm_pdu(routable, config)
pdu = routable.pdu

# protocol.py preSubmitSm: fill an unset source_addr from the connector config,
# then convert data_coding int -> DataCoding object for the wire encoder.
if pdu.params.get("source_addr") is None and config.source_addr is not None:
    pdu.params["source_addr"] = config.source_addr
dc = pdu.params.get("data_coding")
if isinstance(dc, int):
    name = data_coding_default_value_map.get(dc)
    pdu.params["data_coding"] = DataCoding(schemeData=getattr(DataCodingDefault, name)) if name else None

print(binascii.hexlify(PDUEncoder().encode(pdu)[16:]).decode())
`

// TestFrontDoorSubmitBodyMatchesLegacySendPath closes plan 003 Step 5: the Go
// front-door submit body equals the legacy full-send-path wire body byte-for-
// byte, for the DEFAULT connector and non-default connectors, across a matrix
// of PDU-param shapes. The Go side derives its params from smppc.Config the same
// way production does (Validate + PDUDefaults + the empty-source default fill),
// so this proves the resolved defaults, not hand-picked constants.
func TestFrontDoorSubmitBodyMatchesLegacySendPath(t *testing.T) {
	pythonPath := os.Getenv("PYTHON_PATH")
	if pythonPath == "" {
		t.Skip("PYTHON_PATH is required")
	}

	rows := []struct {
		name       string
		cfg        smppc.Config // connector PDU-default overrides (pre-Validate)
		source     string       // submit source_addr ("" exercises the config default)
		dest       string
		message    string
		dataCoding uint8
	}{
		{
			name:   "default connector (legacy TON/NPI 2/1/1/1, no service_type/protocol_id)",
			cfg:    smppc.Config{},
			source: "1111", dest: "2222", message: "hi", dataCoding: 0,
		},
		{
			name:   "non-default TON/NPI + service_type",
			cfg:    smppc.Config{SrcTON: 5, SrcNPI: 0, DstTON: 1, DstNPI: 1, ServiceType: "CMT"},
			source: "SENDER", dest: "31612345678", message: "hello world", dataCoding: 0,
		},
		{
			name:   "protocol_id + sm_default_msg_id + service_type",
			cfg:    smppc.Config{ProtocolID: 0x34, SmDefaultMsgID: 5, ServiceType: "WAP"},
			source: "999", dest: "2255", message: "params", dataCoding: 0,
		},
		{
			name:   "empty source falls back to connector source_addr (preSubmitSm)",
			cfg:    smppc.Config{SourceAddr: "BRAND"},
			source: "", dest: "2222", message: "from-config", dataCoding: 0,
		},
		{
			name:   "UCS2 data_coding with connector defaults",
			cfg:    smppc.Config{ServiceType: "CMT"},
			source: "1111", dest: "2222", message: "hi", dataCoding: 8,
		},
	}

	bridgeCtx, cancelBridge := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancelBridge()
	bridge, err := picklecompat.NewBridge(bridgeCtx, pythonPath)
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Close()

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			// Legacy body via the full send path.
			spec := map[string]any{
				"config": configSpec(row.cfg),
				"msg": map[string]any{
					"source_addr":      row.source,
					"destination_addr": row.dest,
					"short_message":    row.message,
					"data_coding":      row.dataCoding,
				},
			}
			specJSON, err := json.Marshal(spec)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, pythonPath, "-c", sendPathOracleScript, string(specJSON))
			command.Env = append(os.Environ(), "PYTHONPATH=../../..")
			output, err := command.Output()
			if err != nil {
				t.Fatalf("oracle: %v (%s)", err, output)
			}
			legacyBody, err := hex.DecodeString(string(bytes.TrimSpace(output)))
			if err != nil {
				t.Fatalf("oracle hex %q: %v", output, err)
			}

			// Go body: resolve the connector's PDU defaults exactly like production.
			cfg := row.cfg
			cfg.CID, cfg.Host, cfg.Port, cfg.SystemID = "oracletest", "127.0.0.1", 2775, "s"
			if err := cfg.Validate(); err != nil {
				t.Fatalf("validate config: %v", err)
			}
			defaults := cfg.PDUDefaults()
			source := row.source
			if source == "" && defaults.SourceAddr != "" {
				source = defaults.SourceAddr // submit_service empty-source fallback
			}
			encoded, err := bridge.EncodeSubmitSM(ctx, picklecompat.SubmitSMEncodeRequest{
				Sequence:             1,
				SourceAddr:           picklecompat.Bytes(source),
				DestinationAddr:      picklecompat.Bytes(row.dest),
				ShortMessage:         picklecompat.Bytes(row.message),
				DataCoding:           row.dataCoding,
				SourceAddrTON:        defaults.SourceAddrTON,
				SourceAddrNPI:        defaults.SourceAddrNPI,
				DestAddrTON:          defaults.DestAddrTON,
				DestAddrNPI:          defaults.DestAddrNPI,
				ServiceType:          defaults.ServiceType,
				ProtocolID:           defaults.ProtocolID,
				ReplaceIfPresentFlag: defaults.ReplaceIfPresentFlag,
				SmDefaultMsgID:       defaults.SmDefaultMsgID,
				IncludeBill:          false,
			})
			if err != nil {
				t.Fatal(err)
			}
			body, _, err := bridge.DecodeSubmitSM(ctx, encoded.Body)
			if err != nil {
				t.Fatal(err)
			}
			wire, err := smppwire.Encode(smppwire.PDU{
				Header: smppwire.Header{CommandID: smppwire.CommandSubmitSM, SequenceNumber: 1},
				SM:     &body,
			})
			if err != nil {
				t.Fatal(err)
			}
			goBody := wire[16:]
			if !bytes.Equal(goBody, legacyBody) {
				t.Fatalf("send-path body diverges:\n  go %s\n  py %s",
					hex.EncodeToString(goBody), hex.EncodeToString(legacyBody))
			}
		})
	}
}

// configSpec renders a connector's PDU-param overrides for the oracle: a zero/
// empty field is sent as null so SMPPClientConfig applies its legacy default,
// symmetric with how smppc.Config.Validate() resolves the same zero to the same
// default before PDUDefaults() reads it.
func configSpec(cfg smppc.Config) map[string]any {
	spec := map[string]any{}
	putInt := func(key string, value int) {
		if value != 0 {
			spec[key] = value
		}
	}
	putInt("src_ton", cfg.SrcTON)
	putInt("src_npi", cfg.SrcNPI)
	putInt("dst_ton", cfg.DstTON)
	putInt("dst_npi", cfg.DstNPI)
	putInt("protocol_id", int(cfg.ProtocolID))
	putInt("sm_default_msg_id", int(cfg.SmDefaultMsgID))
	if cfg.ServiceType != "" {
		spec["service_type"] = cfg.ServiceType
	}
	if cfg.SourceAddr != "" {
		spec["source_addr"] = cfg.SourceAddr
	}
	return spec
}
