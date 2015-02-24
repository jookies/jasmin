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
import functools
from twisted.trial import unittest
from twisted.internet import error, reactor, defer
from twisted.internet.protocol import Factory 
from twisted.python import log
import mock
from jasmin.vendor.smpp.twisted.protocol import SMPPClientProtocol
from jasmin.vendor.smpp.twisted.client import SMPPClientTransmitter, SMPPClientReceiver, SMPPClientTransceiver, DataHandlerResponse, SMPPClientService
from jasmin.vendor.smpp.twisted.tests.smsc_simulator import *
from jasmin.vendor.smpp.pdu.error import *
from jasmin.vendor.smpp.twisted.config import SMPPClientConfig
from jasmin.vendor.smpp.pdu.operations import *
from jasmin.vendor.smpp.pdu.pdu_types import *

class SimulatorTestCase(unittest.TestCase):
    protocol = BlackHoleSMSC
    configArgs = {}
    
    def setUp(self):
        self.factory = Factory()
        self.factory.protocol = self.protocol
        self.port = reactor.listenTCP(0, self.factory)
        self.testPort = self.port.getHost().port
        
        args = self.configArgs.copy()
        args['host'] = self.configArgs.get('host', 'localhost')
        args['port'] = self.configArgs.get('port', self.testPort)
        args['username'] = self.configArgs.get('username', '')
        args['password'] = self.configArgs.get('password', '')
        
        self.config = SMPPClientConfig(**args)
        
    def tearDown(self):
        self.port.stopListening()

class SessionInitTimeoutTestCase(SimulatorTestCase):
    configArgs = {
        'sessionInitTimerSecs': 0.1,
    }
    
    def test_bind_transmitter_timeout(self):
        client = SMPPClientTransmitter(self.config)
        return self.assertFailure(client.connectAndBind(), SMPPSessionInitTimoutError)

    def test_bind_receiver_timeout(self):
        client = SMPPClientReceiver(self.config, lambda smpp, pdu: None)
        return self.assertFailure(client.connectAndBind(), SMPPSessionInitTimoutError)

    def test_bind_transceiver_timeout(self):
        client = SMPPClientTransceiver(self.config, lambda smpp, pdu: None)
        return self.assertFailure(client.connectAndBind(), SMPPSessionInitTimoutError)

class BindErrorTestCase(SimulatorTestCase):
    protocol = BindErrorSMSC

    def test_bind_error(self):
        client = SMPPClientTransmitter(self.config)
        return self.assertFailure(client.connectAndBind(), SMPPTransactionError)

class BindErrorGenericNackTestCase(SimulatorTestCase):
    protocol = BindErrorGenericNackSMSC

    def test_bind_error_generic_nack(self):
        client = SMPPClientTransmitter(self.config)
        return self.assertFailure(client.connectAndBind(), SMPPGenericNackTransactionError)

class UnbindTimeoutTestCase(SimulatorTestCase):
    protocol = UnbindNoResponseSMSC
    configArgs = {
        'sessionInitTimerSecs': 0.1,
    }

    def setUp(self):
        SimulatorTestCase.setUp(self)
        self.unbindDeferred = defer.Deferred()

    def test_unbind_timeout(self):
        client = SMPPClientTransmitter(self.config)
        bindDeferred = client.connectAndBind().addCallback(self.do_unbind)
        return defer.DeferredList([
            bindDeferred, #asserts that bind was successful
            self.assertFailure(self.unbindDeferred, SMPPSessionInitTimoutError), #asserts that unbind timed out
            ]
        )
        
    def do_unbind(self, smpp):
        smpp.unbind().chainDeferred(self.unbindDeferred)
        return smpp

class ResponseTimeoutTestCase(SimulatorTestCase):
    protocol = NoResponseOnSubmitSMSC
    configArgs = {
        'responseTimerSecs': 0.1,
    }

    def setUp(self):
        SimulatorTestCase.setUp(self)
        self.disconnectDeferred = defer.Deferred()
        self.submitSMDeferred1 = defer.Deferred()
        self.submitSMDeferred2 = defer.Deferred()

    def test_response_timeout(self):
        client = SMPPClientTransmitter(self.config)
        bindDeferred = client.connectAndBind().addCallback(self.do_test_setup)
        self.disconnectDeferred.addCallback(self.verify)
        return defer.DeferredList([
            bindDeferred, #asserts that bind was successful
            self.assertFailure(self.submitSMDeferred1, SMPPRequestTimoutError), #asserts that request1 timed out
            self.assertFailure(self.submitSMDeferred2, SMPPRequestTimoutError), #asserts that request2 timed out
            self.disconnectDeferred, #asserts that disconnect deferred was triggered
            ]
        )
    
    def do_test_setup(self, smpp):
        self.smpp = smpp
        smpp.getDisconnectedDeferred().chainDeferred(self.disconnectDeferred)
        smpp.sendDataRequest(SubmitSM(source_addr='t1', destination_addr='1208230', short_message='HELLO')).chainDeferred(self.submitSMDeferred1)
        smpp.sendDataRequest(SubmitSM(source_addr='t2', destination_addr='1208230', short_message='HELLO')).chainDeferred(self.submitSMDeferred2)
        smpp.sendPDU = mock.Mock(wraps=smpp.sendPDU)
        return smpp

    #Test unbind sent    
    def verify(self, result):
        self.assertEquals(1, self.smpp.sendPDU.call_count)
        sent = self.smpp.sendPDU.call_args[0][0]
        self.assertTrue(isinstance(sent, Unbind))

class InactivityTimeoutTestCase(SimulatorTestCase):
    protocol = HappySMSC
    configArgs = {
        'inactivityTimerSecs': 0.1,
    }

    def setUp(self):
        SimulatorTestCase.setUp(self)
        self.disconnectDeferred = defer.Deferred()

    def test_inactivity_timeout(self):
        client = SMPPClientTransmitter(self.config)
        bindDeferred = client.connectAndBind().addCallback(self.do_test_setup)
        self.disconnectDeferred.addCallback(self.verify)
        return defer.DeferredList([
            bindDeferred, #asserts that bind was successful
            self.disconnectDeferred, #asserts that disconnect deferred was triggered
            ]
        )
    
    def do_test_setup(self, smpp):
        self.smpp = smpp
        smpp.getDisconnectedDeferred().chainDeferred(self.disconnectDeferred)
        smpp.sendPDU = mock.Mock(wraps=smpp.sendPDU)
        return smpp

    #Test unbind sent    
    def verify(self, result):
        self.assertEquals(1, self.smpp.sendPDU.call_count)
        sent = self.smpp.sendPDU.call_args[0][0]
        self.assertTrue(isinstance(sent, Unbind))

class ServerInitiatedUnbindTestCase(SimulatorTestCase):
    protocol = UnbindOnSubmitSMSC

    def setUp(self):
        SimulatorTestCase.setUp(self)
        self.disconnectDeferred = defer.Deferred()
        self.submitSMDeferred = defer.Deferred()

    def test_server_unbind(self):
        client = SMPPClientTransmitter(self.config)
        bindDeferred = client.connectAndBind().addCallback(self.mock_stuff)
        self.disconnectDeferred.addCallback(self.verify)
        return defer.DeferredList([
            bindDeferred, #asserts that bind was successful
            self.disconnectDeferred, #asserts that disconnect deferred was triggered,
            self.assertFailure(self.submitSMDeferred, SMPPClientSessionStateError), #asserts that outbound txn was canceled
            ]
        )
        
    def mock_stuff(self, smpp):
        self.smpp = smpp
        smpp.getDisconnectedDeferred().chainDeferred(self.disconnectDeferred)
        smpp.sendDataRequest(SubmitSM(source_addr='mobileway', destination_addr='1208230', short_message='HELLO1')).chainDeferred(self.submitSMDeferred)
        smpp.sendPDU = mock.Mock(wraps=smpp.sendPDU)
        return smpp
        
    def verify(self, result):
        self.assertEquals(1, self.smpp.sendPDU.call_count)
        sent = self.smpp.sendPDU.call_args[0][0]
        self.assertEquals(UnbindResp(1), sent)

class EnquireLinkTestCase(SimulatorTestCase):
    protocol = EnquireLinkEchoSMSC
    configArgs = {
        'enquireLinkTimerSecs': 0.1,
    }

    @defer.inlineCallbacks
    def test_enquire_link(self):
        client = SMPPClientTransmitter(self.config)
        smpp = yield client.connect()
        #Assert that enquireLinkTimer is not yet active on connection
        self.assertEquals(None, smpp.enquireLinkTimer)
        
        bindDeferred = client.bind(smpp)
        #Assert that enquireLinkTimer is not yet active until bind is complete
        self.assertEquals(None, smpp.enquireLinkTimer)
        yield bindDeferred
        #Assert that enquireLinkTimer is now active after bind is complete
        self.assertNotEquals(None, smpp.enquireLinkTimer)
        
        #Wrap functions for tracking
        smpp.sendPDU = mock.Mock(wraps=smpp.sendPDU)
        smpp.PDUReceived = mock.Mock(wraps=smpp.PDUReceived)
        
        yield self.wait(0.25)
        
        self.verifyEnquireLink(smpp)
        
        #Assert that enquireLinkTimer is still active
        self.assertNotEquals(None, smpp.enquireLinkTimer)
        
        unbindDeferred = smpp.unbind()

        #Assert that enquireLinkTimer is no longer active after unbind is issued
        self.assertEquals(None, smpp.enquireLinkTimer)
        
        yield unbindDeferred
        #Assert that enquireLinkTimer is no longer active after unbind is complete
        self.assertEquals(None, smpp.enquireLinkTimer)
        yield smpp.disconnect()
        
    def wait(self, time_secs):
        finished = defer.Deferred()
        reactor.callLater(time_secs, finished.callback, None)
        return finished
                
    def verifyEnquireLink(self, smpp):
        self.assertEquals(4, smpp.sendPDU.call_count)
        self.assertEquals(4, smpp.PDUReceived.call_count)
        sent1 = smpp.sendPDU.call_args_list[0][0][0]
        sent2 = smpp.sendPDU.call_args_list[1][0][0]
        sent3 = smpp.sendPDU.call_args_list[2][0][0]
        sent4 = smpp.sendPDU.call_args_list[3][0][0]
        recv1 = smpp.PDUReceived.call_args_list[0][0][0]
        recv2 = smpp.PDUReceived.call_args_list[1][0][0]
        recv3 = smpp.PDUReceived.call_args_list[2][0][0]
        recv4 = smpp.PDUReceived.call_args_list[3][0][0]

        self.assertEquals(EnquireLink(2), sent1)
        self.assertEquals(EnquireLinkResp(2), recv1)
        
        self.assertEquals(EnquireLink(1), recv2)
        self.assertEquals(EnquireLinkResp(1), sent2)
        
        self.assertEquals(EnquireLink(3), sent3)
        self.assertEquals(EnquireLinkResp(3), recv3)
        
        self.assertEquals(EnquireLink(2), recv4)
        self.assertEquals(EnquireLinkResp(2), sent4)
        
class TransmitterLifecycleTestCase(SimulatorTestCase):
    protocol = HappySMSC

    def setUp(self):
        SimulatorTestCase.setUp(self)
        self.unbindDeferred = defer.Deferred()
        self.submitSMDeferred = defer.Deferred()
        self.disconnectDeferred = defer.Deferred()

    def test_unbind(self):
        client = SMPPClientTransmitter(self.config)
        bindDeferred = client.connectAndBind().addCallback(self.do_lifecycle)
        return defer.DeferredList([
            bindDeferred, #asserts that bind was successful
            self.submitSMDeferred, #asserts that submit was successful
            self.unbindDeferred, #asserts that unbind was successful
            self.disconnectDeferred, #asserts that disconnect was successful
            ]
        )
        
    def do_lifecycle(self, smpp):
        smpp.getDisconnectedDeferred().chainDeferred(self.disconnectDeferred)
        smpp.sendDataRequest(SubmitSM()).chainDeferred(self.submitSMDeferred).addCallback(lambda result: smpp.unbindAndDisconnect().chainDeferred(self.unbindDeferred))
        return smpp        

class AlertNotificationTestCase(SimulatorTestCase):
    protocol = AlertNotificationSMSC

    @defer.inlineCallbacks
    def test_alert_notification(self):
        client = SMPPClientTransmitter(self.config)
        smpp = yield client.connectAndBind()
        
        alertNotificationDeferred = defer.Deferred()
        alertHandler = mock.Mock(wraps=lambda smpp, pdu: alertNotificationDeferred.callback(None))
        smpp.setAlertNotificationHandler(alertHandler)
                
        sendDataDeferred = smpp.sendDataRequest(DataSM())
        
        smpp.sendPDU = mock.Mock(wraps=smpp.sendPDU)
        smpp.PDUReceived = mock.Mock(wraps=smpp.PDUReceived)
        
        yield sendDataDeferred
        yield alertNotificationDeferred
        yield smpp.unbindAndDisconnect()
        
        self.assertEquals(1, alertHandler.call_count)
        self.assertEquals(smpp, alertHandler.call_args[0][0])
        self.assertTrue(isinstance(alertHandler.call_args[0][1], AlertNotification))
        
        self.assertEquals(1, smpp.sendPDU.call_count)
        self.assertEquals(3, smpp.PDUReceived.call_count)
        sent1 = smpp.sendPDU.call_args[0][0]
        recv1 = smpp.PDUReceived.call_args_list[0][0][0]
        recv2 = smpp.PDUReceived.call_args_list[1][0][0]
        recv3 = smpp.PDUReceived.call_args_list[2][0][0]
        self.assertTrue(isinstance(recv1, DataSMResp))
        self.assertTrue(isinstance(recv2, AlertNotification))
        self.assertTrue(isinstance(sent1, Unbind))
        self.assertTrue(isinstance(recv3, UnbindResp))

class CommandLengthTooShortTestCase(SimulatorTestCase):
    protocol = CommandLengthTooShortSMSC

    def test_generic_nack_on_invalid_cmd_len(self):
        client = SMPPClientTransceiver(self.config, lambda smpp, pdu: None)
        smpp = yield client.connectAndBind()

        msgSentDeferred = defer.Deferred()

        smpp.sendPDU = mock.Mock()
        smpp.sendPDU.side_effect = functools.partial(self.mock_side_effect, msgSentDeferred)

        yield msgSentDeferred

        yield smpp.disconnect()

        self.assertEquals(1, smpp.sendPDU.call_count)
        smpp.sendPDU.assert_called_with(GenericNack(status=CommandStatus.ESME_RINVMSGLEN))

    def mock_side_effect(self, msgSentDeferred, pdu):
        msgSentDeferred.callback(None)
        return mock.DEFAULT

class CommandLengthTooLongTestCase(SimulatorTestCase):
    protocol = CommandLengthTooLongSMSC
    configArgs = {
        'pduReadTimerSecs': 0.1,
    }

    @defer.inlineCallbacks
    def test_generic_nack_on_invalid_cmd_len(self):
        client = SMPPClientTransceiver(self.config, lambda smpp, pdu: None)
        smpp = yield client.connectAndBind()
        
        msgSentDeferred = defer.Deferred()
        
        smpp.sendPDU = mock.Mock()
        smpp.sendPDU.side_effect = functools.partial(self.mock_side_effect, msgSentDeferred)
        
        yield msgSentDeferred
        
        yield smpp.disconnect()
        
        self.assertEquals(1, smpp.sendPDU.call_count)
        smpp.sendPDU.assert_called_with(GenericNack(status=CommandStatus.ESME_RINVCMDLEN))
        
    def mock_side_effect(self, msgSentDeferred, pdu):
        msgSentDeferred.callback(None)
        return mock.DEFAULT

class InvalidCommandIdTestCase(SimulatorTestCase):
    protocol = InvalidCommandIdSMSC

    @defer.inlineCallbacks
    def test_generic_nack_on_invalid_cmd_id(self):
        client = SMPPClientTransceiver(self.config, lambda smpp, pdu: None)
        smpp = yield client.connectAndBind()
        
        msgSentDeferred = defer.Deferred()
        
        smpp.sendPDU = mock.Mock()
        smpp.sendPDU.side_effect = functools.partial(self.mock_side_effect, msgSentDeferred)
        
        yield msgSentDeferred
        
        yield smpp.disconnect()
        
        self.assertEquals(1, smpp.sendPDU.call_count)
        smpp.sendPDU.assert_called_with(GenericNack(status=CommandStatus.ESME_RINVCMDID))
    
    def mock_side_effect(self, msgSentDeferred, pdu):
        msgSentDeferred.callback(None)
        return mock.DEFAULT
                
class NonFatalParseErrorTestCase(SimulatorTestCase):
    protocol = NonFatalParseErrorSMSC

    @defer.inlineCallbacks
    def test_nack_on_invalid_msg(self):
        client = SMPPClientTransceiver(self.config, lambda smpp, pdu: None)
        smpp = yield client.connectAndBind()
        
        msgSentDeferred = defer.Deferred()
        
        smpp.sendPDU = mock.Mock()
        smpp.sendPDU.side_effect = functools.partial(self.mock_side_effect, msgSentDeferred)
        
        yield msgSentDeferred
        
        yield smpp.disconnect()
        
        self.assertEquals(1, smpp.sendPDU.call_count)
        smpp.sendPDU.assert_called_with(QuerySMResp(seqNum=self.protocol.seqNum, status=CommandStatus.ESME_RINVSRCTON))
                    
    def mock_side_effect(self, msgSentDeferred, pdu):
        msgSentDeferred.callback(None)
        return mock.DEFAULT
        
class GenericNackNoSeqNumTestCase(SimulatorTestCase):
    protocol = GenericNackNoSeqNumOnSubmitSMSC

    @defer.inlineCallbacks
    def test_generic_nack_no_seq_num(self):
        client = SMPPClientTransceiver(self.config, lambda smpp, pdu: None)
        smpp = yield client.connectAndBind()
        
        try:
            submitDeferred = smpp.sendDataRequest(SubmitSM())
            smpp.sendPDU = mock.Mock(wraps=smpp.sendPDU)
            yield submitDeferred
        except SMPPClientConnectionCorruptedError:
            pass
        else:
            self.assertTrue(False, "SMPPClientConnectionCorruptedError not raised")
            
        #for nack with no seq num, the connection is corrupt so don't unbind()
        self.assertEquals(0, smpp.sendPDU.call_count)      
        
class GenericNackWithSeqNumTestCase(SimulatorTestCase):
    protocol = GenericNackWithSeqNumOnSubmitSMSC

    @defer.inlineCallbacks
    def test_generic_nack_no_seq_num(self):
        client = SMPPClientTransceiver(self.config, lambda smpp, pdu: None)
        smpp = yield client.connectAndBind()
        
        try:
            yield smpp.sendDataRequest(SubmitSM())
        except SMPPGenericNackTransactionError:
            pass
        else:
            self.assertTrue(False, "SMPPGenericNackTransactionError not raised")
        finally:
            yield smpp.unbindAndDisconnect()
                
class ErrorOnSubmitTestCase(SimulatorTestCase):
    protocol = ErrorOnSubmitSMSC

    @defer.inlineCallbacks
    def test_error_on_submit(self):
        client = SMPPClientTransceiver(self.config, lambda smpp, pdu: None)
        smpp = yield client.connectAndBind()
        try:
            yield smpp.sendDataRequest(SubmitSM())
        except SMPPTransactionError:
            pass
        else:
            self.assertTrue(False, "SMPPTransactionError not raised")
        finally:
            yield smpp.unbindAndDisconnect()
        
class ReceiverLifecycleTestCase(SimulatorTestCase):
    protocol = DeliverSMAndUnbindSMSC

    @defer.inlineCallbacks
    def test_receiver_lifecycle(self):
        client = SMPPClientReceiver(self.config, lambda smpp, pdu: None)
        smpp = yield client.connectAndBind()
        
        smpp.PDUReceived = mock.Mock(wraps=smpp.PDUReceived)
        smpp.sendPDU = mock.Mock(wraps=smpp.sendPDU)
        
        yield smpp.getDisconnectedDeferred()
        
        self.assertEquals(2, smpp.PDUReceived.call_count)
        self.assertEquals(2, smpp.sendPDU.call_count)
        recv1 = smpp.PDUReceived.call_args_list[0][0][0]
        recv2 = smpp.PDUReceived.call_args_list[1][0][0]
        sent1 = smpp.sendPDU.call_args_list[0][0][0]
        sent2 = smpp.sendPDU.call_args_list[1][0][0]
        self.assertTrue(isinstance(recv1, DeliverSM))
        self.assertEquals(recv1.requireAck(recv1.seqNum), sent1)
        self.assertTrue(isinstance(recv2, Unbind))
        self.assertEquals(recv2.requireAck(recv2.seqNum), sent2)

class ReceiverDataHandlerExceptionTestCase(SimulatorTestCase):
    protocol = DeliverSMSMSC

    @defer.inlineCallbacks
    def test_receiver_exception(self):
        client = SMPPClientReceiver(self.config, self.barf)
        smpp = yield client.connectAndBind()
        
        smpp.PDUReceived = mock.Mock(wraps=smpp.PDUReceived)
        smpp.sendPDU = mock.Mock(wraps=smpp.sendPDU)
        
        yield smpp.getDisconnectedDeferred()
        
        self.assertEquals(2, smpp.PDUReceived.call_count)
        self.assertEquals(2, smpp.sendPDU.call_count)
        recv1 = smpp.PDUReceived.call_args_list[0][0][0]
        recv2 = smpp.PDUReceived.call_args_list[1][0][0]
        sent1 = smpp.sendPDU.call_args_list[0][0][0]
        sent2 = smpp.sendPDU.call_args_list[1][0][0]
        self.assertTrue(isinstance(recv1, DeliverSM))
        self.assertEquals(recv1.requireAck(recv1.seqNum, CommandStatus.ESME_RX_T_APPN), sent1)
        self.assertTrue(isinstance(sent2, Unbind))
        self.assertTrue(isinstance(recv2, UnbindResp))
                
    def barf(self, smpp, pdu):
        raise ValueError('barf')
        
class ReceiverDataHandlerBadResponseParamTestCase(SimulatorTestCase):
    protocol = DeliverSMSMSC

    @defer.inlineCallbacks
    def test_receiver_bad_resp_param(self):
        client = SMPPClientReceiver(self.config, self.respondBadParam)
        smpp = yield client.connectAndBind()
        
        smpp.PDUReceived = mock.Mock(wraps=smpp.PDUReceived)
        smpp.sendPDU = mock.Mock(wraps=smpp.sendPDU)
        
        yield smpp.getDisconnectedDeferred()
        
        self.assertEquals(2, smpp.PDUReceived.call_count)
        self.assertEquals(2, smpp.sendPDU.call_count)
        recv1 = smpp.PDUReceived.call_args_list[0][0][0]
        recv2 = smpp.PDUReceived.call_args_list[1][0][0]
        sent1 = smpp.sendPDU.call_args_list[0][0][0]
        sent2 = smpp.sendPDU.call_args_list[1][0][0]
        self.assertTrue(isinstance(recv1, DeliverSM))
        self.assertEquals(recv1.requireAck(recv1.seqNum, CommandStatus.ESME_RX_T_APPN), sent1)
        self.assertTrue(isinstance(sent2, Unbind))
        self.assertTrue(isinstance(recv2, UnbindResp))
        
    def respondBadParam(self, smpp, pdu):
        return DataHandlerResponse(delivery_failure_reason=DeliveryFailureReason.PERMANENT_NETWORK_ERROR)

class ReceiverUnboundErrorTestCase(SimulatorTestCase):
    protocol = DeliverSMBeforeBoundSMSC

    def setUp(self):
        SimulatorTestCase.setUp(self)

    def test_receiver_exception(self):
        client = SMPPClientReceiver(self.config, lambda smpp, pdu: None)
        bindDeferred = client.connectAndBind()
        return self.assertFailure(bindDeferred, SessionStateError)

class OutbindTestCase(SimulatorTestCase):
    protocol = OutbindSMSC

    def msgHandler(self, smpp, pdu):
        smpp.unbindAndDisconnect()
        return None

    @defer.inlineCallbacks
    def test_outbind(self):
        client = SMPPClientReceiver(self.config, self.msgHandler)
        smpp = yield client.connect()
        yield smpp.getDisconnectedDeferred()

class SMPPClientServiceBindTimeoutTestCase(SimulatorTestCase):
    configArgs = {
        'sessionInitTimerSecs': 0.1,
    }

    def test_bind_transmitter_timeout(self):
        client = SMPPClientTransmitter(self.config)
        svc = SMPPClientService(client)
        stopDeferred = svc.getStopDeferred()
        startDeferred = svc.startService()
        return defer.DeferredList([
            self.assertFailure(startDeferred, SMPPSessionInitTimoutError),
            self.assertFailure(stopDeferred, SMPPSessionInitTimoutError),
        ])    
        
if __name__ == '__main__':
    observer = log.PythonLoggingObserver()
    observer.start()
    logging.basicConfig(level=logging.DEBUG)
    
    import sys
    from twisted.scripts import trial
    sys.argv.extend([sys.argv[0]])
    trial.run()