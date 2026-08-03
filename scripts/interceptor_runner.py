"""MO/MT interceptor script runner (jasmin InterceptorPB.perspective_run_script).

A JSON-lines subprocess, like scripts/pickle_bridge.py: each stdin line is a
request, each stdout line the response. It runs a user's Python interception
script with the legacy globals and script contract, on a routable projected
from the Go submit/deliver path.

Faithfulness boundary: the Go MT interception hook runs pre-encode, so the
routable exposed here carries the fields Go routing sees — source_addr,
destination_addr, short_message (as bytes, in routable.pdu.params) and tags —
plus smpp_status/http_status/extra. Scripts that read/mutate those fields, set
tags, or set a status to reject behave exactly as under interceptord. Mutating
PDU params not present pre-encode (e.g. data_coding) is out of scope of this
hook and is ignored on the way back.
"""
import sys
import json
import base64
import datetime as dt


def _apply_resource_limits():
    """Bound what one interception script can consume from the host.

    This is containment, not a sandbox. Scripts run with the full Python
    builtins on purpose: the interceptord contract is a Jasmin one, real
    customer scripts use ordinary Python, and an allowlist would break them.
    Anyone who can author an interceptor can therefore read files, exec, and
    open sockets AS THE GATEWAY'S OS USER -- authoring an interceptor is
    equivalent to shell access on this host, and the deployment has to treat it
    that way (see docs/operations/security.md).

    What these limits do give is a bound on the damage a runaway or hostile
    script does to everything else in the process: an address-space cap turns a
    memory bomb into a MemoryError instead of an OOM kill of the whole gateway,
    and a CPU cap backstops the runner's own wall-clock deadline for a script
    that blocks in C code where that deadline cannot interrupt it.
    """
    try:
        import resource
    except ImportError:  # non-POSIX; the Go-side deadline still applies
        return
    for name, limit in (
        # Wall-clock is bounded by the Go runner (DefaultScriptTimeout). This is
        # the CPU-time backstop for a script the deadline cannot interrupt.
        ("RLIMIT_CPU", 30),
        # 512 MiB of address space: far above any legitimate script, far below
        # what it takes to destabilise the host.
        ("RLIMIT_AS", 512 * 1024 * 1024),
        # A script has no reason to write files; if it does, not gigabytes.
        ("RLIMIT_FSIZE", 16 * 1024 * 1024),
        # No core dumps: they would contain message content.
        ("RLIMIT_CORE", 0),
    ):
        constant = getattr(resource, name, None)
        if constant is None:
            continue
        try:
            soft, hard = resource.getrlimit(constant)
            # Never raise an existing limit, and never exceed the hard ceiling.
            target = limit if hard == resource.RLIM_INFINITY else min(limit, hard)
            if soft != resource.RLIM_INFINITY and soft <= target:
                continue
            resource.setrlimit(constant, (target, hard))
        except (ValueError, OSError):
            # A hardened host may already forbid raising or lowering this. The
            # limit is defence in depth, so failing to set one is not fatal.
            continue


_apply_resource_limits()


class _PDU:
    """A minimal pdu proxy exposing .params (a dict of bytes), like the legacy
    RoutableSubmitSm.pdu the scripts mutate."""
    def __init__(self, params):
        self.params = params
        self.id = "submit_sm"


class _Routable:
    """A minimal routable proxy: .pdu.params plurality plus .tags, matching the
    attributes interception scripts commonly touch."""
    def __init__(self, params, tags):
        self.pdu = _PDU(params)
        self.tags = tags


# Safe stdlib modules exposed to scripts, matching the legacy runner globals.
_SAFE_MODULES = {
    "hashlib": __import__("hashlib"),
    "re": __import__("re"),
    "json": __import__("json"),
    "datetime": __import__("datetime"),
    "math": __import__("math"),
    "struct": __import__("struct"),
}

_compiled = {}


def _compile(py_code):
    key = hash(py_code)
    if key not in _compiled:
        _compiled[key] = compile(py_code, "", "exec")
    return _compiled[key]


def _b(value):
    return base64.b64decode(value) if value else b""


def _run(req):
    params = {
        "source_addr": _b(req["routable"].get("source_addr")),
        "destination_addr": _b(req["routable"].get("destination_addr")),
        "short_message": _b(req["routable"].get("short_message")),
    }
    tags = list(req["routable"].get("tags") or [])
    routable = _Routable(params, tags)

    glo = {
        "routable": routable,
        "smpp_status": req.get("smpp_status"),
        "http_status": req.get("http_status"),
        "extra": {},
    }
    glo.update(_SAFE_MODULES)

    start = dt.datetime.now()
    eval(_compile(req["py_code"]), glo)  # noqa: S307 — the interceptord contract
    delay_ms = int((dt.datetime.now() - start).total_seconds() * 1000)

    smpp_status = glo["smpp_status"]
    http_status = glo["http_status"]
    if smpp_status is None and http_status is None:
        action = "continue"
    else:
        # Legacy: if one status is set both must be, so an error surfaces on
        # both apis. Non-int smpp -> 255 (ESME_RUNKNOWNERR), non-int http -> 520.
        action = "reject"
        if not isinstance(smpp_status, int):
            smpp_status = 255
        if not isinstance(http_status, int):
            http_status = 520

    def _enc(value):
        return base64.b64encode(value if isinstance(value, bytes) else str(value).encode("latin1")).decode("ascii")

    return {
        "status": "ok",
        "action": action,
        "smpp_status": smpp_status,
        "http_status": http_status,
        "routable": {
            "source_addr": _enc(routable.pdu.params.get("source_addr", b"")),
            "destination_addr": _enc(routable.pdu.params.get("destination_addr", b"")),
            "short_message": _enc(routable.pdu.params.get("short_message", b"")),
            "tags": [str(t) for t in routable.tags],
        },
        "extra": glo["extra"],
        "delay_ms": delay_ms,
    }


def run():
    import logging
    logging.basicConfig(stream=sys.stderr, level=logging.WARNING)
    for line in sys.stdin:
        try:
            req = json.loads(line)
            if req.get("action") == "ping":
                print(json.dumps({"status": "ok"}))
            elif req.get("action") == "run":
                print(json.dumps(_run(req)))
            else:
                print(json.dumps({"status": "error", "message": "unknown action"}))
        except Exception as e:  # noqa: BLE001 — the runner must never crash the loop
            logging.error("interceptor script failed: %s", type(e).__name__)
            print(json.dumps({"status": "error", "message": str(e)}))
        sys.stdout.flush()


if __name__ == "__main__":
    run()
