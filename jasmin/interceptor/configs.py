"""
Config file handlers for 'interceptor' section in interceptor.cfg
"""

import logging
from jasmin.config.tools import ConfigFile

class InterceptorPBConfig(ConfigFile):
    "Config handler for 'interceptor' section"

    def __init__(self, config_file = None):
        ConfigFile.__init__(self, config_file)
        
        self.bind = self._get('interceptor', 'bind', '0.0.0.0')
        self.port = self._getint('interceptor', 'port', 8987)
        
        self.authentication = self._getbool('interceptor', 'authentication', True)
        self.admin_username = self._get('interceptor', 'admin_username', 'iadmin')
        self.admin_password = self._get('interceptor', 'admin_password', "dd8b84cdb60655fed3b9b2d668c5bd9e").decode('hex')

        # Logging
        self.log_level = logging.getLevelName(self._get('interceptor', 'log_level', 'INFO'))
        self.log_rotate = self._get('interceptor', 'log_rotate', 'W6')
        self.log_file = self._get('interceptor', 'log_file', '/var/log/jasmin/interceptor.log')
        self.log_format = self._get('interceptor', 'log_format', '%(asctime)s %(levelname)-8s %(process)d %(message)s')
        self.log_date_format = self._get('interceptor', 'log_date_format', '%Y-%m-%d %H:%M:%S')