# Copyright (c) Jookies LTD <jasmin@jookies.net>
# See LICENSE for details.

"""Jasmin SMS Gateway by Jookies LTD <jasmin@jookies.net>"""

MAJOR = 0
MINOR = 9
PATCH = 11
META = 'b'

def get_version():
    "Will return Jasmin's version"
    return '%s.%s' % (MAJOR, MINOR)

def get_release():
    "PEP 440 format"
    return '%s.%s.%s%s' % (MAJOR, MINOR, META, PATCH)

__version__ = get_version()
__release__ = get_release()
