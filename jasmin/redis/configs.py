"""
Config file handler for 'redis-client' section in jasmin.cfg
"""

import logging
import os
import re

from jasmin.config import ConfigFile

ROOT_PATH = os.getenv('ROOT_PATH', '/')
LOG_PATH = os.getenv('LOG_PATH', '%s/var/log/jasmin/' % ROOT_PATH)
REDIS_URL = os.getenv('REDIS_URL', None)


class RedisForJasminConfig(ConfigFile):
    """Config handler for 'redis-client' section"""

    def __init__(self, config_file=None):
        ConfigFile.__init__(self, config_file)

        if REDIS_URL is not None:
            # Take redis config from REDIS_URL env variable (used by heroku)
            self.password, self.host, self.port = \
                re.search(r"^redis\:\/\/\:([a-z0-9]+)@((?!-)[-a-zA-Z0-9.]{1,63}(?<!-))\:(\d+)$", REDIS_URL).groups()
        else:
            self.host = self._get('redis-client', 'host', '127.0.0.1')
            self.port = self._getint('redis-client', 'port', 6379)
            self.password = self._get('redis-client', 'password', None)

        self.dbid = self._getint('redis-client', 'dbid', '0')
        self.poolsize = self._getint('redis-client', 'poolsize', 10)

        self.log_level = logging.getLevelName(self._get('redis-client', 'log_level', 'INFO'))
        self.log_file = self._get('redis-client',
                                  'log_file', '%s/redis-client.log' % LOG_PATH)
        self.log_rotate = self._get('redis-client', 'log_rotate', 'W6')
        self.log_format = self._get('redis-client',
                                    'log_format', '%(asctime)s %(levelname)-8s %(process)d %(message)s')
        self.log_date_format = self._get('redis-client', 'log_date_format', '%Y-%m-%d %H:%M:%S')
