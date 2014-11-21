"""
Test cases for smpp server
- These are test cases for only Jasmin's code, smpp.twisted tests are not included here.
- These are test done through real tcp network (smpp.twisted tests are done through 
	proto_helpers.StringTransport())
"""

import logging
import pickle
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
from jasmin.protocols.smpp.protocol import *
from jasmin.routing.router import RouterPB
from jasmin.routing.configs import RouterPBConfig
from jasmin.routing.jasminApi import User, Group

class RouterPBTestCases(TestCase):
	def provision_new_user(self, user):
		# This is normally done through jcli API (or any other high level API to come)
		# Using perspective_user_add() is just a shortcut for testing purposes
		if user.group not in self.router_factory.groups:
			self.router_factory.perspective_group_add(pickle.dumps(user.group))
		self.router_factory.perspective_user_add(pickle.dumps(user))

	def setUp(self):
		# Initiating config objects without any filename
		# will lead to setting defaults and that's what we
		# need to run the tests
		self.routerpb_config = RouterPBConfig()
		
		# Instanciate RouterPB but will not launch a server
		# we only need the instance to access its .users attribute
		# for authentication
		self.router_factory = RouterPB()
		self.router_factory.setConfig(self.routerpb_config, persistenceTimer = False)

		# Provision a user into router
		u1 = User('u1', Group('test'), 'foo', 'bar')
		self.provision_new_user(u1)

class SMPPServerTestCases(RouterPBTestCases):
	port = 27750

	def setUp(self):
		RouterPBTestCases.setUp(self)

		# SMPPServerConfig init
		args = {'id': 'smpps_01_%s' % self.port, 'port': self.port, 
				'systems': {'foo': {"max_bindings": 2}},
				'log_level': logging.DEBUG}
		self.smpps_config = SMPPServerConfig(**args)

		# Portal init
		_portal = portal.Portal(SmppsRealm(self.smpps_config.id, self.router_factory))
		_portal.registerChecker(RouterAuthChecker(self.router_factory))

		# SMPPServerFactory init
		self.smpps_factory = SMPPServerFactory(self.smpps_config, auth_portal=_portal)
		self.smpps_port = reactor.listenTCP(self.smpps_config.port, self.smpps_factory)

	@defer.inlineCallbacks
	def tearDown(self):
		yield self.smpps_port.stopListening()

class SMPPClientTestCases(SMPPServerTestCases):

	def setUp(self):
		SMPPServerTestCases.setUp(self)

		# SMPPClientConfig init
		args = {'id': 'smppc_01', 'port': self.port,
				'log_level': logging.DEBUG, 
				'reconnectOnConnectionLoss': False,
				'username': 'foo', 'password': 'bar'}
		self.smppc_config = SMPPClientConfig(**args)

		# SMPPClientFactory init
		self.smppc_factory = SMPPClientFactory(self.smppc_config)

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
		self.smpps_config.systems['foo']['max_bindings'] = 0

		# Connect and bind
		yield self.smppc_factory.connectAndBind()
		self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.UNBOUND)