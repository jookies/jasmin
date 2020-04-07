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
import logging, binascii
from twisted.trial import unittest
from twisted.internet import error, reactor, defer
from twisted.python import log
from mock import Mock, sentinel
from jasmin.vendor.smpp.pdu.error import *
from jasmin.vendor.smpp.pdu.operations import *
from jasmin.vendor.smpp.pdu.pdu_types import *
from jasmin.vendor.smpp.twisted.config import SMPPClientConfig
from jasmin.vendor.smpp.twisted.protocol import SMPPClientProtocol, SMPPSessionStates, SMPPOutboundTxnResult, DataHandlerResponse

class FakeClientError(SMPPClientError):
    pass

class ProtocolTestCase(unittest.TestCase):
    
    def getProtocolObject(self):
        smpp = SMPPClientProtocol()
        config = SMPPClientConfig(
            host='localhost',
            port = 82,
            username = '',
            password = '',
        )
        smpp.config = Mock(return_value=config)
        return smpp
        
    def test_corruptData(self):
        smpp = self.getProtocolObject()
        self.assertEquals('', smpp.recvBuffer)
        smpp.sendPDU = Mock()
        smpp.cancelOutboundTransactions = Mock()
        smpp.shutdown = Mock()
        smpp.sessionState = SMPPSessionStates.BOUND_TRX
        smpp.corruptDataRecvd()
        #Assert that corrupt data
        #Triggers a shutdown call
        self.assertEquals(1, smpp.shutdown.call_count)
        #Causes outbound transactions to be canceled
        self.assertEquals(1, smpp.cancelOutboundTransactions.call_count)
        #Responds with a generic nack with invalid cmd len error status
        nackResp = smpp.sendPDU.call_args[0][0]
        self.assertEquals(GenericNack(seqNum=None, status=CommandStatus.ESME_RINVCMDLEN), nackResp)
        #Causes new data received to be ignored
        newDataHex = 'afc4'
        smpp.dataReceived(binascii.a2b_hex(newDataHex))
        self.assertEquals(newDataHex, binascii.b2a_hex(smpp.recvBuffer))
        #Causes new data requests to fail immediately
        submitPdu = SubmitSM(
            source_addr_ton=AddrTon.ALPHANUMERIC,
            source_addr='mobileway',
            dest_addr_ton=AddrTon.INTERNATIONAL,
            dest_addr_npi=AddrNpi.ISDN,
            destination_addr='1208230',
            short_message='HELLO',
        )
        return self.assertFailure(smpp.sendDataRequest(submitPdu), SMPPClientConnectionCorruptedError)
    
    def test_cancelOutboundTransactions(self):
        smpp = self.getProtocolObject()
        smpp.sendPDU = Mock()
        smpp.sessionState = SMPPSessionStates.BOUND_TRX
        #start two transactions
        submitPdu1 = SubmitSM(
            source_addr_ton=AddrTon.ALPHANUMERIC,
            source_addr='mobileway',
            dest_addr_ton=AddrTon.INTERNATIONAL,
            dest_addr_npi=AddrNpi.ISDN,
            destination_addr='1208230',
            short_message='HELLO1',
        )
        submitPdu2 = SubmitSM(
            source_addr_ton=AddrTon.ALPHANUMERIC,
            source_addr='mobileway',
            dest_addr_ton=AddrTon.INTERNATIONAL,
            dest_addr_npi=AddrNpi.ISDN,
            destination_addr='1208230',
            short_message='HELLO2',
        )
        d1 = smpp.sendDataRequest(submitPdu1)
        d2 = smpp.sendDataRequest(submitPdu2)
        self.assertEquals(2, len(smpp.outTxns))
        smpp.cancelOutboundTransactions(FakeClientError('test'))
        return defer.DeferredList([
            self.assertFailure(d1, FakeClientError),
            self.assertFailure(d2, FakeClientError),
        ])
    
    def test_finish_txns(self):
        smpp = self.getProtocolObject()
        smpp.sendPDU = Mock()
        smpp.sessionState = SMPPSessionStates.BOUND_TRX
        
        #setup outbound txns
        outPdu1 = SubmitSM(
            seqNum=98790,
            source_addr='mobileway',
            destination_addr='1208230',
            short_message='HELLO1',
        )
        outRespPdu1 = outPdu1.requireAck(seqNum=outPdu1.seqNum)
        
        outPdu2 = SubmitSM(
            seqNum=875,
            source_addr='mobileway',
            destination_addr='1208230',
            short_message='HELLO1',
        )
        outRespPdu2 = outPdu2.requireAck(seqNum=outPdu2.seqNum, status=CommandStatus.ESME_RINVSRCTON)
        
        outDeferred1 = smpp.startOutboundTransaction(outPdu1, 1)
        outDeferred2 = smpp.startOutboundTransaction(outPdu2, 1)
        
        finishOutTxns = smpp.finishOutboundTxns()
                
        #Simulate second txn having error
        smpp.endOutboundTransactionErr(outRespPdu2, FakeClientError('test'))
        #Assert txns not done yet
        self.assertFalse(finishOutTxns.called)
        
        #Simulate first txn finishing
        smpp.endOutboundTransaction(outRespPdu1)
        #Assert txns are all done
        self.assertTrue(finishOutTxns.called)
        
        return defer.DeferredList([
            outDeferred1,
            self.assertFailure(outDeferred2, FakeClientError),
            finishOutTxns,
        ]
        )
    
    def test_graceful_unbind(self):
        smpp = self.getProtocolObject()
        smpp.sendPDU = Mock()
        smpp.sessionState = SMPPSessionStates.BOUND_TRX
        
        #setup outbound txn
        outPdu = SubmitSM(
            seqNum=98790,
            source_addr='mobileway',
            destination_addr='1208230',
            short_message='HELLO1',
        )
        outRespPdu = outPdu.requireAck(seqNum=outPdu.seqNum)
        outDeferred = smpp.startOutboundTransaction(outPdu, 1)
        #setup inbound txn
        inPdu = DeliverSM(
            seqNum=764765,
            source_addr='mobileway',
            destination_addr='1208230',
            short_message='HELLO1',
        )
        inDeferred = smpp.startInboundTransaction(inPdu)
        
        #Call unbind
        unbindDeferred = smpp.unbind()
        
        #Assert unbind request not sent and deferred not fired
        self.assertEquals(0, smpp.sendPDU.call_count)
        self.assertFalse(unbindDeferred.called)

        #Simulate inbound txn finishing
        smpp.endInboundTransaction(inPdu)
        
        #Assert unbind request not sent and deferred not fired
        self.assertEquals(0, smpp.sendPDU.call_count)
        self.assertFalse(unbindDeferred.called)
        
        #Simulate outbound txn finishing
        smpp.endOutboundTransaction(outRespPdu)
        
        #Assert unbind request was sent but deferred not yet fired
        self.assertEquals(1, smpp.sendPDU.call_count)
        sentPdu = smpp.sendPDU.call_args[0][0]
        self.assertTrue(isinstance(sentPdu, Unbind))
        self.assertFalse(unbindDeferred.called)
        
        bindResp = UnbindResp(seqNum=sentPdu.seqNum)
        
        #Simulate unbind_resp
        smpp.endOutboundTransaction(bindResp)
        
        #Assert unbind deferred fired
        self.assertTrue(unbindDeferred.called)
        self.assertTrue(isinstance(unbindDeferred.result, SMPPOutboundTxnResult))
        expectedResult = SMPPOutboundTxnResult(smpp, sentPdu, bindResp)
        self.assertEquals(expectedResult, unbindDeferred.result)
        
    def test_bind_when_not_in_open_state(self):
        smpp = self.getProtocolObject()
        smpp.sessionState = SMPPSessionStates.BOUND_TRX
        return self.assertFailure(smpp.bindAsTransmitter(), SMPPClientSessionStateError)
        
    def test_unbind_when_not_bound(self):
        smpp = self.getProtocolObject()
        smpp.sessionState = SMPPSessionStates.BIND_TX_PENDING
        return self.assertFailure(smpp.unbind(), SMPPClientSessionStateError)

    def test_server_initiated_unbind_cancels_enquire_link_timer(self):
        smpp = self.getProtocolObject()
        smpp.sendResponse = Mock()
        smpp.disconnect = Mock()
        
        smpp.sessionState = SMPPSessionStates.BOUND_TRX
        smpp.activateEnquireLinkTimer()
        self.assertNotEquals(None, smpp.enquireLinkTimer)
        smpp.onPDURequest_unbind(Unbind())
        self.assertEquals(None, smpp.enquireLinkTimer)
        
    def test_sendDataRequest_when_not_bound(self):
        smpp = self.getProtocolObject()
        smpp.sessionState = SMPPSessionStates.BIND_TX_PENDING
        return self.assertFailure(smpp.sendDataRequest(SubmitSM()), SMPPClientSessionStateError)

    def test_sendDataRequest_invalid_pdu(self):
        smpp = self.getProtocolObject()
        smpp.sessionState = SMPPSessionStates.BOUND_TRX
        return self.assertFailure(smpp.sendDataRequest(Unbind()), SMPPClientError)
    
    def test_data_handler_return_none(self):
        smpp = self.getProtocolObject()
        smpp.sendPDU = Mock()
        reqPDU = DeliverSM(5)
        smpp.PDURequestSucceeded(None, reqPDU)
        self.assertEquals(1, smpp.sendPDU.call_count)
        sent = smpp.sendPDU.call_args[0][0]
        self.assertEquals(DeliverSMResp(5), sent)
        
    def test_data_handler_return_status(self):
        smpp = self.getProtocolObject()
        smpp.sendPDU = Mock()
        reqPDU = DeliverSM(5)
        smpp.PDURequestSucceeded(CommandStatus.ESME_RINVSRCTON, reqPDU)
        self.assertEquals(1, smpp.sendPDU.call_count)
        sent = smpp.sendPDU.call_args[0][0]
        self.assertEquals(DeliverSMResp(5, CommandStatus.ESME_RINVSRCTON), sent)

    def test_data_handler_return_resp(self):
        smpp = self.getProtocolObject()
        smpp.sendPDU = Mock()
        reqPDU = DataSM(6)
        smpp.PDURequestSucceeded(DataHandlerResponse(CommandStatus.ESME_RINVSRCTON, delivery_failure_reason=DeliveryFailureReason.PERMANENT_NETWORK_ERROR), reqPDU)
        self.assertEquals(1, smpp.sendPDU.call_count)
        sent = smpp.sendPDU.call_args[0][0]
        self.assertEquals(DataSMResp(6, CommandStatus.ESME_RINVSRCTON, delivery_failure_reason=DeliveryFailureReason.PERMANENT_NETWORK_ERROR), sent)

    def test_data_handler_return_junk(self):
        smpp = self.getProtocolObject()
        smpp.sendPDU = Mock()
        smpp.shutdown = Mock()
        reqPDU = DeliverSM(5)
        smpp.PDURequestSucceeded(3, reqPDU)
        self.assertEquals(1, smpp.shutdown.call_count)
        self.assertEquals(1, smpp.sendPDU.call_count)
        sent = smpp.sendPDU.call_args[0][0]
        self.assertEquals(DeliverSMResp(5, CommandStatus.ESME_RX_T_APPN), sent)

if __name__ == '__main__':
    observer = log.PythonLoggingObserver()
    observer.start()
    logging.basicConfig(level=logging.DEBUG)
    
    import sys
    from twisted.scripts import trial
    sys.argv.extend([sys.argv[0]])
    trial.run()