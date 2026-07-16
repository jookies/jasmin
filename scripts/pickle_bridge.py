import sys
import json
import pickle
import base64
import os

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
    if isinstance(obj, (list, tuple)):
        # Avoid marking immutable primitives as visited if you want, but lists/tuples should be tracked
        visited.add(id(obj))
        return [serialize(i, visited) for i in obj]
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
    logging.basicConfig(filename='pickle_bridge.log', level=logging.DEBUG)
    logging.debug("Bridge started")
    for line in sys.stdin:
        try:
            logging.debug(f"Received: {line.strip()}")
            req = json.loads(line)
            action = req.get("action")
            
            if action == "decode":
                data = base64.b64decode(req["data"])
                obj = pickle.loads(data)
                res = json.dumps({"status": "ok", "result": serialize(obj)})
                logging.debug(f"Sending: {res}")
                print(res)
            elif action == "encode":
                obj = deserialize(req["result"])
                data = pickle.dumps(obj)
                res = json.dumps({"status": "ok", "data": base64.b64encode(data).decode('ascii')})
                logging.debug(f"Sending: {res}")
                print(res)
            else:
                print(json.dumps({"status": "error", "message": "unknown action"}))
        except Exception as e:
            logging.exception("Error in bridge")
            print(json.dumps({"status": "error", "message": str(e)}))
        sys.stdout.flush()

if __name__ == "__main__":
    run()
