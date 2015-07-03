"""
Config file handlers for 'client-management' and 'sm-listener' section in jasmin.conf
"""

import logging
import ast
from jasmin.config.tools import ConfigFile

DEFAULT_LOGFORMAT = '%(asctime)s %(levelname)-8s %(process)d %(message)s'

class SMPPClientPBConfig(ConfigFile):
    "Config handler for 'client-management' section"
    
    def __init__(self, config_file = None):
        ConfigFile.__init__(self, config_file)

        self.store_path = self._get('client-management', 
                                    'store_path', 
                                    '/etc/jasmin/store')
        
        self.bind = self._get('client-management', 'bind', '0.0.0.0')
        self.port = self._getint('client-management', 'port', 8989)
        
        self.authentication = self._getbool('client-management', 
                                            'authentication', 
                                            True)
        self.admin_username = self._get('client-management', 
                                        'admin_username', 
                                        'cmadmin')
        self.admin_password = self._get('client-management', 
                                        'admin_password', 
                                        "e1c5136acafb7016bc965597c992eb82"
                                        ).decode('hex')

        self.log_level = logging.getLevelName(
                                              self._get('client-management', 
                                                        'log_level', 'INFO'
                                                        )
                                              )
        self.log_file = self._get('client-management', 
                                  'log_file', 
                                  '/var/log/jasmin/smppclient-manager.log')
        self.log_format = self._get('client-management', 
                                    'log_format', 
                                    DEFAULT_LOGFORMAT)
        self.log_date_format = self._get('client-management', 
                                         'log_date_format', 
                                         '%Y-%m-%d %H:%M:%S')
        self.pickle_protocol = self._getint('client-management', 
                                            'pickle_protocol', 
                                            2)

class SMPPClientSMListenerConfig(ConfigFile):
    "Config handler for 'sm-listener' section"

    def __init__(self, config_file = None):
        ConfigFile.__init__(self, config_file)

        self.publish_submit_sm_resp = self._getbool('sm-listener', 
                                                    'publish_submit_sm_resp', 
                                                    False)

        self.smpp_receipt_on_success_submit_sm_resp = self._getbool('sm-listener', 
                                                                    'smpp_receipt_on_success_submit_sm_resp', 
                                                                    False)

        self.submit_error_retrial = ast.literal_eval(
                self._get('sm-listener', 'submit_error_retrial', """{
                            'ESME_RSYSERR':         {'count': 2, 'delay': 30},
                            'ESME_RTHROTTLED':      {'count': 6, 'delay': 5},
                            'ESME_RMSGQFUL':        {'count': 2, 'delay': 180},
                            'ESME_RINVSCHED':       {'count': 2, 'delay': 300},
                       }""")
            )

        self.submit_max_age_smppc_not_ready = self._getint('sm-listener', 
            'submit_max_age_smppc_not_ready', 
            1200)

        self.submit_retrial_delay_smppc_not_ready = self._getint('sm-listener', 
            'submit_retrial_delay_smppc_not_ready', 
            60)
        
        self.log_level = logging.getLevelName(self._get('sm-listener', 
                                                        'log_level', 
                                                        'INFO'))
        self.log_file = self._get('sm-listener', 
                                  'log_file', 
                                  '/var/log/jasmin/messages.log')
        self.log_format = self._get('sm-listener', 
                                    'log_format', 
                                    DEFAULT_LOGFORMAT)
        self.log_date_format = self._get('sm-listener', 
                                         'log_date_format', 
                                         '%Y-%m-%d %H:%M:%S')
