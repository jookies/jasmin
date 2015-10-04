# Copyright (c) Fourat Zouari <fourat@gmail.com>
# See LICENSE for details.

"""Jasmin SMS Gateway by Fourat ZOUARI <fourat@gmail.com>"""

MAJOR = 0
MINOR = 6
PATCH = 43
META = 'post'

def get_version():
    return '%s.%s' % (MAJOR, MINOR)

def get_release():
    "PEP 440 format"
    return '%s.%s.%s%s' % (MAJOR, MINOR, META, PATCH)

__version__ = get_version()
__release__ = get_release()
