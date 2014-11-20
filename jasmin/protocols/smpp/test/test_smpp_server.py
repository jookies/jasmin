"""
Test cases for smpp server
- These are test cases for only Jasmin's code, smpp.twisted tests are not included here.
- These are test done through real tcp network (smpp.twisted tests are done through 
	proto_helpers.StringTransport())
"""

import logging
from twisted.internet import reactor, defer
from jasmin.protocols.smpp.protocol import *
from twisted.trial.unittest import TestCase
from twisted.internet.protocol import Factory 
from twisted.cred.portal import Portal
from twisted.cred.portal import IRealm
from twisted.cred.checkers import InMemoryUsernamePasswordDatabaseDontUse
from zope.interface import implements
from jasmin.protocols.smpp.configs import SMPPServerConfig, SMPPClientConfig
from jasmin.protocols.smpp.factory import SMPPServerFactory, SMPPClientFactory
from jasmin.protocols.smpp.protocol import *

class SMPPServerTestCases(TestCase):
	port = 27750

	class SmppRealm(object):
		implements(IRealm)
		
		def requestAvatar(self, avatarId, mind, *interfaces):
			return ('SMPP', avatarId, lambda: None)

	def setUp(self):
		# SMPPServerConfig init
		args = {'id': 'smpps_01', 'port': self.port, 
				'systems': {'foo': {"max_bindings": 2}},
				'log_level': logging.DEBUG}
		self.smpps_config = SMPPServerConfig(**args)

		# Portal init
		portal = Portal(self.SmppRealm())
		credential_checker = InMemoryUsernamePasswordDatabaseDontUse()
		credential_checker.addUser('foo', 'bar')
		portal.registerChecker(credential_checker)

		# SMPPServerFactory init
		self.smpps_factory = SMPPServerFactory(self.smpps_config, auth_portal=portal)
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