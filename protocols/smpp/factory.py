#pylint: disable-msg=W0401,W0611
import logging
from OpenSSL import SSL
from twisted.internet.protocol import ClientFactory
from twisted.internet import defer, reactor, ssl
from jasmin.protocols.smpp.protocol import SMPPClientProtocol
from jasmin.vendor.smpp.pdu.error import *

LOG_CATEGORY_BASE = "smpp.client"

class SmppClientIsNotConnected(Exception):
    """
    An exception that is raised when a trying to use smpp object when
    it is still None (before callbacking bind())
    """

class SMPPClientFactory(ClientFactory):
    protocol = SMPPClientProtocol

    def __init__(self, config, msgHandler = None):
        self.reconnectTimer = None
        self.smpp = None
        self.connectionRetry = True
        self.config = config
                
        # Set up a dedicated logger
        self.log = logging.getLogger(LOG_CATEGORY_BASE+".%s" % config.id)
        if len(self.log.handlers) != 1:
            self.log.setLevel(config.log_level)
            handler = logging.FileHandler(filename=config.log_file)
            formatter = logging.Formatter(config.log_format, config.log_date_format)
            handler.setFormatter(formatter)
            self.log.addHandler(handler)

        if msgHandler is None:
            self.msgHandler = self.msgHandlerStub
        else:
            self.msgHandler = msgHandler
    
    def buildProtocol(self, addr):
        """Provision protocol with the dedicated logger
        """
        proto = ClientFactory.buildProtocol(self, addr)
        proto.log = self.log
        
        return proto
        
    def getConfig(self):
        return self.config
    
    def msgHandlerStub(self, smpp, pdu):
        self.log.warn("msgHandlerStub: Received an unhandled message %s ..." % pdu)

    def startedConnecting(self, connector):
        self.log.info("Connecting to %s ..." % connector.getDestination())

    def getExitDeferred(self):
        """Get a Deferred so you can be notified on disconnect and exited
        This deferred is called once disconnection occurs without a further
        reconnection retrys
        """
        return self.exitDeferred

    def clientConnectionFailed(self, connector, reason):
        """Connection failed
        """
        self.log.error("Connection failed. Reason: %s" % str(reason))
        
        if self.config.reconnectOnConnectionFailure and self.connectionRetry:
            self.log.info("Reconnecting after %d seconds ..." % self.config.reconnectOnConnectionFailureDelay)
            self.reconnectTimer = reactor.callLater(self.config.reconnectOnConnectionFailureDelay, self.reConnect, connector)
        else:
            self.connectDeferred.errback(reason)
            self.exitDeferred.callback(None)
            self.log.info("Exiting.")
        
    def clientConnectionLost(self, connector, reason):
        """Connection lost
        """
        self.log.error("Connection lost. Reason: %s" % str(reason))

        if self.config.reconnectOnConnectionLoss and self.connectionRetry:
            self.log.info("Reconnecting after %d seconds ..." % self.config.reconnectOnConnectionLossDelay)
            self.reconnectTimer = reactor.callLater(self.config.reconnectOnConnectionLossDelay, self.reConnect, connector)
        else:
            self.exitDeferred.callback(None)
            self.log.info("Exiting.")
        
    def reConnect(self, connector = None):
        if connector is None:
            self.log.error("No connector to retry !")
        else:
            # Reset deferred if it were called before
            if self.connectDeferred.called == True:
                self.connectDeferred = defer.Deferred()
                self.connectDeferred.addCallback(self.bind)
            
            # And try to connect again
            connector.connect()
            
    def _connect(self):
        self.connectionRetry = True
        
        if self.config.useSSL:
            self.log.info('Establishing SSL connection to %s:%d' % (self.config.host, self.config.port))
            reactor.connectSSL(self.config.host, self.config.port, self, CtxFactory(self.config))
        else:
            self.log.info('Establishing TCP connection to %s:%d' % (self.config.host, self.config.port))
            reactor.connectTCP(self.config.host, self.config.port, self)
        
        self.exitDeferred = defer.Deferred()
        self.connectDeferred = defer.Deferred()
        return self.connectDeferred
    
    def connectAndBind(self):
        self._connect()
        self.connectDeferred.addCallback(self.bind)
                
        return self.connectDeferred
    
    def disconnect(self):
        if self.smpp is not None:
            self.log.info('Disconnecting SMPP client')
            return self.smpp.unbindAndDisconnect()
        else:
            return None
        
    def stopConnectionRetrying(self):
        """This will stop the factory from reconnecting
        It is used whenever a service stop has been requested, the connectionRetry flag
        is reset to True upon connect() call
        """
        
        self.log.info('Stopped automatic connection retrying.')
        if self.reconnectTimer and self.reconnectTimer.active():
            self.reconnectTimer.cancel()
            self.reconnectTimer = None

        self.connectionRetry = False

    def disconnectAndDontRetryToConnect(self):
        self.log.info('Ordering a disconnect with no further reconnections.')
        self.stopConnectionRetrying()
        return self.disconnect()
    
    def bind(self, smpp):
        self.smpp = smpp
        
        if self.config.bindOperation == 'transceiver':
            return smpp.bindAsTransceiver()
        elif self.config.bindOperation == 'receiver':
            return smpp.bindAsReceiver()
        elif self.config.bindOperation == 'transmitter':
            return smpp.bindAsTransmitter()
        else:
            raise SMPPClientError("Invalid bind operation: %s" % self.config.bindOperation)
    
    def getSessionState(self):
        if self.smpp is None:
            return None
        else:
            return self.smpp.sessionState
        
class CtxFactory(ssl.ClientContextFactory):

    def __init__(self, config):
        self.smppConfig = config
    
    def getContext(self):
        self.method = SSL.SSLv23_METHOD
        ctx = ssl.ClientContextFactory.getContext(self)
        if self.smppConfig.SSLCertificateFile:
            ctx.use_certificate_file(self.smppConfig.SSLCertificateFile)
        return ctx
