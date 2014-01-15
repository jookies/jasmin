# Copyright 2012 Fourat Zouari <fourat@gmail.com>
# See LICENSE for details.

import logging
from jasmin.config.tools import ConfigFile

class RouterPBConfig(ConfigFile):
    def __init__(self, config_file = None):
        ConfigFile.__init__(self, config_file)
        
        self.bind = self._get('router', 'bind', '0.0.0.0')
        self.port = self._getint('router', 'port', 8988)
        
        self.pickle_protocol = self._getint('router', 'pickle_protocol', 2)

        # Logging
        self.log_level = logging.getLevelName(self._get('router', 'log_level', 'INFO'))
        self.log_file = self._get('router', 'log_file', '/var/log/jasmin/router.log')
        self.log_format = self._get('router', 'log_format', '%(asctime)s %(levelname)-8s %(process)d %(message)s')
        self.log_date_format = self._get('router', 'log_date_format', '%Y-%m-%d %H:%M:%S')
        
class deliverSmThrowerConfig(ConfigFile):
    def __init__(self, config_file = None):
        ConfigFile.__init__(self, config_file)
        
        self.timeout = self._getint('deliversm-thrower', 'timeout', 30)
        self.retryDelay = self._getint('deliversm-thrower', 'retry_delay', 30)
        self.maxRetries = self._getint('deliversm-thrower', 'max_retries', 3)
        
        # Logging
        self.log_level = logging.getLevelName(self._get('deliversm-thrower', 'log_level', 'INFO'))
        self.log_file = self._get('deliversm-thrower', 'log_file', '/var/log/jasmin/deliversm-thrower.log')
        self.log_format = self._get('deliversm-thrower', 'log_format', '%(asctime)s %(levelname)-8s %(process)d %(message)s')
        self.log_date_format = self._get('deliversm-thrower', 'log_date_format', '%Y-%m-%d %H:%M:%S')

class DLRThrowerConfig(ConfigFile):
    def __init__(self, config_file = None):
        ConfigFile.__init__(self, config_file)
        
        self.timeout = self._getint('dlr-thrower', 'timeout', 30)
        self.retry_delay = self._getint('dlr-thrower', 'retry_delay', 30)
        self.max_retries = self._getint('dlr-thrower', 'max_retries', 3)
        
        # Logging
        self.log_level = logging.getLevelName(self._get('dlr-thrower', 'log_level', 'INFO'))
        self.log_file = self._get('dlr-thrower', 'log_file', '/var/log/jasmin/dlr-thrower.log')
        self.log_format = self._get('dlr-thrower', 'log_format', '%(asctime)s %(levelname)-8s %(process)d %(message)s')
        self.log_date_format = self._get('dlr-thrower', 'log_date_format', '%Y-%m-%d %H:%M:%S')