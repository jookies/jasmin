import sys
import json
import pickle
import base64
import os
import re
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
            elif action == "encode":
                obj = deserialize(req["result"])
                data = pickle.dumps(obj)
                res = json.dumps({"status": "ok", "data": base64.b64encode(data).decode('ascii')})
                print(res)
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
                custom_tlvs = []
                for item in payload.get("custom_tlvs", []):
                    value = item["value"]
                    custom_tlvs.append((int(item["tag"]), len(value), "octet_string", value))
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
