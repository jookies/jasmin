# Copyright (c) Jookies LTD <jasmin@jookies.net>
# See LICENSE for details.

"""Jasmin SMS Gateway by Jookies LTD <jasmin@jookies.net>"""
import binascii
from jasmin.protocols.http.errors import UrlArgsValidationError, AuthenticationError


def hex2bin(hex_content):
    """Convert hex-content back to binary data, raise a UrlArgsValidationError on failure"""

    try:
        return binascii.unhexlify(hex_content)
    except Exception as e:
        raise UrlArgsValidationError("Invalid hex-content data: '%s'" % hex_content)

def authenticate_user(username, password, routerpb, stats, log):
    if isinstance(username, bytes):
        username = username.decode()
    if isinstance(password, bytes):
        password = password.decode()

    user = routerpb.authenticateUser(
        username=username,
        password=password)
    if user is None:
        stats.inc('auth_error_count')

        log.debug(
            "Authentication failure for username:%s and password:%s",
            username, password)
        log.error(
            "Authentication failure for username:%s",
            username)
        raise AuthenticationError(
            'Authentication failure for username:%s' % username)
    return user