# Copyright 2012 Fourat Zouari <fourat@gmail.com>
# See LICENSE for details.

import logging
from jasmin.config.tools import ConfigFile

class HTTPApiConfig(ConfigFile):
    def __init__(self, config_file = None):
        ConfigFile.__init__(self, config_file)
        
        self.bind = self._get('http-api', 'bind', '0.0.0.0')
        self.port = self._get('http-api', 'port', 1401)

        # Logging
        self.access_log = self._get('http-api', 'access_log', '/var/log/jasmin/http-accesslog.log')
        self.log_level = logging.getLevelName(self._get('http-api', 'log_level', 'INFO'))
        self.log_file = self._get('http-api', 'log_file', '/var/log/jasmin/http-api.log')
        self.log_format = self._get('http-api', 'log_format', '%(asctime)s %(levelname)-8s %(process)d %(message)s')
        self.log_date_format = self._get('http-api', 'log_date_format', '%Y-%m-%d %H:%M:%S')
