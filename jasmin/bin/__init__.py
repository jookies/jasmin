# Copyright (c) Jookies LTD <jasmin@jookies.net>
# See LICENSE for details.

"""Jasmin SMS Gateway by Jookies LTD <jasmin@jookies.net>"""
import os
from jasmin.config import LOG_PATH


class BaseDaemon:
    def __init__(self, opt):
        self.options = opt
        self.components = {}

        # Create LOG_PATH if it's not found
        os.makedirs(LOG_PATH, exist_ok=True)
