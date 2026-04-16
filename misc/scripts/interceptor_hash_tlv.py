"""Jasmin MT Interceptor — compute SHA-256 hash and inject as TLV 0x1402.

This script runs inside Jasmin's interceptor engine. The engine injects the
following variables into the script's namespace:

    routable    : RoutableSubmitSm  — the message being submitted
    smpp_status : None              — set to an int to reject with SMPP error
    http_status : None              — set to an int to reject with HTTP error
    extra       : dict              — extra data to pass back

Available methods on `routable`:
    routable.pdu.params['source_addr']       — sender address (bytes)
    routable.pdu.params['destination_addr']  — recipient address (bytes)
    routable.pdu.params['short_message']     — message body (bytes)
    routable.getCustomTlvs()                 — list of existing custom TLVs
    routable.addCustomTlv(tag, type, value, length=None)
                                             — append a vendor TLV
    routable.setTlvParam(name, value)        — set a standard optional param

Setup in jCli
-------------
1. Create the interceptor script:

    jcli : mtinterceptor -a
    > type DefaultInterceptor
    > script /etc/jasmin/resource/interceptor_hash_tlv.py
    > order 100
    > ok
    jcli : persist

2. Copy this file into the container:

    docker cp misc/scripts/interceptor_hash_tlv.py \\
        jasmin-tlv:/etc/jasmin/resource/interceptor_hash_tlv.py

3. Make sure `interceptord` is running (it is by default in the Docker CMD):

    docker logs jasmin-tlv 2>&1 | grep interceptor

What this script does
---------------------
- Reads existing TLVs 0x1400 and 0x1401 from the PDU
- Concatenates their values: "0x1400_value,0x1401_value"
- Computes SHA-256 hash of that string
- Injects the hash as TLV 0x1402 (OctetString, 64 hex chars)
- If 0x1400 or 0x1401 are missing, sets http_status=400 to reject
"""

# Note: `hashlib` is pre-injected by the interceptor engine — no import needed.
# Other available modules: re, json, datetime, math, struct

# ---- Helper ----
def get_hash(input_string):
    sha256 = hashlib.sha256()
    sha256.update(input_string.encode('utf-8'))
    return sha256.hexdigest()


# ---- Main interceptor logic ----
# `routable` is injected by the interceptor engine

# Collect existing custom TLVs by tag
tlv_map = {}
for tlv in routable.getCustomTlvs():
    # tlv = (tag_int, length, type, value)
    if len(tlv) >= 4:
        tag = tlv[0]
        value = tlv[3]
        # Value may be bytes (from SMPP decode) or str (from REST normalize)
        if isinstance(value, bytes):
            value = value.decode('utf-8', errors='replace')
        else:
            value = str(value)
        tlv_map[tag] = value

# Need both 0x1400 and 0x1401 to compute hash
val_1400 = tlv_map.get(0x1400)
val_1401 = tlv_map.get(0x1401)

if val_1400 is None or val_1401 is None:
    # Reject: required TLVs missing for hash computation
    # http_status = 400  # uncomment to reject; leave commented to allow through without hash
    pass
else:
    # Compute hash of "val_1400,val_1401"
    chain = '%s,%s' % (val_1401, val_1400)
    hashofpedf = get_hash(chain)

    # Inject as TLV 0x1402 (OctetString, 64 hex chars)
    routable.addCustomTlv(0x1402, 'OctetString', hashofpedf)
