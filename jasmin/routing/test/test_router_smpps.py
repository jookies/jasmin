# -*- coding: utf-8 -*- 
import logging
import mock
from twisted.internet import defer
from jasmin.vendor.smpp.twisted.protocol import SMPPSessionStates
from jasmin.vendor.smpp.pdu import pdu_types, pdu_encoding
from jasmin.routing.test.test_router import (SMPPClientManagerPBTestCase, id_generator)
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

class SmppServerTestCase(SMPPClientManagerPBTestCase):

    @defer.inlineCallbacks
    def setUp(self):
        yield SMPPClientManagerPBTestCase.setUp(self)

        self.encoder = pdu_encoding.PDUEncoder()

        # SMPPServerConfig init
        args = {'id': 'smpps_01_%s' % 27750, 'port': 27750, 
                'log_level': logging.DEBUG}
        self.smpps_config = SMPPServerConfig(**args)

        # Portal init
        _portal = portal.Portal(SmppsRealm(self.smpps_config.id, self.pbRoot_f))
        _portal.registerChecker(RouterAuthChecker(self.pbRoot_f))

        # SMPPServerFactory init
        self.smpps_factory = LastProtoSMPPServerFactory(self.smpps_config, auth_portal=_portal)

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
        yield SMPPClientManagerPBTestCase.tearDown(self)

        self.smpps_proto.connectionLost('test end')

    def _bind(self, user):
        self.smpps_proto.bind_type = pdu_types.CommandId.bind_transceiver
        self.smpps_proto.sessionState = SMPPSessionStates.BOUND_TRX
        self.smpps_proto.user = user
        self.smpps_proto.system_id = user.username
        self.smpps_factory.addBoundConnection(self.smpps_proto, user)

class SubmitSmDeliveryTestCases(RouterPBProxy, SmppServerTestCase):

    @defer.inlineCallbacks
    def provision_user_connector(self):
        # provision user
        g1 = Group(1)
        yield self.group_add(g1)        
        self.c1 = SmppClientConnector(id_generator())
        u1_password = 'password'
        self.u1 = User(1, g1, 'username', u1_password)
        yield self.user_add(self.u1)

        # provision route
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
        self._bind(self.u1)
        self.smpps_proto.dataReceived(self.encoder.encode(self.SubmitSmPDU))

        print self.smpps_proto.sendPDU.call_args_list[0][0], self.smpps_proto.sendPDU.call_count
        # Assertions
        # smpps sent back a response ?
        #self.assertEqual(self.smpps_proto.sendPDU.call_count, 1)
        # smpps response was a submit_sm_resp with ESME_ROK ?
        response_pdu = self.smpps_proto.sendPDU.call_args_list[0][0][0]
        #self.assertEqual(response_pdu.id, pdu_types.CommandId.submit_sm_resp)
        #self.assertEqual(response_pdu.seqNum, 1)
        #self.assertEqual(response_pdu.status, pdu_types.CommandStatus.ESME_ROK)
        
        # Since Connector doesnt really exist, the message will not be routed
        # to a queue, a 500 error will be returned, and more details will be written
        # in smpp client manager log:
        # 'Trying to enqueue a SUBMIT_SM to a connector with an unknown cid: '
        #try:
        #    yield getPage(url_ok)
        #except Exception, e:
        #    lastErrorStatus = e.status
        #self.assertEqual(lastErrorStatus, '500')
        
        # We should receive a msg id
        #c = yield getPage(url_ok)
        #self.assertEqual(c[:7], 'Success')
        # @todo: Should be a real uuid pattern testing 
        #self.assertApproximates(len(c), 40, 10)

    @defer.inlineCallbacks
    def test_delivery_from_smpps_with_default_src_addr(self):
        yield self.connect('127.0.0.1', self.pbPort)
        yield self.SMPPClientManagerPBProxy.connect('127.0.0.1', self.CManagerPort)

    @defer.inlineCallbacks
    def test_delivery_from_smpps_to_unknown_smppc(self):
        yield self.connect('127.0.0.1', self.pbPort)
        yield self.SMPPClientManagerPBProxy.connect('127.0.0.1', self.CManagerPort)

    @defer.inlineCallbacks
    def test_delivery_from_smpps_to_offline_smppc(self):
        yield self.connect('127.0.0.1', self.pbPort)
        yield self.SMPPClientManagerPBProxy.connect('127.0.0.1', self.CManagerPort)

    @defer.inlineCallbacks
    def test_delivery_from_smpps_no_route_found(self):
        yield self.connect('127.0.0.1', self.pbPort)
        yield self.SMPPClientManagerPBProxy.connect('127.0.0.1', self.CManagerPort)

    @defer.inlineCallbacks
    def test_delivery_from_smpps_dlr_requested(self):
        yield self.connect('127.0.0.1', self.pbPort)
        yield self.SMPPClientManagerPBProxy.connect('127.0.0.1', self.CManagerPort)