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
    """No-op. Kept for API compatibility with earlier revisions.

    smpp.pdu3 >= 0.6 already serialises `pdu.custom_tlvs` via
    `PDUEncoder.encodeBody` -> `encodeRawParams`, using the same
    (tag, length, type, value) tuple shape that the TLV injection sites in
    `listeners.py` and `operations.py` already populate.
    """
    from smpp.pdu.pdu_encoding import PDUEncoder  # fail fast if missing
    setattr(PDUEncoder, _PATCH_FLAG, True)  # marker for tests / introspection


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
