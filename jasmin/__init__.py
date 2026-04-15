# Copyright (c) Jookies LTD <jasmin@jookies.net>
# See LICENSE for details.

"""Jasmin SMS Gateway by Jookies LTD <jasmin@jookies.net>"""
import os
import re

MAJOR = 0
MINOR = 11
PATCH = 1
META = ''


def get_version():
    """Will return Jasmin's version"""
    return '%s.%s' % (MAJOR, MINOR)


def get_release():
    """PEP 440 format"""
    return '%s.%s.%s%s' % (MAJOR, MINOR, META, PATCH)


__version__ = get_version()
__release__ = get_release()

HOSTNAME = os.getenv('HOSTNAME', 'default-hostname')
RUNNING_KUBERNETES = False
PRIMARY_POD = False
if 'KUBERNETES_SERVICE_HOST' in os.environ:
    RUNNING_KUBERNETES = True
    _r = re.search(r"^([a-z0-9A-Z]+)\-(\d+)$", HOSTNAME)
    if len(_r.groups()) == 2 and int(_r.group(2)) == 0:
        PRIMARY_POD = True
