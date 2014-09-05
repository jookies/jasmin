from jasmin.config.tools import ConfigFile
import logging

class SMPPClientPBConfig(ConfigFile):
    def __init__(self, config_file = None):
        ConfigFile.__init__(self, config_file)

        self.store_path = self._get('client-management', 'store_path', '/etc/jasmin/store')
        
        self.bind = self._get('client-management', 'bind', '0.0.0.0')
        self.port = self._getint('client-management', 'port', 8989)
        
        self.authentication = self._getbool('client-management', 'authentication', True)
        self.admin_username = self._get('client-management', 'admin_username', 'cmadmin')
        self.admin_password = self._get('client-management', 'admin_password', "e1c5136acafb7016bc965597c992eb82").decode('hex')

        self.log_level = logging.getLevelName(self._get('client-management', 'log_level', 'INFO'))
        self.log_file = self._get('client-management', 'log_file', '/var/log/jasmin/smppclient-manager.log')
        self.log_format = self._get('client-management', 'log_format', '%(asctime)s %(levelname)-8s %(process)d %(message)s')
        self.log_date_format = self._get('client-management', 'log_date_format', '%Y-%m-%d %H:%M:%S')
        self.pickle_protocol = self._getint('client-management', 'pickle_protocol', 2)

class SMPPClientSMListenerConfig(ConfigFile):
    def __init__(self, config_file = None):
        ConfigFile.__init__(self, config_file)
        
        self.log_level = logging.getLevelName(self._get('sm-listener', 'log_level', 'INFO'))
        self.log_file = self._get('sm-listener', 'log_file', '/var/log/jasmin/messages.log')
        self.log_format = self._get('sm-listener', 'log_format', '%(asctime)s %(levelname)-8s %(process)d %(message)s')
        self.log_date_format = self._get('sm-listener', 'log_date_format', '%Y-%m-%d %H:%M:%S')
