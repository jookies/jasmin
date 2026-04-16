.. _custom_tlvs:

######################
Custom TLV Parameters
######################

Jasmin supports user-defined **TLV** (Tag-Length-Value) *optional parameters* on outbound
``submit_sm`` PDUs, as defined in `SMPP v3.4 §5.3 <https://smpp.org/SMPP_v3_4_Issue1_2.pdf>`_.
TLVs carry vendor-specific or operator-specific data (e.g. billing references,
user-message identifiers, MNO-mandated tags) alongside the standard SMPP fields.

Custom TLVs are handled at **three levels**:

* **Connector config (validation rules)** — declares which tags exist, their
  wire encoding type, max byte length, and whether they are required. Set via
  ``jcli``. The connector does **not** inject default values.
* **Per-message values** — provided by the REST / HTTP ``/send`` caller in the
  ``custom_tlvs`` JSON field. The caller supplies values; the connector resolves
  encoding types and validates.
* **Interceptor scripts** — an MT interceptor can add, modify or remove TLVs on
  the routable before dispatch.

.. important::
   Use tags in the **SMPP v3.4 vendor-specific range** ``0x1400``–``0x3FFF``. Tags
   below ``0x1400`` are reserved for the standard optional parameters
   (``user_message_reference``, ``source_port`` …) and are handled natively by the
   SMPP stack — overriding them with ``custom_tlvs`` is not supported.

.. _custom_tlvs_connector:

Connector-level TLV rules
*************************

The connector declares **validation rules** for vendor TLVs: tag, type, max byte
length on the wire, and whether the tag is required. Values are **not** configured
at the connector level — they come from the submitter.

String format
=============

.. code-block:: text

   tag,type,max_length[,required|optional]; ...

* Multiple rules are joined with ``;``.
* Each rule's fields are joined with ``,``.
* ``required`` / ``optional`` defaults to ``optional``.

.. list-table:: Field reference
   :widths: 15 25 25 35
   :header-rows: 1

   * - Field
     - Format
     - Example
     - Notes
   * - **tag**
     - Hex (``0x`` prefix) or decimal integer
     - ``0x1401``, ``5121``
     - Vendor-specific range: ``0x1400``–``0x3FFF``.
   * - **type**
     - One of ``Int1``, ``Int2``, ``Int4``, ``Int8``, ``OctetString``, ``COctetString``
     - ``OctetString``
     - Determines the wire encoding (see type table below). Must match what the
       upstream SMSC expects.
   * - **max_length**
     - Positive integer, or ``-`` for unlimited
     - ``20``, ``8``, ``-``
     - Max encoded byte length on the wire. ``Int8`` is always 8. ``OctetString``
       = byte count of the UTF-8 encoded value.
   * - **required / optional**
     - ``required`` or ``optional`` (default)
     - ``required``
     - ``required`` causes the submit to be rejected if the per-message payload
       does not include this tag.

Example
=======

.. code-block:: text

   jcli : smppccm -u smalert
   > custom_tlvs 0x1402,OctetString,64,optional;0x1401,OctetString,20,required;0x1400,OctetString,20,required
   > ok
   jcli : smppccm -0 smalert
   jcli : smppccm -1 smalert
   jcli : persist

Clearing connector TLVs:

.. code-block:: text

   > custom_tlvs none
   > ok
   > persist

.. _custom_tlvs_types:

Supported TLV types
*******************

.. list-table::
   :widths: 15 30 55
   :header-rows: 1

   * - Type
     - Wire encoding
     - Typical use
   * - ``Int1``
     - 1 byte unsigned (``>B``)
     - Flags, small enums (0–255)
   * - ``Int2``
     - 2 bytes big-endian (``>H``)
     - Ports, short IDs (0–65535)
   * - ``Int4``
     - 4 bytes big-endian (``>I``)
     - Standard IDs (up to ~4 billion)
   * - ``Int8``
     - 8 bytes big-endian (``>Q``)
     - Large IDs (up to ~18 quintillion)
   * - ``OctetString``
     - Raw UTF-8 bytes
     - Text identifiers, tokens, hashes
   * - ``COctetString``
     - UTF-8 bytes + NUL terminator
     - C-style strings

.. _custom_tlvs_rest:

Per-message TLVs via REST / HTTP API
*************************************

Values are supplied in the ``custom_tlvs`` JSON field on each submit request.
The field is a **dict** where each key is a hex tag string (with an optional
``:Type`` suffix) and each value is the TLV payload.

Example 1 — Type resolved from connector config (simplest)
===========================================================

.. code-block:: http

   POST /secure/send HTTP/1.1
   Authorization: Basic dXNlcjpwYXNz
   Content-Type: application/json

   {
     "to": "+919216217231",
     "from": "ABXOTP",
     "content": "Your OTP is 5249",
     "custom_tlvs": {
       "0x1401": "1401778070000018542",
       "0x1400": "1707167205648943173"
     }
   }

The connector config declares ``0x1401,OctetString,20,required`` so Jasmin
encodes the value as a 19-byte UTF-8 OctetString on the wire. The caller does
not need to know the encoding type.

Example 2 — Caller declares type explicitly
=============================================

.. code-block:: json

   {
     "custom_tlvs": {
       "0x1401:OctetString": "1401778070000018542",
       "0x1400:Int8": 1707167205648943173,
       "0x1402:OctetString": "9c70f816...a042"
     }
   }

The ``:Type`` suffix in the key overrides the connector config. Useful when the
same tag needs different encodings across callers, or for ad-hoc tags not in the
connector config.

Example 3 — Interceptor-generated TLV (hash)
==============================================

The caller sends only ``0x1400`` and ``0x1401``; an MT interceptor computes
SHA-256 and injects ``0x1402`` automatically:

.. code-block:: json

   {
     "custom_tlvs": {
       "0x1401": "1401778070000018542",
       "0x1400": "1707167205648943173"
     }
   }

The interceptor appends ``0x1402`` to the PDU before dispatch. On the wire:

.. code-block:: text

   TLV 0x1400 len=19 '1707167205648943173'
   TLV 0x1401 len=19 '1401778070000018542'
   TLV 0x1402 len=64 '9c70f8165e4cadbb1965d9d105d5543c3ade38aa74dac1b58a76faeec3b413bc'

See ``misc/scripts/interceptor_hash_tlv.py`` for the interceptor script.

Example 4 — Legacy tuple format (backward compatible)
======================================================

.. code-block:: json

   {
     "custom_tlvs": [
       [5121, null, "OctetString", "1401778070000018542"],
       [5120, null, "Int8", 1707167205648943173]
     ]
   }

Still accepted but not recommended. Use the dict format.

Type resolution priority
========================

.. list-table::
   :widths: 10 40 50
   :header-rows: 1

   * - Priority
     - Source
     - Example
   * - 1 (highest)
     - Caller-provided type in the key
     - ``"0x1401:Int8": 170...``
   * - 2
     - Connector config type
     - ``0x1401,OctetString,20,required``
   * - 3 (fallback)
     - ``OctetString`` default
     - Tag not in connector config

.. _custom_tlvs_interceptor:

TLVs in interceptor scripts
****************************

MT and MO interceptor scripts receive a ``routable`` object with these methods:

.. list-table::
   :widths: 40 60
   :header-rows: 1

   * - Method
     - Description
   * - ``routable.getCustomTlvs()``
     - Returns list of ``(tag, length, type, value)`` tuples
   * - ``routable.addCustomTlv(tag, type, value)``
     - Appends a new vendor TLV
   * - ``routable.setTlvParam(name, value)``
     - Sets a standard SMPP optional param (e.g. ``source_port``)

The interceptor engine pre-injects these safe stdlib modules into the script
namespace: ``hashlib``, ``re``, ``json``, ``datetime``, ``math``, ``struct``.

Example — SHA-256 hash TLV:

.. code-block:: python

   # Runs inside Jasmin interceptor engine — no imports needed
   tlv_map = {}
   for tlv in routable.getCustomTlvs():
       if len(tlv) >= 4:
           val = tlv[3]
           if isinstance(val, bytes):
               val = val.decode('utf-8', errors='replace')
           tlv_map[tlv[0]] = str(val)

   val_1400 = tlv_map.get(0x1400)
   val_1401 = tlv_map.get(0x1401)

   if val_1400 and val_1401:
       chain = '%s,%s' % (val_1401, val_1400)
       sha = hashlib.sha256(chain.encode('utf-8')).hexdigest()
       routable.addCustomTlv(0x1402, 'OctetString', sha)

.. _custom_tlvs_inbound:

Inbound TLVs (receive direction)
*********************************

Vendor-range TLVs on incoming ``deliver_sm`` / MO / DLR PDUs are captured by a
decode-side patch and attached to ``pdu.custom_tlvs``. Their types are resolved
from the connector config (same rule set used for outbound). They appear in the
log line:

.. code-block:: text

   TLV-RX [cid:smalert] [tag:0x1401] [type:OctetString] [value_preview:b'1401778070000018542']
   SMS-MO [cid:smalert] ... [tlvs:0x1401:b'1401778070000018542',0x1400:b'1707167205648943173']

.. _custom_tlvs_logging:

Logging and privacy
*******************

By default, Jasmin writes TLV tags **and** values to the log:

.. code-block:: text

   SMS-MT [cid:smalert] ... [tlvs:0x1401:1401778070000018542,0x1400:1707167205648943173]

Outbound TLV type resolution is also logged:

.. code-block:: text

   TLV [msgid:xxx] [tag:0x1401] [caller_type:OctetString] [value_preview:'1401...']
   TLV [msgid:xxx] [tag:0x1401] [resolved_type:OctetString]

Set ``log_privacy = True`` on the SMPP client connector to redact values:

.. code-block:: text

   SMS-MT [cid:smalert] ... [tlvs:0x1401,0x1400]

.. _custom_tlvs_wire_debug:

Wire debug logging
******************

Set environment variable ``JASMIN_TLV_WIRE_LOG=1`` to log every outgoing PDU's
hex bytes at INFO level (logger ``jasmin.tlv.wire``):

.. code-block:: bash

   docker run -d --name jasmin-tlv \
     -e JASMIN_TLV_WIRE_LOG=1 \
     jasmin:0.12-tlv

   docker logs jasmin-tlv 2>&1 | grep jasmin.tlv.wire

Output:

.. code-block:: text

   OUT pdu=submit_sm seq=5 len=251 tlvs=0x1402,0x1401,0x1400 hex=0000...

.. _custom_tlvs_troubleshooting:

Troubleshooting
***************

* **"Rejecting SubmitSmPDU[...]: missing required TLV 0x1401"** — the connector
  config declares the tag as ``required`` but the caller did not include it in
  ``custom_tlvs``. Add it to the JSON body.
* **"Rejecting SubmitSmPDU[...]: TLV 0x1400 value length 30 exceeds configured
  max 20"** — the encoded value is larger than the connector's ``max_length``
  setting. Shorten the value or increase the limit.
* **"Invalid TLV type: X. Must be one of: Int1, Int2, Int4, Int8, OctetString,
  COctetString"** — casing matters (``Int8`` not ``int8``).
* **``optional_len=0`` in tcpdump** — the per-message ``custom_tlvs`` dict was
  empty or missing. Verify the REST body includes the field and that the
  connector's jCli state was not wiped by a container rebuild.
* **``NACK/0x000000c4/Invalid TLV value`` from SMSC** — the encoding type does
  not match what the SMSC expects. Check with the vendor (e.g. ``Int8`` vs
  ``OctetString``).
* **``NameError: name 'hashlib' is not defined`` in interceptor** — ensure Jasmin
  is running the version that pre-injects stdlib modules into the interceptor
  namespace (``jasmin/interceptor/interceptor.py``).
