"""
Copyright 2009-2010 Mozes, Inc.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either expressed or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
"""

class SMPPConfig(object):
    
    def __init__(self, **kwargs):
        self.sessionInitTimerSecs = kwargs.get('sessionInitTimerSecs', 30)
        self.enquireLinkTimerSecs = kwargs.get('enquireLinkTimerSecs', 10)
        self.inactivityTimerSecs = kwargs.get('inactivityTimerSecs', 120)
        self.responseTimerSecs = kwargs.get('responseTimerSecs', 60)
        self.pduReadTimerSecs = kwargs.get('pduReadTimerSecs', 10)

class SMPPClientConfig(SMPPConfig):
    
    def __init__(self, **kwargs):
        super(SMPPClientConfig, self).__init__(**kwargs)
        self.host = kwargs['host']
        self.port = kwargs['port']
        self.username = kwargs['username']
        self.password = kwargs['password']
        self.systemType = kwargs.get('systemType', '')
        self.useSSL = kwargs.get('useSSL', False)
        self.SSLCertificateFile = kwargs.get('SSLCertificateFile', None)
        self.addressRange = kwargs.get('addressRange', None)
        self.addressTon = kwargs.get('addressTon', None)
        self.addressNpi = kwargs.get('addressNpi', None)

class SMPPServerConfig(SMPPConfig):
    
    def __init__(self, **kwargs):
        """
        @param systems: A dict of data representing the available
        systems.
        { "username1": {"max_bindings" : 2},
          "username2": {"max_bindings" : 1}
        }
        """
        super(SMPPServerConfig, self).__init__(**kwargs)
        self.systems = kwargs.get('systems', {})
        self.msgHandler = kwargs['msgHandler']