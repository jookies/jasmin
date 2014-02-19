# Copyright 2012 Fourat Zouari <fourat@gmail.com>
# See LICENSE for details.

import logging
from twisted.internet.protocol import ServerFactory
from jcli import JCliProtocol
from protocol import CmdProtocol
from twisted.conch.telnet import TelnetTransport, TelnetBootstrapProtocol
from twisted.conch.insults import insults
from twisted.test import proto_helpers

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
        if len(self.log.handlers) != 1:
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
        
    def doStart(self):
        ServerFactory.doStart(self)
        
        # Load configuration profile
        self.log.info("OnStart loading configuration profile: '%s'" % self.config.load_profile)
        proto = self.buildProtocol(('127.0.0.1', 0))
        tr = proto_helpers.StringTransport()
        proto.makeConnection(tr)
        proto.dataReceived('load -p %s\r\n' % self.config.load_profile)
        proto.dataReceived('quit\r\n')
        proto.connectionLost(None)