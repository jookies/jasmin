"""
Config file handlers for 'router', 'deliversm-httpthrower' and 'dlr-thrower' section in jasmin.cfg
"""

import cPickle as pickle
import logging
import os
from jasmin.config.tools import ConfigFile

# Related to travis-ci builds
ROOT_PATH = os.getenv('ROOT_PATH', '/')

class RouterPBConfig(ConfigFile):
    "Config handler for 'router' section"

    def __init__(self, config_file=None):
        ConfigFile.__init__(self, config_file)

        self.store_path = self._get('router', 'store_path', '%s/etc/jasmin/store' % ROOT_PATH)

        self.persistence_timer_secs = self._getint('router', 'persistence_timer_secs', 60)

        self.bind = self._get('router', 'bind', '0.0.0.0')
        self.port = self._getint('router', 'port', 8988)

        self.authentication = self._getbool('router', 'authentication', True)
        self.admin_username = self._get('router', 'admin_username', 'radmin')
        self.admin_password = self._get(
            'router', 'admin_password', "82a606ca5a0deea2b5777756788af5c8").decode('hex')

        self.pickle_protocol = self._getint('router', 'pickle_protocol', pickle.HIGHEST_PROTOCOL)

        # Logging
        self.log_level = logging.getLevelName(self._get('router', 'log_level', 'INFO'))
        self.log_rotate = self._get('router', 'log_rotate', 'W6')
        self.log_file = self._get('router', 'log_file', '%s/var/log/jasmin/router.log' % ROOT_PATH)
        self.log_format = self._get(
            'router', 'log_format', '%(asctime)s %(levelname)-8s %(process)d %(message)s')
        self.log_date_format = self._get('router', 'log_date_format', '%Y-%m-%d %H:%M:%S')

class deliverSmThrowerConfig(ConfigFile):
    "Config handler for 'deliversm-thrower' section"

    def __init__(self, config_file=None):
        ConfigFile.__init__(self, config_file)

        self.timeout = self._getint('deliversm-thrower', 'http_timeout', 30)
        self.retry_delay = self._getint('deliversm-thrower', 'retry_delay', 30)
        self.max_retries = self._getint('deliversm-thrower', 'max_retries', 3)

        # Logging
        self.log_level = logging.getLevelName(self._get('deliversm-thrower', 'log_level', 'INFO'))
        self.log_file = self._get(
            'deliversm-thrower', 'log_file', '%s/var/log/jasmin/deliversm-thrower.log' % ROOT_PATH)
        self.log_rotate = self._get('deliversm-thrower', 'log_rotate', 'W6')
        self.log_format = self._get(
            'deliversm-thrower', 'log_format', '%(asctime)s %(levelname)-8s %(process)d %(message)s')
        self.log_date_format = self._get('deliversm-thrower', 'log_date_format', '%Y-%m-%d %H:%M:%S')

class DLRThrowerConfig(ConfigFile):
    "Config handler for 'dlr-thrower' section"

    def __init__(self, config_file=None):
        ConfigFile.__init__(self, config_file)

        self.timeout = self._getint('dlr-thrower', 'http_timeout', 30)
        self.retry_delay = self._getint('dlr-thrower', 'retry_delay', 30)
        self.max_retries = self._getint('dlr-thrower', 'max_retries', 3)

        #139: need configuration to send deliver_sm instead of data_sm for SMPP delivery receipt
        # 20150521: it seems better to get deliver_sm the default pdu for receipts
        self.dlr_pdu = self._get('dlr-thrower', 'dlr_pdu', 'deliver_sm')

        # Logging
        self.log_level = logging.getLevelName(self._get('dlr-thrower', 'log_level', 'INFO'))
        self.log_file = self._get('dlr-thrower', 'log_file', '%s/var/log/jasmin/dlr-thrower.log' % ROOT_PATH)
        self.log_rotate = self._get('dlr-thrower', 'log_rotate', 'W6')
        self.log_format = self._get(
            'dlr-thrower', 'log_format', '%(asctime)s %(levelname)-8s %(process)d %(message)s')
        self.log_date_format = self._get('dlr-thrower', 'log_date_format', '%Y-%m-%d %H:%M:%S')
