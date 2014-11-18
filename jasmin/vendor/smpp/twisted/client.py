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
import logging
from OpenSSL import SSL
from twisted.internet.protocol import ClientFactory
from twisted.internet import defer, reactor, ssl
from twisted.application import service
from jasmin.vendor.smpp.twisted.protocol import SMPPClientProtocol, DataHandlerResponse

LOG_CATEGORY="smpp.twisted.client"

class SMPPClientFactory(ClientFactory):

    protocol = SMPPClientProtocol

    def __init__(self, config):
        self.config = config
        self.buildProtocolDeferred = defer.Deferred()
        self.log = logging.getLogger(LOG_CATEGORY)
    
    def getConfig(self):
        return self.config

    def buildProtocol(self, addr):
        p = ClientFactory.buildProtocol(self, addr)
        #This is a sneaky way of passing the protocol instance back to the caller
        reactor.callLater(0, self.buildProtocolDeferred.callback, p)
        return p
    
    def clientConnectionFailed(self, connector, reason):
        """Connection failed
        """
        self.log.error("Connection failed. Reason: %s" % str(reason))
        self.buildProtocolDeferred.errback(reason)

class CtxFactory(ssl.ClientContextFactory):

    def __init__(self, config):
        self.smppConfig = config
    
    def getContext(self):
        self.method = SSL.SSLv23_METHOD
        ctx = ssl.ClientContextFactory.getContext(self)
        if self.smppConfig.SSLCertificateFile:
            ctx.use_certificate_file(self.smppConfig.SSLCertificateFile)
        return ctx

class SMPPClientBase(object):
    msgHandler = None
    
    def __init__(self, config):
        self.config = config
        self.log = logging.getLogger(LOG_CATEGORY)
        self.smpp = None
        self.bindDeferred = None

    def connect(self):
        factory = SMPPClientFactory(self.config)
        if self.config.useSSL:
            self.log.warning('Establishing SSL connection to %s:%d' % (self.config.host, self.config.port))
            reactor.connectSSL(self.config.host, self.config.port, factory, CtxFactory(self.config))
        else:
            self.log.warning('Establishing TCP connection to %s:%d' % (self.config.host, self.config.port))
            reactor.connectTCP(self.config.host, self.config.port, factory) 
        return factory.buildProtocolDeferred.addCallback(self.onConnect)
    
    def onConnect(self, smpp):
        self.smpp = smpp
        if self.msgHandler is not None:
            smpp.setDataRequestHandler(self.msgHandler)
        return smpp
        
    def connectAndBind(self):
        self.bindDeferred = defer.Deferred()
        self.connect().addCallback(self.doBind).addErrback(self.bindDeferred.errback)
        return self.bindDeferred
        
    def doBind(self, smpp):
        self.bind(smpp).addCallback(self.bound).addErrback(self.bindFailed, smpp)
        return smpp
        
    def bind(self, smpp):
        raise NotImplementedError()
        
    #If bind fails, don't errback until we're disconnected
    def bindFailed(self, error, smpp):
        smpp.getDisconnectedDeferred().addCallback(lambda result: self.bindDeferred.errback(error))
        
    def bound(self, result):
        self.bindDeferred.callback(result.smpp)

class SMPPClientTransmitter(SMPPClientBase):
        
    def bind(self, smpp):
        return smpp.bindAsTransmitter()

class SMPPClientReceiver(SMPPClientBase):
    
    def __init__(self, config, msgHandler):
        SMPPClientBase.__init__(self, config)
        self.msgHandler = msgHandler

    def bind(self, smpp):
        return smpp.bindAsReceiver(self.msgHandler)

class SMPPClientTransceiver(SMPPClientReceiver):

    def bind(self, smpp):
        return smpp.bindAsTransceiver(self.msgHandler)

#TODO - move this to mozes code base since
# the service support in Twisted is so crappy
class SMPPClientService(service.Service):
    
    def __init__(self, smppClient):
        self.client = smppClient
        self.stopDeferred = defer.Deferred()
        
    def getStopDeferred(self):
        return self.stopDeferred
    
    @defer.inlineCallbacks
    def startService(self):
        service.Service.startService(self)
        bindDeferred = self.client.connectAndBind()
        bindDeferred.addErrback(self.handleStartError)
        smpp = yield bindDeferred
        smpp.getDisconnectedDeferred().chainDeferred(self.stopDeferred)
        defer.returnValue(smpp)
    
    def handleStartError(self, error):
        self.stopDeferred.errback(error)
        return error
        
    def stopService(self):
        service.Service.stopService(self)
        if self.client.smpp:
            self.log.info("Stopping SMPP Client")
            return self.client.smpp.unbindAndDisconnect()

