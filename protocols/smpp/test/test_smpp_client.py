# Copyright 2012 Fourat Zouari <fourat@gmail.com>
# See LICENSE for details.

"""
Test cases for smpp client
These are test cases for only Jasmin's code, smpp.twisted tests are not included here
"""

import logging
import mock
import time
from jasmin.protocols.smpp.protocol import *
from jasmin.protocols.smpp.test.smsc_simulator import *
from jasmin.protocols.smpp.operations import SMPPOperationFactory
from twisted.internet import defer
from twisted.trial.unittest import TestCase
from twisted.internet.protocol import Factory 
from twisted.python import log
from jasmin.protocols.smpp.configs import SMPPClientConfig
from jasmin.protocols.smpp.factory import SMPPClientFactory
from jasmin.vendor.smpp.pdu.pdu_types import CommandStatus
from jasmin.vendor.smpp.pdu.operations import *
from jasmin.vendor.smpp.pdu.error import *
from jasmin.routing.test.codepages import GSM0338, ISO8859_1

class SimulatorTestCase(TestCase):
    protocol = HappySMSC
    
    configArgs = {
        'id': 'test-id',
        'sessionInitTimerSecs': 0.1,
        'reconnectOnConnectionFailure': False,
        'reconnectOnConnectionLoss': False,
        #'port': 2775,
        'username': 'smppclient1',
        #'password': 'password',
    }
    

    smpp = None
    shortMsg = 'Short message.'
    longMsg = '0123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789.'
    concatenated2Msgs = '012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012END'
    source_addr = '99999999'
    destination_addr = '11111111'
    
    def setUp(self):
        self.factory = Factory()
        self.factory.protocol = self.protocol
        self.port = reactor.listenTCP(9001, self.factory)
        self.testPort = self.port.getHost().port
        
        args = self.configArgs.copy()
        args['host'] = self.configArgs.get('host', 'localhost')
        args['port'] = self.configArgs.get('port', self.testPort)
        args['username'] = self.configArgs.get('username', 'anyusername')
        args['password'] = self.configArgs.get('password', '')
        args['log_level'] = self.configArgs.get('log_level', logging.DEBUG)
        self.config = SMPPClientConfig(**args)
        
        self.opFactory = SMPPOperationFactory(self.config)
        
    def tearDown(self):
        self.port.stopListening()
        
    def composeMessage(self, characters, length):
        if length <= len(characters):
            return ''.join(random.sample(characters, length))
        else:
            s = ''
            while len(s) < length:
                s += ''.join(random.sample(characters, len(characters)))
            return s[:length]
    
    def verifyUnbindSuccess(self, smpp, sent, recv):
        self.assertTrue(isinstance(recv, UnbindResp))
        self.assertEquals(sent.requireAck(sent.seqNum), recv)
        
class BindTestCase(SimulatorTestCase):
    
    def verify(self, smpp, respPdu):
        self.assertEquals(1, smpp.PDUReceived.call_count)
        self.assertEquals(1, smpp.sendPDU.call_count)
        recv1 = smpp.PDUReceived.call_args_list[0][0][0]
        sent1 = smpp.sendPDU.call_args_list[0][0][0]
        self.assertTrue(isinstance(recv1, respPdu))
        self.assertTrue(isinstance(recv1, sent1.requireAck))
        self.verifyUnbindSuccess(smpp, sent1, recv1)
        
        return recv1, sent1

class BindTransmitterTestCase(BindTestCase):
    @defer.inlineCallbacks
    def test_bind_unbind_transmitter(self):
        self.config.bindOperation = 'transmitter'
        client = SMPPClientFactory(self.config)
        # Connect and bind
        yield client.connectAndBind()
        smpp = client.smpp
        
        smpp.PDUReceived = mock.Mock(wraps=smpp.PDUReceived)
        smpp.sendPDU = mock.Mock(wraps=smpp.sendPDU)

        # Session state check
        self.assertEqual(SMPPSessionStates.BOUND_TX, smpp.sessionState)

        # Unbind & Disconnect
        yield client.disconnect()
        
        ##############
        # Assertions :
        # Protocol verification
        recv1, sent1 = self.verify(smpp, UnbindResp)
        # Unbind successfull
        self.assertEqual(recv1.status, CommandStatus.ESME_ROK)
        # Session state check
        self.assertEqual(SMPPSessionStates.UNBOUND, smpp.sessionState)

class BindReceiverTestCase(BindTestCase):
    @defer.inlineCallbacks
    def test_bind_unbind_receiver(self):
        self.config.bindOperation = 'receiver'
        client = SMPPClientFactory(self.config)
        # Connect and bind
        yield client.connectAndBind()
        smpp = client.smpp
        
        smpp.PDUReceived = mock.Mock(wraps=smpp.PDUReceived)
        smpp.sendPDU = mock.Mock(wraps=smpp.sendPDU)

        # Session state check
        self.assertEqual(SMPPSessionStates.BOUND_RX, smpp.sessionState)

        # Unbind & Disconnect
        yield client.disconnect()
        
        ##############
        # Assertions :
        # Protocol verification
        recv1, sent1 = self.verify(smpp, UnbindResp)
        # Unbind successfull
        self.assertEqual(recv1.status, CommandStatus.ESME_ROK)
        # Session state check
        self.assertEqual(SMPPSessionStates.UNBOUND, smpp.sessionState)

class BindTransceiverTestCase(BindTestCase):
    @defer.inlineCallbacks
    def test_bind_unbind_transceiver(self):
        client = SMPPClientFactory(self.config)
        # Connect and bind
        yield client.connectAndBind()
        smpp = client.smpp
        
        smpp.PDUReceived = mock.Mock(wraps=smpp.PDUReceived)
        smpp.sendPDU = mock.Mock(wraps=smpp.sendPDU)

        # Session state check
        self.assertEqual(SMPPSessionStates.BOUND_TRX, smpp.sessionState)

        # Unbind & Disconnect
        yield client.disconnect()
        
        ##############
        # Assertions :
        # Protocol verification
        recv1, sent1 = self.verify(smpp, UnbindResp)
        # Unbind successfull
        self.assertEqual(recv1.status, CommandStatus.ESME_ROK)
        # Session state check
        self.assertEqual(SMPPSessionStates.UNBOUND, smpp.sessionState)

class NoBindResponseTestCase(SimulatorTestCase):
    protocol = BlackHoleSMSC

    @defer.inlineCallbacks
    def test_bind_to_blackhole(self):
        client = SMPPClientFactory(self.config)
        # Connect and bind
        yield self.assertFailure(client.connectAndBind(), SMPPSessionInitTimoutError)
        
class ReconnectionOnConnectionFailureTestCase(SimulatorTestCase):
    configArgs = {
        'id': 'test-id',
        'sessionInitTimerSecs': 0.1,
        'reconnectOnConnectionFailure': True,
        'reconnectOnConnectionFailureDelay': 2,
        'reconnectOnConnectionLoss': False,
        'reconnectOnConnectionLossDelay': 2,
        'port': 9001,
    }

    def setUp(self):
        self.factory = Factory()
        self.factory.protocol = self.protocol

        args = self.configArgs.copy()
        args['host'] = self.configArgs.get('host', 'localhost')
        args['port'] = self.configArgs.get('port', '2775')
        args['username'] = self.configArgs.get('username', 'anyusername')
        args['password'] = self.configArgs.get('password', '')
        args['log_level'] = self.configArgs.get('log_level', logging.DEBUG)
        
        self.config = SMPPClientConfig(**args)
        self.opFactory = SMPPOperationFactory(self.config)

        # Start listening 5 seconds later, the client shall successfully reconnect
        reactor.callLater(5, self.startListening, self.config.port)

    def startListening(self, port):
        self.port = reactor.listenTCP(port, self.factory)
    
    @defer.inlineCallbacks
    def test_reconnect_on_connection_failure(self):
        client = SMPPClientFactory(self.config)
        client.reConnect = mock.Mock(wraps=client.reConnect)
        # Connect and bind
        yield client.connectAndBind()
        smpp = client.smpp

        smpp.PDUReceived = mock.Mock(wraps=smpp.PDUReceived)
        smpp.sendPDU = mock.Mock(wraps=smpp.sendPDU)

        # Unbind & Disconnect
        yield client.disconnect()
        
        ##############
        # Assertions :
        # Protocol verification
        self.assertEquals(1, smpp.PDUReceived.call_count)
        self.assertEquals(1, smpp.sendPDU.call_count)
        self.assertNotEqual(0, client.reConnect.call_count)

class ReconnectionOnAuthenticationFailureTestCase(SimulatorTestCase):
    protocol = BindErrorSMSC
    
    configArgs = {
        'id': 'test-id',
        'sessionInitTimerSecs': 0.1,
        'reconnectOnConnectionFailure': True,
        'reconnectOnConnectionFailureDelay': 2,
        'reconnectOnConnectionLoss': True,
        'reconnectOnConnectionLossDelay': 2,
        'port': 9001,
    }

    @defer.inlineCallbacks
    def test_reconnect_on_authentication_failure(self):
        client = SMPPClientFactory(self.config)
        client.reConnect = mock.Mock(wraps=client.reConnect)
        # Connect and bind
        yield client.connectAndBind()

        smpp = client.smpp
        reactor.callLater(5, client.disconnectAndDontRetryToConnect)
        
        # Normally, the client shall not exit since it should retry to connect
        # Triggering the exitDeferred callback is a way to continue this test
        reactor.callLater(6, client.exitDeferred.callback, None)
        
        yield client.getExitDeferred()
        
        ##############
        # Assertions :
        # Protocol verification
        self.assertNotEqual(0, client.reConnect.call_count)

class ReconnectionOnConnectionLossTestCase(SimulatorTestCase):
    # @todo: Test reconnection trying if the server goes down after bind success
    pass

class SubmitSmTestCase(SimulatorTestCase):
    @defer.inlineCallbacks
    def test_submit_sm_transmitter_success(self):
        client = SMPPClientFactory(self.config)
        # Connect and bind
        yield client.connectAndBind()
        smpp = client.smpp

        smpp.PDUReceived = mock.Mock(wraps=smpp.PDUReceived)
        smpp.sendPDU = mock.Mock(wraps=smpp.sendPDU)
        
        # Send submit_sm
        SubmitSmPDU = self.opFactory.SubmitSM(
            source_addr=self.source_addr,
            destination_addr=self.destination_addr,
            short_message=self.shortMsg,
        )
        yield smpp.sendDataRequest(SubmitSmPDU)
        
        # Unbind & Disconnect
        yield smpp.unbindAndDisconnect()
        
        ##############
        # Assertions :
        self.assertEquals(2, smpp.PDUReceived.call_count)
        self.assertEquals(2, smpp.sendPDU.call_count)
        recv1 = smpp.PDUReceived.call_args_list[0][0][0]
        recv2 = smpp.PDUReceived.call_args_list[1][0][0]
        sent1 = smpp.sendPDU.call_args_list[0][0][0]
        sent2 = smpp.sendPDU.call_args_list[1][0][0]
        self.assertTrue(isinstance(recv1, SubmitSMResp))
        self.assertTrue(isinstance(recv1, sent1.requireAck))
        self.assertEqual(recv1.status, CommandStatus.ESME_ROK)
        self.verifyUnbindSuccess(smpp, sent2, recv2)

    def handleSubmitSmResp(self, txn):
        self.assertIsInstance(txn, SMPPOutboundTxnResult)
        self.assertEqual(txn.response.status, CommandStatus.ESME_ROK)
        self.assertEqual(txn.response.id, CommandId.submit_sm_resp)
        
    @defer.inlineCallbacks
    def test_submit_sm_resp_deferred(self):
        client = SMPPClientFactory(self.config)
        # Connect and bind
        yield client.connectAndBind()
        smpp = client.smpp

        smpp.PDUReceived = mock.Mock(wraps=smpp.PDUReceived)
        smpp.sendPDU = mock.Mock(wraps=smpp.sendPDU)
        
        # Send submit_sm
        SubmitSmPDU = self.opFactory.SubmitSM(
            source_addr=self.source_addr,
            destination_addr=self.destination_addr,
            short_message=self.shortMsg,
        )
        yield smpp.sendDataRequest(SubmitSmPDU).addCallback(self.handleSubmitSmResp)
        
        # Unbind & Disconnect
        yield smpp.unbindAndDisconnect()
        
        ##############
        # Assertions :
        self.assertEquals(2, smpp.PDUReceived.call_count)
        self.assertEquals(2, smpp.sendPDU.call_count)
        recv1 = smpp.PDUReceived.call_args_list[0][0][0]
        recv2 = smpp.PDUReceived.call_args_list[1][0][0]
        sent1 = smpp.sendPDU.call_args_list[0][0][0]
        sent2 = smpp.sendPDU.call_args_list[1][0][0]
        self.assertTrue(isinstance(recv1, SubmitSMResp))
        self.assertTrue(isinstance(recv1, sent1.requireAck))
        self.assertEqual(recv1.status, CommandStatus.ESME_ROK)
        self.verifyUnbindSuccess(smpp, sent2, recv2)

class LongSubmitSmTestCase(SimulatorTestCase):
    def prepareMocks(self, smpp):
        smpp.PDUReceived = mock.Mock(wraps=smpp.PDUReceived)
        smpp.sendPDU = mock.Mock(wraps=smpp.sendPDU)
        smpp.startLongSubmitSmTransaction = mock.Mock(wraps=smpp.startLongSubmitSmTransaction)
        smpp.endLongSubmitSmTransaction = mock.Mock(wraps=smpp.endLongSubmitSmTransaction)
        
class LongSubmitSmWithSARTestCase(LongSubmitSmTestCase):
    def runAsserts(self, smpp, content, nbrParts):
        self.assertEquals(nbrParts + 1, smpp.PDUReceived.call_count)
        self.assertEquals(nbrParts + 1, smpp.sendPDU.call_count)
        recv = {}
        sent = {}
        for i in range(nbrParts + 1):
            recv[i] = smpp.PDUReceived.call_args_list[i][0][0]
            sent[i] = smpp.sendPDU.call_args_list[i][0][0]
            
        # Assert for received PDUs
        for i in range(nbrParts):
            self.assertTrue(isinstance(recv[i], SubmitSMResp))
            self.assertTrue(isinstance(recv[i], sent[i].requireAck))
            self.assertEqual(recv[i].status, CommandStatus.ESME_ROK)
            
        # Assert SAR parameters
        sar_msg_ref_num = sent[0].params['sar_msg_ref_num']
        for i in range(nbrParts):
            self.assertEqual(nbrParts, sent[i].params['sar_total_segments'])
            self.assertEqual(i+1, sent[i].params['sar_segment_seqnum'])
            self.assertEqual(sar_msg_ref_num, sent[i].params['sar_msg_ref_num'])
            
        # Assert no LongSubmitSm transactions are still open
        self.assertEqual(0, len(smpp.longSubmitSmTxns))
        # Assert transactions are being started and ended
        self.assertEquals(1, smpp.startLongSubmitSmTransaction.call_count)
        self.assertEquals(nbrParts, smpp.endLongSubmitSmTransaction.call_count)
        
        # Assert the content after concatenation is the same as original
        concatenatedMsg = ''
        for i in range(nbrParts):
            concatenatedMsg += sent[i].params['short_message']
        self.assertEqual(concatenatedMsg, content)
        
        # Assert unbind were successfull
        self.verifyUnbindSuccess(smpp, sent[nbrParts], recv[nbrParts])
        
class LongSubmitSmWithUDHTestCase(LongSubmitSmTestCase):
    def runAsserts(self, smpp, content, nbrParts):
        self.assertEquals(nbrParts + 1, smpp.PDUReceived.call_count)
        self.assertEquals(nbrParts + 1, smpp.sendPDU.call_count)
        recv = {}
        sent = {}
        for i in range(nbrParts + 1):
            recv[i] = smpp.PDUReceived.call_args_list[i][0][0]
            sent[i] = smpp.sendPDU.call_args_list[i][0][0]
            
        # Assert for received PDUs
        for i in range(nbrParts):
            self.assertTrue(isinstance(recv[i], SubmitSMResp))
            self.assertTrue(isinstance(recv[i], sent[i].requireAck))
            self.assertEqual(recv[i].status, CommandStatus.ESME_ROK)
        
        # Assert UDH parameters
        msg_ref_num = sent[0].params['short_message'][3]
        for i in range(nbrParts):
            self.assertEqual(sent[i].params['short_message'][:3], '\x05\x00\x03')
            self.assertEqual(nbrParts, struct.unpack('!B', sent[i].params['short_message'][4])[0])
            self.assertEqual(i+1, struct.unpack('!B', sent[i].params['short_message'][5])[0])
            self.assertEqual(msg_ref_num, sent[i].params['short_message'][3])
            
        # Assert no LongSubmitSm transactions are still open
        self.assertEqual(0, len(smpp.longSubmitSmTxns))
        # Assert transactions are being started and ended
        self.assertEquals(1, smpp.startLongSubmitSmTransaction.call_count)
        self.assertEquals(nbrParts, smpp.endLongSubmitSmTransaction.call_count)
        
        # Assert the content after concatenation is the same as original
        concatenatedMsg = ''
        for i in range(nbrParts):
            # Remove UDH (6 bytes)
            concatenatedMsg += sent[i].params['short_message'][6:]
        self.assertEqual(concatenatedMsg, content)
        
        # Assert unbind were successfull
        self.verifyUnbindSuccess(smpp, sent[nbrParts], recv[nbrParts])
        
class LongSubmitSmUsingSARTestCase(LongSubmitSmWithSARTestCase):
    @defer.inlineCallbacks
    def test_long_submit_sm_7bit(self):
        client = SMPPClientFactory(self.config)
        # Connect and bind
        yield client.connectAndBind()
        smpp = client.smpp
        self.prepareMocks(smpp)

        # Send submit_sm
        content = self.composeMessage(GSM0338, 612) # 612 = 153 * 4
        SubmitSmPDU = self.opFactory.SubmitSM(
            source_addr=self.source_addr,
            destination_addr=self.destination_addr,
            short_message=content,
            data_coding = 0,
        )
        yield smpp.sendDataRequest(SubmitSmPDU)
        
        # Unbind & Disconnect
        yield smpp.unbindAndDisconnect()
        
        ##############
        # Assertions :
        self.runAsserts(smpp, content, len(content) / 153)

    @defer.inlineCallbacks
    def test_very_long_submit_sm_7bit(self):
        client = SMPPClientFactory(self.config)
        # Connect and bind
        yield client.connectAndBind()
        smpp = client.smpp
        self.prepareMocks(smpp)

        # Send submit_sm
        content = self.composeMessage(GSM0338, 1530) # 1530 = 153 * 10
        SubmitSmPDU = self.opFactory.SubmitSM(
            source_addr=self.source_addr,
            destination_addr=self.destination_addr,
            short_message=content,
            data_coding = 0,
        )
        yield smpp.sendDataRequest(SubmitSmPDU)
        
        # Unbind & Disconnect
        yield smpp.unbindAndDisconnect()
        
        ##############
        # Assertions :
        self.assertEquals(self.config.longContentMaxParts + 1, smpp.PDUReceived.call_count)
        self.assertEquals(self.config.longContentMaxParts + 1, smpp.sendPDU.call_count)

    @defer.inlineCallbacks
    def test_long_submit_sm_8bit(self):
        client = SMPPClientFactory(self.config)
        # Connect and bind
        yield client.connectAndBind()
        smpp = client.smpp
        self.prepareMocks(smpp)

        # Send submit_sm
        content = self.composeMessage(ISO8859_1, 670) # 670 = 134 * 5
        SubmitSmPDU = self.opFactory.SubmitSM(
            source_addr=self.source_addr,
            destination_addr=self.destination_addr,
            short_message=content,
            data_coding = 3,
        )
        yield smpp.sendDataRequest(SubmitSmPDU)
        
        # Unbind & Disconnect
        yield smpp.unbindAndDisconnect()
        
        ##############
        # Assertions :
        self.runAsserts(smpp, content, len(content) / 134)

    @defer.inlineCallbacks
    def test_very_long_submit_sm_8bit(self):
        client = SMPPClientFactory(self.config)
        # Connect and bind
        yield client.connectAndBind()
        smpp = client.smpp
        self.prepareMocks(smpp)

        # Send submit_sm
        content = self.composeMessage(ISO8859_1, 1340) # 1340 = 134 * 10
        SubmitSmPDU = self.opFactory.SubmitSM(
            source_addr=self.source_addr,
            destination_addr=self.destination_addr,
            short_message=content,
            data_coding = 3,
        )
        yield smpp.sendDataRequest(SubmitSmPDU)
        
        # Unbind & Disconnect
        yield smpp.unbindAndDisconnect()
        
        ##############
        # Assertions :
        self.assertEquals(self.config.longContentMaxParts + 1, smpp.PDUReceived.call_count)
        self.assertEquals(self.config.longContentMaxParts + 1, smpp.sendPDU.call_count)

    @defer.inlineCallbacks
    def test_long_submit_sm_16bit(self):
        client = SMPPClientFactory(self.config)
        # Connect and bind
        yield client.connectAndBind()
        smpp = client.smpp
        self.prepareMocks(smpp)

        # Send submit_sm
        UCS2 = {'\x0623', '\x0631', '\x0646', '\x0628'}
        content = self.composeMessage(UCS2, 335) # 335 = 67 * 5
        SubmitSmPDU = self.opFactory.SubmitSM(
            source_addr=self.source_addr,
            destination_addr=self.destination_addr,
            short_message=content,
            data_coding = 8,
        )
        yield smpp.sendDataRequest(SubmitSmPDU)
        
        # Unbind & Disconnect
        yield smpp.unbindAndDisconnect()
        
        ##############
        # Assertions :
        self.runAsserts(smpp, content, len(content) / 67)

    @defer.inlineCallbacks
    def test_very_long_submit_sm_16bit(self):
        client = SMPPClientFactory(self.config)
        # Connect and bind
        yield client.connectAndBind()
        smpp = client.smpp
        self.prepareMocks(smpp)

        # Send submit_sm
        UCS2 = {'\x0623', '\x0631', '\x0646', '\x0628'}
        content = self.composeMessage(UCS2, 3350) # 3350 = 67 * 10
        SubmitSmPDU = self.opFactory.SubmitSM(
            source_addr=self.source_addr,
            destination_addr=self.destination_addr,
            short_message=content,
            data_coding = 8,
        )
        yield smpp.sendDataRequest(SubmitSmPDU)
        
        # Unbind & Disconnect
        yield smpp.unbindAndDisconnect()
        
        ##############
        # Assertions :
        self.assertEquals(self.config.longContentMaxParts + 1, smpp.PDUReceived.call_count)
        self.assertEquals(self.config.longContentMaxParts + 1, smpp.sendPDU.call_count)

class LongSubmitSmUsingUDHTestCase(LongSubmitSmWithUDHTestCase):
    configArgs = {
        'id': 'test-id',
        'sessionInitTimerSecs': 0.1,
        'reconnectOnConnectionFailure': False,
        'reconnectOnConnectionLoss': False,
        'username': 'smppclient1',
        'longContentSplit': 'udh', # <--- This is where to set UDH splitting method
    }
    
    @defer.inlineCallbacks
    def test_long_submit_sm_7bit(self):
        client = SMPPClientFactory(self.config)
        # Connect and bind
        yield client.connectAndBind()
        smpp = client.smpp
        self.prepareMocks(smpp)

        # Send submit_sm
        content = self.composeMessage(GSM0338, 765) # 765 = 153 * 5
        SubmitSmPDU = self.opFactory.SubmitSM(
            source_addr=self.source_addr,
            destination_addr=self.destination_addr,
            short_message=content,
            data_coding = 0,
        )
        yield smpp.sendDataRequest(SubmitSmPDU)
        
        # Unbind & Disconnect
        yield smpp.unbindAndDisconnect()
        
        ##############
        # Assertions :
        self.runAsserts(smpp, content, len(content) / 153)

    @defer.inlineCallbacks
    def test_very_long_submit_sm_7bit(self):
        client = SMPPClientFactory(self.config)
        # Connect and bind
        yield client.connectAndBind()
        smpp = client.smpp
        self.prepareMocks(smpp)

        # Send submit_sm
        content = self.composeMessage(GSM0338, 1530) # 1530 = 153 * 10
        SubmitSmPDU = self.opFactory.SubmitSM(
            source_addr=self.source_addr,
            destination_addr=self.destination_addr,
            short_message=content,
            data_coding = 0,
        )
        yield smpp.sendDataRequest(SubmitSmPDU)
        
        # Unbind & Disconnect
        yield smpp.unbindAndDisconnect()
        
        ##############
        # Assertions :
        self.assertEquals(self.config.longContentMaxParts + 1, smpp.PDUReceived.call_count)
        self.assertEquals(self.config.longContentMaxParts + 1, smpp.sendPDU.call_count)

    @defer.inlineCallbacks
    def test_long_submit_sm_8bit(self):
        client = SMPPClientFactory(self.config)
        # Connect and bind
        yield client.connectAndBind()
        smpp = client.smpp
        self.prepareMocks(smpp)

        # Send submit_sm
        content = self.composeMessage(ISO8859_1, 402) # 402 = 134 * 3
        SubmitSmPDU = self.opFactory.SubmitSM(
            source_addr=self.source_addr,
            destination_addr=self.destination_addr,
            short_message=content,
            data_coding = 3,
        )
        yield smpp.sendDataRequest(SubmitSmPDU)
        
        # Unbind & Disconnect
        yield smpp.unbindAndDisconnect()
        
        ##############
        # Assertions :
        self.runAsserts(smpp, content, len(content) / 134)

    @defer.inlineCallbacks
    def test_very_long_submit_sm_8bit(self):
        client = SMPPClientFactory(self.config)
        # Connect and bind
        yield client.connectAndBind()
        smpp = client.smpp
        self.prepareMocks(smpp)

        # Send submit_sm
        content = self.composeMessage(ISO8859_1, 1340) # 1340 = 134 * 10
        SubmitSmPDU = self.opFactory.SubmitSM(
            source_addr=self.source_addr,
            destination_addr=self.destination_addr,
            short_message=content,
            data_coding = 3,
        )
        yield smpp.sendDataRequest(SubmitSmPDU)
        
        # Unbind & Disconnect
        yield smpp.unbindAndDisconnect()
        
        ##############
        # Assertions :
        self.assertEquals(self.config.longContentMaxParts + 1, smpp.PDUReceived.call_count)
        self.assertEquals(self.config.longContentMaxParts + 1, smpp.sendPDU.call_count)

    @defer.inlineCallbacks
    def test_long_submit_sm_16bit(self):
        client = SMPPClientFactory(self.config)
        # Connect and bind
        yield client.connectAndBind()
        smpp = client.smpp
        self.prepareMocks(smpp)

        # Send submit_sm
        UCS2 = {'\x0623', '\x0631', '\x0646', '\x0628'}
        content = self.composeMessage(UCS2, 335) # 335 = 67 * 5
        SubmitSmPDU = self.opFactory.SubmitSM(
            source_addr=self.source_addr,
            destination_addr=self.destination_addr,
            short_message=content,
            data_coding = 8,
        )
        yield smpp.sendDataRequest(SubmitSmPDU)
        
        # Unbind & Disconnect
        yield smpp.unbindAndDisconnect()
        
        ##############
        # Assertions :
        self.runAsserts(smpp, content, len(content) / 67)

    @defer.inlineCallbacks
    def test_very_long_submit_sm_16bit(self):
        client = SMPPClientFactory(self.config)
        # Connect and bind
        yield client.connectAndBind()
        smpp = client.smpp
        self.prepareMocks(smpp)

        # Send submit_sm
        UCS2 = {'\x0623', '\x0631', '\x0646', '\x0628'}
        content = self.composeMessage(UCS2, 536) # 536 = 67 * 8
        SubmitSmPDU = self.opFactory.SubmitSM(
            source_addr=self.source_addr,
            destination_addr=self.destination_addr,
            short_message=content,
            data_coding = 8,
        )
        yield smpp.sendDataRequest(SubmitSmPDU)
        
        # Unbind & Disconnect
        yield smpp.unbindAndDisconnect()
        
        ##############
        # Assertions :
        self.assertEquals(self.config.longContentMaxParts + 1, smpp.PDUReceived.call_count)
        self.assertEquals(self.config.longContentMaxParts + 1, smpp.sendPDU.call_count)

class VeryLongSubmitSmUsingSARTestCase(LongSubmitSmWithSARTestCase):
    configArgs = {
        'id': 'test-id',
        'sessionInitTimerSecs': 0.1,
        'reconnectOnConnectionFailure': False,
        'reconnectOnConnectionLoss': False,
        'username': 'smppclient1',
        'longContentMaxParts': 8,
    }

    @defer.inlineCallbacks
    def test_long_submit_sm_7bit(self):
        client = SMPPClientFactory(self.config)
        # Connect and bind
        yield client.connectAndBind()
        smpp = client.smpp
        self.prepareMocks(smpp)

        # Send submit_sm
        content = self.composeMessage(GSM0338, 1224) # 1224 = 153 * 8
        SubmitSmPDU = self.opFactory.SubmitSM(
            source_addr=self.source_addr,
            destination_addr=self.destination_addr,
            short_message=content,
            data_coding = 0,
        )
        yield smpp.sendDataRequest(SubmitSmPDU)
        
        # Unbind & Disconnect
        yield smpp.unbindAndDisconnect()
        
        ##############
        # Assertions :
        self.runAsserts(smpp, content, len(content) / 153)

    @defer.inlineCallbacks
    def test_very_long_submit_sm_7bit(self):
        client = SMPPClientFactory(self.config)
        # Connect and bind
        yield client.connectAndBind()
        smpp = client.smpp
        self.prepareMocks(smpp)

        # Send submit_sm
        content = self.composeMessage(GSM0338, 1530) # 1530 = 153 * 10
        SubmitSmPDU = self.opFactory.SubmitSM(
            source_addr=self.source_addr,
            destination_addr=self.destination_addr,
            short_message=content,
            data_coding = 0,
        )
        yield smpp.sendDataRequest(SubmitSmPDU)
        
        # Unbind & Disconnect
        yield smpp.unbindAndDisconnect()
        
        ##############
        # Assertions :
        self.assertEquals(self.config.longContentMaxParts + 1, smpp.PDUReceived.call_count)
        self.assertEquals(self.config.longContentMaxParts + 1, smpp.sendPDU.call_count)

    @defer.inlineCallbacks
    def test_long_submit_sm_8bit(self):
        client = SMPPClientFactory(self.config)
        # Connect and bind
        yield client.connectAndBind()
        smpp = client.smpp
        self.prepareMocks(smpp)

        # Send submit_sm
        content = self.composeMessage(ISO8859_1, 1072) # 1072 = 134 * 8
        SubmitSmPDU = self.opFactory.SubmitSM(
            source_addr=self.source_addr,
            destination_addr=self.destination_addr,
            short_message=content,
            data_coding = 3,
        )
        yield smpp.sendDataRequest(SubmitSmPDU)
        
        # Unbind & Disconnect
        yield smpp.unbindAndDisconnect()
        
        ##############
        # Assertions :
        self.runAsserts(smpp, content, len(content) / 134)

    @defer.inlineCallbacks
    def test_very_long_submit_sm_8bit(self):
        client = SMPPClientFactory(self.config)
        # Connect and bind
        yield client.connectAndBind()
        smpp = client.smpp
        self.prepareMocks(smpp)

        # Send submit_sm
        content = self.composeMessage(ISO8859_1, 1340) # 1340 = 134 * 10
        SubmitSmPDU = self.opFactory.SubmitSM(
            source_addr=self.source_addr,
            destination_addr=self.destination_addr,
            short_message=content,
            data_coding = 3,
        )
        yield smpp.sendDataRequest(SubmitSmPDU)
        
        # Unbind & Disconnect
        yield smpp.unbindAndDisconnect()
        
        ##############
        # Assertions :
        self.assertEquals(self.config.longContentMaxParts + 1, smpp.PDUReceived.call_count)
        self.assertEquals(self.config.longContentMaxParts + 1, smpp.sendPDU.call_count)

    @defer.inlineCallbacks
    def test_long_submit_sm_16bit(self):
        client = SMPPClientFactory(self.config)
        # Connect and bind
        yield client.connectAndBind()
        smpp = client.smpp
        self.prepareMocks(smpp)

        # Send submit_sm
        UCS2 = {'\x0623', '\x0631', '\x0646', '\x0628'}
        content = self.composeMessage(UCS2, 536) # 536 = 67 * 8
        SubmitSmPDU = self.opFactory.SubmitSM(
            source_addr=self.source_addr,
            destination_addr=self.destination_addr,
            short_message=content,
            data_coding = 8,
        )
        yield smpp.sendDataRequest(SubmitSmPDU)
        
        # Unbind & Disconnect
        yield smpp.unbindAndDisconnect()
        
        ##############
        # Assertions :
        self.runAsserts(smpp, content, len(content) / 67)

    @defer.inlineCallbacks
    def test_very_long_submit_sm_16bit(self):
        client = SMPPClientFactory(self.config)
        # Connect and bind
        yield client.connectAndBind()
        smpp = client.smpp
        self.prepareMocks(smpp)

        # Send submit_sm
        UCS2 = {'\x0623', '\x0631', '\x0646', '\x0628'}
        content = self.composeMessage(UCS2, 3350) # 3350 = 67 * 10
        SubmitSmPDU = self.opFactory.SubmitSM(
            source_addr=self.source_addr,
            destination_addr=self.destination_addr,
            short_message=content,
            data_coding = 8,
        )
        yield smpp.sendDataRequest(SubmitSmPDU)
        
        # Unbind & Disconnect
        yield smpp.unbindAndDisconnect()
        
        ##############
        # Assertions :
        self.assertEquals(self.config.longContentMaxParts + 1, smpp.PDUReceived.call_count)
        self.assertEquals(self.config.longContentMaxParts + 1, smpp.sendPDU.call_count)

class VeryLongSubmitSmUsingUDHTestCase(LongSubmitSmWithUDHTestCase):
    configArgs = {
        'id': 'test-id',
        'sessionInitTimerSecs': 0.1,
        'reconnectOnConnectionFailure': False,
        'reconnectOnConnectionLoss': False,
        'username': 'smppclient1',
        'longContentMaxParts': 8,
        'longContentSplit': 'udh',
    }

    @defer.inlineCallbacks
    def test_long_submit_sm_7bit(self):
        client = SMPPClientFactory(self.config)
        # Connect and bind
        yield client.connectAndBind()
        smpp = client.smpp
        self.prepareMocks(smpp)

        # Send submit_sm
        content = self.composeMessage(GSM0338, 1224) # 1224 = 153 * 8
        SubmitSmPDU = self.opFactory.SubmitSM(
            source_addr=self.source_addr,
            destination_addr=self.destination_addr,
            short_message=content,
            data_coding = 0,
        )
        yield smpp.sendDataRequest(SubmitSmPDU)
        
        # Unbind & Disconnect
        yield smpp.unbindAndDisconnect()
        
        ##############
        # Assertions :
        self.runAsserts(smpp, content, len(content) / 153)

    @defer.inlineCallbacks
    def test_very_long_submit_sm_7bit(self):
        client = SMPPClientFactory(self.config)
        # Connect and bind
        yield client.connectAndBind()
        smpp = client.smpp
        self.prepareMocks(smpp)

        # Send submit_sm
        content = self.composeMessage(GSM0338, 1530) # 1530 = 153 * 10
        SubmitSmPDU = self.opFactory.SubmitSM(
            source_addr=self.source_addr,
            destination_addr=self.destination_addr,
            short_message=content,
            data_coding = 0,
        )
        yield smpp.sendDataRequest(SubmitSmPDU)
        
        # Unbind & Disconnect
        yield smpp.unbindAndDisconnect()
        
        ##############
        # Assertions :
        self.assertEquals(self.config.longContentMaxParts + 1, smpp.PDUReceived.call_count)
        self.assertEquals(self.config.longContentMaxParts + 1, smpp.sendPDU.call_count)

    @defer.inlineCallbacks
    def test_long_submit_sm_8bit(self):
        client = SMPPClientFactory(self.config)
        # Connect and bind
        yield client.connectAndBind()
        smpp = client.smpp
        self.prepareMocks(smpp)

        # Send submit_sm
        content = self.composeMessage(ISO8859_1, 1072) # 1072 = 134 * 8
        SubmitSmPDU = self.opFactory.SubmitSM(
            source_addr=self.source_addr,
            destination_addr=self.destination_addr,
            short_message=content,
            data_coding = 3,
        )
        yield smpp.sendDataRequest(SubmitSmPDU)
        
        # Unbind & Disconnect
        yield smpp.unbindAndDisconnect()
        
        ##############
        # Assertions :
        self.runAsserts(smpp, content, len(content) / 134)

    @defer.inlineCallbacks
    def test_very_long_submit_sm_8bit(self):
        client = SMPPClientFactory(self.config)
        # Connect and bind
        yield client.connectAndBind()
        smpp = client.smpp
        self.prepareMocks(smpp)

        # Send submit_sm
        content = self.composeMessage(ISO8859_1, 1340) # 1340 = 134 * 10
        SubmitSmPDU = self.opFactory.SubmitSM(
            source_addr=self.source_addr,
            destination_addr=self.destination_addr,
            short_message=content,
            data_coding = 3,
        )
        yield smpp.sendDataRequest(SubmitSmPDU)
        
        # Unbind & Disconnect
        yield smpp.unbindAndDisconnect()
        
        ##############
        # Assertions :
        self.assertEquals(self.config.longContentMaxParts + 1, smpp.PDUReceived.call_count)
        self.assertEquals(self.config.longContentMaxParts + 1, smpp.sendPDU.call_count)

    @defer.inlineCallbacks
    def test_long_submit_sm_16bit(self):
        client = SMPPClientFactory(self.config)
        # Connect and bind
        yield client.connectAndBind()
        smpp = client.smpp
        self.prepareMocks(smpp)

        # Send submit_sm
        UCS2 = {'\x0623', '\x0631', '\x0646', '\x0628'}
        content = self.composeMessage(UCS2, 536) # 536 = 67 * 8
        SubmitSmPDU = self.opFactory.SubmitSM(
            source_addr=self.source_addr,
            destination_addr=self.destination_addr,
            short_message=content,
            data_coding = 8,
        )
        yield smpp.sendDataRequest(SubmitSmPDU)
        
        # Unbind & Disconnect
        yield smpp.unbindAndDisconnect()
        
        ##############
        # Assertions :
        self.runAsserts(smpp, content, len(content) / 67)

    @defer.inlineCallbacks
    def test_very_long_submit_sm_16bit(self):
        client = SMPPClientFactory(self.config)
        # Connect and bind
        yield client.connectAndBind()
        smpp = client.smpp
        self.prepareMocks(smpp)

        # Send submit_sm
        UCS2 = {'\x0623', '\x0631', '\x0646', '\x0628'}
        content = self.composeMessage(UCS2, 3350) # 3350 = 67 * 10
        SubmitSmPDU = self.opFactory.SubmitSM(
            source_addr=self.source_addr,
            destination_addr=self.destination_addr,
            short_message=content,
            data_coding = 8,
        )
        yield smpp.sendDataRequest(SubmitSmPDU)
        
        # Unbind & Disconnect
        yield smpp.unbindAndDisconnect()
        
        ##############
        # Assertions :
        self.assertEquals(self.config.longContentMaxParts + 1, smpp.PDUReceived.call_count)
        self.assertEquals(self.config.longContentMaxParts + 1, smpp.sendPDU.call_count)

class LongSubmitSmErrorOnSubmitSmTestCase(SimulatorTestCase):
    protocol = ErrorOnSubmitSMSC
    
    @defer.inlineCallbacks
    def test_long_submit_sm_gsm0338(self):
        client = SMPPClientFactory(self.config)
        # Connect and bind
        yield client.connectAndBind()
        smpp = client.smpp

        smpp.PDUReceived = mock.Mock(wraps=smpp.PDUReceived)
        smpp.sendPDU = mock.Mock(wraps=smpp.sendPDU)
        smpp.startLongSubmitSmTransaction = mock.Mock(wraps=smpp.startLongSubmitSmTransaction)
        
        # Send submit_sm
        SubmitSmPDU = self.opFactory.SubmitSM(
            source_addr=self.source_addr,
            destination_addr=self.destination_addr,
            short_message=self.concatenated2Msgs,
        )
        yield smpp.sendDataRequest(SubmitSmPDU)
        
        # Unbind & Disconnect
        yield smpp.unbindAndDisconnect()
        
        ##############
        # Assertions :
        self.assertEquals(3, smpp.PDUReceived.call_count)
        self.assertEquals(3, smpp.sendPDU.call_count)
        recv1 = smpp.PDUReceived.call_args_list[0][0][0]
        recv2 = smpp.PDUReceived.call_args_list[1][0][0]
        recv3 = smpp.PDUReceived.call_args_list[2][0][0]
        sent1 = smpp.sendPDU.call_args_list[0][0][0]
        sent2 = smpp.sendPDU.call_args_list[1][0][0]
        sent3 = smpp.sendPDU.call_args_list[2][0][0]
        self.assertTrue(isinstance(recv1, SubmitSMResp))
        self.assertTrue(isinstance(recv1, sent1.requireAck))
        self.assertEqual(recv1.status, CommandStatus.ESME_RINVESMCLASS)
        self.assertEqual(2, sent1.params['sar_total_segments'])
        self.assertEqual(2, sent2.params['sar_total_segments'])
        self.assertEqual(1, sent1.params['sar_segment_seqnum'])
        self.assertEqual(2, sent2.params['sar_segment_seqnum'])
        self.assertEqual(sent1.params['sar_msg_ref_num'], sent2.params['sar_msg_ref_num'])
        self.assertTrue(isinstance(recv2, SubmitSMResp))
        self.assertTrue(isinstance(recv2, sent1.requireAck))
        self.assertEqual(recv2.status, CommandStatus.ESME_RINVESMCLASS)
        self.assertEqual(0, len(smpp.longSubmitSmTxns))
        self.assertEquals(1, smpp.startLongSubmitSmTransaction.call_count)
        self.verifyUnbindSuccess(smpp, sent3, recv3)
        
class LongSubmitSmGenerickNackTestCase(SimulatorTestCase):
    protocol = GenericNackNoSeqNumOnSubmitSMSC
    
    @defer.inlineCallbacks
    def test_long_submit_sm_gsm0338(self):
        client = SMPPClientFactory(self.config)
        # Connect and bind
        yield client.connectAndBind()
        smpp = client.smpp

        smpp.PDUReceived = mock.Mock(wraps=smpp.PDUReceived)
        smpp.sendPDU = mock.Mock(wraps=smpp.sendPDU)
        smpp.startLongSubmitSmTransaction = mock.Mock(wraps=smpp.startLongSubmitSmTransaction)
        smpp.cancelLongSubmitSmTransactions = mock.Mock(wraps=smpp.cancelLongSubmitSmTransactions)
        
        # Send submit_sm
        SubmitSmPDU = self.opFactory.SubmitSM(
            source_addr=self.source_addr,
            destination_addr=self.destination_addr,
            short_message=self.concatenated2Msgs,
        )
        yield self.assertFailure(smpp.sendDataRequest(SubmitSmPDU), SMPPClientConnectionCorruptedError)
        
        # Unbind & Disconnect
        yield smpp.unbindAndDisconnect()
        
        ##############
        # Assertions :
        self.assertEquals(1, smpp.PDUReceived.call_count)
        self.assertEquals(2, smpp.sendPDU.call_count)
        recv1 = smpp.PDUReceived.call_args_list[0][0][0]
        sent1 = smpp.sendPDU.call_args_list[0][0][0]
        self.assertEqual(recv1.status, CommandStatus.ESME_RINVCMDLEN)
        self.assertEqual(2, sent1.params['sar_total_segments'])
        self.assertEqual(1, sent1.params['sar_segment_seqnum'])
        self.assertEqual(0, len(smpp.longSubmitSmTxns))
        self.assertEquals(1, smpp.startLongSubmitSmTransaction.call_count)
        self.assertEquals(1, smpp.cancelLongSubmitSmTransactions.call_count)

class SubmitSmIncorrectlyBoundTestCase(SimulatorTestCase):
    protocol = NoSubmitSmWhenReceiverIsBoundSMSC
    
    @defer.inlineCallbacks
    def test_submit_sm_receiver_failure(self):
        client = SMPPClientFactory(self.config)
        # Connect and bind
        yield client.connectAndBind()
        smpp = client.smpp

        smpp.PDUReceived = mock.Mock(wraps=smpp.PDUReceived)
        smpp.sendPDU = mock.Mock(wraps=smpp.sendPDU)
        
        # Send submit_sm
        SubmitSmPDU = self.opFactory.SubmitSM(
            source_addr=self.source_addr,
            destination_addr=self.destination_addr,
            short_message=self.shortMsg,
        )
        try:
            yield smpp.sendDataRequest(SubmitSmPDU)
        except SMPPTransactionError:
            pass
        else:
            self.assertTrue(False, "SMPPTransactionError not raised")
    test_submit_sm_receiver_failure.skip = 'SMPPClientProtocol.endOutboundTransaction is changed to handle all errors in callback, no more errors will be raised'
            
class DeliverSmAckTestCase(SimulatorTestCase):
    @defer.inlineCallbacks
    def test_deliver_sm_ack(self):
        client = SMPPClientFactory(self.config)
        # Connect and bind
        yield client.connectAndBind()
        smpp = client.smpp

        smpp.PDUReceived = mock.Mock(wraps=smpp.PDUReceived)
        smpp.sendPDU = mock.Mock(wraps=smpp.sendPDU)
        
        # Send submit_sm
        SubmitSmPDU = self.opFactory.SubmitSM(
            source_addr=self.source_addr,
            destination_addr=self.destination_addr,
            short_message=self.shortMsg,
        )
        yield smpp.sendDataRequest(SubmitSmPDU)
        
        # Unbind & Disconnect
        yield smpp.unbindAndDisconnect()
        
        ##############
        # Assertions :
        self.assertEquals(2, smpp.PDUReceived.call_count)
        self.assertEquals(2, smpp.sendPDU.call_count)
        recv1 = smpp.PDUReceived.call_args_list[0][0][0]
        recv2 = smpp.PDUReceived.call_args_list[1][0][0]
        sent1 = smpp.sendPDU.call_args_list[0][0][0]
        sent2 = smpp.sendPDU.call_args_list[1][0][0]
        self.assertTrue(isinstance(recv1, SubmitSMResp))
        self.assertTrue(isinstance(recv1, sent1.requireAck))
        self.assertEqual(recv1.status, CommandStatus.ESME_ROK)
        self.verifyUnbindSuccess(smpp, sent2, recv2)

if __name__ == '__main__':
    observer = log.PythonLoggingObserver()
    observer.start()
    logging.basicConfig(level=logging.DEBUG)
    
    import sys
    from twisted.scripts import trial
    sys.argv.extend([sys.argv[0]])
    trial.run()
