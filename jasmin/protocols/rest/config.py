"""This is the jasmin-celery and jasmin-restapi configurations"""

import logging
import os
import sys
from jasmin.config import ConfigFile, ROOT_PATH, LOG_PATH

CONFIG_PATH = os.getenv('CONFIG_PATH', '%s/etc/jasmin/' % ROOT_PATH)
REST_API_CONFIG = os.getenv('REST_API_CONFIG', '%s/rest-api.cfg' % CONFIG_PATH)

class RestAPIForJasminConfig(ConfigFile):
    """Config handler for 'rest-api' section"""

    def __init__(self, config_file=REST_API_CONFIG):
        ConfigFile.__init__(self, config_file)

        # Celery global configuration
        self.timezone = self._get('celery', 'timezone', 'UTC')
        self.enable_utc = self._getbool('celery', 'enable_utc', True)
        self.accept_content = self._get('celery', 'accept_content', 'json').split(',')

        # AMQP Broker Connection configuration
        self.broker_url = 'amqp://%s:%s@%s:%s/%s' % (
            self._get('amqp-broker', 'username', 'guest'),
            self._get('amqp-broker', 'password', 'guest'),
            self._get('amqp-broker', 'host', '127.0.0.1'),
            self._getint('amqp-broker', 'port', 5672),
            self._get('amqp-broker', 'vhost', '/'))
        
        self.broker_heartbeat = self._getint('amqp-broker', 'broker_heartbeat', 120)
        self.broker_heartbeat_checkrate = self._getint('amqp-broker', 'broker_heartbeat_checkrate', 2)
        self.broker_connection_timeout = self._getint('amqp-broker', 'broker_connection_timeout', 4)
        self.broker_connection_retry = self._getbool('amqp-broker', 'broker_connection_retry', True)
        self.broker_connection_startup_retry = self._getbool('amqp-broker', 'broker_connection_startup_retry', True)
        self.broker_connection_max_retries = self._getint('amqp-broker', 'broker_connection_max_retries', 100)
        self.task_serializer = self._get('amqp-broker', 'task_serializer', 'json')

        # Redis configuration
        redis_password = self._get('redis-client', 'password', None)
        self.result_backend = 'redis://:%s@%s:%s/%s' % (
            redis_password if redis_password is not None else '',
            self._get('redis-client', 'host', '127.0.0.1'),
            self._getint('redis-client', 'port', 6379),
            self._getint('redis-client', 'result_dbid', 1))
        
        self.redis_socket_timeout = self._getint('redis-client', 'redis_socket_timeout', 120)
        self.result_expires = self._getint('redis-client', 'result_expires', 86400)
        self.result_serializer = self._get('redis-client', 'result_serializer', 'json')

        
        self.http_api_uri = 'http://%s:%s' % (
            self._get('rest-api', 'http_api_host', '127.0.0.1'), 
            self._get('rest-api', 'http_api_port', '1401'))
        
        self.show_jasmin_version = self._getbool('rest-api', 'show_jasmin_version', True)
        self.auth_cache_seconds = self._getint('rest-api', 'auth_cache_seconds', 10)
        self.auth_cache_max_keys = self._getint('rest-api', 'auth_cache_max_keys', 500)
        self.http_throughput_per_worker = self._getint('rest-api', 'http_throughput_per_worker', 8)
        self.smart_qos = self._getbool('rest-api', 'smart_qos', True)
        
        self.log_level = logging.getLevelName(self._get('rest-api', 'log_level', 'INFO'))
        self.log_file = self._get('rest-api', 'log_file', '%s/rest-api.log' % LOG_PATH)
        self.log_rotate = self._get('rest-api', 'log_rotate', 'W6')
        self.log_format = self._get('rest-api', 'log_format', '%(asctime)s %(levelname)-8s %(process)d %(message)s')
        self.log_date_format = self._get('rest-api', 'log_date_format', '%Y-%m-%d %H:%M:%S')

        self.logger = logging.getLogger('jasmin-restapi')
        if len(self.logger.handlers) == 0:
            self.logger.setLevel(self.log_level)
            if 'stdout' in self.log_file:
                handler = logging.StreamHandler(sys.stdout)
            else:
                handler = logging.handlers.TimedRotatingFileHandler(filename=self.log_file, when=self.log_rotate)
            handler.setFormatter(logging.Formatter(self.log_format, self.log_date_format))
            self.logger.addHandler(handler)


