# Copyright (c) Jookies LTD <jasmin@jookies.net>
# See LICENSE for details.

"""Jasmin SMS Gateway by Jookies LTD <jasmin@jookies.net>"""
import binascii
from jasmin.protocols.http.errors import UrlArgsValidationError

def hex2bin(hex_content):
    """Convert hex-content back to binary data, raise a UrlArgsValidationError on failure"""

    try:
        return binascii.unhexlify(hex_content)
    except Exception as e:
        raise UrlArgsValidationError("Invalid hex-content data: '%s'" % hex_content)