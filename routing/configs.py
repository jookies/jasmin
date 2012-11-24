# Copyright 2012 Fourat Zouari <fourat@gmail.com>
# See LICENSE for details.

import logging
from jasmin.config.tools import ConfigFile

class RouterPBConfig(ConfigFile):
    def __init__(self, config_file = None):
        ConfigFile.__init__(self, config_file)
        
        # Logging
        self.port = self._getint('router', 'port', 8988)
        self.log_level = logging.getLevelName(self._get('router', 'log_level', 'INFO'))
        self.log_file = self._get('router', 'log_file', '/tmp/router.log')
        self.log_format = self._get('router', 'log_format', '%(asctime)s %(levelname)-8s %(process)d %(message)s')
        self.log_date_format = self._get('router', 'log_date_format', '%Y-%m-%d %H:%M:%S')
        self.pickle_protocol = self._getint('router', 'pickle_protocol', 2)