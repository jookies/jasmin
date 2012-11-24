# Copyright 2012 Fourat Zouari <fourat@gmail.com>
# See LICENSE for details.

from jasmin.config.tools import ConfigFile
import logging

class SMPPClientPBConfig(ConfigFile):
    def __init__(self, config_file = None):
        ConfigFile.__init__(self, config_file)
        
        self.port = self._getint('client-management', 'port', 8989)
        self.log_level = logging.getLevelName(self._get('client-management', 'log_level', 'INFO'))
        self.log_file = self._get('client-management', 'log_file', '/tmp/smppclient-manager.log')
        self.log_format = self._get('client-management', 'log_format', '%(asctime)s %(levelname)-8s %(process)d %(message)s')
        self.log_date_format = self._get('client-management', 'log_date_format', '%Y-%m-%d %H:%M:%S')
        self.pickle_protocol = self._getint('client-management', 'pickle_protocol', 2)

class SMPPClientSMListenerConfig(ConfigFile):
    def __init__(self, config_file = None):
        ConfigFile.__init__(self, config_file)
        
        self.log_level = logging.getLevelName(self._get('sm-listener', 'log_level', 'INFO'))
        self.log_file = self._get('sm-listener', 'log_file', '/tmp/messages.log')
        self.log_format = self._get('sm-listener', 'log_format', '%(asctime)s %(levelname)-8s %(process)d %(message)s')
        self.log_date_format = self._get('sm-listener', 'log_date_format', '%Y-%m-%d %H:%M:%S')