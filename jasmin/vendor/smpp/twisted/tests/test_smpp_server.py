import unittest
import logging
from twisted.web import resource
from twisted.internet import defer, reactor, task
from twisted.application import internet
from twisted.trial.unittest import TestCase
from twisted.cred.portal import IRealm    
from twisted.cred.checkers import InMemoryUsernamePasswordDatabaseDontUse
from twisted.cred.portal import Portal
from twisted.test import proto_helpers
from zope.interface import implements      

from jasmin.vendor.smpp.twisted.config import SMPPServerConfig, SMPPClientConfig
from jasmin.vendor.smpp.twisted.server import SMPPServerFactory, SMPPBindManager
from jasmin.vendor.smpp.twisted.protocol import SMPPSessionStates, DataHandlerResponse
from jasmin.vendor.smpp.twisted.client import SMPPClientTransceiver

from jasmin.vendor.smpp.pdu import pdu_types, operations, pdu_encoding

import mock

logging.basicConfig(level = logging.DEBUG)

def _makeMockServerConnection(key, bind_type):
    mk_server_cnxn = mock.Mock('mock svr cnxn')
    mk_server_cnxn.system_id = key
    mk_server_cnxn.bind_type = bind_type
    return mk_server_cnxn
    
class SMPPServerFactoryTests(unittest.TestCase):

    def setUp(self):
        self.config = mock.Mock('mock smpp config')
        self.config.systems = {'lala': {'max_bindings': 2}}
    
    def tearDown(self):
        pass
        
    @unittest.skip('''Jasmin update: All vendor tests shall be skipped)''')
    def test_canOpenNewConnection_transceiver(self):
        server = SMPPServerFactory(self.config, None)
        can_bind = server.canOpenNewConnection('lala', pdu_types.CommandId.bind_transceiver)
        self.assertTrue(can_bind, 'Should be able to bind trx as none bound yet')
        can_bind = server.canOpenNewConnection('lala', pdu_types.CommandId.bind_transmitter)
        self.assertTrue(can_bind, 'Should be able to bind tx as none bound yet')
        can_bind = server.canOpenNewConnection('lala', pdu_types.CommandId.bind_receiver)
        self.assertTrue(can_bind, 'Should be able to bind rx as none bound yet')
        
        mk_server_cnxn = _makeMockServerConnection('lala', pdu_types.CommandId.bind_transceiver)
        server.addBoundConnection(mk_server_cnxn)
        can_bind = server.canOpenNewConnection('lala', pdu_types.CommandId.bind_transceiver)
        self.assertTrue(can_bind, 'Should still be able to bind as only one trx bound')
        can_bind = server.canOpenNewConnection('lala', pdu_types.CommandId.bind_transmitter)
        self.assertTrue(can_bind, 'Should still be able to bind as only one trx bound')
        can_bind = server.canOpenNewConnection('lala', pdu_types.CommandId.bind_receiver)
        self.assertTrue(can_bind, 'Should still be able to bind as only one trx bound')
        
        mk_server_cnxn = _makeMockServerConnection('lala', pdu_types.CommandId.bind_transceiver)
        server.addBoundConnection(mk_server_cnxn)
        can_bind = server.canOpenNewConnection('lala', pdu_types.CommandId.bind_transceiver)
        self.assertFalse(can_bind, 'Should not be able to bind as two trx already bound')
        can_bind = server.canOpenNewConnection('lala', pdu_types.CommandId.bind_transmitter)
        self.assertFalse(can_bind, 'Should not be able to bind as two trx already bound')
        can_bind = server.canOpenNewConnection('lala', pdu_types.CommandId.bind_receiver)
        self.assertFalse(can_bind, 'Should not be able to bind as two trx already bound')
        
    @unittest.skip('''Jasmin update: All vendor tests shall be skipped)''')
    def test_canOpenNewConnection_transmitter(self):
        server = SMPPServerFactory(self.config, None)
        can_bind = server.canOpenNewConnection('lala', pdu_types.CommandId.bind_transceiver)
        self.assertTrue(can_bind, 'Should be able to bind trx as none bound yet')
        can_bind = server.canOpenNewConnection('lala', pdu_types.CommandId.bind_transmitter)
        self.assertTrue(can_bind, 'Should be able to bind tx as none bound yet')
        can_bind = server.canOpenNewConnection('lala', pdu_types.CommandId.bind_receiver)
        self.assertTrue(can_bind, 'Should be able to bind rx as none bound yet')
        
        mk_server_cnxn = _makeMockServerConnection('lala', pdu_types.CommandId.bind_transmitter)
        server.addBoundConnection(mk_server_cnxn)
        can_bind = server.canOpenNewConnection('lala', pdu_types.CommandId.bind_transceiver)
        self.assertTrue(can_bind, 'Should still be able to bind as only one tx bound')
        can_bind = server.canOpenNewConnection('lala', pdu_types.CommandId.bind_transmitter)
        self.assertTrue(can_bind, 'Should still be able to bind as only one tx bound')
        can_bind = server.canOpenNewConnection('lala', pdu_types.CommandId.bind_receiver)
        self.assertTrue(can_bind, 'Should still be able to bind only bindings are tx')
        
        mk_server_cnxn = _makeMockServerConnection('lala', pdu_types.CommandId.bind_transmitter)
        server.addBoundConnection(mk_server_cnxn)
        can_bind = server.canOpenNewConnection('lala', pdu_types.CommandId.bind_transceiver)
        self.assertFalse(can_bind, 'Should not be able to bind as two tx already bound')
        can_bind = server.canOpenNewConnection('lala', pdu_types.CommandId.bind_transmitter)
        self.assertFalse(can_bind, 'Should not be able to bind as two tx already bound')
        # NOTE this one different
        can_bind = server.canOpenNewConnection('lala', pdu_types.CommandId.bind_receiver)
        self.assertTrue(can_bind, 'Should still be able to bind only bindings are tx')
        
    @unittest.skip('''Jasmin update: All vendor tests shall be skipped)''')
    def test_canOpenNewConnection_receiver(self):
        server = SMPPServerFactory(self.config, None)
        can_bind = server.canOpenNewConnection('lala', pdu_types.CommandId.bind_transceiver)
        self.assertTrue(can_bind, 'Should be able to bind trx as none bound yet')
        can_bind = server.canOpenNewConnection('lala', pdu_types.CommandId.bind_transmitter)
        self.assertTrue(can_bind, 'Should be able to bind tx as none bound yet')
        can_bind = server.canOpenNewConnection('lala', pdu_types.CommandId.bind_receiver)
        self.assertTrue(can_bind, 'Should be able to bind rx as none bound yet')
        
        mk_server_cnxn = _makeMockServerConnection('lala', pdu_types.CommandId.bind_receiver)
        server.addBoundConnection(mk_server_cnxn)
        can_bind = server.canOpenNewConnection('lala', pdu_types.CommandId.bind_transceiver)
        self.assertTrue(can_bind, 'Should still be able to bind as only one rx bound')
        can_bind = server.canOpenNewConnection('lala', pdu_types.CommandId.bind_transmitter)
        self.assertTrue(can_bind, 'Should still be able to bind only bindings are rx')
        can_bind = server.canOpenNewConnection('lala', pdu_types.CommandId.bind_receiver)
        self.assertTrue(can_bind, 'Should still be able to bind as only one rx bound')
        
        mk_server_cnxn = _makeMockServerConnection('lala', pdu_types.CommandId.bind_receiver)
        server.addBoundConnection(mk_server_cnxn)
        can_bind = server.canOpenNewConnection('lala', pdu_types.CommandId.bind_transceiver)
        self.assertFalse(can_bind, 'Should not be able to bind as two rx already bound')
        # NOTE this one different
        can_bind = server.canOpenNewConnection('lala', pdu_types.CommandId.bind_transmitter)
        self.assertTrue(can_bind, 'Should still be able to bind only bindings are rx')
        can_bind = server.canOpenNewConnection('lala', pdu_types.CommandId.bind_receiver)
        self.assertFalse(can_bind, 'Should not be able to bind as two rx already bound')
        
    @unittest.skip('''Jasmin update: All vendor tests shall be skipped)''')
    def test_canOpenNewConnection_multitypes(self):
        server = SMPPServerFactory(self.config, None)
        
        can_bind = server.canOpenNewConnection('lala', pdu_types.CommandId.bind_transceiver)
        self.assertTrue(can_bind, 'Should be able to bind trx as none bound yet')
        can_bind = server.canOpenNewConnection('lala', pdu_types.CommandId.bind_transmitter)
        self.assertTrue(can_bind, 'Should be able to bind tx as none bound yet')
        can_bind = server.canOpenNewConnection('lala', pdu_types.CommandId.bind_receiver)
        self.assertTrue(can_bind, 'Should be able to bind rx as none bound yet')
        
        mk_server_cnxn = _makeMockServerConnection('lala', pdu_types.CommandId.bind_transceiver)
        server.addBoundConnection(mk_server_cnxn)
        can_bind = server.canOpenNewConnection('lala', pdu_types.CommandId.bind_transceiver)
        self.assertTrue(can_bind, 'Should still be able to bind as only one trx bound')
        can_bind = server.canOpenNewConnection('lala', pdu_types.CommandId.bind_transmitter)
        self.assertTrue(can_bind, 'Should still be able to bind as only one trx bound')
        can_bind = server.canOpenNewConnection('lala', pdu_types.CommandId.bind_receiver)
        self.assertTrue(can_bind, 'Should still be able to bind as only one trx bound')
        
        mk_server_cnxn = _makeMockServerConnection('lala', pdu_types.CommandId.bind_transmitter)
        server.addBoundConnection(mk_server_cnxn)
        can_bind = server.canOpenNewConnection('lala', pdu_types.CommandId.bind_transceiver)
        self.assertFalse(can_bind, 'Should not be able to bind as one trx and one tx already bound')
        can_bind = server.canOpenNewConnection('lala', pdu_types.CommandId.bind_transmitter)
        self.assertFalse(can_bind, 'Should not be able to bind as one trx and one tx already bound')
        can_bind = server.canOpenNewConnection('lala', pdu_types.CommandId.bind_receiver)
        self.assertTrue(can_bind, 'Should still be able to bind as only one trx and one tx bound')
        
        mk_server_cnxn = _makeMockServerConnection('lala', pdu_types.CommandId.bind_receiver)
        server.addBoundConnection(mk_server_cnxn)
        can_bind = server.canOpenNewConnection('lala', pdu_types.CommandId.bind_transceiver)
        self.assertFalse(can_bind, 'Should not be able to bind as one trx, one tx, and one rx already bound')
        can_bind = server.canOpenNewConnection('lala', pdu_types.CommandId.bind_transmitter)
        self.assertFalse(can_bind, 'Should not be able to bind as one trx, one tx, and one rx already bound')
        can_bind = server.canOpenNewConnection('lala', pdu_types.CommandId.bind_receiver)
        self.assertFalse(can_bind, 'Should not be able to bind as one trx, one tx, and one rx already bound')
        

class SMPPBindManagerTests(unittest.TestCase):
    
    @unittest.skip('''Jasmin update: All vendor tests shall be skipped)''')
    def test_getNextBindingForDelivery(self):
        bm = SMPPBindManager('blah')
        
        # add an initial rx
        mk_server_cnxn_rx1 = _makeMockServerConnection('blah', pdu_types.CommandId.bind_receiver)
        bm.addBinding(mk_server_cnxn_rx1)
        
        # Get the only rx
        deliverer = bm.getNextBindingForDelivery()
        self.assertEqual(mk_server_cnxn_rx1, deliverer)
        
        # Get the only rx again
        deliverer = bm.getNextBindingForDelivery()
        self.assertEqual(mk_server_cnxn_rx1, deliverer)
        
        # Add a new rx
        mk_server_cnxn_rx2 = _makeMockServerConnection('blah', pdu_types.CommandId.bind_receiver)
        bm.addBinding(mk_server_cnxn_rx2)
        
        # Get the new rx
        deliverer = bm.getNextBindingForDelivery()
        self.assertEqual(mk_server_cnxn_rx2, deliverer)
        
        # Expect the original rx again
        deliverer = bm.getNextBindingForDelivery()
        self.assertEqual(mk_server_cnxn_rx1, deliverer)
        
        # Add a new tx - shouldn't affect deliverer selection at all
        mk_server_cnxn_tx1 = _makeMockServerConnection('blah', pdu_types.CommandId.bind_transmitter)
        bm.addBinding(mk_server_cnxn_tx1)
        
        # Expect the 2nd rx again
        deliverer = bm.getNextBindingForDelivery()
        self.assertEqual(mk_server_cnxn_rx2, deliverer)

        # Add a new trx
        mk_server_cnxn_trx1 = _makeMockServerConnection('blah', pdu_types.CommandId.bind_transceiver)
        bm.addBinding(mk_server_cnxn_trx1)
    
        # Expect the new trx
        deliverer = bm.getNextBindingForDelivery()
        self.assertEqual(mk_server_cnxn_trx1, deliverer)
        
        # Remove the 1st rx
        bm.removeBinding(mk_server_cnxn_rx1)
        
        # Expect the 2nd rx again
        deliverer = bm.getNextBindingForDelivery()
        self.assertEqual(mk_server_cnxn_rx2, deliverer)
        
        # Expect the new trx
        deliverer = bm.getNextBindingForDelivery()
        self.assertEqual(mk_server_cnxn_trx1, deliverer)
        
        # Expect the 2nd rx again
        deliverer = bm.getNextBindingForDelivery()
        self.assertEqual(mk_server_cnxn_rx2, deliverer)

class SMPPServerBaseTest(TestCase):
    def _serviceHandler(self, system_id, smpp, pdu):
        self.service_calls.append((system_id, smpp, pdu))
        return pdu_types.CommandStatus.ESME_ROK

    class SmppRealm(object):
        implements(IRealm)

        def requestAvatar(self, avatarId, mind, *interfaces):
            return ('SMPP', avatarId, lambda: None)

    def _bind(self):
        self.proto.bind_type = pdu_types.CommandId.bind_transceiver
        self.proto.sessionState = SMPPSessionStates.BOUND_TRX
        self.proto.system_id = 'userA'
        self.factory.addBoundConnection(self.proto)

class SMPPServerTestCase(SMPPServerBaseTest):

    def setUp(self):
        self.service_calls = []
        self.encoder = pdu_encoding.PDUEncoder()
        self.smpp_config = SMPPServerConfig(msgHandler=self._serviceHandler,
                                            systems={'userA': {"max_bindings": 2}}
                                            )
        portal = Portal(self.SmppRealm())
        credential_checker = InMemoryUsernamePasswordDatabaseDontUse()
        credential_checker.addUser('userA', 'valid')
        portal.registerChecker(credential_checker)
        self.factory = SMPPServerFactory(self.smpp_config, auth_portal=portal)
        self.proto = self.factory.buildProtocol(('127.0.0.1', 0))
        self.tr = proto_helpers.StringTransport()
        self.proto.makeConnection(self.tr)

    def tearDown(self):
        self.proto.connectionLost('test end')

    @unittest.skip('''Jasmin update: All vendor tests shall be skipped)''')
    def testTRXBindRequest(self):
        pdu = operations.BindTransceiver(
            system_id = 'userA',
            password = 'valid',
            seqNum = 1
        )
        self.proto.dataReceived(self.encoder.encode(pdu))
        expected_pdu = operations.BindTransceiverResp(system_id='userA', seqNum=1)
        self.assertEqual(self.tr.value(), self.encoder.encode(expected_pdu))
        connection = self.factory.getBoundConnections('userA')
        self.assertEqual(connection.system_id, 'userA')
        self.assertEqual(connection._binds[pdu_types.CommandId.bind_transceiver][0], self.proto)

    @unittest.skip('''Jasmin update: All vendor tests shall be skipped)''')
    def testTRXBindRequestInvalidSysId(self):
        pdu = operations.BindTransceiver(
            system_id = 'userB',
            password = 'valid',
            seqNum = 1
        )
        self.proto.dataReceived(self.encoder.encode(pdu))
        expected_pdu = operations.BindTransceiverResp(system_id='userB', seqNum=1, status=pdu_types.CommandStatus.ESME_RINVSYSID)
        self.assertEqual(self.tr.value(), self.encoder.encode(expected_pdu))
        connection = self.factory.getBoundConnections('userA')
        self.assertEqual(connection, None)
        connection = self.factory.getBoundConnections('userB')
        self.assertEqual(connection, None)

    @unittest.skip('''Jasmin update: All vendor tests shall be skipped)''')
    def testTRXBindRequestInvalidPassword(self):
        pdu = operations.BindTransceiver(
            system_id = 'userA',
            password = 'invalid',
            seqNum = 1
        )
        self.proto.dataReceived(self.encoder.encode(pdu))
        expected_pdu = operations.BindTransceiverResp(system_id='userA', seqNum=1, status=pdu_types.CommandStatus.ESME_RINVPASWD)
        self.assertEqual(self.tr.value(), self.encoder.encode(expected_pdu))
        connection = self.factory.getBoundConnections('userA')
        self.assertEqual(connection, None)
 
    @unittest.skip('''Jasmin update: All vendor tests shall be skipped)''')
    def testTransmitterBindRequest(self):
        system_id = 'userA'
        pdu = operations.BindTransmitter(
            system_id = system_id,
            password = 'valid',
            seqNum = 1
        )
        self.proto.dataReceived(self.encoder.encode(pdu))
        expected_pdu = operations.BindTransmitterResp(system_id='userA', seqNum=1)
        self.assertEqual(self.tr.value(), self.encoder.encode(expected_pdu))
        self.tr.clear()
        connection = self.factory.getBoundConnections(system_id)
        self.assertEqual(connection.system_id, system_id)
        self.assertEqual(connection._binds[pdu_types.CommandId.bind_transmitter][0], self.proto)
        bind_manager = self.factory.getBoundConnections(system_id)
        delivery_binding = bind_manager.getNextBindingForDelivery()
        self.assertTrue(delivery_binding is None)

    @unittest.skip('''Jasmin update: All vendor tests shall be skipped)''')
    def testReceiverBindRequest(self):
        system_id = 'userA'
        pdu = operations.BindReceiver(
            system_id = system_id,
            password = 'valid',
            seqNum = 1
        )
        self.proto.dataReceived(self.encoder.encode(pdu))
        expected_pdu = operations.BindReceiverResp(system_id='userA', seqNum=1)
        self.assertEqual(self.tr.value(), self.encoder.encode(expected_pdu))
        self.tr.clear()
        connection = self.factory.getBoundConnections(system_id)
        self.assertEqual(connection.system_id, system_id)
        self.assertEqual(connection._binds[pdu_types.CommandId.bind_receiver][0], self.proto)
        bind_manager = self.factory.getBoundConnections(system_id)
        delivery_binding = bind_manager.getNextBindingForDelivery()
        self.assertTrue(delivery_binding is not None)
        # TODO Identify what should be returned here
        pdu = operations.SubmitSM(source_addr='t1', destination_addr='1208230', short_message='HELLO', seqNum=6)
        self.proto.dataReceived(self.encoder.encode(pdu))
        expected_pdu = operations.SubmitSMResp(seqNum=6)
        self.assertEqual(self.tr.value(), self.encoder.encode(expected_pdu))

 
    @unittest.skip('''Jasmin update: All vendor tests shall be skipped)''')
    def testUnboundSubmitRequest(self):
        pdu = operations.SubmitSM(source_addr='t1', destination_addr='1208230', short_message='HELLO', seqNum=1)
        self.proto.dataReceived(self.encoder.encode(pdu))
        expected_pdu = operations.SubmitSMResp(status=pdu_types.CommandStatus.ESME_RINVBNDSTS, seqNum=1)
        self.assertEqual(self.tr.value(), self.encoder.encode(expected_pdu))
    
    @unittest.skip('''Jasmin update: All vendor tests shall be skipped)''')
    def testUnboundSubmitRequest(self):
        pdu = operations.EnquireLink(seqNum = 576)
        self.proto.dataReceived(self.encoder.encode(pdu))
        expected_pdu = operations.EnquireLinkResp(status=pdu_types.CommandStatus.ESME_RINVBNDSTS, seqNum=576)
        self.assertEqual(self.tr.value(), self.encoder.encode(expected_pdu))

    @unittest.skip('''Jasmin update: All vendor tests shall be skipped)''')
    def testTRXUnbindRequest(self):
        self._bind()
        pdu = operations.Unbind(seqNum = 346)
        self.proto.dataReceived(self.encoder.encode(pdu))
        expected_pdu = operations.UnbindResp(seqNum=346)
        self.assertEqual(self.tr.value(), self.encoder.encode(expected_pdu))
        connection = self.factory.getBoundConnections('userA')
        self.assertEqual(connection.system_id, 'userA')
        # Still in list of binds as the connection has not been closed yet. is removed after test tearDown
        self.assertEqual(connection._binds[pdu_types.CommandId.bind_transceiver][0].sessionState, SMPPSessionStates.UNBOUND)
 
    @unittest.skip('''Jasmin update: All vendor tests shall be skipped)''')
    def testTRXSubmitSM(self):
        self._bind()
        pdu = operations.SubmitSM(source_addr='t1', destination_addr='1208230', short_message='HELLO', seqNum=6)
        self.proto.dataReceived(self.encoder.encode(pdu))
        expected_pdu = operations.SubmitSMResp(seqNum=6)
        self.assertEqual(self.tr.value(), self.encoder.encode(expected_pdu))
        system_id, smpp, pdu_notified = self.service_calls.pop()
        self.assertEqual(system_id, self.proto.system_id)
	self.assertEqual(pdu.params['short_message'], pdu_notified.params['short_message'])
        self.assertEqual(pdu.params['source_addr'], pdu_notified.params['source_addr'])
        self.assertEqual(pdu.params['destination_addr'], pdu_notified.params['destination_addr'])

    @unittest.skip('''Jasmin update: All vendor tests shall be skipped)''')
    def testTRXDataSM(self):
        self._bind()
        pdu = operations.DataSM(source_addr='t1', destination_addr='1208230', short_message='tests', seqNum=6)
        self.proto.dataReceived(self.encoder.encode(pdu))
        expected_pdu = operations.DataSMResp(seqNum=6)
        self.assertEqual(self.tr.value(), self.encoder.encode(expected_pdu))
        system_id, smpp, pdu_notified = self.service_calls.pop()
        self.assertEqual(system_id, self.proto.system_id)
        
    @unittest.skip('''Jasmin update: All vendor tests shall be skipped)''')
    def testTRXQuerySM(self):
        def _serviceHandler(system_id, smpp, pdu):
            self.service_calls.append((system_id, smpp, pdu))
            return DataHandlerResponse(status=pdu_types.CommandStatus.ESME_ROK,
                                       message_id='tests',
                                       final_date=None,
                                       message_state=pdu_types.MessageState.ACCEPTED,
                                       error_code=0)
        self.proto.dataRequestHandler = lambda *args, **kwargs: _serviceHandler(self.proto.system_id, *args, **kwargs)
        self._bind()
        pdu = operations.QuerySM(message_id='tests', source_addr='t1', seqNum=23)
        self.proto.dataReceived(self.encoder.encode(pdu))
        expected_pdu = operations.QuerySMResp(message_id='tests', error_code=0, final_date=None, message_state=pdu_types.MessageState.ACCEPTED ,seqNum=23)
        # Does not work as application using library must reply correctly.
        print self.tr.value()
        self.assertEqual(self.tr.value(), self.encoder.encode(expected_pdu))
        system_id, smpp, pdu_notified = self.service_calls.pop()
        self.assertEqual(system_id, self.proto.system_id)
 
    @unittest.skip('''Jasmin update: All vendor tests shall be skipped)''')
    def testRecievePduWhileUnbindPending(self):
        self._bind()
        self.proto.unbind()
        expected_pdu = operations.Unbind(seqNum=1)
        self.assertEqual(self.tr.value(), self.encoder.encode(expected_pdu))
        self.tr.clear()
        pdu = operations.SubmitSM(source_addr='t1', destination_addr='1208230', short_message='HELLO', seqNum=6)
        self.proto.dataReceived(self.encoder.encode(pdu))
        expected_pdu = operations.SubmitSMResp(seqNum=6)
        self.assertEqual(self.tr.value(), '')
        pdu = operations.UnbindResp(seqNum=1)
        self.proto.dataReceived(self.encoder.encode(pdu))
        
    @unittest.skip('''Jasmin update: All vendor tests shall be skipped)''')
    def testTRXClientUnbindRequestAfterSubmit(self):
        d = defer.Deferred()
        def _serviceHandler(system_id, smpp, pdu):
            logging.debug("%s, %s, %s", system_id, smpp, pdu)
            return d
        self.proto.dataRequestHandler = lambda *args, **kwargs: _serviceHandler(self.proto.system_id, *args, **kwargs)
        self._bind()

        pdu = operations.SubmitSM(source_addr='t1', destination_addr='1208230', short_message='HELLO', seqNum=1)
        self.proto.dataReceived(self.encoder.encode(pdu))
        pdu = operations.Unbind(seqNum = 52)
        self.proto.dataReceived(self.encoder.encode(pdu))

        #All PDU requests should fail now.
        #Once we fire this we should get our Submit Resp and the unbind Resp
	pdu = operations.SubmitSM(source_addr='t1', destination_addr='1208230', short_message='goodbye', seqNum=5)
	self.proto.dataReceived(self.encoder.encode(pdu))

        unbind_resp_pdu = operations.UnbindResp(seqNum=52)
        submit_fail_pdu = operations.SubmitSMResp(status=pdu_types.CommandStatus.ESME_RINVBNDSTS, seqNum=5)
        # We should have a reply here as our service handler should not be called
        self.assertEqual(self.tr.value(), self.encoder.encode(submit_fail_pdu))
        self.tr.clear()
        
        d.callback(pdu_types.CommandStatus.ESME_ROK)
        #Then we should get our initial message response and the unbind response
        expected_pdu = operations.SubmitSMResp(seqNum=1)
        self.assertEqual(self.tr.value(), '%s%s' % (self.encoder.encode(expected_pdu), self.encoder.encode(unbind_resp_pdu)))

    @unittest.skip('''Jasmin update: All vendor tests shall be skipped)''')
    def testTRXServerUnbindRequestAfterSubmit(self):
        deferreds = []
        def _serviceHandler(system_id, smpp, pdu):
            d = defer.Deferred()
            deferreds.append(d)
            logging.debug("%s, %s, %s", system_id, smpp, pdu)
            return d
        self.proto.dataRequestHandler = lambda *args, **kwargs: _serviceHandler(self.proto.system_id, *args, **kwargs)
        self._bind()

        pdu = operations.SubmitSM(source_addr='t1', destination_addr='1208230', short_message='HELLO', seqNum=1)
        self.proto.dataReceived(self.encoder.encode(pdu))
        unbind_d = self.proto.unbind()
        print self.tr.value()

        pdu2 = operations.SubmitSM(source_addr='t1', destination_addr='1208230', short_message='HELLO2', seqNum=2)
        self.proto.dataReceived(self.encoder.encode(pdu))
        
        self.assertEqual(1, len(deferreds))
        self.assertEqual(self.tr.value(), '')
        self.tr.clear()
        deferreds[-1].callback(pdu_types.CommandStatus.ESME_ROK)
        deferreds = deferreds[:-1]
        submit_resp_pdu = operations.SubmitSMResp(seqNum=1)

        unbind_pdu = operations.Unbind(seqNum=1)
        # We should have a reply here as our service handler should not be called
        self.assertEqual(self.tr.value(), '%s%s' % (self.encoder.encode(submit_resp_pdu), self.encoder.encode(unbind_pdu)))
        self.tr.clear()
        pdu = operations.UnbindResp(seqNum=1)
        self.proto.dataReceived(self.encoder.encode(pdu))

class SMPPServerTimeoutTestCase(SMPPServerBaseTest):

    def setUp(self):
        self.service_calls = []
        self.clock = task.Clock()
        self.encoder = pdu_encoding.PDUEncoder()
        self.smpp_config = SMPPServerConfig(msgHandler=self._serviceHandler,
                                            systems={'userA': {"max_bindings": 2}},
                                            enquireLinkTimerSecs=0.1,
                                            responseTimerSecs=0.1
                                            )
        portal = Portal(self.SmppRealm())
        credential_checker = InMemoryUsernamePasswordDatabaseDontUse()
        credential_checker.addUser('userA', 'valid')
        portal.registerChecker(credential_checker)
        self.factory = SMPPServerFactory(self.smpp_config, auth_portal=portal)
        self.proto = self.factory.buildProtocol(('127.0.0.1', 0))
        self.proto.callLater = self.clock.callLater
        self.tr = proto_helpers.StringTransport()
        self.proto.makeConnection(self.tr)

    @unittest.skip('''Jasmin update: All vendor tests shall be skipped)''')
    def testEnquireTimeout(self):
        self._bind()
        pdu = operations.SubmitSM(source_addr='t1', destination_addr='1208230', short_message='HELLO', seqNum=6)
        self.proto.dataReceived(self.encoder.encode(pdu))
        expected_pdu = operations.SubmitSMResp(seqNum=6)
        self.assertEqual(self.tr.value(), self.encoder.encode(expected_pdu))
        self.service_calls.pop()
        self.tr.clear()
        self.clock.advance(0.1)
        self.clock.advance(0.1)
        self.assertEqual(self.proto.sessionState, SMPPSessionStates.UNBIND_PENDING)
