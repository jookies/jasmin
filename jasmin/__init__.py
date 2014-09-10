# Copyright (c) Fourat Zouari <fourat@gmail.com>
# See LICENSE for details.

"""Jasmin SMS Gateway by Fourat ZOUARI <fourat@gmail.com>"""

MAJOR = 0
MINOR = 4
PATCH = 0
META = 'beta'

def get_version():
    return '%s.%s' % (MAJOR, MINOR)

def get_release():
    return '%s.%s.%s-%s' % (MAJOR, MINOR, PATCH, META)

__version__ = get_version()
__release__ = get_release()