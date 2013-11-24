# Copyright 2012 Fourat Zouari <fourat@gmail.com>
# See LICENSE for details.

from jasmin.vendor.smpp.twisted.protocol import SMPPClientProtocol as twistedSMPPClientProtocol
from jasmin.vendor.smpp.twisted.protocol import SMPPSessionStates, SMPPOutboundTxn, SMPPOutboundTxnResult
from jasmin.vendor.smpp.pdu.pdu_types import CommandId, CommandStatus, DataCoding, DataCodingDefault
from jasmin.vendor.smpp.pdu.constants import data_coding_default_value_map
from jasmin.vendor.smpp.pdu.operations import *
from twisted.internet import defer, reactor
from jasmin.protocols.smpp.error import *
from jasmin.vendor.smpp.pdu.pdu_types import PDURequest

LOG_CATEGORY="smpp.twisted.protocol"

class SMPPClientProtocol( twistedSMPPClientProtocol ):
    def __init__( self ):
        twistedSMPPClientProtocol.__init__(self)
        
        self.longSubmitSmTxns = {}
        
    def connectionMade(self):
        twistedSMPPClientProtocol.connectionMade(self)
        self.log.info("Connection made to %s:%s" % (self.factory.config.host, self.factory.config.port))

        self.factory.connectDeferred.callback(self)
        
    def bindAsReceiver(self):
        """This is a different signature where msgHandler is taken from factory
        """
        return twistedSMPPClientProtocol.bindAsReceiver(self, self.factory.msgHandler)
    
    def bindAsTransceiver(self):
        """This is a different signature where msgHandler is taken from factory
        """
        return twistedSMPPClientProtocol.bindAsTransceiver(self, self.factory.msgHandler)    

    def bindFailed(self, reason):
        self.log.error("Bind failed [%s]. Disconnecting..." % reason)
        self.disconnect()
        if reason.check(SMPPRequestTimoutError):
            raise SMPPSessionInitTimoutError(str(reason))
        
    def endOutboundTransaction(self, respPDU):
        txn = self.closeOutboundTransaction(respPDU.seqNum)
        
        # Any status of a SubmitSMResp must be handled as a normal status
        if isinstance(txn.request, SubmitSM) or respPDU.status == CommandStatus.ESME_ROK:
            if not isinstance(respPDU, txn.request.requireAck):
                txn.ackDeferred.errback(SMPPProtocolError("Invalid PDU response type [%s] returned for request type [%s]" % (type(respPDU), type(txn.request))))
                return
            #Do callback
            txn.ackDeferred.callback(SMPPOutboundTxnResult(self, txn.request, respPDU))
            return
        
        if isinstance(respPDU, GenericNack):
            txn.ackDeferred.errback(SMPPGenericNackTransactionError(respPDU, txn.request))
            return
        
        txn.ackDeferred.errback(SMPPTransactionError(respPDU, txn.request))

    def cancelOutboundTransactions(self, error):
        """Cancels LongSubmitSmTransactions when cancelling OutboundTransactions
        """
        twistedSMPPClientProtocol.cancelOutboundTransactions(self, error)
        self.cancelLongSubmitSmTransactions(error)

    def cancelLongSubmitSmTransactions(self, error):
        for item in self.longSubmitSmTxns.values():
            reqPDU = item['txn'].request
            
            self.log.exception(error)
            txn = self.closeLongSubmitSmTransaction(reqPDU.params['sar_msg_ref_num'])
            #Do errback
            txn.ackDeferred.errback(error)
            
    def startLongSubmitSmTransaction(self, reqPDU, timeout):
        if reqPDU.params['sar_msg_ref_num'] in self.longSubmitSmTxns:
            raise ValueError('sar_msg_ref_num [%s] is already in progess.' % reqPDU.params['sar_msg_ref_num'])
        
        #Create callback deferred
        ackDeferred = defer.Deferred()
        #Create response timer
        timer = reactor.callLater(timeout, self.onResponseTimeout, reqPDU, timeout)
        #Save transaction
        self.longSubmitSmTxns[reqPDU.params['sar_msg_ref_num']] = {
                                                                   'txn' : SMPPOutboundTxn(reqPDU, timer, ackDeferred),
                                                                   'nack_count' : reqPDU.params['sar_total_segments']
                                                                   }
        self.log.debug("Long submit_sm transaction started with sar_msg_ref_num %s" % reqPDU.params['sar_msg_ref_num'])
        return ackDeferred
    
    def closeLongSubmitSmTransaction(self, sar_msg_ref_num):
        self.log.debug("Long submit_sm transaction finished with sar_msg_ref_num %s" % sar_msg_ref_num)        
            
        txn = self.longSubmitSmTxns[sar_msg_ref_num]['txn']
        # Remove txn
        del self.longSubmitSmTxns[sar_msg_ref_num]
        # Cancel response timer
        if txn.timer.active():
            txn.timer.cancel()
            
        return txn
    
    def endLongSubmitSmTransaction(self, _SMPPOutboundTxnResult):
        reqPDU = _SMPPOutboundTxnResult.request
        respPDU = _SMPPOutboundTxnResult.response
        
        # Do we have txn with the given ref ?
        if reqPDU.params['sar_msg_ref_num'] not in self.longSubmitSmTxns:
            raise ValueError('Transaction with sar_msg_ref_num [%s] was not found.' % reqPDU.params['sar_msg_ref_num'])

        # Decrement pending ACKs
        if self.longSubmitSmTxns[reqPDU.params['sar_msg_ref_num']]['nack_count'] > 0:
            self.longSubmitSmTxns[reqPDU.params['sar_msg_ref_num']]['nack_count'] -= 1
            self.log.debug("Long submit_sm transaction with sar_msg_ref_num %s has been updated, nack_count: %s" 
                            % (reqPDU.params['sar_msg_ref_num'], self.longSubmitSmTxns[reqPDU.params['sar_msg_ref_num']]['nack_count']))

        # End the transaction if no more pending ACKs
        if self.longSubmitSmTxns[reqPDU.params['sar_msg_ref_num']]['nack_count'] == 0:
            txn = self.closeLongSubmitSmTransaction(reqPDU.params['sar_msg_ref_num'])
                    
            #Do callback
            txn.ackDeferred.callback(SMPPOutboundTxnResult(self, txn.request, respPDU))

    def endLongSubmitSmTransactionErr(self, failure):
        # Return on generick NACK
        try:
            failure.raiseException()
        except SMPPClientConnectionCorruptedError as error:
            return
            
    def doSendRequest(self, pdu, timeout):
        if self.connectionCorrupted:
            raise SMPPClientConnectionCorruptedError()
        if not isinstance( pdu, PDURequest ) or pdu.requireAck is None:
            raise SMPPClientError("Invalid PDU to send: %s" % pdu)

        if pdu.commandId == CommandId.submit_sm:
            # Convert data_coding from int to DataCoding object
            if 'data_coding' in pdu.params and isinstance(pdu.params['data_coding'], int):
                intVal = pdu.params['data_coding']
                if intVal in data_coding_default_value_map:
                    name = data_coding_default_value_map[intVal]
                    pdu.params['data_coding'] = DataCoding(schemeData = getattr(DataCodingDefault, name))
                else:
                    pdu.params['data_coding'] = None
                    
            # short_message must be in unicode format
            if not isinstance(pdu.params['short_message'], unicode):
                raise ShortMessageCodingError("SubmitSm's short_message must be in unicode, found %s:%s" % (type(pdu.params['short_message']), pdu.params['short_message']))
                
            # Start a LongSubmitSmTransaction if pdu is a long submit_sm and send multiple
            # pdus, each with an OutboundTransaction
            # - Every OutboundTransaction is closed upon receiving the correct submit_sm_resp
            # - Every LongSubmitSmTransaction is closed upong closing all included OutboundTransactions
            if 'sar_msg_ref_num' in pdu.params:
                partedSmPdu = pdu
                first = True
                
                # Iterate through parted PDUs
                while True:
                    partedSmPdu.seqNum = self.claimSeqNum()
                    self.sendPDU(partedSmPdu)
                    # Not like parent protocol's sendPDU, we don't return per pdu
                    # deferred, we'll return per transaction deferred instead
                    self.startOutboundTransaction(partedSmPdu, timeout).addCallbacks(
                                                                                     self.endLongSubmitSmTransaction, 
                                                                                     self.endLongSubmitSmTransactionErr
                                                                                     )
                    
                    # Start a transaction using the first parted PDU
                    if first:
                        first = False
                        txn = self.startLongSubmitSmTransaction(partedSmPdu, timeout)
    
                    try:
                        # There still another PDU to go for
                        partedSmPdu = partedSmPdu.nextPdu
                    except AttributeError:
                        break
    
                return txn
        
        return twistedSMPPClientProtocol.doSendRequest(self, pdu, timeout)