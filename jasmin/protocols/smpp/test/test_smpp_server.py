"""
Test cases for smpp server
- These are test cases for only Jasmin's code, smpp.twisted tests are not included here.
- These are test done through real tcp network (smpp.twisted tests are done through 
	proto_helpers.StringTransport())
"""

import logging
import pickle
import mock
import copy
from datetime import datetime, timedelta
from twisted.internet import reactor, defer
from jasmin.protocols.smpp.protocol import *
from twisted.trial.unittest import TestCase
from twisted.internet.protocol import Factory 
from zope.interface import implements
from twisted.cred import portal
from jasmin.tools.cred.portal import SmppsRealm
from jasmin.tools.cred.checkers import RouterAuthChecker
from jasmin.protocols.smpp.configs import SMPPServerConfig, SMPPClientConfig
from jasmin.protocols.smpp.factory import SMPPServerFactory, SMPPClientFactory
from jasmin.managers.clients import SMPPClientManagerPB
from jasmin.managers.configs import SMPPClientPBConfig
from jasmin.protocols.smpp.protocol import *
from jasmin.routing.router import RouterPB
from jasmin.routing.configs import RouterPBConfig
from jasmin.routing.jasminApi import User, Group, SmppClientConnector
from jasmin.vendor.smpp.pdu import pdu_types
from jasmin.vendor.smpp.pdu.error import SMPPTransactionError
from jasmin.routing.Routes import DefaultRoute
from jasmin.routing.test.test_router import id_generator

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
		self.routerpb_factory = RouterPB()
		self.routerpb_factory.setConfig(self.routerpb_config, persistenceTimer = False)

		# Provision a user and default route into RouterPB
		self.foo = User('u1', Group('test'), 'username', 'password')
		self.c1 = SmppClientConnector(id_generator())
		self.defaultroute = DefaultRoute(self.c1)
		self.provision_user_defaultroute(user = self.foo, defaultroute = self.defaultroute)

	def provision_user_defaultroute(self, user, defaultroute = None):
		# This is normally done through jcli API (or any other high level API to come)
		# Using perspective_user_add() is just a shortcut for testing purposes
		if user.group not in self.routerpb_factory.groups:
			self.routerpb_factory.perspective_group_add(pickle.dumps(user.group))
		self.routerpb_factory.perspective_user_add(pickle.dumps(user))

		# provision route
		if defaultroute is not None:
			self.routerpb_factory.perspective_mtroute_add(pickle.dumps(defaultroute), 0)

class SMPPServerTestCases(RouterPBTestCases):
	port = 27750

	def setUp(self):
		RouterPBTestCases.setUp(self)

		# SMPPServerConfig init
		args = {'id': 'smpps_01_%s' % self.port, 'port': self.port, 
				'log_level': logging.DEBUG}
		self.smpps_config = SMPPServerConfig(**args)

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

		# SMPPClientConfig init
		args = {'id': 'smppc_01', 'port': self.port,
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
	def test_bind_max_bindings_limit(self):
		user = self.routerpb_factory.getUser('u1')
		user.smpps_credential.setQuota('max_bindings', 0)

		# Connect and bind
		yield self.smppc_factory.connectAndBind()
		self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.UNBOUND)

	@defer.inlineCallbacks
	def test_bind_max_bind_authorization(self):
		user = self.routerpb_factory.getUser('u1')
		user.smpps_credential.setAuthorization('bind', False)

		# Connect and bind
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
		waitDeferred = defer.Deferred()
		reactor.callLater(1, waitDeferred.callback, None)
		yield waitDeferred

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
		waitDeferred = defer.Deferred()
		reactor.callLater(1, waitDeferred.callback, None)
		yield waitDeferred

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
		waitDeferred = defer.Deferred()
		reactor.callLater(1, waitDeferred.callback, None)
		yield waitDeferred

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
		waitDeferred = defer.Deferred()
		reactor.callLater(3, waitDeferred.callback, None)
		yield waitDeferred

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
		waitDeferred = defer.Deferred()
		reactor.callLater(4, waitDeferred.callback, None)
		yield waitDeferred

		self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.NONE)

class UserCnxStatusTestCases(SMPPClientTestCases):

	def setUp(self):
		SMPPClientTestCases.setUp(self)

		self.user = self.routerpb_factory.getUser('u1')		

	@defer.inlineCallbacks
	def test_smpps_binds_count(self):
		# User have never binded
		self.assertEqual(self.user.CnxStatus.smpps['bind_count'], 0)
		self.assertEqual(self.user.CnxStatus.smpps['unbind_count'], 0)
		self.assertEqual(self.user.CnxStatus.smpps['last_activity_at'], 0)

		# Connect and bind
		yield self.smppc_factory.connectAndBind()
		self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.BOUND_TRX)

		# One bind
		self.assertEqual(self.user.CnxStatus.smpps['bind_count'], 1)
		self.assertEqual(self.user.CnxStatus.smpps['unbind_count'], 0)
		self.assertApproximates(datetime.now(), 
								self.user.CnxStatus.smpps['last_activity_at'], 
								timedelta( seconds = 0.1 ))

		# Unbind & Disconnect
 		yield self.smppc_factory.smpp.unbindAndDisconnect()
		self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.UNBOUND)

		# Still one bind
		self.assertEqual(self.user.CnxStatus.smpps['bind_count'], 1)
		self.assertEqual(self.user.CnxStatus.smpps['unbind_count'], 1)
		self.assertApproximates(datetime.now(), 
								self.user.CnxStatus.smpps['last_activity_at'], 
								timedelta( seconds = 0.1 ))

	@defer.inlineCallbacks
	def test_smpps_unbind_count_connection_loss(self):
		"""Check if the counter is decremented when connection is dropped
		without issuing an unbind request, this test will wait for 1s to
		let the server detect the connection loss and act accordingly"""
		# Connect and bind
		yield self.smppc_factory.connectAndBind()
		self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.BOUND_TRX)

		# Disconnect without issuing an unbind request
 		self.smppc_factory.smpp.transport.abortConnection()

 		# Wait for 1s
		waitDeferred = defer.Deferred()
		reactor.callLater(1, waitDeferred.callback, None)
		yield waitDeferred

		# Unbind were triggered on server side
		self.assertEqual(self.user.CnxStatus.smpps['unbind_count'], 1)

	@defer.inlineCallbacks
	def test_smpps_bound_trx(self):
		# User have never binded
		self.assertEqual(self.user.CnxStatus.smpps['bound_connections_count'], {'bind_transmitter': 0,
																				'bind_receiver': 0,
																				'bind_transceiver': 0,})
		self.assertEqual(self.user.CnxStatus.smpps['last_activity_at'], 0)

		# Connect and bind
		yield self.smppc_factory.connectAndBind()
		self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.BOUND_TRX)

		# One bind
		self.assertEqual(self.user.CnxStatus.smpps['bound_connections_count'], {'bind_transmitter': 0,
																				'bind_receiver': 0,
																				'bind_transceiver': 1,})
		self.assertApproximates(datetime.now(), 
								self.user.CnxStatus.smpps['last_activity_at'], 
								timedelta( seconds = 0.1 ))

		# Unbind & Disconnect
 		yield self.smppc_factory.smpp.unbindAndDisconnect()
		self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.UNBOUND)

		# Still one bind
		self.assertEqual(self.user.CnxStatus.smpps['bound_connections_count'], {'bind_transmitter': 0,
																				'bind_receiver': 0,
																				'bind_transceiver': 0,})
		self.assertApproximates(datetime.now(), 
								self.user.CnxStatus.smpps['last_activity_at'], 
								timedelta( seconds = 0.1 ))

	@defer.inlineCallbacks
	def test_smpps_bound_rx(self):
		self.smppc_config.bindOperation = 'receiver'

		# User have never binded
		self.assertEqual(self.user.CnxStatus.smpps['bound_connections_count'], {'bind_transmitter': 0,
																				'bind_receiver': 0,
																				'bind_transceiver': 0,})
		self.assertEqual(self.user.CnxStatus.smpps['last_activity_at'], 0)

		# Connect and bind
		yield self.smppc_factory.connectAndBind()
		self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.BOUND_RX)

		# One bind
		self.assertEqual(self.user.CnxStatus.smpps['bound_connections_count'], {'bind_transmitter': 0,
																				'bind_receiver': 1,
																				'bind_transceiver': 0,})
		self.assertApproximates(datetime.now(), 
								self.user.CnxStatus.smpps['last_activity_at'], 
								timedelta( seconds = 0.1 ))

		# Unbind & Disconnect
 		yield self.smppc_factory.smpp.unbindAndDisconnect()
		self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.UNBOUND)

		# Still one bind
		self.assertEqual(self.user.CnxStatus.smpps['bound_connections_count'], {'bind_transmitter': 0,
																				'bind_receiver': 0,
																				'bind_transceiver': 0,})
		self.assertApproximates(datetime.now(), 
								self.user.CnxStatus.smpps['last_activity_at'], 
								timedelta( seconds = 0.1 ))

	@defer.inlineCallbacks
	def test_smpps_bound_tx(self):
		self.smppc_config.bindOperation = 'transmitter'

		# User have never binded
		self.assertEqual(self.user.CnxStatus.smpps['bound_connections_count'], {'bind_transmitter': 0,
																				'bind_receiver': 0,
																				'bind_transceiver': 0,})
		self.assertEqual(self.user.CnxStatus.smpps['last_activity_at'], 0)

		# Connect and bind
		yield self.smppc_factory.connectAndBind()
		self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.BOUND_TX)

		# One bind
		self.assertEqual(self.user.CnxStatus.smpps['bound_connections_count'], {'bind_transmitter': 1,
																				'bind_receiver': 0,
																				'bind_transceiver': 0,})
		self.assertApproximates(datetime.now(), 
								self.user.CnxStatus.smpps['last_activity_at'], 
								timedelta( seconds = 0.1 ))

		# Unbind & Disconnect
 		yield self.smppc_factory.smpp.unbindAndDisconnect()
		self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.UNBOUND)

		# Still one bind
		self.assertEqual(self.user.CnxStatus.smpps['bound_connections_count'], {'bind_transmitter': 0,
																				'bind_receiver': 0,
																				'bind_transceiver': 0,})
		self.assertApproximates(datetime.now(), 
								self.user.CnxStatus.smpps['last_activity_at'], 
								timedelta( seconds = 0.1 ))

	@defer.inlineCallbacks
	def test_enquire_link_set_last_activity(self):
		self.smppc_config.enquireLinkTimerSecs = 1

		# Connect and bind
		yield self.smppc_factory.connectAndBind()
		self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.BOUND_TRX)

 		# Wait
		waitDeferred = defer.Deferred()
		reactor.callLater(5, waitDeferred.callback, None)
		yield waitDeferred

		self.assertApproximates(datetime.now(), 
								self.user.CnxStatus.smpps['last_activity_at'], 
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
		waitDeferred = defer.Deferred()
		reactor.callLater(3, waitDeferred.callback, None)
		yield waitDeferred

		# SMPPClient > SMPPServer
		yield self.smppc_factory.lastProto.sendDataRequest(self.SubmitSmPDU)

		self.assertApproximates(datetime.now(), 
								self.user.CnxStatus.smpps['last_activity_at'], 
								timedelta( seconds = 1 ))

		# Unbind & Disconnect
 		yield self.smppc_factory.smpp.unbindAndDisconnect()
		self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.UNBOUND)

	@defer.inlineCallbacks
	def test_submit_sm_request_count(self):
		# Connect and bind
		yield self.smppc_factory.connectAndBind()
		self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.BOUND_TRX)

		# Assert before
		self.assertEqual(self.user.CnxStatus.smpps['submit_sm_request_count'], 0)

		# SMPPClient > SMPPServer
		yield self.smppc_factory.lastProto.sendDataRequest(self.SubmitSmPDU)

		# Assert after
		self.assertEqual(self.user.CnxStatus.smpps['submit_sm_request_count'], 1)

		# Unbind & Disconnect
 		yield self.smppc_factory.smpp.unbindAndDisconnect()
		self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.UNBOUND)