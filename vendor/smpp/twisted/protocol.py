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

import struct, logging, sys, StringIO, binascii
from enum import Enum
from jasmin.vendor.smpp.pdu.namedtuple import namedtuple    
from twisted.internet import protocol, defer, reactor
from jasmin.vendor.smpp.pdu.operations import *
from jasmin.vendor.smpp.pdu.pdu_encoding import PDUEncoder
from jasmin.vendor.smpp.pdu.pdu_types import PDURequest, PDUResponse, PDUDataRequest, CommandStatus
from jasmin.vendor.smpp.pdu.error import *
from asn2wrs import InstanceOfType

LOG_CATEGORY="smpp.twisted.protocol"

SMPPSessionStates = Enum(
    'NONE',
    'OPEN',
    'BIND_TX_PENDING',
    'BOUND_TX',
    'BIND_RX_PENDING',
    'BOUND_RX',
    'BIND_TRX_PENDING',
    'BOUND_TRX',
    'UNBIND_PENDING',
    'UNBIND_RECEIVED',
    'UNBOUND'
)

SMPPOutboundTxn = namedtuple('SMPPOutboundTxn', 'request, timer, ackDeferred')
SMPPOutboundTxnResult = namedtuple('SMPPOutboundTxnResult', 'smpp, request, response')

class DataHandlerResponse(object):
    
    def __init__(self, status, **params):
        self.status = status
        self.params = params

class SMPPClientProtocol( protocol.Protocol ):
    """Short Message Peer to Peer Protocol v3.4 implementing ESME (client)"""
    version = 0x34

    def __init__( self ):
        self.log = logging.getLogger(LOG_CATEGORY)
        self.recvBuffer = ""
        self.connectionCorrupted = False
        self.pduReadTimer = None
        self.enquireLinkTimer = None
        self.inactivityTimer = None
        self.lastSeqNum = 0
        self.dataRequestHandler = None
        self.alertNotificationHandler = None
        self.inTxns = {}
        self.outTxns = {}
        self.sessionState = SMPPSessionStates.NONE
        self.encoder = PDUEncoder()
        self.disconnectedDeferred = defer.Deferred()

    def config(self):
        return self.factory.getConfig()

    def connectionMade(self):
        """When TCP connection is made
        """
        protocol.Protocol.connectionMade(self)

        self.sessionState = SMPPSessionStates.OPEN
        self.log.warning("Connection established")

    def connectionLost( self, reason ):
        protocol.Protocol.connectionLost( self, reason )
        self.log.warning("Disconnected: %s" % reason)
        
        self.sessionState = SMPPSessionStates.NONE

        self.cancelEnquireLinkTimer()
        self.cancelInactivityTimer()

        self.disconnectedDeferred.callback(None)

    def dataReceived( self, data ):
        """ Looks for a full PDU (protocol data unit) and passes it from
        rawMessageReceived.
        """
        # if self.log.isEnabledFor(logging.DEBUG):
        #     self.log.debug("Received data [%s]" % binascii.b2a_hex(data))
        
        self.recvBuffer = self.recvBuffer + data
        
        while True:
            if self.connectionCorrupted:
                return
            msg = self.readMessage()
            if msg is None:
                break
            self.endPDURead()
            self.rawMessageReceived(msg)
            
        if len(self.recvBuffer) > 0:
            self.incompletePDURead()
                  
    def incompletePDURead(self):
        if self.pduReadTimer and self.pduReadTimer.active():
            return
        self.pduReadTimer = reactor.callLater(self.config().pduReadTimerSecs, self.onPDUReadTimeout)

    def endPDURead(self):
        if self.pduReadTimer and self.pduReadTimer.active():
            self.pduReadTimer.cancel()

    def readMessage(self):
        pduLen = self.getMessageLength()
        if pduLen is None:
            return None
        return self.getMessage(pduLen)

    def getMessageLength(self):
        if len(self.recvBuffer) < 4:
            return None
        return struct.unpack('!L', self.recvBuffer[:4])[0]
    
    def getMessage(self, pduLen):
        if len(self.recvBuffer) < pduLen:
            return None
        
        message = self.recvBuffer[:pduLen]
        self.recvBuffer = self.recvBuffer[pduLen:]
        return message        
        
    def corruptDataRecvd(self, status=CommandStatus.ESME_RINVCMDLEN):
        self.sendPDU(GenericNack(status=status))
        self.onCorruptConnection()
        
    def onCorruptConnection(self):
        """ Once the connection is corrupt, the PDU boundaries are lost and it's impossible to
            continue processing messages.
                - Set a flag to indicate corrupt connection
                    - no more parse attempts should be made for inbound data
                    - no more outbound requests should be attempted (they should errback immediately)
                - Cancel outstanding outbound requests (which have not yet been ack'ed)
                    (removed from the list and errback called)
                - Shutdown
        """
        self.log.critical("Connection is corrupt!!! Shutting down...")
        self.connectionCorrupted = True
        self.cancelOutboundTransactions(SMPPClientConnectionCorruptedError())
        self.shutdown()

    def getHeader(self, message):
        try:            
            return self.encoder.decodeHeader(StringIO.StringIO(message[:self.encoder.HEADER_LEN]))
        except:
            return {}
    
    def onPDUReadTimeout(self):
        self.log.critical('PDU read timed out. Buffer is now considered corrupt')
        self.corruptDataRecvd()

    def rawMessageReceived( self, message ):
        """Called once a PDU (protocol data unit) boundary is identified.

        Creates an SMPP PDU class from the data and calls PDUReceived dispatcher
        """
        pdu = None
        try:
            pdu = self.encoder.decode(StringIO.StringIO(message))
        except PDUCorruptError, e:
            self.log.exception(e)
            self.log.critical("Received corrupt PDU %s" % binascii.b2a_hex(message))
            self.corruptDataRecvd(status=e.status)
        except PDUParseError, e:
            self.log.exception(e)
            self.log.critical("Received unparsable PDU %s" % binascii.b2a_hex(message))
            header = self.getHeader(message)
            seqNum = header.get('sequence_number', None)
            commandId = header.get('command_id', None)
            self.sendPDU(getPDUClass(commandId).requireAck(seqNum=seqNum, status=e.status))
        else:
            self.PDUReceived(pdu)

    def PDUReceived( self, pdu ):
        """Dispatches incoming PDUs
        """
        if self.log.isEnabledFor(logging.DEBUG):
            self.log.debug("Received PDU: %s" % pdu)
        
        #Signal SMPP operation
        self.onSMPPOperation()
        
        if isinstance(pdu, PDURequest):
            self.PDURequestReceived(pdu)
        elif isinstance(pdu, PDUResponse):
            self.PDUResponseReceived(pdu)
        else:
            getattr(self, "onPDU_%s" % str(pdu.id))(pdu)

    def PDURequestReceived(self, reqPDU):
        """Handle incoming request PDUs
        """
        if isinstance(reqPDU, PDUDataRequest):
            self.PDUDataRequestReceived(reqPDU)
            return
            
        getattr(self, "onPDURequest_%s" % str(reqPDU.id))(reqPDU)
        
    def onPDU_outbind(self, pdu):
        if self.sessionState != SMPPSessionStates.OPEN:
            self.log.critical('Received outbind command in invalid state %s' % str(self.sessionState))
            self.shutdown()
            return
            
        self.log.warning("Received outbind command")
        self.doBindAsReceiver()
        
    def onPDU_alert_notification(self, pdu):
        if self.sessionState == SMPPSessionStates.UNBIND_PENDING:
            self.log.info("Unbind is pending...Ignoring alert notification PDU %s" % pdu)
            return
            
        if not self.isBound():
            errMsg = 'Received alert notification when not bound %s' % pdu
            self.cancelOutboundTransactions(SessionStateError(errMsg, CommandStatus.ESME_RINVBNDSTS))
            self.log.critical(errMsg)
            self.shutdown()
            return
            
        if self.alertNotificationHandler:
            try:
                self.alertNotificationHandler(self, pdu)
            except Exception, e:
                self.log.critical('Alert handler threw exception: %s' % str(e))
                self.log.exception(e)
                self.shutdown
        
    def onPDURequest_enquire_link(self, reqPDU):
        self.sendResponse(reqPDU)

    def onPDURequest_unbind(self, reqPDU):
        #Allow no more outbound data requests
        #Accept no more inbound requests
        self.sessionState = SMPPSessionStates.UNBIND_RECEIVED
        self.cancelEnquireLinkTimer()
        #Cancel outbound requests
        self.cancelOutboundTransactions(SMPPClientSessionStateError('Unbind received'))
        #Wait for inbound requests to finish then ack and disconnect
        self.finishInboundTxns().addCallback(lambda r: (self.sendResponse(reqPDU) or True) and self.disconnect())
        
    def sendResponse(self, reqPDU, status=CommandStatus.ESME_ROK, **params):
        self.sendPDU(reqPDU.requireAck(reqPDU.seqNum, status, **params))
                
    def PDUDataRequestReceived(self, reqPDU):
        if self.sessionState == SMPPSessionStates.UNBIND_PENDING:
            self.log.info("Unbind is pending...Ignoring data request PDU %s" % reqPDU)
            return
            
        if not self.isBound():
            errMsg = 'Received data request when not bound %s' % reqPDU
            self.cancelOutboundTransactions(SessionStateError(errMsg, CommandStatus.ESME_RINVBNDSTS))
            return self.fatalErrorOnRequest(reqPDU, errMsg, CommandStatus.ESME_RINVBNDSTS)
            
        if self.dataRequestHandler is None:
            return self.fatalErrorOnRequest(reqPDU, 'Missing dataRequestHandler', CommandStatus.ESME_RX_T_APPN)
        
        self.doPDURequest(reqPDU, self.dataRequestHandler)
        
    def fatalErrorOnRequest(self, reqPDU, errMsg, status):
        self.log.critical(errMsg)
        self.sendResponse(reqPDU, status)
        self.shutdown()
        
    def doPDURequest(self, reqPDU, handler):
        self.startInboundTransaction(reqPDU)
        
        handlerCall = defer.maybeDeferred(handler, self, reqPDU)
        handlerCall.addCallback(self.PDURequestSucceeded, reqPDU)
        handlerCall.addErrback(self.PDURequestFailed, reqPDU)
        handlerCall.addBoth(self.PDURequestFinished, reqPDU)
        
    def PDURequestSucceeded(self, dataHdlrResp, reqPDU):
        if reqPDU.requireAck:
            status = CommandStatus.ESME_ROK
            params = {}
            if dataHdlrResp:
                if dataHdlrResp in CommandStatus:
                    status = dataHdlrResp
                elif isinstance(dataHdlrResp, DataHandlerResponse):
                    status = dataHdlrResp.status
                    params = dataHdlrResp.params
                else:
                    self.log.critical("Invalid response type returned from data handler %s" % type(dataHdlrResp))
                    status = CommandStatus.ESME_RX_T_APPN
                    self.shutdown()
    
            self.sendResponse(reqPDU, status, **params)
        
    def PDURequestFailed(self, error, reqPDU):
        self.log.critical('Exception raised handling inbound PDU [%s] hex[%s]: %s' % (reqPDU, binascii.b2a_hex(self.encoder.encode(reqPDU)), error))
        
        if reqPDU.requireAck:
            self.sendResponse(reqPDU, CommandStatus.ESME_RX_T_APPN)
            
        self.shutdown()

    def PDURequestFinished(self, result, reqPDU):
        self.endInboundTransaction(reqPDU)                    
        return result        
    
    def finishTxns(self):
        return defer.DeferredList([self.finishInboundTxns(), self.finishOutboundTxns()])
    
    def finishInboundTxns(self):
        return defer.DeferredList(self.inTxns.values())
        
    def finishOutboundTxns(self):
        return defer.DeferredList([txn.ackDeferred for txn in self.outTxns.values()])
    
    def PDUResponseReceived(self, pdu):
        """Handle incoming response PDUs
        """
        if isinstance(pdu, GenericNack):
            self.log.critical("Recevied generic_nack %s" % pdu)
            if pdu.seqNum is None:
                self.onCorruptConnection()
                return
        
        if pdu.seqNum not in self.outTxns:
            self.log.critical('Response PDU received with unknown outbound transaction sequence number %s' % pdu)
            return
                
        self.endOutboundTransaction(pdu)

    def sendPDU(self, pdu):
        """Send a SMPP PDU
        """
        if self.log.isEnabledFor(logging.DEBUG):
            self.log.debug("Sending PDU: %s" % pdu)
        encoded = self.encoder.encode(pdu)
        
        # if self.log.isEnabledFor(logging.DEBUG):
        #     self.log.debug("Sending data [%s]" % binascii.b2a_hex(encoded))
        
        self.transport.write( encoded )
        self.onSMPPOperation()

    def sendBindRequest(self, pdu):
        return self.sendRequest(pdu, self.config().sessionInitTimerSecs)
    
    def sendRequest(self, pdu, timeout):
        return defer.maybeDeferred(self.doSendRequest, pdu, timeout)
        
    def doSendRequest(self, pdu, timeout):
        if self.connectionCorrupted:
            raise SMPPClientConnectionCorruptedError()
        if not isinstance( pdu, PDURequest ) or pdu.requireAck is None:
            raise SMPPClientError("Invalid PDU to send: %s" % pdu)

        pdu.seqNum = self.claimSeqNum()
        self.sendPDU(pdu)
        return self.startOutboundTransaction(pdu, timeout)
        
    def onSMPPOperation(self):
        """Called whenever an SMPP PDU is sent or received
        """
        if self.isBound():
            self.activateEnquireLinkTimer()

        self.activateInactivityTimer()

    def activateEnquireLinkTimer(self):
        if self.enquireLinkTimer and self.enquireLinkTimer.active():
            self.enquireLinkTimer.reset(self.config().enquireLinkTimerSecs)
        elif self.config().enquireLinkTimerSecs:
            self.enquireLinkTimer = reactor.callLater(self.config().enquireLinkTimerSecs, self.enquireLinkTimerExpired)
            
    def activateInactivityTimer(self):
        if self.inactivityTimer and self.inactivityTimer.active():
            self.inactivityTimer.reset(self.config().inactivityTimerSecs)
        elif self.config().inactivityTimerSecs:
            self.inactivityTimer = reactor.callLater(self.config().inactivityTimerSecs, self.inactivityTimerExpired)
    
    def cancelEnquireLinkTimer(self):
        if self.enquireLinkTimer and self.enquireLinkTimer.active():
            self.enquireLinkTimer.cancel()
            self.enquireLinkTimer = None

    def cancelInactivityTimer(self):
        if self.inactivityTimer and self.inactivityTimer.active():
            self.inactivityTimer.cancel()
            self.inactivityTimer = None
            
    def enquireLinkTimerExpired(self):
        self.sendRequest(EnquireLink(), self.config().responseTimerSecs)

    def inactivityTimerExpired(self):
        self.log.critical("Inactivity timer expired...shutting down")
        self.shutdown()
        
    def isBound(self):
        return self.sessionState in (SMPPSessionStates.BOUND_TX, SMPPSessionStates.BOUND_RX, SMPPSessionStates.BOUND_TRX)
        
    def shutdown(self):
        """ Unbind if appropriate and disconnect """

        if self.isBound() and not self.connectionCorrupted:
            self.log.warning("Shutdown requested...unbinding")
            self.unbind().addBoth(lambda result: self.disconnect())
        elif self.sessionState not in (SMPPSessionStates.UNBIND_RECEIVED, SMPPSessionStates.UNBIND_PENDING):
            self.log.warning("Shutdown requested...disconnecting")
            self.disconnect()
        else:
            self.log.debug("Shutdown already in progress")
    
    def startInboundTransaction(self, reqPDU):
        if reqPDU.seqNum in self.inTxns:
            raise SMPPProtocolError('Duplicate message id [%s] received.  Already in progess.' % reqPDU.seqNum, CommandStatus.ESME_RUNKNOWNERR)
        txnDeferred = defer.Deferred()
        self.inTxns[reqPDU.seqNum] = txnDeferred
        self.log.debug("Inbound transaction started with message id %s" % reqPDU.seqNum)
        return txnDeferred
    
    def endInboundTransaction(self, reqPDU):
        if not reqPDU.seqNum in self.inTxns:
            raise ValueError('Unknown inbound sequence number in transaction for request PDU %s' % reqPDU)
            
        self.log.debug("Inbound transaction finished with message id %s" % reqPDU.seqNum)
        self.inTxns[reqPDU.seqNum].callback(reqPDU)
        del self.inTxns[reqPDU.seqNum]
    
    def startOutboundTransaction(self, reqPDU, timeout):
        if reqPDU.seqNum in self.outTxns:
            raise ValueError('Seq number [%s] is already in progess.' % reqPDU.seqNum)
        
        #Create callback deferred
        ackDeferred = defer.Deferred()
        #Create response timer
        timer = reactor.callLater(timeout, self.onResponseTimeout, reqPDU, timeout)
        #Save transaction
        self.outTxns[reqPDU.seqNum] = SMPPOutboundTxn(reqPDU, timer, ackDeferred)
        self.log.debug("Outbound transaction started with message id %s" % reqPDU.seqNum)
        return ackDeferred
    
    def closeOutboundTransaction(self, seqNum):        
        self.log.debug("Outbound transaction finished with message id %s" % seqNum)        
        
        txn = self.outTxns[seqNum]
        #Remove txn
        del self.outTxns[seqNum]
        #Cancel response timer
        if txn.timer.active():
            txn.timer.cancel()
        return txn
    
    def endOutboundTransaction(self, respPDU):
        txn = self.closeOutboundTransaction(respPDU.seqNum)
                
        if respPDU.status == CommandStatus.ESME_ROK:
            if not isinstance(respPDU, txn.request.requireAck):
                txn.ackDeferred.errback(SMPPProtocolError("Invalid PDU response type [%s] returned for request type [%s]" % (type(respPDU), type(txn.request))))
                return
            #Do callback
            txn.ackDeferred.callback(SMPPOutboundTxnResult(self, txn.request, respPDU))
            return
        
        if isinstance(respPDU, GenericNack):
            txn.ackDeferred.errback(SMPPGenericNackTransactionError(respPDU, txn.request))
            return
            
        errCode = respPDU.status
        txn.ackDeferred.errback(SMPPTransactionError(respPDU, txn.request))
                
    def endOutboundTransactionErr(self, reqPDU, error):
        self.log.exception(error)
        txn = self.closeOutboundTransaction(reqPDU.seqNum)
        #Do errback
        txn.ackDeferred.errback(error)

    def cancelOutboundTransactions(self, error):
        for txn in self.outTxns.values():
            self.endOutboundTransactionErr(txn.request, error)

    def onResponseTimeout(self, reqPDU, timeout):
        errMsg = 'Request timed out after %s secs: %s' % (timeout, reqPDU)
        self.log.critical(errMsg)
        self.endOutboundTransactionErr(reqPDU, SMPPRequestTimoutError(errMsg))
        self.shutdown()
    
    def claimSeqNum(self):
        self.lastSeqNum += 1
        return self.lastSeqNum
            
    def bindSucceeded(self, result, nextState):
        self.sessionState = nextState
        self.log.warning("Bind succeeded...now in state %s" % str(self.sessionState))
        self.activateEnquireLinkTimer()
        return result
        
    def bindFailed(self, reason):
        self.log.error("Bind failed [%s]. Disconnecting..." % reason)
        self.disconnect()
        if reason.check(SMPPRequestTimoutError):
            raise SMPPSessionInitTimoutError(str(reason))
        return reason
        
    def unbindSucceeded(self, result):
        self.sessionState = SMPPSessionStates.UNBOUND
        self.log.warning("Unbind succeeded")
        return result
        
    def unbindFailed(self, reason):
        self.log.error("Unbind failed [%s]. Disconnecting..." % reason)
        self.disconnect()
        if reason.check(SMPPRequestTimoutError):
            raise SMPPSessionInitTimoutError(str(reason))
        return reason
        
    def bind(self, pdu, pendingState, boundState):
        if self.sessionState != SMPPSessionStates.OPEN:
            return defer.fail(SMPPClientSessionStateError('bind called with illegal session state: %s' % self.sessionState))
        
        bindDeferred = self.sendBindRequest(pdu)
        bindDeferred.addCallback(self.bindSucceeded, boundState)
        bindDeferred.addErrback(self.bindFailed)
        self.sessionState = pendingState
        return bindDeferred
        
    def unbindAfterInProgressTxnsFinished(self, result, unbindDeferred):
        self.log.warning('Issuing unbind request')
        self.sendBindRequest(Unbind()).addCallbacks(self.unbindSucceeded, self.unbindFailed).chainDeferred(unbindDeferred)
        
    def doBindAsReceiver(self):
        self.log.warning('Requesting bind as receiver')
        pdu = BindReceiver(
            system_id = self.config().username,
            password = self.config().password, 
            system_type = self.config().systemType,
            address_range = self.config().addressRange,
            addr_ton = self.config().addressTon,
            addr_npi = self.config().addressNpi,
            interface_version = self.version
        )
        return self.bind(pdu, SMPPSessionStates.BIND_RX_PENDING, SMPPSessionStates.BOUND_RX)
        
    #
    # Public command functions
    #
    def bindAsTransmitter(self):
        """Bind to SMSC as transmitter
        
        Result is a Deferred object
        """
        self.log.warning('Requesting bind as transmitter')
        pdu = BindTransmitter(
            system_id = self.config().username, 
            password = self.config().password, 
            system_type = self.config().systemType,
            address_range = self.config().addressRange,
            addr_ton = self.config().addressTon,
            addr_npi = self.config().addressNpi,
            interface_version = self.version
        )
        return self.bind(pdu, SMPPSessionStates.BIND_TX_PENDING, SMPPSessionStates.BOUND_TX)

    def bindAsReceiver(self, dataRequestHandler):
        """Bind to SMSC as receiver
        
        Result is a Deferred object
        """
        self.setDataRequestHandler(dataRequestHandler)
        return self.doBindAsReceiver()

    def bindAsTransceiver(self, dataRequestHandler):
        """Bind to SMSC as transceiver
        
        Result is a Deferred object
        """
        self.setDataRequestHandler(dataRequestHandler)
        self.log.warning('Requesting bind as transceiver')
        pdu = BindTransceiver(
            system_id = self.config().username, 
            password = self.config().password,
            system_type = self.config().systemType,
            address_range = self.config().addressRange,
            addr_ton = self.config().addressTon,
            addr_npi = self.config().addressNpi,
            interface_version = self.version
        )
        return self.bind(pdu, SMPPSessionStates.BIND_TRX_PENDING, SMPPSessionStates.BOUND_TRX)
    
    def unbind(self):
        """Unbind from SMSC
        
        Result is a Deferred object
        """
        if not self.isBound():
            return defer.fail(SMPPClientSessionStateError('unbind called with illegal session state: %s' % self.sessionState))
        
        self.cancelEnquireLinkTimer()
        
        self.log.info('Waiting for in-progress transactions to finish...')
                
        #Signal that
        #   - no new data requests should be sent
        #   - no new incoming data requests should be accepted
        self.sessionState = SMPPSessionStates.UNBIND_PENDING
                
        unbindDeferred = defer.Deferred()
        #Wait for any in-progress txns to finish
        self.finishTxns().addCallback(self.unbindAfterInProgressTxnsFinished, unbindDeferred)
        #Result is the deferred for the unbind txn
        return unbindDeferred
            
    def unbindAndDisconnect(self):
        """Unbind from SMSC and disconnect
        
        Result is a Deferred object
        """
        return self.unbind().addBoth(lambda result: self.disconnect())
    
    def disconnect(self):
        """Disconnect from SMSC
        """
        if self.isBound():
            self.log.warning("Disconnecting while bound to SMSC...")
        else:
            self.log.warning("Disconnecting...")
        self.sessionState = SMPPSessionStates.UNBOUND
        self.transport.loseConnection()
    
    def sendDataRequest( self, pdu ):
        """Send a SMPP Request Message

        Argument is an SMPP PDUDataRequest (protocol data unit).
        Result is a Deferred object
        """
        if not isinstance( pdu, PDUDataRequest ):
            return defer.fail(SMPPClientError("Invalid PDU passed to sendDataRequest(): %s" % pdu))
        if not self.isBound():
            return defer.fail(SMPPClientSessionStateError('Not bound'))
        return self.sendRequest(pdu, self.config().responseTimerSecs)
        
    def getDisconnectedDeferred(self):
        """Get a Deferred so you can be notified on disconnect
        """
        return self.disconnectedDeferred
        
    def setDataRequestHandler(self, handler):
        """Set handler to use for receiving data requests
        """
        self.dataRequestHandler = handler
        
    def setAlertNotificationHandler(self, handler):
        """Set handler to use for receiving data requests
        """
        self.alertNotificationHandler = handler
