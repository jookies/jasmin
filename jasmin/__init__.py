# Copyright (c) Fourat Zouari <fourat@gmail.com>
# See LICENSE for details.

"""Jasmin SMS Gateway by Fourat ZOUARI <fourat@gmail.com>"""

VERSION = '0.4'
RELEASE = '%s.0-alpha' % VERSION

def get_version():
    return VERSION

def get_release():
    return RELEASE

__version__ = get_version()
__release__ = get_release()