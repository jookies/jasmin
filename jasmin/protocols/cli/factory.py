import re
import logging
from logging.handlers import TimedRotatingFileHandler
from twisted.internet import reactor, defer
from twisted.internet.protocol import ServerFactory
from jasmin.protocols.cli.jcli import JCliProtocol
from jasmin.protocols.cli.protocol import CmdProtocol
from twisted.conch.telnet import TelnetTransport, TelnetBootstrapProtocol
from twisted.conch.insults import insults
from twisted.test import proto_helpers
from hashlib import md5
    
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
                                                    CmdProtocol)
        
class JCliFactory(ServerFactory):
    def __init__(self, config, SMPPClientManagerPB, RouterPB, loadConfigProfileWithCreds = {'username', 'password'}):
        self.config = config
        self.pb = {'smppcm': SMPPClientManagerPB, 'router': RouterPB}
        # Protocol sessions are kept here:
        self.sessions = {}
        self.sessionRef = 0
        self.sessionsOnline = 0
        # When defined, configuration profile will be loaded on startup
        self.loadConfigProfileWithCreds = loadConfigProfileWithCreds
              
        # Set up and configure a dedicated logger
        self.log = logging.getLogger('jcli')
        if len(self.log.handlers) != 1:
            self.log.setLevel(config.log_level)
            handler = TimedRotatingFileHandler(filename=self.config.log_file, 
                when = self.config.log_rotate)
            formatter = logging.Formatter(config.log_format, config.log_date_format)
            handler.setFormatter(formatter)
            self.log.addHandler(handler)
        
        
        # Init protocol
        self.protocol = lambda: JCliTelnetTransport(TelnetBootstrapProtocol,
                                                    insults.ServerProtocol,
                                                    JCliProtocol)

    @defer.inlineCallbacks
    def doStart(self):
        ServerFactory.doStart(self)
        
        # Wait for AMQP to get ready
        self.log.info("Waiting for AMQP to get ready")
        yield self.pb['smppcm'].amqpBroker.channelReady

        # Load configuration profile
        proto = self.buildProtocol(('127.0.0.1', 0))
        tr = proto_helpers.StringTransport()
        proto.makeConnection(tr)

        if self.config.authentication and self.loadConfigProfileWithCreds['username'] is not None and self.loadConfigProfileWithCreds['password'] is not None:
            self.log.info("OnStart loading configuration default profile with username: '%s'" % (self.loadConfigProfileWithCreds['username']))
            
            if (self.loadConfigProfileWithCreds['username'] != self.config.admin_username or 
                md5(self.loadConfigProfileWithCreds['password']).digest() != self.config.admin_password):
                self.log.error("Authentication error, cannot load configuration profile with provided username: '%s'" % 
                    self.loadConfigProfileWithCreds['username'])
                proto.connectionLost(None)
                defer.returnValue(False)
            
            proto.dataReceived('%s\r\n' % self.loadConfigProfileWithCreds['username'])
            proto.dataReceived('%s\r\n' % self.loadConfigProfileWithCreds['password'])
        elif self.config.authentication:
            self.log.error('Authentication is required and no credentials were given, config. profile will not be loaded')
            proto.connectionLost(None)
            defer.returnValue(False)
        else:
            self.log.info("OnStart loading configuration default profile without credentials (auth. is not required)")
            
        proto.dataReceived('load\r\n')
        
        # Wait some more time till all configurations are loaded
        pending_load = ['mtrouter', 'morouter', 'filter', 'group', 'smppcc', 'httpcc', 'user']
        while True:
            for pl in pending_load:
                if re.match(r'.*%s configuration loaded.*' % pl, tr.value(), re.DOTALL):
                    self.log.info("%s configuration loaded." % pl)
                    pending_load.remove(pl)
            
            if len(pending_load) > 0:
                waitDeferred = defer.Deferred()
                reactor.callLater(0.3, waitDeferred.callback, None)
                yield waitDeferred
            else:
                break

        proto.dataReceived('quit\r\n')
        proto.connectionLost(None)
        defer.returnValue(False)