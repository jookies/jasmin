"""Symmetric handling of vendor-range TLVs on inbound SMPP PDUs.

Outbound (encode) direction is already covered by the installed smpp.pdu3
(>= 0.6): `PDUEncoder.encodeBody` calls `encodeRawParams(pdu.custom_tlvs)` and
serialises each `(tag, length, value_type, value)` tuple onto the wire. The
TLV commit (354d31e1) already populates `pdu.custom_tlvs` before dispatch, so
no encode-side patch is needed.

Inbound (decode) is the missing half: `OptionEncoder._decode` maps any tag
outside its known whitelist to `Tag.vendor_specific_bypass`, and
`decodeOptionalParams` silently skips those entries — so MO / DLR /
submit_sm PDUs arriving with vendor-range TLVs have them consumed off the
wire but never exposed on the PDU object. `format_tlvs_for_log` and any
downstream code therefore see "none" on the inbound path.

This module installs a monkey-patch on the decode side that captures the
original tag integer and raw octet value into a thread-local bucket and
attaches it to the PDU as `pdu.custom_tlvs` using the same
`(tag, length, type, value)` tuple shape the encode side uses. That makes
the TLV attribute symmetric in both directions.
"""

from __future__ import annotations

import struct
import threading
from typing import Any, Iterable


# Keep this set in sync with:
#   jasmin/protocols/smpp/configs.py       -> valid_tlv_types
#   jasmin/protocols/cli/smppccm.py        -> parseTlvString valid_types
_TYPE_TO_INT_FMT = {
    'Int1': '>B',
    'Int2': '>H',
    'Int4': '>I',
    'Int8': '>Q',
}


_VALID_TLV_TYPES = {'Int1', 'Int2', 'Int4', 'Int8', 'OctetString', 'COctetString'}


def _parse_tag_key(tag_key: str) -> tuple[int, str | None]:
    """Parse a tag key that may carry an optional type hint.

    Accepted formats::

        "0x1401"                → (0x1401, None)
        "0x1401:OctetString"    → (0x1401, 'OctetString')
        "0x1401:Int8"           → (0x1401, 'Int8')
        "5121"                  → (5121,   None)
        "5121:Int4"             → (5121,   'Int4')

    The type hint is optional.  When present it takes precedence over the
    connector config; when absent ``resolve_tlv_types()`` fills it in at
    dispatch time.
    """
    tag_key = tag_key.strip()
    tlv_type = None
    if ':' in tag_key:
        parts = tag_key.split(':', 1)
        tag_str = parts[0].strip()
        type_candidate = parts[1].strip()
        if type_candidate in _VALID_TLV_TYPES:
            tlv_type = type_candidate
        else:
            # Colon is part of the tag string (shouldn't happen, but be safe)
            tag_str = tag_key
    else:
        tag_str = tag_key
    tag_int = int(tag_str, 16) if tag_str.lower().startswith('0x') else int(tag_str)
    return tag_int, tlv_type


def normalize_custom_tlvs(raw: Any) -> list[tuple]:
    """Accept various input shapes for per-message custom_tlvs and normalize
    to the internal tuple list ``[(tag_int, None, type_or_None, value), ...]``.

    Accepted shapes on the REST / HTTP API boundary:

    **Preferred** — dict with optional type hint in the key::

        {"0x1401:OctetString": "1401778070000018542",
         "0x1400": "1707167205648943173"}

    When the type is omitted from the key, the connector config provides it
    at dispatch time via ``resolve_tlv_types()``.

    **Legacy** — list of 4-tuples ``[tag, length, type, value]``::

        [[5121, null, "Int8", 1707167205648943173]]

    Empty / falsy input yields ``[]``.
    """
    if not raw:
        return []

    # ---- dict: {"0x1401": value, "0x1401:OctetString": value, ...} -----
    if isinstance(raw, dict):
        result = []
        for tag_key, value in raw.items():
            if isinstance(tag_key, str):
                tag_int, tlv_type = _parse_tag_key(tag_key)
            else:
                tag_int = int(tag_key)
                tlv_type = None
            result.append((tag_int, None, tlv_type, value))
        return result

    # ---- list: legacy [[tag, len, type, val], ...] or already tuples ----
    if isinstance(raw, (list, tuple)):
        result = []
        for entry in raw:
            if isinstance(entry, dict):
                # [{"tag": "0x1401", "value": 123}, ...]
                tag_key = entry.get('tag', entry.get('0', 0))
                if isinstance(tag_key, str):
                    tag_key = tag_key.strip()
                    tag_int = int(tag_key, 16) if tag_key.lower().startswith('0x') else int(tag_key)
                else:
                    tag_int = int(tag_key)
                result.append((tag_int, None, None, entry.get('value')))
            elif isinstance(entry, (list, tuple)):
                if len(entry) >= 4:
                    result.append(tuple(entry[:4]))
                elif len(entry) >= 2:
                    result.append((int(entry[0]), None, None, entry[-1]))
                # else skip malformed
            else:
                pass  # skip scalar
        return result

    # ---- JSON string (from URL query-param encoding) --------------------
    if isinstance(raw, (str, bytes)):
        import json
        if isinstance(raw, bytes):
            raw = raw.decode('utf-8', errors='replace')
        raw = raw.strip()
        if not raw:
            return []
        try:
            parsed = json.loads(raw)
        except (json.JSONDecodeError, ValueError):
            return []
        return normalize_custom_tlvs(parsed)

    return []


def resolve_tlv_types(pdu_tlvs: list, connector_rules: list) -> list:
    """Resolve ``None``-typed TLV tuples using the connector's declared types.

    Per-message TLVs arrive from the REST caller as ``(tag, None, None, value)``
    (normalised form). The connector config declares each tag's type. This
    function matches them up so the upstream encoder can serialize correctly.

    Tuples whose tag is NOT in the connector rules default to ``'OctetString'``.
    Already-typed tuples (type not None) are left as-is.
    """
    if not pdu_tlvs:
        return pdu_tlvs

    rule_map = {}
    for r in (connector_rules or []):
        rule_map[int(r['tag']) & 0xFFFF] = r

    result = []
    for t in pdu_tlvs:
        if len(t) < 4 or t[2] is not None:
            result.append(t)
            continue
        tag = int(t[0]) & 0xFFFF
        rule = rule_map.get(tag)
        tlv_type = rule['type'] if rule else 'OctetString'
        # For integer types, coerce value to int if it's still a string.
        value = t[3]
        if tlv_type in _TYPE_TO_INT_FMT and isinstance(value, str):
            value = int(value, 16) if value.lower().startswith('0x') else int(value)
        result.append((t[0], t[1], tlv_type, value))
    return result


def encode_tlv_value(value: Any, tlv_type: str | None) -> bytes:
    """Encode a TLV value according to its declared SMPP type.

    - Bytes are returned verbatim (allows the caller to pre-encode if needed).
    - Int types pack big-endian via struct.
    - OctetString: UTF-8 bytes of str(value).
    - COctetString: UTF-8 bytes of str(value) plus a terminating NUL.
    - Unknown / None type: fall back to UTF-8 bytes (safe default for
      vendor-range identifiers that are typically ASCII digit strings).
    """
    if isinstance(value, (bytes, bytearray)):
        return bytes(value)

    t = (tlv_type or '').strip()
    if t in _TYPE_TO_INT_FMT:
        return struct.pack(_TYPE_TO_INT_FMT[t], int(value))
    if t == 'COctetString':
        return str(value).encode('utf-8') + b'\x00'
    # 'OctetString' or unknown/None → raw UTF-8 bytes
    return str(value).encode('utf-8')


def encoded_value_length(value: Any, tlv_type: str | None) -> int:
    """Return the byte length that `value` would take on the wire for the
    given TLV type. Used by connector-level max-length validation."""
    return len(encode_tlv_value(value, tlv_type))


def validate_custom_tlvs(pdu_tlvs: Iterable | None,
                         connector_rules: Iterable | None) -> tuple[bool, str | None]:
    """Validate per-message PDU TLVs against connector rules.

    `pdu_tlvs` is the PDU's `custom_tlvs` list: tuples `(tag, length, type, value)`.
    `connector_rules` is the smppcc `custom_tlvs` config: list of dicts
    `{tag, type, length (max bytes, None = unbounded), required}`.

    Returns `(True, None)` if OK, else `(False, "<human message>")`.
    Rules checked:
      - required tag not present   -> fail
      - encoded value length > max -> fail

    Per-message TLVs whose tag is NOT in the rule set are allowed through
    untouched (the connector only expresses *rules*, not a whitelist).
    """
    if not connector_rules:
        return True, None

    by_tag = {}
    for t in (pdu_tlvs or []):
        if t and len(t) >= 1:
            by_tag[int(t[0]) & 0xFFFF] = t

    for rule in connector_rules:
        tag = int(rule['tag']) & 0xFFFF
        present = by_tag.get(tag)
        if present is None:
            if rule.get('required'):
                return False, 'missing required TLV 0x%04X' % tag
            continue
        max_len = rule.get('length')
        if max_len is None:
            continue
        # Tuple shape: (tag, length, type, value). Be lenient about missing fields.
        if len(present) >= 4:
            value = present[3]
            type_str = present[2] or rule.get('type')
        elif len(present) == 3:
            value = present[2]
            type_str = rule.get('type')
        elif len(present) == 2:
            value = present[1]
            type_str = rule.get('type')
        else:
            continue
        actual = encoded_value_length(value, type_str)
        if actual > max_len:
            return False, (
                'TLV 0x%04X value length %d exceeds configured max %d'
                % (tag, actual, max_len))
    return True, None


def encode_custom_tlvs(custom_tlvs: Iterable | None) -> bytes:
    """Encode an iterable of (tag, length, type, value) tuples into a
    concatenation of SMPP TLV headers+bodies.

    Empty / None input yields an empty bytestring so the caller can
    short-circuit without any code path change.
    """
    if not custom_tlvs:
        return b''

    out = bytearray()
    for tlv in custom_tlvs:
        # Tolerant extraction: support 2-, 3-, 4-tuples (tag, value),
        # (tag, type, value), (tag, length, type, value).
        if len(tlv) >= 4:
            tag, _length, tlv_type, value = tlv[0], tlv[1], tlv[2], tlv[3]
        elif len(tlv) == 3:
            tag, tlv_type, value = tlv
        elif len(tlv) == 2:
            tag, value = tlv
            tlv_type = None
        else:
            continue

        body = encode_tlv_value(value, tlv_type)
        out.extend(struct.pack('>HH', int(tag) & 0xFFFF, len(body)))
        out.extend(body)

    return bytes(out)


# --- Encoder patch (retained as a no-op for backward-compat) ---
#
# Upstream smpp.pdu3 >= 0.6 already emits `pdu.custom_tlvs` via
# encodeBody -> encodeRawParams. An earlier revision of this module wrapped
# PDUEncoder.encode to do the same, which caused TLVs to appear on the wire
# twice. We keep the function name so importers stay source-compatible, but
# make it a no-op: upstream already does the right thing.

_PATCH_FLAG = '_jasmin_custom_tlv_patched'


def install_pdu_encoder_patch() -> None:
    """Extend the upstream `encodeRawParams` to also support `Int8`.

    smpp.pdu3 0.6's `encodeRawParams` only handles
    Int1/Int2/Int4/OctetString/COctetString and silently skips entries with
    any other type string (`else: continue`). For 19-digit vendor identifiers
    that don't fit in Int4 (2^32 ≈ 10 digits), the SMSC typically expects an
    8-byte big-endian integer — our CLI/config already accepts `Int8`, this
    patch is what actually encodes it.

    The patch delegates to the original encoder for all other types, so
    behaviour of the known types is unchanged. Idempotent.
    """
    from smpp.pdu.pdu_encoding import PDUEncoder, Int2Encoder

    if getattr(PDUEncoder, _PATCH_FLAG, False):
        return

    _orig_encode_raw = PDUEncoder.encodeRawParams

    def _encode_raw_with_int8(self, tlvs):
        if not tlvs:
            return _orig_encode_raw(self, tlvs)

        # Split Int8 entries out: encode them manually, and hand everything
        # else (including invalid rows) to the original encoder verbatim.
        int8_entries = []
        passthrough = []
        for tlv in tlvs:
            if len(tlv) == 4 and tlv[2] == 'Int8':
                int8_entries.append(tlv)
            else:
                passthrough.append(tlv)

        result = _orig_encode_raw(self, passthrough)

        for tag, length, _value_type, value in int8_entries:
            encoded_value = struct.pack('>Q', int(value))
            enc_len = length if length is not None else len(encoded_value)
            result += Int2Encoder().encode(tag) + Int2Encoder().encode(enc_len) + encoded_value

        return result

    PDUEncoder.encodeRawParams = _encode_raw_with_int8
    setattr(PDUEncoder, _PATCH_FLAG, True)


# --- Decoder monkey-patch -------------------------------------------------
#
# Upstream smpp.pdu3 silently drops vendor-range TLVs: OptionEncoder._decode
# maps any tag outside its fixed whitelist to Tag.vendor_specific_bypass, then
# decodeOptionalParams skips those. The raw bytes are consumed (so the rest of
# the PDU still parses), but the original tag and value never surface on the
# PDU object — so MO/DLR/submit_sm arriving with vendor TLVs loses them.
#
# We tap OptionEncoder.decode to capture the *original* tag integer plus the
# decoded octet value into a thread-local list, then hang that list on the PDU
# as `custom_tlvs` once PDUEncoder.decode returns. This mirrors the shape
# populated on the egress side, so format_tlvs_for_log and any downstream code
# see vendor TLVs uniformly regardless of direction.

_DECODE_PATCH_FLAG = '_jasmin_custom_tlv_decode_patched'
_decode_ctx = threading.local()


def install_pdu_decoder_patch() -> None:
    """Patch smpp.pdu3 decode path to preserve vendor-range TLVs on the PDU.

    Idempotent. Safe to call together with install_pdu_encoder_patch().
    """
    from smpp.pdu.pdu_encoding import PDUEncoder, OptionEncoder

    if getattr(PDUEncoder, _DECODE_PATCH_FLAG, False):
        return

    _orig_option_decode = OptionEncoder.decode
    _orig_pdu_decode = PDUEncoder.decode

    def _option_decode_tap(self, file):
        # Peek the raw 2-byte tag (big-endian) before the upstream code maps
        # vendor-range tags to the vendor_specific_bypass enum value.
        here = file.tell()
        head = file.read(4)  # tag(2) + length(2)
        file.seek(here)
        if len(head) < 4:
            return _orig_option_decode(self, file)

        raw_tag = int.from_bytes(head[0:2], 'big')
        raw_len = int.from_bytes(head[2:4], 'big')

        option = _orig_option_decode(self, file)

        if option.tag.name == 'vendor_specific_bypass':
            bucket = getattr(_decode_ctx, 'inflight_tlvs', None)
            if bucket is None:
                bucket = []
                _decode_ctx.inflight_tlvs = bucket
            # Store using the same (tag, length, type, value) shape the
            # encoder and listeners/operations expect.
            value = option.value
            if isinstance(value, (bytes, bytearray)):
                value = bytes(value)
            bucket.append((raw_tag, raw_len, 'OctetString', value))

        return option

    def _pdu_decode_tap(self, file):
        # Fresh bucket for this decode call (thread-local so nested or
        # concurrent decodes don't bleed into each other).
        _decode_ctx.inflight_tlvs = []
        try:
            pdu = _orig_pdu_decode(self, file)
        finally:
            captured = getattr(_decode_ctx, 'inflight_tlvs', None)
            _decode_ctx.inflight_tlvs = None
        if captured:
            pdu.custom_tlvs = captured
        return pdu

    OptionEncoder.decode = _option_decode_tap
    PDUEncoder.decode = _pdu_decode_tap
    setattr(PDUEncoder, _DECODE_PATCH_FLAG, True)


# --- Outbound wire hex logger (debug aid) ---------------------------------
#
# When investigating missing TLVs on the wire, it's useful to see exactly what
# bytes Jasmin hands to the transport *after* encode. This patch wraps
# `SMPPProtocolBase.sendPDU` to emit a single INFO log line per PDU with:
#   - command id + sequence number
#   - total PDU length
#   - short human summary of the TLV section (if any)
#   - full hex (capped to avoid flooding large MT bodies)
# Turned on via the environment variable JASMIN_TLV_WIRE_LOG=1 (opt-in).

import logging
import os

_WIRE_LOG_PATCH_FLAG = '_jasmin_wire_log_patched'


def install_sendpdu_wire_logger() -> None:
    """Log outgoing PDU bytes + TLV summary at INFO level.

    Opt-in via env var `JASMIN_TLV_WIRE_LOG=1` (or truthy). Idempotent.
    """
    if not os.environ.get('JASMIN_TLV_WIRE_LOG', '').strip():
        return

    try:
        from smpp.twisted.protocol import SMPPProtocolBase
    except ImportError:
        return

    if getattr(SMPPProtocolBase, _WIRE_LOG_PATCH_FLAG, False):
        return

    _orig_send = SMPPProtocolBase.sendPDU
    logger = logging.getLogger('jasmin.tlv.wire')
    if not logger.handlers:
        # Attach a stdout handler if jasmin hasn't wired one already.
        h = logging.StreamHandler()
        h.setFormatter(logging.Formatter('%(asctime)s %(levelname)s %(name)s %(message)s'))
        logger.addHandler(h)
        logger.setLevel(logging.INFO)

    def _send_with_wire_log(self, pdu):
        try:
            encoded = self.encoder.encode(pdu)
            # TLV summary
            tlvs = getattr(pdu, 'custom_tlvs', None) or []
            tlv_summary = ','.join('0x%04X' % int(t[0]) for t in tlvs) if tlvs else 'none'
            # Cap hex for readability; still enough to eyeball the TLV section.
            hex_blob = encoded.hex()
            if len(hex_blob) > 600:
                hex_blob = hex_blob[:600] + '...(+%d bytes)' % ((len(hex_blob) - 600) // 2)
            cmd_id = getattr(pdu, 'commandId', None)
            logger.info(
                'OUT pdu=%s seq=%s len=%d tlvs=%s hex=%s',
                getattr(cmd_id, 'name', str(cmd_id)),
                getattr(pdu, 'seqNum', '?'),
                len(encoded), tlv_summary, hex_blob)
            # Hand the already-encoded bytes to the transport directly so we
            # don't encode twice.
            self.transport.write(encoded)
            self.onSMPPOperation()
        except Exception:
            # On any error in the wrapper, fall back to the original path so
            # we never regress send behaviour because of a debug aid.
            return _orig_send(self, pdu)

    SMPPProtocolBase.sendPDU = _send_with_wire_log
    setattr(SMPPProtocolBase, _WIRE_LOG_PATCH_FLAG, True)
