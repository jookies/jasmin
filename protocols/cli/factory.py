# Copyright 2012 Fourat Zouari <fourat@gmail.com>
# See LICENSE for details.

import logging
from twisted.internet.protocol import ServerFactory
from jcli import JCliProtocol
from protocol import CmdProtocol
from twisted.conch.telnet import TelnetTransport, TelnetBootstrapProtocol
from twisted.conch.insults import insults

class JCliTelnetTransport(TelnetTransport):
    def connectionLost(self, reason):
        'Overrides TelnetTransport.connectionLost() to prevent errbacks'
        if self.protocol is not None:
            try:
                self.protocol.connectionLost(reason)
            finally:
                del self.protocol

class CmdFactory(ServerFactory):
    def __init__(self):
        # Protocol sessions are kept here:
        self.sessions = {}
        self.sessionRef = 0
        self.sessionsOnline = 0
        
        self.log = logging.getLogger('CmdServer')
                
        # Init protocol
        self.protocol = lambda: JCliTelnetTransport(TelnetBootstrapProtocol,
                               insults.ServerProtocol,
                               CmdProtocol,
                               factory = self,
                               log = self.log)
        
class JCliFactory(ServerFactory):
    def __init__(self, config, SMPPClientManagerPB, RouterPB):
        self.config = config
        self.pb = {'smppcm': SMPPClientManagerPB, 'router': RouterPB}
        # Protocol sessions are kept here:
        self.sessions = {}
        self.sessionRef = 0
        self.sessionsOnline = 0
                
        # Set up a dedicated logger
        self.log = logging.getLogger('jcli')
        self.log.setLevel(config.log_level)
        handler = logging.FileHandler(filename=config.log_file)
        formatter = logging.Formatter(config.log_format, config.log_date_format)
        handler.setFormatter(formatter)
        self.log.addHandler(handler)
        
        # Init protocol
        self.protocol = lambda: JCliTelnetTransport(TelnetBootstrapProtocol,
                               insults.ServerProtocol,
                               JCliProtocol,
                               factory = self,
                               log = self.log)