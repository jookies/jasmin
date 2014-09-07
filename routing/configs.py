"""
Config file handlers for 'router', 'deliversm-httpthrower' and 'dlr-thrower'  section in jasmin.conf
"""

import logging
from jasmin.config.tools import ConfigFile

class RouterPBConfig(ConfigFile):
    "Config handler for 'router' section"

    def __init__(self, config_file = None):
        ConfigFile.__init__(self, config_file)
        
        self.store_path = self._get('router', 'store_path', '/etc/jasmin/store')

        self.bind = self._get('router', 'bind', '0.0.0.0')
        self.port = self._getint('router', 'port', 8988)
        
        self.authentication = self._getbool('router', 'authentication', True)
        self.admin_username = self._get('router', 'admin_username', 'radmin')
        self.admin_password = self._get('router', 'admin_password', "82a606ca5a0deea2b5777756788af5c8").decode('hex')

        self.pickle_protocol = self._getint('router', 'pickle_protocol', 2)

        # Logging
        self.log_level = logging.getLevelName(self._get('router', 'log_level', 'INFO'))
        self.log_file = self._get('router', 'log_file', '/var/log/jasmin/router.log')
        self.log_format = self._get('router', 'log_format', '%(asctime)s %(levelname)-8s %(process)d %(message)s')
        self.log_date_format = self._get('router', 'log_date_format', '%Y-%m-%d %H:%M:%S')
        
class deliverSmHttpThrowerConfig(ConfigFile):
    "Config handler for 'deliversm-httpthrower' section"

    def __init__(self, config_file = None):
        ConfigFile.__init__(self, config_file)
        
        self.timeout = self._getint('deliversm-httpthrower', 'timeout', 30)
        self.retryDelay = self._getint('deliversm-httpthrower', 'retry_delay', 30)
        self.maxRetries = self._getint('deliversm-httpthrower', 'max_retries', 3)
        
        # Logging
        self.log_level = logging.getLevelName(self._get('deliversm-httpthrower', 'log_level', 'INFO'))
        self.log_file = self._get('deliversm-httpthrower', 'log_file', '/var/log/jasmin/deliversm-httpthrower.log')
        self.log_format = self._get('deliversm-httpthrower', 'log_format', '%(asctime)s %(levelname)-8s %(process)d %(message)s')
        self.log_date_format = self._get('deliversm-httpthrower', 'log_date_format', '%Y-%m-%d %H:%M:%S')

class DLRThrowerConfig(ConfigFile):
    "Config handler for 'dlr-thrower' section"

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