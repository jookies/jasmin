# -*- coding: utf-8 -*- 
import logging
import mock
import copy
from twisted.internet import reactor, defer
from jasmin.vendor.smpp.twisted.protocol import SMPPSessionStates
from jasmin.vendor.smpp.pdu import pdu_types, pdu_encoding
from jasmin.routing.test.test_router import (SMPPClientManagerPBTestCase, HappySMSCTestCase,
                                            SubmitSmTestCaseTools, id_generator)
from jasmin.routing.proxies import RouterPBProxy
from jasmin.routing.Routes import DefaultRoute
from jasmin.protocols.smpp.configs import SMPPServerConfig, SMPPClientConfig
from jasmin.protocols.smpp.factory import SMPPServerFactory
from jasmin.tools.cred.portal import SmppsRealm
from jasmin.tools.cred.checkers import RouterAuthChecker
from jasmin.routing.jasminApi import *
from jasmin.vendor.smpp.pdu.operations import SubmitSM, DeliverSM
from twisted.cred import portal
from twisted.test import proto_helpers

class LastProtoSMPPServerFactory(SMPPServerFactory):
    """This a SMPPServerFactory used to keep track of the last protocol instance for
    testing purpose"""

    lastProto = None
    def buildProtocol(self, addr):
        self.lastProto = SMPPServerFactory.buildProtocol(self, addr)
        return self.lastProto

class SmppServerTestCase(HappySMSCTestCase):

    @defer.inlineCallbacks
    def setUp(self):
        yield HappySMSCTestCase.setUp(self)

        self.encoder = pdu_encoding.PDUEncoder()

        # SMPPServerConfig init
        args = {'id': 'smpps_01_%s' % 27750, 'port': 27750}
        self.smpps_config = SMPPServerConfig(**args)

        # Portal init
        _portal = portal.Portal(SmppsRealm(self.smpps_config.id, self.pbRoot_f))
        _portal.registerChecker(RouterAuthChecker(self.pbRoot_f))

        # Install mocks
        self.clientManager_f.perspective_submit_sm = mock.Mock(wraps=self.clientManager_f.perspective_submit_sm)

        # SMPPServerFactory init
        self.smpps_factory = LastProtoSMPPServerFactory(self.smpps_config, 
                                                        auth_portal = _portal,
                                                        RouterPB = self.pbRoot_f,
                                                        SMPPClientManagerPB = self.clientManager_f)

        # Init protocol for testing
        self.smpps_proto = self.smpps_factory.buildProtocol(('127.0.0.1', 0))
        self.smpps_tr = proto_helpers.StringTransport()
        self.smpps_proto.makeConnection(self.smpps_tr)

        # Install mocks
        self.smpps_proto.sendPDU = mock.Mock(wraps=self.smpps_proto.sendPDU)

        # PDUs used for tests
        self.SubmitSmPDU = SubmitSM(
            source_addr = '1234',
            destination_addr = '4567',
            short_message = 'hello !',
            seqNum = 1,
        )

    @defer.inlineCallbacks
    def tearDown(self):
        yield HappySMSCTestCase.tearDown(self)

        self.smpps_proto.connectionLost('test end')

    @defer.inlineCallbacks
    def provision_user_connector(self, add_route = True):
        # provision user
        g1 = Group(1)
        yield self.group_add(g1)        
        self.c1 = SmppClientConnector(id_generator())
        u1_password = 'password'
        self.u1 = User(1, g1, 'username', u1_password)
        yield self.user_add(self.u1)

        # provision route
        if add_route:
            yield self.mtroute_add(DefaultRoute(self.c1), 0)

    def _bind_smpps(self, user):
        self.smpps_proto.bind_type = pdu_types.CommandId.bind_transceiver
        self.smpps_proto.sessionState = SMPPSessionStates.BOUND_TRX
        self.smpps_proto.user = user
        self.smpps_proto.system_id = user.username
        self.smpps_factory.addBoundConnection(self.smpps_proto, user)

class SubmitSmDeliveryTestCases(RouterPBProxy, SmppServerTestCase):

    @defer.inlineCallbacks
    def provision_user_connector(self, add_route = True):
        # provision user
        g1 = Group(1)
        yield self.group_add(g1)        
        self.c1 = SmppClientConnector(id_generator())
        u1_password = 'password'
        self.u1 = User(1, g1, 'username', u1_password)
        yield self.user_add(self.u1)

        # provision route
        if add_route:
            yield self.mtroute_add(DefaultRoute(self.c1), 0)

    @defer.inlineCallbacks
    def test_successful_delivery_from_smpps_to_smppc(self):
        yield self.connect('127.0.0.1', self.pbPort)
        yield self.provision_user_connector()
        
        # add connector
        yield self.SMPPClientManagerPBProxy.connect('127.0.0.1', self.CManagerPort)
        c1Config = SMPPClientConfig(id=self.c1.cid)
        yield self.SMPPClientManagerPBProxy.add(c1Config)
        
        # Bind and send a SMS MT through smpps interface
        self._bind_smpps(self.u1)
        self.smpps_proto.dataReceived(self.encoder.encode(self.SubmitSmPDU))

        # Assertions
        # smpps sent back a response ?
        self.assertEqual(self.smpps_proto.sendPDU.call_count, 1)
        # smpps response was a submit_sm_resp with ESME_ROK ?
        response_pdu = self.smpps_proto.sendPDU.call_args_list[0][0][0]
        self.assertEqual(response_pdu.id, pdu_types.CommandId.submit_sm_resp)
        self.assertEqual(response_pdu.seqNum, 1)
        self.assertEqual(response_pdu.status, pdu_types.CommandStatus.ESME_ROK)
        self.assertTrue(response_pdu.params['message_id'] is not None)

    @defer.inlineCallbacks
    def test_seqNum(self):
        yield self.connect('127.0.0.1', self.pbPort)
        yield self.provision_user_connector()
        
        # add connector
        yield self.SMPPClientManagerPBProxy.connect('127.0.0.1', self.CManagerPort)
        c1Config = SMPPClientConfig(id=self.c1.cid)
        yield self.SMPPClientManagerPBProxy.add(c1Config)
        
        # Bind and send many SMS MT through smpps interface
        self._bind_smpps(self.u1)
        count = 5
        SubmitSmPDU = copy.deepcopy(self.SubmitSmPDU)
        for i in range(count):
            self.smpps_proto.dataReceived(self.encoder.encode(SubmitSmPDU))
            SubmitSmPDU.seqNum += 1

        # Assertions
        # smpps sent back a response ?
        self.assertEqual(self.smpps_proto.sendPDU.call_count, count)
        # Collect message_ids from submit_sm_resps
        current_seqNum = 1
        for call_arg in self.smpps_proto.sendPDU.call_args_list:
            response_pdu = call_arg[0][0]
            self.assertEqual(response_pdu.id, pdu_types.CommandId.submit_sm_resp)
            self.assertEqual(response_pdu.status, pdu_types.CommandStatus.ESME_ROK)
            # is seqNum correctly incrementing ?
            self.assertEqual(response_pdu.seqNum, current_seqNum)
            current_seqNum+= 1

    @defer.inlineCallbacks
    def test_delivery_from_smpps_with_default_src_addr(self):
        yield self.connect('127.0.0.1', self.pbPort)
        yield self.provision_user_connector()
        default_source_addr = 'JASMINTEST'
        self.u1.mt_credential.setDefaultValue('source_address', default_source_addr)
        
        # add connector
        yield self.SMPPClientManagerPBProxy.connect('127.0.0.1', self.CManagerPort)
        c1Config = SMPPClientConfig(id=self.c1.cid)
        yield self.SMPPClientManagerPBProxy.add(c1Config)
        
        # Bind and send a SMS MT through smpps interface
        self._bind_smpps(self.u1)
        SubmitSmPDU = copy.deepcopy(self.SubmitSmPDU)
        SubmitSmPDU.params['source_addr'] = None
        self.smpps_proto.dataReceived(self.encoder.encode(SubmitSmPDU))

        # Assertions
        # submit_sm source_addr has been changed to default one
        SentSubmitSmPDU = self.clientManager_f.perspective_submit_sm.call_args_list[0][0][1]
        self.assertEqual(SentSubmitSmPDU.params['source_addr'], default_source_addr)

    @defer.inlineCallbacks
    def test_delivery_from_smpps_to_unknown_smppc(self):
        yield self.connect('127.0.0.1', self.pbPort)
        yield self.provision_user_connector()
        
        # Will not add connector to SMPPClientManagerPB
        yield self.SMPPClientManagerPBProxy.connect('127.0.0.1', self.CManagerPort)
        
        # Bind and send a SMS MT through smpps interface
        self._bind_smpps(self.u1)
        self.smpps_proto.dataReceived(self.encoder.encode(self.SubmitSmPDU))

        # Assertions
        # smpps sent back a response ?
        self.assertEqual(self.smpps_proto.sendPDU.call_count, 1)
        # smpps response was a submit_sm_resp with ESME_ROK ?
        response_pdu = self.smpps_proto.sendPDU.call_args_list[0][0][0]
        self.assertEqual(response_pdu.id, pdu_types.CommandId.submit_sm_resp)
        self.assertEqual(response_pdu.seqNum, 1)
        self.assertEqual(response_pdu.status, pdu_types.CommandStatus.ESME_RSYSERR)
        self.assertTrue('message_id' not in response_pdu.params)

    @defer.inlineCallbacks
    def test_delivery_from_smpps_no_route_found(self):
        yield self.connect('127.0.0.1', self.pbPort)
        yield self.provision_user_connector(add_route = False)
        
        # Will not add connector to SMPPClientManagerPB
        yield self.SMPPClientManagerPBProxy.connect('127.0.0.1', self.CManagerPort)

        # Bind and send a SMS MT through smpps interface
        self._bind_smpps(self.u1)
        self.smpps_proto.dataReceived(self.encoder.encode(self.SubmitSmPDU))

        # Assertions
        # smpps sent back a response ?
        self.assertEqual(self.smpps_proto.sendPDU.call_count, 1)
        # smpps response was a submit_sm_resp with ESME_ROK ?
        response_pdu = self.smpps_proto.sendPDU.call_args_list[0][0][0]
        self.assertEqual(response_pdu.id, pdu_types.CommandId.submit_sm_resp)
        self.assertEqual(response_pdu.seqNum, 1)
        self.assertEqual(response_pdu.status, pdu_types.CommandStatus.ESME_RSYSERR)
        self.assertTrue('message_id' not in response_pdu.params)

class BillRequestSubmitSmRespCallbackingTestCases(RouterPBProxy, SmppServerTestCase, 
                                                SubmitSmTestCaseTools):

    @defer.inlineCallbacks
    def test_unrated_route_limited_submit_sm_count(self):
        yield self.connect('127.0.0.1', self.pbPort)
        mt_c = MtMessagingCredential()
        mt_c.setQuota('balance', 2.0)
        mt_c.setQuota('submit_sm_count', 10)
        user = User(1, Group(1), 'username', 'password', mt_c)
        yield self.prepareRoutingsAndStartConnector(user = user)
        assertionUser = self.pbRoot_f.getUser(user.uid)

        # Mock user's updateQuota callback
        assertionUser.mt_credential.updateQuota = mock.Mock(wraps = assertionUser.mt_credential.updateQuota)

        # Bind and send a SMS MT through smpps interface
        self._bind_smpps(self.u1)
        self.smpps_proto.dataReceived(self.encoder.encode(self.SubmitSmPDU))
        
        # Wait 1 seconds for submit_sm_resp
        exitDeferred = defer.Deferred()
        reactor.callLater(1, exitDeferred.callback, None)
        yield exitDeferred

        yield self.stopSmppClientConnectors()
        
        # Run tests
        # Assert quotas were not updated
        self.assertEquals(assertionUser.mt_credential.updateQuota.call_count, 1)
        callArgs = assertionUser.mt_credential.updateQuota.call_args_list
        self.assertEquals(callArgs[0][0][0], 'submit_sm_count')
        self.assertEquals(callArgs[0][0][1], -1)
        # Assert quotas after SMS is sent
        self.assertAlmostEqual(assertionUser.mt_credential.getQuota('balance'), 2.0)
        self.assertAlmostEqual(assertionUser.mt_credential.getQuota('submit_sm_count'), 9)

    @defer.inlineCallbacks
    def test_unrated_route_unlimited_submit_sm_count(self):
        yield self.connect('127.0.0.1', self.pbPort)
        mt_c = MtMessagingCredential()
        mt_c.setQuota('balance', 2.0)
        user = User(1, Group(1), 'username', 'password', mt_c)
        yield self.prepareRoutingsAndStartConnector(user = user)
        assertionUser = self.pbRoot_f.getUser(user.uid)

        # Mock user's updateQuota callback
        assertionUser.mt_credential.updateQuota = mock.Mock(wraps = assertionUser.mt_credential.updateQuota)

        # Bind and send a SMS MT through smpps interface
        self._bind_smpps(self.u1)
        self.smpps_proto.dataReceived(self.encoder.encode(self.SubmitSmPDU))
        
        # Wait 1 seconds for submit_sm_resp
        exitDeferred = defer.Deferred()
        reactor.callLater(1, exitDeferred.callback, None)
        yield exitDeferred

        yield self.stopSmppClientConnectors()
        
        # Run tests
        # Assert quotas were not updated
        self.assertEquals(assertionUser.mt_credential.updateQuota.call_count, 0)
        # Assert quotas after SMS is sent
        self.assertAlmostEqual(assertionUser.mt_credential.getQuota('balance'), 2.0)
        self.assertEqual(assertionUser.mt_credential.getQuota('submit_sm_count'), None)

    @defer.inlineCallbacks
    def test_rated_route(self):
        yield self.connect('127.0.0.1', self.pbPort)
        mt_c = MtMessagingCredential()
        mt_c.setQuota('balance', 2.0)
        mt_c.setQuota('submit_sm_count', 10)
        user = User(1, Group(1), 'username', 'password', mt_c)
        yield self.prepareRoutingsAndStartConnector(route_rate = 1.0, user = user)
        assertionUser = self.pbRoot_f.getUser(user.uid)

        # Mock user's updateQuota callback
        assertionUser.mt_credential.updateQuota = mock.Mock(wraps = assertionUser.mt_credential.updateQuota)

        # Bind and send a SMS MT through smpps interface
        self._bind_smpps(user)
        self.smpps_proto.dataReceived(self.encoder.encode(self.SubmitSmPDU))
        
        # Wait 1 seconds for submit_sm_resp
        exitDeferred = defer.Deferred()
        reactor.callLater(1, exitDeferred.callback, None)
        yield exitDeferred

        yield self.stopSmppClientConnectors()
        
        # Run tests
        # Assert quotas were updated
        callArgs = assertionUser.mt_credential.updateQuota.call_args_list
        self.assertEquals(callArgs[0][0][0], 'balance')
        self.assertEquals(callArgs[0][0][1], -1)
        self.assertEquals(callArgs[1][0][0], 'submit_sm_count')
        self.assertEquals(callArgs[1][0][1], -1)
        self.assertEquals(assertionUser.mt_credential.updateQuota.call_count, 2)
        # Assert quotas after SMS is sent
        self.assertAlmostEqual(assertionUser.mt_credential.getQuota('balance'), 1.0)
        self.assertAlmostEqual(assertionUser.mt_credential.getQuota('submit_sm_count'), 9)

    @defer.inlineCallbacks
    def test_rated_route_early_decrement_balance_percent(self):
        """Will test that user must be charged initially 10% of the router rate on submit_sm
        enqueuing and the all the rest (90%) when submit_sm_resp is received
        """
        yield self.connect('127.0.0.1', self.pbPort)
        mt_c = MtMessagingCredential()
        mt_c.setQuota('balance', 2.0)
        mt_c.setQuota('submit_sm_count', 10)
        mt_c.setQuota('early_decrement_balance_percent', 10)
        user = User(1, Group(1), 'username', 'password', mt_c)
        yield self.prepareRoutingsAndStartConnector(route_rate = 1.0, user = user)
        assertionUser = self.pbRoot_f.getUser(user.uid)

        # Mock user's updateQuota callback
        assertionUser.mt_credential.updateQuota = mock.Mock(wraps = assertionUser.mt_credential.updateQuota)

        # Bind and send a SMS MT through smpps interface
        self._bind_smpps(user)
        self.smpps_proto.dataReceived(self.encoder.encode(self.SubmitSmPDU))
        
        # Wait 1 seconds for submit_sm_resp
        exitDeferred = defer.Deferred()
        reactor.callLater(1, exitDeferred.callback, None)
        yield exitDeferred

        yield self.stopSmppClientConnectors()
        
        # Run tests
        # Assert quotas were updated
        callArgs = assertionUser.mt_credential.updateQuota.call_args_list
        self.assertEquals(callArgs[0][0][0], 'balance')
        self.assertEquals(callArgs[0][0][1], -0.1)
        self.assertEquals(callArgs[1][0][0], 'submit_sm_count')
        self.assertEquals(callArgs[1][0][1], -1)
        self.assertEquals(callArgs[2][0][0], 'balance')
        self.assertEquals(callArgs[2][0][1], -0.9)
        self.assertEquals(assertionUser.mt_credential.updateQuota.call_count, 3)
        # Assert quotas after SMS is sent
        self.assertAlmostEqual(assertionUser.mt_credential.getQuota('balance'), 1.0)
        self.assertAlmostEqual(assertionUser.mt_credential.getQuota('submit_sm_count'), 9)