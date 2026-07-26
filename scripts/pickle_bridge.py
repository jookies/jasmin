import sys
import json
import pickle
import base64
import os
import re
import io
from datetime import date, datetime, time
from enum import Enum

# Add CWD to sys.path to find local jasmin package
sys.path.append(os.getcwd())

try:
    import smpp.pdu.operations
    import jasmin.routing.Routable
except ImportError:
    pass

def serialize(obj, visited=None):
    if visited is None:
        visited = set()
    
    if id(obj) in visited:
        return {"__type__": "ref", "id": id(obj)}
    
    if isinstance(obj, bytes):
        return {"__type__": "bytes", "base64": base64.b64encode(obj).decode('ascii')}
    if obj.__class__.__module__ == "smpp.pdu.pdu_types" and obj.__class__.__name__ == "PriorityFlag":
        from smpp.pdu.pdu_encoding import PriorityFlagEncoder
        return PriorityFlagEncoder().encode(obj)[0]
    if obj.__class__.__module__ == "smpp.pdu.pdu_types" and obj.__class__.__name__ == "DataCoding":
        from smpp.pdu.pdu_encoding import DataCodingEncoder
        return DataCodingEncoder().encode(obj)[0]
    if isinstance(obj, Enum):
        return obj.value
    if isinstance(obj, (datetime, date, time)):
        return obj.isoformat()
    if isinstance(obj, re.Pattern):
        return {"__type__": "regex", "pattern": serialize(obj.pattern, visited), "flags": obj.flags}
    if obj.__class__.__module__ == "smpp.pdu.pdu_types" and obj.__class__.__name__ == "EsmClass":
        from smpp.pdu.pdu_encoding import EsmClassEncoder
        return EsmClassEncoder().encode(obj)[0]
    if isinstance(obj, (list, tuple, set, frozenset)):
        # Sets occur inside SMPP enum/value objects; sort by repr so bridge
        # diagnostics remain deterministic without executing comparison hooks.
        values = list(obj)
        if isinstance(obj, (set, frozenset)):
            values.sort(key=repr)
        visited.add(id(obj))
        return [serialize(i, visited) for i in values]
    if isinstance(obj, dict):
        visited.add(id(obj))
        return {str(k): serialize(v, visited) for k, v in obj.items()}
    if hasattr(obj, "__dict__"):
        visited.add(id(obj))
        data = {k: serialize(v, visited) for k, v in obj.__dict__.items() if not k.startswith('_')}
        data["__class__"] = f"{obj.__class__.__module__}.{obj.__class__.__name__}"
        return data
    return obj

def deserialize(obj):
    if isinstance(obj, dict):
        if "__type__" in obj and obj["__type__"] == "bytes":
            return base64.b64decode(obj["base64"])
        if "__class__" in obj:
            cls_name = obj.pop("__class__")
            try:
                mod_name, class_name = cls_name.rsplit(".", 1)
                mod = __import__(mod_name, fromlist=[class_name])
                cls = getattr(mod, class_name)
                inst = cls.__new__(cls)
                for k, v in obj.items():
                    setattr(inst, k, deserialize(v))
                return inst
            except Exception:
                # Fallback to dict if class cannot be instantiated
                return {k: deserialize(v) for k, v in obj.items()}
        return {k: deserialize(v) for k, v in obj.items()}
    if isinstance(obj, list):
        return [deserialize(i) for i in obj]
    return obj


class DeliverSMUnpickler(pickle.Unpickler):
    """Restricted protocol-2 loader for routed deliver_sm bodies."""
    def find_class(self, module, name):
        allowed = (
            (module == "smpp.pdu.operations" and name in ("DeliverSM", "DataSM"))
            or module == "smpp.pdu.pdu_types"
            or (module == "_codecs" and name == "encode")
            or (module in ("__builtin__", "builtins") and name in ("bytes", "set", "frozenset"))
            or (module == "datetime" and name in ("datetime", "timezone", "timedelta"))
        )
        if not allowed:
            raise pickle.UnpicklingError("forbidden global %s.%s" % (module, name))
        return super().find_class(module, name)


class ConnectorListUnpickler(pickle.Unpickler):
    """Restricted loader for the dst-connectors header (HttpConnector list)."""
    def find_class(self, module, name):
        allowed = (
            (module == "jasmin.routing.jasminApi" and name in ("Connector", "HttpConnector", "SmppServerSystemIdConnector"))
        )
        if not allowed:
            raise pickle.UnpicklingError("forbidden global %s.%s" % (module, name))
        return super().find_class(module, name)


class SubmitSMUnpickler(pickle.Unpickler):
    """Restricted protocol-2 loader for the connector's trusted SubmitSM boundary."""
    def find_class(self, module, name):
        allowed = (
            (module == "smpp.pdu.operations" and name == "SubmitSM")
            or module == "smpp.pdu.pdu_types"
            or (module == "_codecs" and name == "encode")
            or (module in ("__builtin__", "builtins") and name in ("set", "frozenset"))
            or (module == "datetime" and name in ("datetime", "timezone", "timedelta"))
        )
        if not allowed:
            raise pickle.UnpicklingError("forbidden global %s.%s" % (module, name))
        return super().find_class(module, name)


def _encoded_byte(encoder, value, name):
    if value is None:
        return 0
    encoded = encoder().encode(value, name)
    if len(encoded) != 1:
        raise ValueError("%s did not encode to one byte" % name)
    return encoded[0]


def _data_coding_value(value):
    """Mirror SMPPClientProtocol.preSubmitSm: the HTTP-API front door pickles
    data_coding as a plain int, but DataCodingEncoder expects a DataCoding
    object. A known int becomes its DataCoding object; an unknown int becomes
    None (wire 0x00). A value already decoded off the wire (a DataCoding object)
    or None passes through untouched, so SMPP-origin submits are unaffected."""
    if not isinstance(value, int):
        return value
    from smpp.pdu.constants import data_coding_default_value_map
    from smpp.pdu.pdu_types import DataCoding, DataCodingDefault
    name = data_coding_default_value_map.get(value)
    if name is None:
        return None
    return DataCoding(schemeData=getattr(DataCodingDefault, name))


def _raw_byte(value, name):
    if value is None:
        return 0
    if isinstance(value, bool) or not isinstance(value, int) or value < 0 or value > 255:
        raise ValueError("%s is outside uint8" % name)
    return value


def _binary(value, name, maximum):
    if value is None:
        return b""
    if not isinstance(value, bytes) or len(value) > maximum:
        raise ValueError("%s is not bounded bytes" % name)
    return value


def _time_bytes(value, name):
    if value is None:
        return b""
    from smpp.pdu.pdu_encoding import TimeEncoder
    encoded = TimeEncoder().encode(value, name)
    if not encoded.endswith(b"\x00") or len(encoded) > 17:
        raise ValueError("%s has invalid SMPP time encoding" % name)
    return encoded[:-1]


def decode_submit_sm(data):
    from smpp.pdu.operations import SubmitSM
    from smpp.pdu.pdu_encoding import (
        AddrNpiEncoder, AddrTonEncoder, DataCodingEncoder, EsmClassEncoder,
        PriorityFlagEncoder, RegisteredDeliveryEncoder, ReplaceIfPresentFlagEncoder,
    )

    if not data.startswith(b"\x80\x02"):
        raise pickle.UnpicklingError("SubmitSM boundary requires protocol 2")
    stream = io.BytesIO(data)
    obj = SubmitSMUnpickler(stream).load()
    if stream.read(1):
        raise pickle.UnpicklingError("trailing bytes after SubmitSM pickle")
    if obj.__class__ is not SubmitSM:
        raise pickle.UnpicklingError("root object is not allowlisted SubmitSM")
    params = obj.params
    if not isinstance(params, dict):
        raise ValueError("SubmitSM params are not a mapping")

    result = {
        "service_type": _binary(params.get("service_type"), "service_type", 5),
        "source_addr_ton": _encoded_byte(AddrTonEncoder, params.get("source_addr_ton"), "source_addr_ton"),
        "source_addr_npi": _encoded_byte(AddrNpiEncoder, params.get("source_addr_npi"), "source_addr_npi"),
        "source_addr": _binary(params.get("source_addr"), "source_addr", 20),
        "dest_addr_ton": _encoded_byte(AddrTonEncoder, params.get("dest_addr_ton"), "dest_addr_ton"),
        "dest_addr_npi": _encoded_byte(AddrNpiEncoder, params.get("dest_addr_npi"), "dest_addr_npi"),
        "destination_addr": _binary(params.get("destination_addr"), "destination_addr", 20),
        "esm_class": _encoded_byte(EsmClassEncoder, params.get("esm_class"), "esm_class"),
        "protocol_id": _raw_byte(params.get("protocol_id"), "protocol_id"),
        "priority_flag": _encoded_byte(PriorityFlagEncoder, params.get("priority_flag"), "priority_flag"),
        "schedule_delivery_time": _time_bytes(params.get("schedule_delivery_time"), "schedule_delivery_time"),
        "validity_period": _time_bytes(params.get("validity_period"), "validity_period"),
        "registered_delivery": _encoded_byte(RegisteredDeliveryEncoder, params.get("registered_delivery"), "registered_delivery"),
        "replace_if_present_flag": _encoded_byte(ReplaceIfPresentFlagEncoder, params.get("replace_if_present_flag"), "replace_if_present_flag"),
        "data_coding": _encoded_byte(DataCodingEncoder, _data_coding_value(params.get("data_coding")), "data_coding"),
        "sm_default_msg_id": _raw_byte(params.get("sm_default_msg_id"), "sm_default_msg_id"),
        "short_message": _binary(params.get("short_message"), "short_message", 255),
        "optional_tlvs": [],
    }
    for key, tag, size in (
        ("sar_msg_ref_num", 0x020c, 2),
        ("sar_total_segments", 0x020e, 1),
        ("sar_segment_seqnum", 0x020f, 1),
    ):
        value = params.get(key)
        if value is not None:
            if isinstance(value, bool) or not isinstance(value, int) or value < 0 or value >= 1 << (size * 8):
                raise ValueError("%s is outside its wire width" % key)
            result["optional_tlvs"].append({"tag": tag, "value": value.to_bytes(size, "big")})
    payload = params.get("message_payload")
    if payload is not None:
        result["optional_tlvs"].append({"tag": 0x0424, "value": _binary(payload, "message_payload", 65535)})

    # Project pdu.custom_tlvs verbatim as [tag, length, type, value] entries so
    # the Go session can resolve connector rules, validate, and wire-encode
    # them exactly like the legacy listener + patched encoder. The legacy wire
    # path does not allowlist these tags — even SAR-range tags in custom_tlvs
    # are emitted verbatim as vendor TLVs — so no tag is routed into
    # optional_tlvs here. Structural checks stay minimal: the Go side enforces
    # the legacy crash boundaries and rejects what legacy would reject.
    result["custom_tlvs"] = []
    for item in getattr(obj, "custom_tlvs", []):
        if not isinstance(item, tuple) or len(item) != 4:
            raise ValueError("malformed custom TLV")
        result["custom_tlvs"].append(list(item))

    present_sar = {item["tag"] for item in result["optional_tlvs"] if item["tag"] in (0x020c, 0x020e, 0x020f)}
    if present_sar and present_sar != {0x020c, 0x020e, 0x020f}:
        raise ValueError("incomplete SAR option set")
    if present_sar:
        total = next(item["value"][0] for item in result["optional_tlvs"] if item["tag"] == 0x020e)
        sequence = next(item["value"][0] for item in result["optional_tlvs"] if item["tag"] == 0x020f)
        if total == 0 or sequence == 0 or sequence > total:
            raise ValueError("invalid SAR total/sequence")
    return serialize(result)


def decode_routed_deliver_sm(connectors_data, body_data):
    """Project a RoutedDeliverSmContent's pickled pieces for the MO thrower:
    the destination-connector list and the deliver_sm PDU as a wire-shaped
    body plus re-encoded optional TLVs and verbatim custom_tlvs tuples."""
    from smpp.pdu.operations import DeliverSM, DataSM
    from smpp.pdu.pdu_encoding import (
        AddrNpiEncoder, AddrTonEncoder, DataCodingEncoder, EsmClassEncoder,
        OptionEncoder, PriorityFlagEncoder, RegisteredDeliveryEncoder,
        ReplaceIfPresentFlagEncoder,
    )
    from smpp.pdu.pdu_types import Tag
    from smpp.pdu.constants import tag_name_map

    stream = io.BytesIO(connectors_data)
    connectors = ConnectorListUnpickler(stream).load()
    if stream.read(1):
        raise pickle.UnpicklingError("trailing bytes after connector list")
    if not isinstance(connectors, list) or not connectors:
        raise ValueError("dst-connectors is not a non-empty list")
    projected_connectors = []
    for connector in connectors:
        projected_connectors.append({
            "cid": str(getattr(connector, "cid", "")),
            "type": str(getattr(connector, "_type", "")),
            "baseurl": str(getattr(connector, "baseurl", "")),
            "method": str(getattr(connector, "method", "")),
        })

    stream = io.BytesIO(body_data)
    obj = DeliverSMUnpickler(stream).load()
    if stream.read(1):
        raise pickle.UnpicklingError("trailing bytes after deliver_sm pickle")
    if obj.__class__ not in (DeliverSM, DataSM):
        raise pickle.UnpicklingError("root object is not an allowlisted deliver pdu")
    params = obj.params
    if not isinstance(params, dict):
        raise ValueError("deliver_sm params are not a mapping")

    result = {
        "service_type": _binary(params.get("service_type"), "service_type", 5),
        "source_addr_ton": _encoded_byte(AddrTonEncoder, params.get("source_addr_ton"), "source_addr_ton"),
        "source_addr_npi": _encoded_byte(AddrNpiEncoder, params.get("source_addr_npi"), "source_addr_npi"),
        "source_addr": _binary(params.get("source_addr"), "source_addr", 20),
        "dest_addr_ton": _encoded_byte(AddrTonEncoder, params.get("dest_addr_ton"), "dest_addr_ton"),
        "dest_addr_npi": _encoded_byte(AddrNpiEncoder, params.get("dest_addr_npi"), "dest_addr_npi"),
        "destination_addr": _binary(params.get("destination_addr"), "destination_addr", 20),
        "esm_class": _encoded_byte(EsmClassEncoder, params.get("esm_class"), "esm_class"),
        "protocol_id": _raw_byte(params.get("protocol_id"), "protocol_id"),
        "priority_flag": _encoded_byte(PriorityFlagEncoder, params.get("priority_flag"), "priority_flag"),
        "registered_delivery": _encoded_byte(RegisteredDeliveryEncoder, params.get("registered_delivery"), "registered_delivery"),
        "replace_if_present_flag": _encoded_byte(ReplaceIfPresentFlagEncoder, params.get("replace_if_present_flag"), "replace_if_present_flag"),
        "data_coding": _encoded_byte(DataCodingEncoder, params.get("data_coding"), "data_coding"),
        "sm_default_msg_id": _raw_byte(params.get("sm_default_msg_id"), "sm_default_msg_id"),
        "short_message": _binary(params.get("short_message"), "short_message", 255),
        "connectors": projected_connectors,
    }

    # The legacy thrower sends str(validity_period) — a decoded datetime — not
    # the SMPP wire text; project the exact string it would send.
    if params.get("validity_period") is not None:
        result["validity_str"] = str(params["validity_period"])

    # Re-encode every present optional param through its legacy option encoder
    # so the Go side parses one wire-shaped section with the frozen codec.
    option_encoder = OptionEncoder()
    section = bytearray()
    for tag_member, encoder in option_encoder.options.items():
        name = tag_member.name
        if name == "vendor_specific_bypass" or name not in params or params[name] is None:
            continue
        if name in ("schedule_delivery_time", "validity_period"):
            continue
        value_bytes = encoder.encode(params[name])
        wire_tag = tag_name_map[name]
        section += wire_tag.to_bytes(2, "big") + len(value_bytes).to_bytes(2, "big") + value_bytes
    result["optional_section"] = bytes(section)

    result["custom_tlvs"] = []
    for item in getattr(obj, "custom_tlvs", []):
        if not isinstance(item, tuple) or len(item) != 4:
            raise ValueError("malformed custom TLV")
        result["custom_tlvs"].append(list(item))

    return serialize(result)


def run():
    import logging
    # A bridge invocation must not mutate the caller's working tree. Diagnostics
    # stay on stderr and never include request/response payloads.
    logging.basicConfig(stream=sys.stderr, level=logging.WARNING)
    for line in sys.stdin:
        try:
            req = json.loads(line)
            action = req.get("action")
            
            if action == "decode":
                data = base64.b64decode(req["data"])
                obj = pickle.loads(data)
                res = json.dumps({"status": "ok", "result": serialize(obj)})
                print(res)
            elif action == "decode_submit_sm":
                data = base64.b64decode(req["data"], validate=True)
                print(json.dumps({"status": "ok", "result": decode_submit_sm(data)}))
            elif action == "decode_routed_deliver_sm":
                connectors_data = base64.b64decode(req["connectors"], validate=True)
                body_data = base64.b64decode(req["data"], validate=True)
                print(json.dumps({"status": "ok", "result": decode_routed_deliver_sm(connectors_data, body_data)}))
            elif action == "encode":
                obj = deserialize(req["result"])
                data = pickle.dumps(obj)
                res = json.dumps({"status": "ok", "data": base64.b64encode(data).decode('ascii')})
                print(res)
            elif action == "encode_submit_sm_resp":
                from io import BytesIO
                from smpp.pdu.operations import SubmitSMResp
                from smpp.pdu.pdu_encoding import CommandStatusEncoder

                payload = req["result"]
                status_code = int(payload["command_status"])
                if status_code < 0 or status_code > 0xffffffff:
                    raise ValueError("command_status is outside uint32")
                sequence = int(payload["sequence"])
                if sequence < 1 or sequence > 0x7fffffff:
                    raise ValueError("sequence is outside SMPP range")
                status = CommandStatusEncoder().decode(BytesIO(status_code.to_bytes(4, "big")))
                kwargs = {}
                message_id = base64.b64decode(payload.get("message_id", ""), validate=True)
                if status_code == 0:
                    kwargs["message_id"] = message_id.decode("latin1")
                response = SubmitSMResp(seqNum=sequence, status=status, **kwargs)
                data = pickle.dumps(response, protocol=2)
                print(json.dumps({"status": "ok", "data": base64.b64encode(data).decode("ascii")}))
            elif action == "encode_submit_sm":
                from datetime import datetime
                from io import BytesIO
                from smpp.pdu.operations import SubmitSM
                from smpp.pdu.pdu_encoding import DataCodingEncoder, PriorityFlagEncoder
                from smpp.pdu.pdu_types import (
                    EsmClass, EsmClassGsmFeatures, EsmClassMode, EsmClassType,
                    RegisteredDelivery, RegisteredDeliveryReceipt,
                )
                from jasmin.routing.Bills import SubmitSmBill
                from jasmin.routing.jasminApi import Group, User

                payload = deserialize(req["result"])
                kwargs = {
                    "seqNum": int(payload.get("sequence", 1)),
                    "source_addr": payload.get("source_addr", b"").decode("latin1"),
                    "destination_addr": payload["destination_addr"].decode("latin1"),
                    "short_message": payload["short_message"],
                    "data_coding": DataCodingEncoder().decode(
                        BytesIO(bytes([int(payload.get("data_coding", 0))]))
                    ),
                    "priority_flag": PriorityFlagEncoder().decode(
                        BytesIO(bytes([int(payload.get("priority", 0))]))
                    ),
                }
                if payload.get("schedule_at"):
                    kwargs["schedule_delivery_time"] = datetime.fromisoformat(payload["schedule_at"])
                if payload.get("validity_until"):
                    kwargs["validity_period"] = datetime.fromisoformat(payload["validity_until"])
                if payload.get("udh"):
                    kwargs["esm_class"] = EsmClass(
                        EsmClassMode.DEFAULT,
                        EsmClassType.DEFAULT,
                        [EsmClassGsmFeatures.UDHI_INDICATOR_SET],
                    )
                if payload.get("registered_delivery"):
                    kwargs["registered_delivery"] = RegisteredDelivery(
                        RegisteredDeliveryReceipt.SMSC_DELIVERY_RECEIPT_REQUESTED
                    )
                else:
                    kwargs["registered_delivery"] = RegisteredDelivery(
                        RegisteredDeliveryReceipt.NO_SMSC_DELIVERY_RECEIPT_REQUESTED
                    )
                sar = payload.get("sar")
                if sar:
                    kwargs["sar_msg_ref_num"] = int(sar["reference"])
                    kwargs["sar_total_segments"] = int(sar["total"])
                    kwargs["sar_segment_seqnum"] = int(sar["sequence"])

                pdu = SubmitSM(**kwargs)
                # Each entry is the Python tuple shape [tag, length, type, value]
                # produced by the Go front door (tlv.Normalize output). Values
                # arrived through deserialize(), so bytes wrappers are already
                # Python bytes; everything else (str/int/float/bool/None) is
                # carried verbatim and the legacy listener resolves, validates,
                # and wire-encodes exactly as for Python-published submits.
                custom_tlvs = []
                for item in payload.get("custom_tlvs", []):
                    if not isinstance(item, list) or len(item) != 4:
                        raise ValueError("malformed custom TLV tuple")
                    if isinstance(item[0], bool) or not isinstance(item[0], int):
                        raise ValueError("custom TLV tag is not an integer")
                    custom_tlvs.append(tuple(item))
                if custom_tlvs:
                    pdu.custom_tlvs = custom_tlvs

                body = pickle.dumps(pdu, protocol=2)
                bill_data = None
                if payload.get("include_bill", True):
                    group = Group("runtime")
                    user = User(payload["user_id"], group, payload["username"], "runtime")
                    bill = SubmitSmBill(user)
                    bill.bid = payload["bill_id"]
                    bill.setAmount("submit_sm", float(payload.get("submit_sm_amount", 0)))
                    bill.setAmount("submit_sm_resp", float(payload.get("submit_sm_resp_amount", 0)))
                    bill.setAction("decrement_submit_sm_count", int(payload.get("decrement_submit_sm_count", 0)))
                    bill_data = pickle.dumps(bill, protocol=2)

                result = {
                    "body": base64.b64encode(body).decode("ascii"),
                    "bill": None if bill_data is None else base64.b64encode(bill_data).decode("ascii"),
                }
                print(json.dumps({"status": "ok", "result": result}))
            else:
                print(json.dumps({"status": "error", "message": "unknown action"}))
        except Exception as e:
            logging.error("bridge request failed: %s", type(e).__name__)
            print(json.dumps({"status": "error", "message": str(e)}))
        sys.stdout.flush()

if __name__ == "__main__":
    run()
