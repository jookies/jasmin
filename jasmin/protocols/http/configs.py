"""
Config file handler for 'http-api' section in jasmin.cfg
"""

import logging
import os
from jasmin.config.tools import ConfigFile

# Related to travis-ci builds
ROOT_PATH = os.getenv('ROOT_PATH', '/')

class HTTPApiConfig(ConfigFile):
    "Config handler for 'http-api' section"

    def __init__(self, config_file=None):
        ConfigFile.__init__(self, config_file)

        self.bind = self._get('http-api', 'bind', '0.0.0.0')
        self.port = self._getint('http-api', 'port', 1401)

        # Logging
        self.access_log = self._get(
            'http-api', 'access_log', '%s/var/log/jasmin/http-accesslog.log' % ROOT_PATH)
        self.log_level = logging.getLevelName(self._get('http-api', 'log_level', 'INFO'))
        self.log_file = self._get('http-api', 'log_file', '%s/var/log/jasmin/http-api.log' % ROOT_PATH)
        self.log_rotate = self._get('http-api', 'log_rotate', 'W6')
        self.log_format = self._get(
            'http-api', 'log_format', '%(asctime)s %(levelname)-8s %(process)d %(message)s')
        self.log_date_format = self._get('http-api', 'log_date_format', '%Y-%m-%d %H:%M:%S')

        # Long message splitting
        self.long_content_max_parts = self._get('http-api', 'long_content_max_parts', 5)
        self.long_content_split = self._get('http-api', 'long_content_split', 'udh') # sar or udh
