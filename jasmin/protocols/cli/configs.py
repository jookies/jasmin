"""
Config file handler for 'jcli' section in jasmin.conf
"""

from jasmin.config.tools import ConfigFile
import logging

class JCliConfig(ConfigFile):
    "Config handler for 'jcli' section"

    def __init__(self, config_file = None):
        ConfigFile.__init__(self, config_file)
        
        self.bind = self._get('jcli', 'bind', '127.0.0.1')
        self.port = self._getint('jcli', 'port', 8990)
        
        self.authentication = self._getint('jcli', 'authentication', True)
        self.admin_username = self._get('jcli', 'admin_username', 'jcliadmin')
        self.admin_password = self._get('jcli', 'admin_password', '79e9b0aa3f3e7c53e916f7ac47439bcb').decode('hex')

        self.log_level = logging.getLevelName(self._get('jcli', 'log_level', 'INFO'))
        self.log_file = self._get('jcli', 'log_file', '/var/log/jasmin/jcli.log')
        self.log_format = self._get('jcli', 'log_format', '%(asctime)s %(levelname)-8s %(process)d %(message)s')
        self.log_date_format = self._get('jcli', 'log_date_format', '%Y-%m-%d %H:%M:%S')