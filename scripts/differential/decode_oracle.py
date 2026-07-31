#!/usr/bin/env python3
"""Check internal/core/msgcontent's vectors against the legacy Python decoder.

The vectors in internal/core/msgcontent/testdata/vectors.json are not hand-written
expectations: every one was produced by running the real decoder from
smsget-jasmin-sms-queues. That makes them evidence rather than opinion, but only
for as long as they still agree with it. This script is what re-checks that, and
what regenerates them if the Python side ever changes.

It loads app/decoder.py directly rather than importing the package, because
`import app` pulls in app/consumer.py, which imports a module that is not on the
path in a checkout.

Usage:
  scripts/differential/decode_oracle.py                     # check, exit 1 on drift
  scripts/differential/decode_oracle.py --update            # rewrite expectations
  scripts/differential/decode_oracle.py --decoder /path/to/app/decoder.py

Vectors marked known_failing are compared too: they record where the Go port
deliberately does not match, so a silent convergence is as much a change as a
divergence. See docs/reference/deviations.md D-004 for the one intended
difference (Options.PreserveOTPDigits), which is NOT a known_failing vector —
those run with Options{}, exact parity.
"""

import argparse
import importlib.util
import json
import sys
import warnings
from pathlib import Path
from types import ModuleType
from typing import Tuple

REPO_ROOT = Path(__file__).resolve().parents[2]
VECTORS = REPO_ROOT / "internal" / "core" / "msgcontent" / "testdata" / "vectors.json"
DEFAULT_DECODER = Path.home() / "Projects/shadowsms/smsget-jasmin-sms-queues/app/decoder.py"


def load_decoder(path: Path) -> ModuleType:
    if not path.exists():
        sys.exit(
            f"decoder not found at {path}\n"
            "Pass --decoder if the Python service lives elsewhere. This script is the\n"
            "only thing tying the vectors to their oracle; without it they are just\n"
            "numbers in a file."
        )
    spec = importlib.util.spec_from_file_location("legacy_decoder", str(path))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def decode(module: ModuleType, raw: bytes, data_coding: int) -> Tuple[str, str]:
    """Run one vector through the oracle the way the Go harness runs it.

    data_coding is -1 for the no-hint path, matching decode_test.go's vector
    struct: negative means decode_with_auto_detection, anything else is the DCS
    byte handed to extract_sms_text. The split has to match exactly, or this
    compares against a different question than the Go test asks.

    extract_sms_text itself cannot be called: it returns only the text, and the
    encoding label — which is half of what a vector pins — is logged and thrown
    away. So its body is reproduced here (decoder.py:817-856): strict UDH strip,
    DCS group resolution, then the DCS or auto path. Any change to that function
    must be mirrored here, which is why this note names the lines.
    """
    if data_coding < 0:
        return module.decode_with_auto_detection(raw)
    if not raw:
        return "", "none"

    message_data = raw
    udh_info = module.parse_udh(raw)
    if udh_info is not None:
        text_start = udh_info["udh_length"] + 1
        if text_start < len(raw):
            message_data = raw[text_start:]

    dcs = module._resolve_dcs_group(data_coding)
    if dcs != module.DataCoding.GSM_7BIT:
        return module.decode_with_dcs(message_data, dcs)
    return module.decode_with_auto_detection(message_data)


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--decoder", type=Path, default=DEFAULT_DECODER)
    parser.add_argument("--vectors", type=Path, default=VECTORS)
    parser.add_argument(
        "--update",
        action="store_true",
        help="rewrite the expected values from the oracle instead of comparing",
    )
    parser.add_argument("--quiet", action="store_true")
    args = parser.parse_args()

    # charset_normalizer warns on every short body; the noise would bury the diff.
    warnings.filterwarnings("ignore")

    module = load_decoder(args.decoder)
    vectors = json.loads(args.vectors.read_text(encoding="utf-8"))

    drift = []
    errors = []
    for vector in vectors:
        raw = bytes.fromhex(vector["input_hex"])
        data_coding = vector.get("data_coding", -1)
        try:
            text, encoding = decode(module, raw, data_coding)
        except Exception as exc:  # the oracle raising IS a result worth recording
            errors.append((vector["name"], repr(exc)))
            continue

        if args.update:
            vector["expected_text"] = text
            vector["expected_encoding"] = encoding
            continue

        want_text = vector["expected_text"]
        want_encoding = vector["expected_encoding"]
        if text != want_text or encoding != want_encoding:
            drift.append((vector["name"], want_text, want_encoding, text, encoding))

    if args.update:
        args.vectors.write_text(
            json.dumps(vectors, ensure_ascii=False, indent=2) + "\n", encoding="utf-8"
        )
        print(f"rewrote {len(vectors)} vectors from {args.decoder}")
        return 0

    for name, reason in errors:
        print(f"ERROR  {name}: {reason}")
    for name, want_text, want_encoding, got_text, got_encoding in drift:
        print(f"DRIFT  {name}")
        print(f"   file:   {want_text!r} via {want_encoding!r}")
        print(f"   oracle: {got_text!r} via {got_encoding!r}")

    if drift or errors:
        print(f"\n{len(drift)} vector(s) drifted, {len(errors)} error(s), out of {len(vectors)}")
        return 1
    if not args.quiet:
        print(f"{len(vectors)} vectors still match the oracle at {args.decoder}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
