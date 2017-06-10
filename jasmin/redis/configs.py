"""
Config file handler for 'redis-client' section in jasmin.cfg
"""

import logging
import os

from jasmin.config.tools import ConfigFile

# Related to travis-ci builds
ROOT_PATH = os.getenv('ROOT_PATH', '/')


class RedisForJasminConfig(ConfigFile):
    """Config handler for 'redis-client' section"""

    def __init__(self, config_file=None):
        ConfigFile.__init__(self, config_file)

        self.host = self._get('redis-client', 'host', '127.0.0.1')
        self.port = self._getint('redis-client', 'port', 6379)
        self.dbid = self._getint('redis-client', 'dbid', '0')
        self.password = self._get('redis-client', 'password', None)
        self.poolsize = self._getint('redis-client', 'poolsize', 10)

        self.log_level = logging.getLevelName(self._get('redis-client', 'log_level', 'INFO'))
        self.log_file = self._get('redis-client',
                                  'log_file', '%s/var/log/jasmin/redis-client.log' % ROOT_PATH)
        self.log_rotate = self._get('redis-client', 'log_rotate', 'W6')
        self.log_format = self._get('redis-client',
                                    'log_format', '%(asctime)s %(levelname)-8s %(process)d %(message)s')
        self.log_date_format = self._get('redis-client', 'log_date_format', '%Y-%m-%d %H:%M:%S')
