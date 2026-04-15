.. _custom_tlvs:

######################
Custom TLV Parameters
######################

Jasmin supports user-defined **TLV** (Tag-Length-Value) *optional parameters* on outbound
``submit_sm`` PDUs, as defined in `SMPP v3.4 §5.3 <https://smpp.org/SMPP_v3_4_Issue1_2.pdf>`_.
TLVs carry vendor-specific or operator-specific data (e.g. billing references,
user-message identifiers, MNO-mandated tags) alongside the standard SMPP fields.

Custom TLVs may be configured at **two levels**:

* **Per-connector defaults** — set once on an SMPP client connector via ``jcli`` and
  applied to every message routed through that connector.
* **Per-message injection** — provided in the HTTP ``/send`` request body and merged
  on top of the connector defaults for that single message.

.. important::
   Use tags in the **SMPP v3.4 vendor-specific range** ``0x1400``–``0x3FFF``. Tags
   below ``0x1400`` are reserved for the standard optional parameters
   (``user_message_reference``, ``source_port`` …) and are handled natively by the
   SMPP stack — overriding them with ``custom_tlvs`` is not supported.

.. _custom_tlvs_connector:

Configuring connector-level default TLVs
*****************************************

Connector-level TLVs are applied to every outbound ``submit_sm`` for a given SMPP
client connector. Configure them through the :doc:`/management/jcli/index` with
the ``smppccm`` module.

String format
=============

.. code-block:: text

   tag,type,value[,required|optional];tag,type,value[,required|optional];...

* Multiple TLVs are joined with ``;``.
* Each TLV's fields are joined with ``,``.
* The last field (``required`` / ``optional``) is **optional** and defaults to
  ``optional``.

.. list-table:: Field reference
   :widths: 15 25 25 35
   :header-rows: 1

   * - Field
     - Format
     - Example
     - Notes
   * - **tag**
     - Hex (``0x`` prefix) or decimal integer
     - ``0x3000``, ``12288``
     - Vendor-specific range: ``0x1400``–``0x3FFF``.
   * - **type**
     - One of ``Int1``, ``Int2``, ``Int4``, ``OctetString``, ``COctetString``
     - ``COctetString``
     - ``Int1/2/4`` = 1/2/4-byte unsigned integer. ``OctetString`` = raw bytes.
       ``COctetString`` = NUL-terminated C string.
   * - **value**
     - Type-dependent literal
     - ``billing-ref-42``, ``0xFF``, ``1234``
     - ``Int*`` values accept decimal or ``0x``-prefixed hex. String values are
       literal and must not contain ``,`` or ``;``.
   * - **required / optional**
     - ``required`` or ``optional`` (default)
     - ``required``
     - ``required`` TLVs cause the submit to fail if the per-message payload does
       not include them. ``optional`` TLVs are injected as defaults when absent.

Example: setting two default TLVs
==================================

The following ``jcli`` session sets a ``COctetString`` TLV at tag ``0x3000``
(optional) and a 2-byte integer TLV at tag ``0x3001`` (required):

.. code-block:: text

   jcli : smppccm -u operator_01
   > set custom_tlvs 0x3000,COctetString,defaultval,optional;0x3001,Int2,1234,required
   Updated SMPPClientConfig key:custom_tlvs value:[{'tag': 12288, ...}]
   > ok
   > persist

Clearing connector TLVs:

.. code-block:: text

   > set custom_tlvs none
   > ok
   > persist

Inspect the connector to confirm:

.. code-block:: text

   > show operator_01

.. _custom_tlvs_http:

Per-message TLVs via the HTTP API
**********************************

Custom TLVs can also be supplied on a single ``/send`` request. They are merged
with the connector defaults (per-message TLVs win on tag conflict).

JSON request body
==================

Each TLV is a 4-element array ``[tag, length, type, value]``:

* ``tag`` — integer (decimal in JSON; ``0x3000`` == ``12288``).
* ``length`` — ``null`` to let Jasmin compute it automatically, or an explicit
  length for wire-format control.
* ``type`` — one of the five types listed above.
* ``value`` — type-dependent literal (string or integer).

.. code-block:: http

   POST /send HTTP/1.1
   Host: 127.0.0.1:1401
   Content-Type: application/json

   {
     "username": "jasmin_user",
     "password": "jasmin_pass",
     "to": "21600000000",
     "content": "Hello",
     "custom_tlvs": [
       [12288, null, "COctetString", "billing-ref-42"],
       [12289, null, "Int2",          5000]
     ]
   }

The ``custom_tlvs`` parameter is optional; when omitted, only connector-level
defaults apply. See :ref:`sending_sms-mt` for the full parameter list.

.. note::
   Tag ``12288`` is the decimal form of hex ``0x3000``. JSON has no native hex
   literal, so integers are written in decimal; ``jcli`` accepts both forms.

.. _custom_tlvs_logging:

Logging and privacy
*******************

By default, Jasmin writes TLV tags **and** values to the connector log when a
``submit_sm`` is sent:

.. code-block:: text

   SMS-MT [cid:operator_01] [queue-msgid:…] [custom_tlvs:0x3000:billing-ref-42,0x3001:5000]

Set ``log_privacy = True`` on the SMPP client connector (or on the
``[smpp-server]`` section of ``jasmin.cfg``) to redact values and log only tag
names, which is useful when TLV payloads carry PII:

.. code-block:: text

   SMS-MT [cid:operator_01] [queue-msgid:…] [custom_tlvs:0x3000,0x3001]

``log_privacy`` also redacts the standard optional parameters (e.g.
``source_port``, ``user_message_reference``) in the same log line.

Troubleshooting
***************

* **"Invalid TLV type: X. Must be one of: Int1, Int2, Int4, OctetString, COctetString"** —
  the second field must be one of the five literal type names; casing matters.
* **"Invalid integer value for TLV: X"** — ``Int1/Int2/Int4`` values must be
  decimal (``1234``) or ``0x``-prefixed hex (``0x04D2``).
* **"TLV format must be: tag,type,value[,required|optional]. Got: X"** — the
  ``jcli`` string has fewer than 3 comma-separated fields or an extra ``;`` at
  the end. String values containing literal commas or semicolons are not
  supported by the connector-level format; use per-message HTTP ``custom_tlvs``
  for such values.
* **"Invalid TLV tag: X"** — tag parsing failed; use either a decimal integer
  or a ``0x``-prefixed hex literal.
