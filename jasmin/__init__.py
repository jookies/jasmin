# Copyright (c) Fourat Zouari <fourat@gmail.com>
# See LICENSE for details.

"""Jasmin SMS Gateway by Fourat ZOUARI <fourat@gmail.com>"""

MAJOR = 0
MINOR = 7
PATCH = 0
META = 'a'

def get_version():
    "Will return Jasmin's version"
    return '%s.%s' % (MAJOR, MINOR)

def get_release():
    "PEP 440 format"
    return '%s.%s%s%s' % (MAJOR, MINOR, META, PATCH)

__version__ = get_version()
__release__ = get_release()
