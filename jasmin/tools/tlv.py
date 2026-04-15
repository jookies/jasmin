"""TLV parameter formatting utilities for logging"""


def format_tlvs_for_log(pdu, log_privacy=False):
    """Format TLV params from a PDU for log output.

    Returns a string like '0x3000:myval,source_port:1234' or 'none'.
    When log_privacy=True, values are hidden: '0x3000,source_port'.
    """
    tlvs = []

    # Standard optional params present in PDU
    mandatory = set(getattr(pdu, 'mandatoryParams', []))
    for key, val in pdu.params.items():
        if key not in mandatory and val is not None:
            if log_privacy:
                tlvs.append(key)
            else:
                tlvs.append('%s:%s' % (key, val))

    # Custom TLVs (raw tuples)
    for tlv in getattr(pdu, 'custom_tlvs', []):
        if len(tlv) >= 4:
            if log_privacy:
                tlvs.append('0x%04X' % tlv[0])
            else:
                tlvs.append('0x%04X:%s' % (tlv[0], tlv[3]))

    return ','.join(tlvs) if tlvs else 'none'
