"""
Test cases for smpp server
"""

import cPickle as pickle
import copy
from datetime import timedelta

import mock
from twisted.cred import portal
from twisted.trial.unittest import TestCase

from jasmin.protocols.smpp.configs import SMPPServerConfig, SMPPClientConfig
from jasmin.protocols.smpp.factory import SMPPServerFactory, SMPPClientFactory
from jasmin.protocols.smpp.protocol import *
from jasmin.protocols.smpp.stats import SMPPServerStatsCollector
from jasmin.routing.Routes import DefaultRoute
from jasmin.routing.configs import RouterPBConfig
from jasmin.routing.jasminApi import User, Group, SmppClientConnector
from jasmin.routing.router import RouterPB
from jasmin.routing.test.test_router import id_generator
from jasmin.tools.cred.checkers import RouterAuthChecker
from jasmin.tools.cred.portal import SmppsRealm
from jasmin.vendor.smpp.pdu.error import SMPPTransactionError


@defer.inlineCallbacks
def waitFor(seconds):
    # Wait seconds
    waitDeferred = defer.Deferred()
    reactor.callLater(seconds, waitDeferred.callback, None)
    yield waitDeferred

class LastProtoSMPPServerFactory(SMPPServerFactory):
    """This a SMPPServerFactory used to keep track of the last protocol instance for
    testing purpose"""

    lastProto = None
    def buildProtocol(self, addr):
        self.lastProto = SMPPServerFactory.buildProtocol(self, addr)
        return self.lastProto
class LastProtoSMPPClientFactory(SMPPClientFactory):
    """This a SMPPClientFactory used to keep track of the last protocol instance for
    testing purpose"""

    lastProto = None
    def buildProtocol(self, addr):
        self.lastProto = SMPPClientFactory.buildProtocol(self, addr)
        return self.lastProto

class RouterPBTestCases(TestCase):

    def setUp(self):
        # Initiating config objects without any filename
        # will lead to setting defaults and that's what we
        # need to run the tests
        self.routerpb_config = RouterPBConfig()

        # Instanciate RouterPB but will not launch a server
        # we only need the instance to access its .users attribute
        # for authentication
        self.routerpb_factory = RouterPB(self.routerpb_config, persistenceTimer=False)

        # Provision a user and default route into RouterPB
        self.foo = User('u1', Group('test'), 'username', 'password')
        self.c1 = SmppClientConnector(id_generator())
        self.defaultroute = DefaultRoute(self.c1)
        self.provision_user_defaultroute(user = self.foo, defaultroute = self.defaultroute)

    def provision_user_defaultroute(self, user, defaultroute = None):
        # This is normally done through jcli API (or any other high level API to come)
        # Using perspective_user_add() is just a shortcut for testing purposes
        if user.group not in self.routerpb_factory.groups:
            self.routerpb_factory.perspective_group_add(pickle.dumps(user.group, pickle.HIGHEST_PROTOCOL))
        self.routerpb_factory.perspective_user_add(pickle.dumps(user, pickle.HIGHEST_PROTOCOL))

        # provision route
        if defaultroute is not None:
            self.routerpb_factory.perspective_mtroute_add(pickle.dumps(defaultroute, pickle.HIGHEST_PROTOCOL), 0)

class SMPPServerTestCases(RouterPBTestCases):

    def setUp(self):
        RouterPBTestCases.setUp(self)

        # SMPPServerConfig init
        self.smpps_config = SMPPServerConfig()

        # Portal init
        _portal = portal.Portal(SmppsRealm(self.smpps_config.id, self.routerpb_factory))
        _portal.registerChecker(RouterAuthChecker(self.routerpb_factory))

        # SMPPServerFactory init
        self.smpps_factory = LastProtoSMPPServerFactory(config = self.smpps_config,
                                                    auth_portal = _portal,
                                                    RouterPB = self.routerpb_factory,
                                                    SMPPClientManagerPB = None)
        self.smpps_port = reactor.listenTCP(self.smpps_config.port, self.smpps_factory)

    @defer.inlineCallbacks
    def tearDown(self):
        yield self.smpps_port.stopListening()

class SMPPClientTestCases(SMPPServerTestCases):

    def setUp(self):
        SMPPServerTestCases.setUp(self)

        self.SubmitSmPDU = SubmitSM(
            source_addr = '1234',
            destination_addr = '4567',
            short_message = 'hello !',
            seqNum = 1,
        )
        self.DeliverSmPDU = DeliverSM(
            source_addr = '4567',
            destination_addr = '1234',
            short_message = 'hello !',
            seqNum = 1,
        )
        self.DataSmPDU = DataSM(
            source_addr = '4567',
            destination_addr = '1234',
            short_message = 'hello !',
            seqNum = 1,
        )

        # SMPPClientConfig init
        args = {'id': 'smppc_01', 'port': self.smpps_config.port,
                'log_level': logging.DEBUG,
                'reconnectOnConnectionLoss': False,
                'username': 'username', 'password': 'password'}
        self.smppc_config = SMPPClientConfig(**args)

        # SMPPClientFactory init
        self.smppc_factory = LastProtoSMPPClientFactory(self.smppc_config)

    @defer.inlineCallbacks
    def tearDown(self):
        yield SMPPServerTestCases.tearDown(self)

class BindTestCases(SMPPClientTestCases):

    @defer.inlineCallbacks
    def test_bind_successfull_trx(self):
        # Connect and bind
        yield self.smppc_factory.connectAndBind()
        self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.BOUND_TRX)

        # Unbind & Disconnect
        yield self.smppc_factory.smpp.unbindAndDisconnect()
        self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.UNBOUND)

    @defer.inlineCallbacks
    def test_bind_successfull_tx(self):
        self.smppc_config.bindOperation = 'transmitter'

        # Connect and bind
        yield self.smppc_factory.connectAndBind()
        self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.BOUND_TX)

        # Unbind & Disconnect
        yield self.smppc_factory.smpp.unbindAndDisconnect()
        self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.UNBOUND)

    @defer.inlineCallbacks
    def test_bind_successfull_rx(self):
        self.smppc_config.bindOperation = 'receiver'

        # Connect and bind
        yield self.smppc_factory.connectAndBind()
        self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.BOUND_RX)

        # Unbind & Disconnect
        yield self.smppc_factory.smpp.unbindAndDisconnect()
        self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.UNBOUND)

    @defer.inlineCallbacks
    def test_bind_wrong_password(self):
        self.smppc_config.password = 'wrong'

        # Connect and bind
        yield self.smppc_factory.connectAndBind()
        self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.UNBOUND)

    @defer.inlineCallbacks
    def test_bind_wrong_username(self):
        self.smppc_config.username = 'wrong'

        # Connect and bind
        yield self.smppc_factory.connectAndBind()
        self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.UNBOUND)

    @defer.inlineCallbacks
    def test_bind_max_bindings_limit_0(self):
        """max_bindings=0:
        No binds will be permitted
        """
        user = self.routerpb_factory.getUser('u1')
        user.smpps_credential.setQuota('max_bindings', 0)

        # Connect and bind
        yield self.smppc_factory.connectAndBind()
        self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.UNBOUND)

    @defer.inlineCallbacks
    def test_bind_max_bindings_limit_1(self):
        """max_bindings=1:
        Only one binding at a time is permitted
        """
        user = self.routerpb_factory.getUser('u1')
        user.smpps_credential.setQuota('max_bindings', 1)

        # Connect and bind (1)
        yield self.smppc_factory.connectAndBind()

        # Connect and bind (2) with same user
        smppc_factory_2 = LastProtoSMPPClientFactory(self.smppc_config)
        yield smppc_factory_2.connectAndBind()

        self.assertEqual(smppc_factory_2.smpp.sessionState, SMPPSessionStates.UNBOUND)

        # Unbind & Disconnect
        yield self.smppc_factory.smpp.unbindAndDisconnect()

    @defer.inlineCallbacks
    def test_bind_authorization(self):
        user = self.routerpb_factory.getUser('u1')
        user.smpps_credential.setAuthorization('bind', False)

        # Connect and bind
        yield self.smppc_factory.connectAndBind()
        self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.UNBOUND)

    @defer.inlineCallbacks
    def test_bind_disabled_user(self):
        "Related to #306"
        user = self.routerpb_factory.getUser('u1')
        user.enabled = False

        # Connect and bind
        yield self.smppc_factory.connectAndBind()
        self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.UNBOUND)

    @defer.inlineCallbacks
    def test_bind_disabled_group(self):
        "Related to #306"
        user = self.routerpb_factory.getUser('u1')
        group = self.routerpb_factory.getGroup(user.group.gid)
        group.enabled = False

        # Connect and bind
        yield self.smppc_factory.connectAndBind()
        self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.UNBOUND)

    @defer.inlineCallbacks
    def test_unbind_user_session(self):
        "Related to #294"
        user = self.routerpb_factory.getUser('u1')

        # Connect and bind
        yield self.smppc_factory.connectAndBind()
        self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.BOUND_TRX)

        yield self.smpps_factory.unbindAndRemoveGateway(user, ban = False)
        self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.NONE)

    @defer.inlineCallbacks
    def test_unbind_user_session_and_ban(self):
        "Related to #305"
        user = self.routerpb_factory.getUser('u1')

        # Connect and bind
        yield self.smppc_factory.connectAndBind()
        self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.BOUND_TRX)

        yield self.smpps_factory.unbindAndRemoveGateway(user)
        self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.NONE)

        # Try to reconnect and bind again
        yield self.smppc_factory.connectAndBind()
        self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.UNBOUND)

class MessagingTestCases(SMPPClientTestCases):

    @defer.inlineCallbacks
    def test_messaging_fidelity(self):
        # Connect and bind
        yield self.smppc_factory.connectAndBind()
        self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.BOUND_TRX)

        # Install mockers
        self.smpps_factory.lastProto.PDUReceived = mock.Mock(wraps=self.smpps_factory.lastProto.PDUReceived)
        self.smppc_factory.lastProto.PDUReceived = mock.Mock(wraps=self.smppc_factory.lastProto.PDUReceived)

        # SMPPServer > SMPPClient
        yield self.smpps_factory.lastProto.sendDataRequest(self.DeliverSmPDU)
        # SMPPClient > SMPPServer
        yield self.smppc_factory.lastProto.sendDataRequest(self.SubmitSmPDU)

        # Unbind & Disconnect
        yield self.smppc_factory.smpp.unbindAndDisconnect()
        self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.UNBOUND)

        # Asserts SMPPServer side
        self.assertEqual(self.smpps_factory.lastProto.PDUReceived.call_count, 3)
        self.assertEqual(self.smpps_factory.lastProto.PDUReceived.call_args_list[1][0][0].params['source_addr'],
                            self.SubmitSmPDU.params['source_addr'])
        self.assertEqual(self.smpps_factory.lastProto.PDUReceived.call_args_list[1][0][0].params['destination_addr'],
                            self.SubmitSmPDU.params['destination_addr'])
        self.assertEqual(self.smpps_factory.lastProto.PDUReceived.call_args_list[1][0][0].params['short_message'],
                            self.SubmitSmPDU.params['short_message'])
        # Asserts SMPPClient side
        self.assertEqual(self.smppc_factory.lastProto.PDUReceived.call_count, 3)
        self.assertEqual(self.smppc_factory.lastProto.PDUReceived.call_args_list[0][0][0].params['source_addr'],
                            self.DeliverSmPDU.params['source_addr'])
        self.assertEqual(self.smppc_factory.lastProto.PDUReceived.call_args_list[0][0][0].params['destination_addr'],
                            self.DeliverSmPDU.params['destination_addr'])
        self.assertEqual(self.smppc_factory.lastProto.PDUReceived.call_args_list[0][0][0].params['short_message'],
                            self.DeliverSmPDU.params['short_message'])

    @defer.inlineCallbacks
    def test_messaging_rx(self):
        "Try to send a submit_sm to server when BOUND_RX"

        self.smppc_config.bindOperation = 'receiver'

        # Connect and bind
        yield self.smppc_factory.connectAndBind()
        self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.BOUND_RX)

        # SMPPClient > SMPPServer
        yield self.smppc_factory.lastProto.sendDataRequest(self.SubmitSmPDU)

        # Wait
        yield waitFor(1)

        # Assert SMPP client got rejected and connection was shutdown
        self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.NONE)

    @defer.inlineCallbacks
    def test_messaging_tx_and_rx(self):
        "Send a deliver_sm to RX client only"

        # Init a second client
        smppc2_config = copy.deepcopy(self.smppc_config)
        smppc2_factory = LastProtoSMPPClientFactory(smppc2_config)

        # First client is transmitter
        self.smppc_config.bindOperation = 'transmitter'
        # Second client is receiver
        smppc2_config.bindOperation = 'receiver'

        # Connect and bind both clients
        yield self.smppc_factory.connectAndBind()
        self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.BOUND_TX)
        yield smppc2_factory.connectAndBind()
        self.assertEqual(smppc2_factory.smpp.sessionState, SMPPSessionStates.BOUND_RX)

        # Install mockers
        self.smppc_factory.lastProto.PDUReceived = mock.Mock(wraps=self.smppc_factory.lastProto.PDUReceived)
        smppc2_factory.lastProto.PDUReceived = mock.Mock(wraps=smppc2_factory.lastProto.PDUReceived)

        # SMPPServer > SMPPClient
        yield self.smpps_factory.lastProto.sendDataRequest(self.DeliverSmPDU)

        # Wait
        yield waitFor(1)

        # Unbind & Disconnect both clients
        yield self.smppc_factory.smpp.unbindAndDisconnect()
        self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.UNBOUND)
        yield smppc2_factory.smpp.unbindAndDisconnect()
        self.assertEqual(smppc2_factory.smpp.sessionState, SMPPSessionStates.UNBOUND)

        # Asserts, DeliverSm must be sent to the BOUND_RX client
        self.assertEqual(self.smppc_factory.lastProto.PDUReceived.call_count, 1)
        self.assertEqual(smppc2_factory.lastProto.PDUReceived.call_count, 2)
        self.assertEqual(smppc2_factory.lastProto.PDUReceived.call_args_list[0][0][0].params['source_addr'],
                            self.DeliverSmPDU.params['source_addr'])
        self.assertEqual(smppc2_factory.lastProto.PDUReceived.call_args_list[0][0][0].params['destination_addr'],
                            self.DeliverSmPDU.params['destination_addr'])
        self.assertEqual(smppc2_factory.lastProto.PDUReceived.call_args_list[0][0][0].params['short_message'],
                            self.DeliverSmPDU.params['short_message'])

    @defer.inlineCallbacks
    def test_deliver_sm_from_smpps(self):
        "This test case will ensure smpps will never accept deliver_sm for delivery"

        # Connect and bind
        yield self.smppc_factory.connectAndBind()
        self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.BOUND_TRX)

        raised = False
        try:
            # SMPPClient > SMPPServer
            yield self.smppc_factory.lastProto.sendDataRequest(self.DeliverSmPDU)
        except Exception, e:
            if isinstance(e, SMPPTransactionError):
                raised = True
        self.assertTrue(raised)

        # Wait
        yield waitFor(1)

        self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.NONE)

class InactivityTestCases(SMPPClientTestCases):

    @defer.inlineCallbacks
    def test_server_unbind_after_inactivity(self):
        """Server will send an unbind request to client when inactivity
        is detected
        """

        self.smppc_config.enquireLinkTimerSecs = 10
        self.smpps_config.inactivityTimerSecs = 2

        # Connect and bind
        yield self.smppc_factory.connectAndBind()
        self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.BOUND_TRX)

        # Wait
        yield waitFor(3)

        self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.NONE)

    @defer.inlineCallbacks
    def test_client_hanging(self):
        """Server will send an unbind request to client when inactivity
        is detected, in this test case the client will not respond, simulating
        a hanging or a network lag
        """

        self.smppc_config.enquireLinkTimerSecs = 10
        self.smpps_config.inactivityTimerSecs = 2
        self.smpps_config.sessionInitTimerSecs = 1

        # Connect and bind
        yield self.smppc_factory.connectAndBind()
        self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.BOUND_TRX)

        # Client's PDURequestReceived() will do nothing when receiving any reqPDU
        self.smppc_factory.lastProto.PDURequestReceived = lambda reqPDU: None
        # Client's sendRequest() will send nothing starting from this moment
        self.smppc_factory.lastProto.sendRequest = lambda pdu, timeout: defer.Deferred()

        # Wait
        yield waitFor(4)

        self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.NONE)

class UserCnxStatusTestCases(SMPPClientTestCases):

    def setUp(self):
        SMPPClientTestCases.setUp(self)

        # Provision a user and default route into RouterPB
        # Add throughput limit on user
        self.foo = User('u1', Group('test'), 'username', 'password')
        self.foo.mt_credential.setQuota('smpps_throughput', 2)
        self.c1 = SmppClientConnector(id_generator())
        self.defaultroute = DefaultRoute(self.c1)
        self.provision_user_defaultroute(user = self.foo, defaultroute = self.defaultroute)

        self.user = self.routerpb_factory.getUser('u1')

    @defer.inlineCallbacks
    def test_smpps_binds_count(self):
        # User have never binded
        _bind_count = self.user.getCnxStatus().smpps['bind_count']
        _unbind_count = self.user.getCnxStatus().smpps['unbind_count']
        _last_activity_at = self.user.getCnxStatus().smpps['last_activity_at']

        # Connect and bind
        yield self.smppc_factory.connectAndBind()
        self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.BOUND_TRX)

        # One bind
        self.assertEqual(self.user.getCnxStatus().smpps['bind_count'], _bind_count+1)
        self.assertEqual(self.user.getCnxStatus().smpps['unbind_count'], _unbind_count+0)
        self.assertApproximates(datetime.now(),
                                self.user.getCnxStatus().smpps['last_activity_at'],
                                timedelta( seconds = 0.1 ))

        # Unbind & Disconnect
        yield self.smppc_factory.smpp.unbindAndDisconnect()
        self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.UNBOUND)

        # Still one bind
        self.assertEqual(self.user.getCnxStatus().smpps['bind_count'], _bind_count+1)
        self.assertEqual(self.user.getCnxStatus().smpps['unbind_count'], _unbind_count+1)
        self.assertApproximates(datetime.now(),
                                self.user.getCnxStatus().smpps['last_activity_at'],
                                timedelta( seconds = 0.1 ))

    @defer.inlineCallbacks
    def test_smpps_unbind_count_connection_loss(self):
        """Check if the counter is decremented when connection is dropped
        without issuing an unbind request, this test will wait for 1s to
        let the server detect the connection loss and act accordingly"""
        _unbind_count = self.user.getCnxStatus().smpps['unbind_count']

        # Connect and bind
        yield self.smppc_factory.connectAndBind()
        self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.BOUND_TRX)

        # Disconnect without issuing an unbind request
        self.smppc_factory.smpp.transport.abortConnection()

        # Wait for 1s
        yield waitFor(1)

        # Unbind were triggered on server side
        self.assertEqual(self.user.getCnxStatus().smpps['unbind_count'], _unbind_count+1)

    @defer.inlineCallbacks
    def test_smpps_bound_trx(self):
        # User have never binded
        self.assertEqual(self.user.getCnxStatus().smpps['bound_connections_count'], {'bind_transmitter': 0,
                                                                                'bind_receiver': 0,
                                                                                'bind_transceiver': 0,})

        # Connect and bind
        yield self.smppc_factory.connectAndBind()
        self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.BOUND_TRX)

        # One bind
        self.assertEqual(self.user.getCnxStatus().smpps['bound_connections_count'], {'bind_transmitter': 0,
                                                                                'bind_receiver': 0,
                                                                                'bind_transceiver': 1,})
        self.assertApproximates(datetime.now(),
                                self.user.getCnxStatus().smpps['last_activity_at'],
                                timedelta( seconds = 0.1 ))

        # Unbind & Disconnect
        yield self.smppc_factory.smpp.unbindAndDisconnect()
        self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.UNBOUND)

        # Still one bind
        self.assertEqual(self.user.getCnxStatus().smpps['bound_connections_count'], {'bind_transmitter': 0,
                                                                                'bind_receiver': 0,
                                                                                'bind_transceiver': 0,})
        self.assertApproximates(datetime.now(),
                                self.user.getCnxStatus().smpps['last_activity_at'],
                                timedelta( seconds = 0.1 ))

    @defer.inlineCallbacks
    def test_smpps_bound_rx(self):
        self.smppc_config.bindOperation = 'receiver'

        # User have never binded
        self.assertEqual(self.user.getCnxStatus().smpps['bound_connections_count'], {'bind_transmitter': 0,
                                                                                'bind_receiver': 0,
                                                                                'bind_transceiver': 0,})

        # Connect and bind
        yield self.smppc_factory.connectAndBind()
        self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.BOUND_RX)

        # One bind
        self.assertEqual(self.user.getCnxStatus().smpps['bound_connections_count'], {'bind_transmitter': 0,
                                                                                'bind_receiver': 1,
                                                                                'bind_transceiver': 0,})
        self.assertApproximates(datetime.now(),
                                self.user.getCnxStatus().smpps['last_activity_at'],
                                timedelta( seconds = 0.1 ))

        # Unbind & Disconnect
        yield self.smppc_factory.smpp.unbindAndDisconnect()
        self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.UNBOUND)

        # Still one bind
        self.assertEqual(self.user.getCnxStatus().smpps['bound_connections_count'], {'bind_transmitter': 0,
                                                                                'bind_receiver': 0,
                                                                                'bind_transceiver': 0,})
        self.assertApproximates(datetime.now(),
                                self.user.getCnxStatus().smpps['last_activity_at'],
                                timedelta( seconds = 0.1 ))

    @defer.inlineCallbacks
    def test_smpps_bound_tx(self):
        self.smppc_config.bindOperation = 'transmitter'

        # User have never binded
        self.assertEqual(self.user.getCnxStatus().smpps['bound_connections_count'], {'bind_transmitter': 0,
                                                                                'bind_receiver': 0,
                                                                                'bind_transceiver': 0,})

        # Connect and bind
        yield self.smppc_factory.connectAndBind()
        self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.BOUND_TX)

        # One bind
        self.assertEqual(self.user.getCnxStatus().smpps['bound_connections_count'], {'bind_transmitter': 1,
                                                                                'bind_receiver': 0,
                                                                                'bind_transceiver': 0,})
        self.assertApproximates(datetime.now(),
                                self.user.getCnxStatus().smpps['last_activity_at'],
                                timedelta( seconds = 0.1 ))

        # Unbind & Disconnect
        yield self.smppc_factory.smpp.unbindAndDisconnect()
        self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.UNBOUND)

        # Still one bind
        self.assertEqual(self.user.getCnxStatus().smpps['bound_connections_count'], {'bind_transmitter': 0,
                                                                                'bind_receiver': 0,
                                                                                'bind_transceiver': 0,})
        self.assertApproximates(datetime.now(),
                                self.user.getCnxStatus().smpps['last_activity_at'],
                                timedelta( seconds = 0.1 ))

    @defer.inlineCallbacks
    def test_enquire_link_set_last_activity(self):
        self.smppc_config.enquireLinkTimerSecs = 1

        # Connect and bind
        yield self.smppc_factory.connectAndBind()
        self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.BOUND_TRX)

        # Wait
        yield waitFor(5)

        self.assertApproximates(datetime.now(),
                                self.user.getCnxStatus().smpps['last_activity_at'],
                                timedelta( seconds = 1 ))

        # Unbind & Disconnect
        yield self.smppc_factory.smpp.unbindAndDisconnect()
        self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.UNBOUND)

    @defer.inlineCallbacks
    def test_submit_sm_set_last_activity(self):
        # Connect and bind
        yield self.smppc_factory.connectAndBind()
        self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.BOUND_TRX)

        # Wait
        yield waitFor(3)

        # SMPPClient > SMPPServer
        yield self.smppc_factory.lastProto.sendDataRequest(self.SubmitSmPDU)

        self.assertApproximates(datetime.now(),
                                self.user.getCnxStatus().smpps['last_activity_at'],
                                timedelta( seconds = 1 ))

        # Unbind & Disconnect
        yield self.smppc_factory.smpp.unbindAndDisconnect()
        self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.UNBOUND)

    @defer.inlineCallbacks
    def test_submit_sm_request_count(self):
        # Connect and bind
        yield self.smppc_factory.connectAndBind()
        self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.BOUND_TRX)

        # Save the 'before' value
        _submit_sm_request_count = self.user.getCnxStatus().smpps['submit_sm_request_count']

        # SMPPClient > SMPPServer
        yield self.smppc_factory.lastProto.sendDataRequest(self.SubmitSmPDU)

        # Assert after
        self.assertEqual(self.user.getCnxStatus().smpps['submit_sm_request_count'], _submit_sm_request_count+1)

        # Unbind & Disconnect
        yield self.smppc_factory.smpp.unbindAndDisconnect()
        self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.UNBOUND)

    @defer.inlineCallbacks
    def test_elink_count(self):
        self.smppc_config.enquireLinkTimerSecs = 1

        # Save the 'before' value
        _elink_count = self.user.getCnxStatus().smpps['elink_count']

        # Connect and bind
        yield self.smppc_factory.connectAndBind()
        self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.BOUND_TRX)

        # Wait
        yield waitFor(5)

        # Unbind & Disconnect
        yield self.smppc_factory.smpp.unbindAndDisconnect()
        self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.UNBOUND)

        # Asserts
        self.assertEqual(_elink_count+4, self.user.getCnxStatus().smpps['elink_count'])

    @defer.inlineCallbacks
    def test_throttling_error_count(self):
        """In this test it is demonstrated the
        difference between submit_sm_request_count and submit_sm_count:
        * submit_sm_request_count: is the number of submit_sm requested by user
        * submit_sm_count: is number of submit_sm accepted from him
        """

        # Connect and bind
        yield self.smppc_factory.connectAndBind()
        self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.BOUND_TRX)

        # Save the 'before' value
        _submit_sm_request_count = self.user.getCnxStatus().smpps['submit_sm_request_count']
        _throttling_error_count = self.user.getCnxStatus().smpps['throttling_error_count']
        _other_submit_error_count = self.user.getCnxStatus().smpps['other_submit_error_count']
        _submit_sm_count = self.user.getCnxStatus().smpps['submit_sm_count']

        # SMPPClient > SMPPServer
        for _ in range(50):
            yield self.smppc_factory.lastProto.sendDataRequest(self.SubmitSmPDU)
            yield waitFor(0.1)

        # Assert after
        self.assertEqual(self.user.getCnxStatus().smpps['submit_sm_request_count'], _submit_sm_request_count+50)
        self.assertEqual(self.user.getCnxStatus().smpps['other_submit_error_count'], _other_submit_error_count)
        self.assertLess(self.user.getCnxStatus().smpps['throttling_error_count'], _submit_sm_request_count+50)
        self.assertGreater(self.user.getCnxStatus().smpps['throttling_error_count'], 0)
        self.assertGreater(self.user.getCnxStatus().smpps['submit_sm_count'], _submit_sm_count)

        # Unbind & Disconnect
        yield self.smppc_factory.smpp.unbindAndDisconnect()
        self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.UNBOUND)

    @defer.inlineCallbacks
    def test_other_submit_error_count(self):
        "Send a submit_sm wile bound_rx: will get a resp with ESME_RINVBNDSTS"

        self.smppc_config.bindOperation = 'receiver'

        # Connect and bind
        yield self.smppc_factory.connectAndBind()
        self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.BOUND_RX)

        # Save the 'before' value
        _other_submit_error_count = self.user.getCnxStatus().smpps['other_submit_error_count']
        _throttling_error_count = self.user.getCnxStatus().smpps['throttling_error_count']

        # SMPPClient > SMPPServer
        yield self.smppc_factory.lastProto.sendDataRequest(self.SubmitSmPDU)

        # Assert after
        self.assertEqual(self.user.getCnxStatus().smpps['other_submit_error_count'], _other_submit_error_count+1)
        self.assertEqual(self.user.getCnxStatus().smpps['throttling_error_count'], _throttling_error_count)

        # Unbind & Disconnect
        yield self.smppc_factory.smpp.unbindAndDisconnect()
        self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.UNBOUND)

    @defer.inlineCallbacks
    def test_deliver_sm_count(self):
        # Connect and bind
        yield self.smppc_factory.connectAndBind()
        self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.BOUND_TRX)

        # Save the 'before' value
        _deliver_sm_count = self.user.getCnxStatus().smpps['deliver_sm_count']

        # SMPPServer > SMPPClient
        yield self.smpps_factory.lastProto.sendDataRequest(self.DeliverSmPDU)

        # Assert after
        self.assertEqual(self.user.getCnxStatus().smpps['deliver_sm_count'], _deliver_sm_count+1)

        # Unbind & Disconnect
        yield self.smppc_factory.smpp.unbindAndDisconnect()
        self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.UNBOUND)

    @defer.inlineCallbacks
    def test_data_sm_count(self):
        # Connect and bind
        yield self.smppc_factory.connectAndBind()
        self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.BOUND_TRX)

        # Save the 'before' value
        _data_sm_count = self.user.getCnxStatus().smpps['data_sm_count']

        # SMPPServer > SMPPClient
        yield self.smpps_factory.lastProto.sendDataRequest(self.DataSmPDU)

        # Assert after
        self.assertEqual(self.user.getCnxStatus().smpps['data_sm_count'], _data_sm_count+1)

        # Unbind & Disconnect
        yield self.smppc_factory.smpp.unbindAndDisconnect()
        self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.UNBOUND)

class SmppsStatsTestCases(SMPPClientTestCases):

    def setUp(self):
        SMPPClientTestCases.setUp(self)

        # Provision a user and default route into RouterPB
        # Add throughput limit on user
        self.foo = User('u1', Group('test'), 'username', 'password')
        self.foo.mt_credential.setQuota('smpps_throughput', 2)
        self.c1 = SmppClientConnector(id_generator())
        self.defaultroute = DefaultRoute(self.c1)
        self.provision_user_defaultroute(user = self.foo, defaultroute = self.defaultroute)

        self.stats = SMPPServerStatsCollector().get(cid = self.smpps_config.id)

    @defer.inlineCallbacks
    def test_elink_count(self):
        self.smppc_config.enquireLinkTimerSecs = 1

        # Save the 'before' value
        _elink_count = self.stats.get('elink_count')

        # Connect and bind
        yield self.smppc_factory.connectAndBind()
        self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.BOUND_TRX)

        # Wait
        yield waitFor(5)

        # Unbind & Disconnect
        yield self.smppc_factory.smpp.unbindAndDisconnect()
        self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.UNBOUND)

        # Asserts
        self.assertEqual(_elink_count+4, self.stats.get('elink_count'))

    @defer.inlineCallbacks
    def test_throttling_error_count(self):
        """In this test it is demonstrated the
        difference between submit_sm_request_count and submit_sm_count:
        * submit_sm_request_count: is the number of submit_sm requested
        * submit_sm_count: is number of submit_sm accepted (replied with ESME_ROK)
        """

        # Connect and bind
        yield self.smppc_factory.connectAndBind()
        self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.BOUND_TRX)

        # Save the 'before' value
        _submit_sm_request_count = self.stats.get('submit_sm_request_count')
        _throttling_error_count = self.stats.get('throttling_error_count')
        _other_submit_error_count = self.stats.get('other_submit_error_count')
        _submit_sm_count = self.stats.get('submit_sm_count')

        # SMPPClient > SMPPServer
        for _ in range(50):
            yield self.smppc_factory.lastProto.sendDataRequest(self.SubmitSmPDU)
            yield waitFor(0.1)

        # Assert after
        self.assertEqual(self.stats.get('submit_sm_request_count'), _submit_sm_request_count+50)
        self.assertEqual(self.stats.get('other_submit_error_count'), _other_submit_error_count)
        self.assertLess(self.stats.get('throttling_error_count'), _submit_sm_request_count+50)
        self.assertGreater(self.stats.get('throttling_error_count'), 0)
        self.assertGreater(self.stats.get('submit_sm_count'), _submit_sm_count)

        # Unbind & Disconnect
        yield self.smppc_factory.smpp.unbindAndDisconnect()
        self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.UNBOUND)

    @defer.inlineCallbacks
    def test_other_submit_error_count(self):
        """Send a submit_sm wile bound_rx: will get a resp with ESME_RINVBNDSTS
        Will also ensure
        """

        self.smppc_config.bindOperation = 'receiver'

        # Connect and bind
        yield self.smppc_factory.connectAndBind()
        self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.BOUND_RX)

        # Save the 'before' value
        _other_submit_error_count = self.stats.get('other_submit_error_count')
        _throttling_error_count = self.stats.get('throttling_error_count')

        # SMPPClient > SMPPServer
        yield self.smppc_factory.lastProto.sendDataRequest(self.SubmitSmPDU)

        # Assert after
        self.assertEqual(self.stats.get('other_submit_error_count'), _other_submit_error_count+1)
        self.assertEqual(self.stats.get('throttling_error_count'), _throttling_error_count)

        # Unbind & Disconnect
        yield self.smppc_factory.smpp.unbindAndDisconnect()
        self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.UNBOUND)

    @defer.inlineCallbacks
    def test_deliver_sm_count(self):
        # Connect and bind
        yield self.smppc_factory.connectAndBind()
        self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.BOUND_TRX)

        # Save the 'before' value
        _deliver_sm_count = self.stats.get('deliver_sm_count')

        # SMPPServer > SMPPClient
        yield self.smpps_factory.lastProto.sendDataRequest(self.DeliverSmPDU)

        # Assert after
        self.assertEqual(self.stats.get('deliver_sm_count'), _deliver_sm_count+1)

        # Unbind & Disconnect
        yield self.smppc_factory.smpp.unbindAndDisconnect()
        self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.UNBOUND)

    @defer.inlineCallbacks
    def test_data_sm_count(self):
        # Connect and bind
        yield self.smppc_factory.connectAndBind()
        self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.BOUND_TRX)

        # Save the 'before' value
        _data_sm_count = self.stats.get('data_sm_count')

        # SMPPServer > SMPPClient
        yield self.smpps_factory.lastProto.sendDataRequest(self.DataSmPDU)

        # Assert after
        self.assertEqual(self.stats.get('data_sm_count'), _data_sm_count+1)

        # Unbind & Disconnect
        yield self.smppc_factory.smpp.unbindAndDisconnect()
        self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.UNBOUND)

class StatsTestCases(SMPPClientTestCases):

    def setUp(self):
        SMPPClientTestCases.setUp(self)

        # Re-init stats singleton collector
        created_at = SMPPServerStatsCollector().get(cid = self.smpps_config.id).get('created_at')
        SMPPServerStatsCollector().get(cid = self.smpps_config.id).init()
        SMPPServerStatsCollector().get(cid = self.smpps_config.id).set('created_at', created_at)

    @defer.inlineCallbacks
    def test_01_smpps_basic(self):
        "A simple test of _some_ stats parameters"
        stats = SMPPServerStatsCollector().get(cid = self.smpps_config.id)

        self.assertTrue(type(stats.get('created_at')) == datetime)
        self.assertEqual(stats.get('last_received_pdu_at'), 0)
        self.assertEqual(stats.get('last_sent_pdu_at'), 0)
        self.assertEqual(stats.get('connected_count'), 0)
        self.assertEqual(stats.get('connect_count'), 0)
        self.assertEqual(stats.get('disconnect_count'), 0)
        self.assertEqual(stats.get('bound_trx_count'), 0)
        self.assertEqual(stats.get('bound_rx_count'), 0)
        self.assertEqual(stats.get('bound_tx_count'), 0)
        self.assertEqual(stats.get('bind_trx_count'), 0)
        self.assertEqual(stats.get('bind_rx_count'), 0)
        self.assertEqual(stats.get('bind_tx_count'), 0)
        self.assertEqual(stats.get('unbind_count'), 0)

        # Connect and bind
        yield self.smppc_factory.connectAndBind()
        self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.BOUND_TRX)

        self.assertTrue(type(stats.get('created_at')) == datetime)
        self.assertTrue(type(stats.get('last_received_pdu_at')) == datetime)
        self.assertTrue(type(stats.get('last_sent_pdu_at')) == datetime)
        self.assertEqual(stats.get('connected_count'), 1)
        self.assertEqual(stats.get('connect_count'), 1)
        self.assertEqual(stats.get('disconnect_count'), 0)
        self.assertEqual(stats.get('bound_trx_count'), 1)
        self.assertEqual(stats.get('bound_rx_count'), 0)
        self.assertEqual(stats.get('bound_tx_count'), 0)
        self.assertEqual(stats.get('bind_trx_count'), 1)
        self.assertEqual(stats.get('bind_rx_count'), 0)
        self.assertEqual(stats.get('bind_tx_count'), 0)
        self.assertEqual(stats.get('unbind_count'), 0)

        # Unbind & Disconnect
        yield self.smppc_factory.smpp.unbindAndDisconnect()
        self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.UNBOUND)

        self.assertTrue(type(stats.get('created_at')) == datetime)
        self.assertTrue(type(stats.get('last_received_pdu_at')) == datetime)
        self.assertTrue(type(stats.get('last_sent_pdu_at')) == datetime)
        self.assertEqual(stats.get('connected_count'), 0)
        self.assertEqual(stats.get('connect_count'), 1)
        self.assertEqual(stats.get('disconnect_count'), 1)
        self.assertEqual(stats.get('bound_trx_count'), 0)
        self.assertEqual(stats.get('bound_rx_count'), 0)
        self.assertEqual(stats.get('bound_tx_count'), 0)
        self.assertEqual(stats.get('bind_trx_count'), 1)
        self.assertEqual(stats.get('bind_rx_count'), 0)
        self.assertEqual(stats.get('bind_tx_count'), 0)
        self.assertEqual(stats.get('unbind_count'), 1)

    @defer.inlineCallbacks
    def test_02_enquire_link(self):
        self.smppc_config.enquireLinkTimerSecs = 1
        stats = SMPPServerStatsCollector().get(cid = self.smpps_config.id)

        self.assertEqual(stats.get('last_received_elink_at'), 0)

        # Connect and bind
        yield self.smppc_factory.connectAndBind()
        self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.BOUND_TRX)

        # Wait some secs in order to receive some elinks
        yield waitFor(6)

        # Unbind & Disconnect
        yield self.smppc_factory.smpp.unbindAndDisconnect()
        self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.UNBOUND)

        self.assertTrue(type(stats.get('last_received_elink_at')) == datetime)
