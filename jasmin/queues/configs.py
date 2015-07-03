"""
Config file handler for 'amqp-broker' section in jasmin.conf
"""

import logging
import txamqp
from jasmin.config.tools import ConfigFile

class AmqpConfig(ConfigFile):
    "Config handler for 'amqp-broker' section"

    def __init__(self, config_file = None):
        ConfigFile.__init__(self, config_file)
        
        self.host = self._get('amqp-broker', 'host', '127.0.0.1')
        self.port = self._getint('amqp-broker', 'port', 5672)
        self.username = self._get('amqp-broker', 'username', 'guest')
        self.password = self._get('amqp-broker', 'password', 'guest')
        self.vhost = self._get('amqp-broker', 'vhost', '/')
        self.spec = self._get('amqp-broker', 'spec', '/etc/jasmin/resource/amqp0-9-1.xml')

        # Logging
        self.log_level = logging.getLevelName(self._get('amqp-broker', 'log_level', 'INFO'))
        self.log_file = self._get('amqp-broker', 'log_file', '/var/log/jasmin/amqp-client.log')
        self.log_rotate = self._get('amqp-broker', 'log_rotate', 'W6')
        self.log_format = self._get('amqp-broker', 'log_format', '%(asctime)s %(levelname)-8s %(process)d %(message)s')
        self.log_date_format = self._get('amqp-broker', 'log_date_format', '%Y-%m-%d %H:%M:%S')
                
        # Reconnection
        self.reconnectOnConnectionLoss = self._getbool('amqp-broker', 'connection_loss_retry', True)
        self.reconnectOnConnectionFailure = self._getbool('amqp-broker', 'connection_failure_retry', True)
        self.reconnectOnConnectionLossDelay = self._getint('amqp-broker', 'connection_loss_retry_delay', 10)
        self.reconnectOnConnectionFailureDelay = self._getint('amqp-broker', 'connection_failure_retry_delay', 10)
    
    def getSpec(self):
        "Will return the specifications from self.spec file"
        
        return txamqp.spec.load(self.spec)
