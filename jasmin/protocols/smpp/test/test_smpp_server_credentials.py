import mock
import copy
from datetime import datetime
from twisted.internet import defer
from jasmin.protocols.smpp.test.test_smpp_server import SMPPClientTestCases
from jasmin.vendor.smpp.twisted.protocol import SMPPSessionStates
from jasmin.vendor.smpp.pdu import pdu_types
from jasmin.vendor.smpp.pdu.constants import priority_flag_value_map
from jasmin.vendor.smpp.pdu.pdu_types import RegisteredDeliveryReceipt, RegisteredDelivery

class AuthorizationsTestCases(SMPPClientTestCases):

	@defer.inlineCallbacks
	def test_authorized_smpps_send(self):
		user = self.routerpb_factory.getUser('u1')
		user.mt_credential.setAuthorization('smpps_send', True)

		# Connect and bind
		yield self.smppc_factory.connectAndBind()
		self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.BOUND_TRX)

		# Install mockers
		self.smppc_factory.lastProto.PDUReceived = mock.Mock(wraps=self.smppc_factory.lastProto.PDUReceived)

		# SMPPClient > SMPPServer
		yield self.smppc_factory.lastProto.sendDataRequest(self.SubmitSmPDU)

		# Unbind & Disconnect
 		yield self.smppc_factory.smpp.unbindAndDisconnect()
		self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.UNBOUND)

		# Asserts SMPPClient side
		self.assertEqual(self.smppc_factory.lastProto.PDUReceived.call_count, 2)
		self.assertEqual(self.smppc_factory.lastProto.PDUReceived.call_args_list[0][0][0].id, 
			pdu_types.CommandId.submit_sm_resp)
		self.assertEqual(self.smppc_factory.lastProto.PDUReceived.call_args_list[0][0][0].status, 
			pdu_types.CommandStatus.ESME_ROK)

	@defer.inlineCallbacks
	def test_nonauthorized_smpps_send(self):
		user = self.routerpb_factory.getUser('u1')
		user.mt_credential.setAuthorization('smpps_send', False)

		# Connect and bind
		yield self.smppc_factory.connectAndBind()
		self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.BOUND_TRX)

		# Install mockers
		self.smppc_factory.lastProto.PDUReceived = mock.Mock(wraps=self.smppc_factory.lastProto.PDUReceived)

		# SMPPClient > SMPPServer
		yield self.smppc_factory.lastProto.sendDataRequest(self.SubmitSmPDU)

		# Unbind & Disconnect
 		yield self.smppc_factory.smpp.unbindAndDisconnect()
		self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.UNBOUND)

		# Asserts SMPPClient side
		self.assertEqual(self.smppc_factory.lastProto.PDUReceived.call_count, 2)
		self.assertEqual(self.smppc_factory.lastProto.PDUReceived.call_args_list[0][0][0].id, 
			pdu_types.CommandId.submit_sm_resp)
		self.assertEqual(self.smppc_factory.lastProto.PDUReceived.call_args_list[0][0][0].status, 
			pdu_types.CommandStatus.ESME_RINVSYSID)

	@defer.inlineCallbacks
	def test_authorized_set_dlr_level(self):
		user = self.routerpb_factory.getUser('u1')
		user.mt_credential.setAuthorization('set_dlr_level', True)

		# Connect and bind
		yield self.smppc_factory.connectAndBind()
		self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.BOUND_TRX)

		# Install mockers
		self.smppc_factory.lastProto.PDUReceived = mock.Mock(wraps=self.smppc_factory.lastProto.PDUReceived)

		# SMPPClient > SMPPServer
		SubmitSmPDU = copy.deepcopy(self.SubmitSmPDU)
		SubmitSmPDU.params['registered_delivery'] = RegisteredDelivery(RegisteredDeliveryReceipt.SMSC_DELIVERY_RECEIPT_REQUESTED_FOR_FAILURE)
		yield self.smppc_factory.lastProto.sendDataRequest(SubmitSmPDU)

		# Unbind & Disconnect
 		yield self.smppc_factory.smpp.unbindAndDisconnect()
		self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.UNBOUND)

		# Asserts SMPPClient side
		self.assertEqual(self.smppc_factory.lastProto.PDUReceived.call_count, 2)
		self.assertEqual(self.smppc_factory.lastProto.PDUReceived.call_args_list[0][0][0].id, 
			pdu_types.CommandId.submit_sm_resp)
		self.assertEqual(self.smppc_factory.lastProto.PDUReceived.call_args_list[0][0][0].status, 
			pdu_types.CommandStatus.ESME_ROK)

	@defer.inlineCallbacks
	def test_nonauthorized_set_dlr_level(self):
		user = self.routerpb_factory.getUser('u1')
		user.mt_credential.setAuthorization('set_dlr_level', False)

		# Connect and bind
		yield self.smppc_factory.connectAndBind()
		self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.BOUND_TRX)

		# Install mockers
		self.smppc_factory.lastProto.PDUReceived = mock.Mock(wraps=self.smppc_factory.lastProto.PDUReceived)

		# SMPPClient > SMPPServer
		SubmitSmPDU = copy.deepcopy(self.SubmitSmPDU)
		SubmitSmPDU.params['registered_delivery'] = RegisteredDelivery(RegisteredDeliveryReceipt.SMSC_DELIVERY_RECEIPT_REQUESTED_FOR_FAILURE)
		yield self.smppc_factory.lastProto.sendDataRequest(SubmitSmPDU)

		# Unbind & Disconnect
 		yield self.smppc_factory.smpp.unbindAndDisconnect()
		self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.UNBOUND)

		# Asserts SMPPClient side
		self.assertEqual(self.smppc_factory.lastProto.PDUReceived.call_count, 2)
		self.assertEqual(self.smppc_factory.lastProto.PDUReceived.call_args_list[0][0][0].id, 
			pdu_types.CommandId.submit_sm_resp)
		self.assertEqual(self.smppc_factory.lastProto.PDUReceived.call_args_list[0][0][0].status, 
			pdu_types.CommandStatus.ESME_RINVSYSID)

	@defer.inlineCallbacks
	def test_authorized_set_source_address(self):
		user = self.routerpb_factory.getUser('u1')
		user.mt_credential.setAuthorization('set_source_address', True)

		# Connect and bind
		yield self.smppc_factory.connectAndBind()
		self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.BOUND_TRX)

		# Install mockers
		self.smppc_factory.lastProto.PDUReceived = mock.Mock(wraps=self.smppc_factory.lastProto.PDUReceived)

		# SMPPClient > SMPPServer
		SubmitSmPDU = copy.deepcopy(self.SubmitSmPDU)
		SubmitSmPDU.params['source_addr'] = 'DEFINED'
		yield self.smppc_factory.lastProto.sendDataRequest(SubmitSmPDU)

		# Unbind & Disconnect
 		yield self.smppc_factory.smpp.unbindAndDisconnect()
		self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.UNBOUND)

		# Asserts SMPPClient side
		self.assertEqual(self.smppc_factory.lastProto.PDUReceived.call_count, 2)
		self.assertEqual(self.smppc_factory.lastProto.PDUReceived.call_args_list[0][0][0].id, 
			pdu_types.CommandId.submit_sm_resp)
		self.assertEqual(self.smppc_factory.lastProto.PDUReceived.call_args_list[0][0][0].status, 
			pdu_types.CommandStatus.ESME_ROK)

	@defer.inlineCallbacks
	def test_nonauthorized_set_source_address(self):
		user = self.routerpb_factory.getUser('u1')
		user.mt_credential.setAuthorization('set_source_address', False)

		# Connect and bind
		yield self.smppc_factory.connectAndBind()
		self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.BOUND_TRX)

		# Install mockers
		self.smppc_factory.lastProto.PDUReceived = mock.Mock(wraps=self.smppc_factory.lastProto.PDUReceived)

		# SMPPClient > SMPPServer
		SubmitSmPDU = copy.deepcopy(self.SubmitSmPDU)
		SubmitSmPDU.params['source_addr'] = 'DEFINED'
		yield self.smppc_factory.lastProto.sendDataRequest(SubmitSmPDU)

		# Unbind & Disconnect
 		yield self.smppc_factory.smpp.unbindAndDisconnect()
		self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.UNBOUND)

		# Asserts SMPPClient side
		self.assertEqual(self.smppc_factory.lastProto.PDUReceived.call_count, 2)
		self.assertEqual(self.smppc_factory.lastProto.PDUReceived.call_args_list[0][0][0].id, 
			pdu_types.CommandId.submit_sm_resp)
		self.assertEqual(self.smppc_factory.lastProto.PDUReceived.call_args_list[0][0][0].status, 
			pdu_types.CommandStatus.ESME_RINVSYSID)

	@defer.inlineCallbacks
	def test_authorized_set_priority(self):
		user = self.routerpb_factory.getUser('u1')
		user.mt_credential.setAuthorization('set_priority', True)

		# Connect and bind
		yield self.smppc_factory.connectAndBind()
		self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.BOUND_TRX)

		# Install mockers
		self.smppc_factory.lastProto.PDUReceived = mock.Mock(wraps=self.smppc_factory.lastProto.PDUReceived)

		# SMPPClient > SMPPServer
		SubmitSmPDU = copy.deepcopy(self.SubmitSmPDU)
		SubmitSmPDU.params['priority_flag'] = priority_flag_value_map[3]
		yield self.smppc_factory.lastProto.sendDataRequest(SubmitSmPDU)

		# Unbind & Disconnect
 		yield self.smppc_factory.smpp.unbindAndDisconnect()
		self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.UNBOUND)

		# Asserts SMPPClient side
		self.assertEqual(self.smppc_factory.lastProto.PDUReceived.call_count, 2)
		self.assertEqual(self.smppc_factory.lastProto.PDUReceived.call_args_list[0][0][0].id, 
			pdu_types.CommandId.submit_sm_resp)
		self.assertEqual(self.smppc_factory.lastProto.PDUReceived.call_args_list[0][0][0].status, 
			pdu_types.CommandStatus.ESME_ROK)

	@defer.inlineCallbacks
	def test_nonauthorized_set_priority(self):
		user = self.routerpb_factory.getUser('u1')
		user.mt_credential.setAuthorization('set_priority', False)

		# Connect and bind
		yield self.smppc_factory.connectAndBind()
		self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.BOUND_TRX)

		# Install mockers
		self.smppc_factory.lastProto.PDUReceived = mock.Mock(wraps=self.smppc_factory.lastProto.PDUReceived)

		# SMPPClient > SMPPServer
		SubmitSmPDU = copy.deepcopy(self.SubmitSmPDU)
		SubmitSmPDU.params['priority_flag'] = priority_flag_value_map[3]
		yield self.smppc_factory.lastProto.sendDataRequest(SubmitSmPDU)

		# Unbind & Disconnect
 		yield self.smppc_factory.smpp.unbindAndDisconnect()
		self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.UNBOUND)

		# Asserts SMPPClient side
		self.assertEqual(self.smppc_factory.lastProto.PDUReceived.call_count, 2)
		self.assertEqual(self.smppc_factory.lastProto.PDUReceived.call_args_list[0][0][0].id, 
			pdu_types.CommandId.submit_sm_resp)
		self.assertEqual(self.smppc_factory.lastProto.PDUReceived.call_args_list[0][0][0].status, 
			pdu_types.CommandStatus.ESME_RINVSYSID)

class FiltersTestCases(SMPPClientTestCases):

	@defer.inlineCallbacks
	def test_filter_destination_address(self):
		user = self.routerpb_factory.getUser('u1')
		user.mt_credential.setValueFilter('destination_address', r'^A.*')

		# Connect and bind
		yield self.smppc_factory.connectAndBind()
		self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.BOUND_TRX)

		# Install mockers
		self.smppc_factory.lastProto.PDUReceived = mock.Mock(wraps=self.smppc_factory.lastProto.PDUReceived)

		# SMPPClient > SMPPServer
		yield self.smppc_factory.lastProto.sendDataRequest(self.SubmitSmPDU)

		# Unbind & Disconnect
 		yield self.smppc_factory.smpp.unbindAndDisconnect()
		self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.UNBOUND)

		# Asserts SMPPClient side
		self.assertEqual(self.smppc_factory.lastProto.PDUReceived.call_count, 2)
		self.assertEqual(self.smppc_factory.lastProto.PDUReceived.call_args_list[0][0][0].id, 
			pdu_types.CommandId.submit_sm_resp)
		self.assertEqual(self.smppc_factory.lastProto.PDUReceived.call_args_list[0][0][0].status, 
			pdu_types.CommandStatus.ESME_RINVDSTADR)

	@defer.inlineCallbacks
	def test_filter_source_address(self):
		user = self.routerpb_factory.getUser('u1')
		user.mt_credential.setValueFilter('source_address', r'^A.*')

		# Connect and bind
		yield self.smppc_factory.connectAndBind()
		self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.BOUND_TRX)

		# Install mockers
		self.smppc_factory.lastProto.PDUReceived = mock.Mock(wraps=self.smppc_factory.lastProto.PDUReceived)

		# SMPPClient > SMPPServer
		yield self.smppc_factory.lastProto.sendDataRequest(self.SubmitSmPDU)

		# Unbind & Disconnect
 		yield self.smppc_factory.smpp.unbindAndDisconnect()
		self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.UNBOUND)

		# Asserts SMPPClient side
		self.assertEqual(self.smppc_factory.lastProto.PDUReceived.call_count, 2)
		self.assertEqual(self.smppc_factory.lastProto.PDUReceived.call_args_list[0][0][0].id, 
			pdu_types.CommandId.submit_sm_resp)
		self.assertEqual(self.smppc_factory.lastProto.PDUReceived.call_args_list[0][0][0].status, 
			pdu_types.CommandStatus.ESME_RINVSRCADR)

	@defer.inlineCallbacks
	def test_filter_priority(self):
		user = self.routerpb_factory.getUser('u1')
		user.mt_credential.setValueFilter('priority', r'^A.*')

		# Connect and bind
		yield self.smppc_factory.connectAndBind()
		self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.BOUND_TRX)

		# Install mockers
		self.smppc_factory.lastProto.PDUReceived = mock.Mock(wraps=self.smppc_factory.lastProto.PDUReceived)

		# SMPPClient > SMPPServer
		yield self.smppc_factory.lastProto.sendDataRequest(self.SubmitSmPDU)

		# Unbind & Disconnect
 		yield self.smppc_factory.smpp.unbindAndDisconnect()
		self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.UNBOUND)

		# Asserts SMPPClient side
		self.assertEqual(self.smppc_factory.lastProto.PDUReceived.call_count, 2)
		self.assertEqual(self.smppc_factory.lastProto.PDUReceived.call_args_list[0][0][0].id, 
			pdu_types.CommandId.submit_sm_resp)
		self.assertEqual(self.smppc_factory.lastProto.PDUReceived.call_args_list[0][0][0].status, 
			pdu_types.CommandStatus.ESME_RINVPRTFLG)

	@defer.inlineCallbacks
	def test_filter_content(self):
		user = self.routerpb_factory.getUser('u1')
		user.mt_credential.setValueFilter('content', r'^A.*')

		# Connect and bind
		yield self.smppc_factory.connectAndBind()
		self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.BOUND_TRX)

		# Install mockers
		self.smppc_factory.lastProto.PDUReceived = mock.Mock(wraps=self.smppc_factory.lastProto.PDUReceived)

		# SMPPClient > SMPPServer
		yield self.smppc_factory.lastProto.sendDataRequest(self.SubmitSmPDU)

		# Unbind & Disconnect
 		yield self.smppc_factory.smpp.unbindAndDisconnect()
		self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.UNBOUND)

		# Asserts SMPPClient side
		self.assertEqual(self.smppc_factory.lastProto.PDUReceived.call_count, 2)
		self.assertEqual(self.smppc_factory.lastProto.PDUReceived.call_args_list[0][0][0].id, 
			pdu_types.CommandId.submit_sm_resp)
		self.assertEqual(self.smppc_factory.lastProto.PDUReceived.call_args_list[0][0][0].status, 
			pdu_types.CommandStatus.ESME_RSYSERR)

class QuotasTestCases(SMPPClientTestCases):

	@defer.inlineCallbacks
	def test_default_unrated_route(self):
		"""
		Default quotas, everything is unlimited
		"""
		user = self.routerpb_factory.getUser('u1')

		# Connect and bind
		yield self.smppc_factory.connectAndBind()
		self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.BOUND_TRX)

		# SMPPClient > SMPPServer
		yield self.smppc_factory.lastProto.sendDataRequest(self.SubmitSmPDU)

		# Unbind & Disconnect
 		yield self.smppc_factory.smpp.unbindAndDisconnect()
		self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.UNBOUND)

		# Assert User quotas still unlimited
		self.assertEqual(user.mt_credential.getQuota('balance'), None)
		self.assertEqual(user.mt_credential.getQuota('submit_sm_count'), None)

	@defer.inlineCallbacks
	def test_unrated_route_limited_quotas(self):
		user = self.routerpb_factory.getUser('u1')
		user.mt_credential.setQuota('balance', 10.0)
		user.mt_credential.setQuota('submit_sm_count', 10)

		# Connect and bind
		yield self.smppc_factory.connectAndBind()
		self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.BOUND_TRX)

		# SMPPClient > SMPPServer
		yield self.smppc_factory.lastProto.sendDataRequest(self.SubmitSmPDU)

		# Unbind & Disconnect
 		yield self.smppc_factory.smpp.unbindAndDisconnect()
		self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.UNBOUND)

		# Assert quotas after SMS is sent
		self.assertEqual(user.mt_credential.getQuota('balance'), 10)
		self.assertEqual(user.mt_credential.getQuota('submit_sm_count'), 9)

	@defer.inlineCallbacks
	def test_unrated_route_unlimited_quotas(self):
		user = self.routerpb_factory.getUser('u1')
		user.mt_credential.setQuota('balance', None)
		user.mt_credential.setQuota('submit_sm_count', None)

		# Connect and bind
		yield self.smppc_factory.connectAndBind()
		self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.BOUND_TRX)

		# SMPPClient > SMPPServer
		yield self.smppc_factory.lastProto.sendDataRequest(self.SubmitSmPDU)

		# Unbind & Disconnect
 		yield self.smppc_factory.smpp.unbindAndDisconnect()
		self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.UNBOUND)

		# Assert quotas after SMS is sent
		self.assertEqual(user.mt_credential.getQuota('balance'), None)
		self.assertEqual(user.mt_credential.getQuota('submit_sm_count'), None)

	@defer.inlineCallbacks
	def test_rated_route_limited_quotas(self):
		user = self.routerpb_factory.getUser('u1')
		user.mt_credential.setQuota('balance', 10.0)
		user.mt_credential.setQuota('submit_sm_count', 10)
		default_route = self.routerpb_factory.getMTRoutingTable().getAll()[0][0]
		default_route.rate = 1.2

		# Connect and bind
		yield self.smppc_factory.connectAndBind()
		self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.BOUND_TRX)

		# SMPPClient > SMPPServer
		yield self.smppc_factory.lastProto.sendDataRequest(self.SubmitSmPDU)

		# Unbind & Disconnect
 		yield self.smppc_factory.smpp.unbindAndDisconnect()
		self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.UNBOUND)

		# Assert quotas after SMS is sent
		self.assertEqual(user.mt_credential.getQuota('balance'), 8.8)
		self.assertEqual(user.mt_credential.getQuota('submit_sm_count'), 9)

	@defer.inlineCallbacks
	def test_rated_route_unlimited_quotas(self):
		user = self.routerpb_factory.getUser('u1')
		user.mt_credential.setQuota('balance', None)
		user.mt_credential.setQuota('submit_sm_count', None)
		default_route = self.routerpb_factory.getMTRoutingTable().getAll()[0][0]
		default_route.rate = 1.2

		# Connect and bind
		yield self.smppc_factory.connectAndBind()
		self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.BOUND_TRX)

		# SMPPClient > SMPPServer
		yield self.smppc_factory.lastProto.sendDataRequest(self.SubmitSmPDU)

		# Unbind & Disconnect
 		yield self.smppc_factory.smpp.unbindAndDisconnect()
		self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.UNBOUND)

		# Assert quotas after SMS is sent
		self.assertEqual(user.mt_credential.getQuota('balance'), None)
		self.assertEqual(user.mt_credential.getQuota('submit_sm_count'), None)

	@defer.inlineCallbacks
	def test_rated_route_insufficient_balance(self):
		user = self.routerpb_factory.getUser('u1')
		user.mt_credential.setQuota('balance', 1.1)
		user.mt_credential.setQuota('submit_sm_count', None)
		default_route = self.routerpb_factory.getMTRoutingTable().getAll()[0][0]
		default_route.rate = 1.2

		# Connect and bind
		yield self.smppc_factory.connectAndBind()
		self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.BOUND_TRX)

		# Install mockers
		self.smppc_factory.lastProto.PDUReceived = mock.Mock(wraps=self.smppc_factory.lastProto.PDUReceived)

		# SMPPClient > SMPPServer
		yield self.smppc_factory.lastProto.sendDataRequest(self.SubmitSmPDU)

		# Unbind & Disconnect
 		yield self.smppc_factory.smpp.unbindAndDisconnect()
		self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.UNBOUND)

		# Assert quotas after SMS is sent
		self.assertEqual(user.mt_credential.getQuota('balance'), 1.1)
		self.assertEqual(user.mt_credential.getQuota('submit_sm_count'), None)
		# Asserts SMPPClient side
		self.assertEqual(self.smppc_factory.lastProto.PDUReceived.call_count, 2)
		self.assertEqual(self.smppc_factory.lastProto.PDUReceived.call_args_list[0][0][0].id, 
			pdu_types.CommandId.submit_sm_resp)
		self.assertEqual(self.smppc_factory.lastProto.PDUReceived.call_args_list[0][0][0].status, 
			pdu_types.CommandStatus.ESME_RSYSERR)

	@defer.inlineCallbacks
	def test_unrated_route_insufficient_submit_sm_count(self):
		user = self.routerpb_factory.getUser('u1')
		user.mt_credential.setQuota('balance', None)
		user.mt_credential.setQuota('submit_sm_count', 0)

		# Connect and bind
		yield self.smppc_factory.connectAndBind()
		self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.BOUND_TRX)

		# Install mockers
		self.smppc_factory.lastProto.PDUReceived = mock.Mock(wraps=self.smppc_factory.lastProto.PDUReceived)

		# SMPPClient > SMPPServer
		yield self.smppc_factory.lastProto.sendDataRequest(self.SubmitSmPDU)

		# Unbind & Disconnect
 		yield self.smppc_factory.smpp.unbindAndDisconnect()
		self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.UNBOUND)

		# Assert quotas after SMS is sent
		self.assertEqual(user.mt_credential.getQuota('balance'), None)
		self.assertEqual(user.mt_credential.getQuota('submit_sm_count'), 0)
		# Asserts SMPPClient side
		self.assertEqual(self.smppc_factory.lastProto.PDUReceived.call_count, 2)
		self.assertEqual(self.smppc_factory.lastProto.PDUReceived.call_args_list[0][0][0].id, 
			pdu_types.CommandId.submit_sm_resp)
		self.assertEqual(self.smppc_factory.lastProto.PDUReceived.call_args_list[0][0][0].status, 
			pdu_types.CommandStatus.ESME_RSYSERR)

	@defer.inlineCallbacks
	def test_rated_route_insufficient_submit_sm_count(self):
		user = self.routerpb_factory.getUser('u1')
		user.mt_credential.setQuota('balance', None)
		user.mt_credential.setQuota('submit_sm_count', 0)
		default_route = self.routerpb_factory.getMTRoutingTable().getAll()[0][0]
		default_route.rate = 1.2

		# Connect and bind
		yield self.smppc_factory.connectAndBind()
		self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.BOUND_TRX)

		# Install mockers
		self.smppc_factory.lastProto.PDUReceived = mock.Mock(wraps=self.smppc_factory.lastProto.PDUReceived)

		# SMPPClient > SMPPServer
		yield self.smppc_factory.lastProto.sendDataRequest(self.SubmitSmPDU)

		# Unbind & Disconnect
 		yield self.smppc_factory.smpp.unbindAndDisconnect()
		self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.UNBOUND)

		# Assert quotas after SMS is sent
		self.assertEqual(user.mt_credential.getQuota('balance'), None)
		self.assertEqual(user.mt_credential.getQuota('submit_sm_count'), 0)
		# Asserts SMPPClient side
		self.assertEqual(self.smppc_factory.lastProto.PDUReceived.call_count, 2)
		self.assertEqual(self.smppc_factory.lastProto.PDUReceived.call_args_list[0][0][0].id, 
			pdu_types.CommandId.submit_sm_resp)
		self.assertEqual(self.smppc_factory.lastProto.PDUReceived.call_args_list[0][0][0].status, 
			pdu_types.CommandStatus.ESME_RSYSERR)

	@defer.inlineCallbacks
	def test_rated_route_early_decrement_balance_percent_insufficient_balance(self):
		'''Balance is greater than the early_decrement_balance_percent but lower than the final rate, 
		user must not be charged in this case, he have to get a balance covering the total rate'''

		user = self.routerpb_factory.getUser('u1')
		user.mt_credential.setQuota('balance', 1.0)
		user.mt_credential.setQuota('early_decrement_balance_percent', 25)
		default_route = self.routerpb_factory.getMTRoutingTable().getAll()[0][0]
		default_route.rate = 2.0

		# Connect and bind
		yield self.smppc_factory.connectAndBind()
		self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.BOUND_TRX)

		# Install mockers
		self.smppc_factory.lastProto.PDUReceived = mock.Mock(wraps=self.smppc_factory.lastProto.PDUReceived)

		# SMPPClient > SMPPServer
		yield self.smppc_factory.lastProto.sendDataRequest(self.SubmitSmPDU)

		# Unbind & Disconnect
 		yield self.smppc_factory.smpp.unbindAndDisconnect()
		self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.UNBOUND)

		# Assert quotas after SMS is sent
		self.assertEqual(user.mt_credential.getQuota('balance'), 1)
		# Asserts SMPPClient side
		self.assertEqual(self.smppc_factory.lastProto.PDUReceived.call_count, 2)
		self.assertEqual(self.smppc_factory.lastProto.PDUReceived.call_args_list[0][0][0].id, 
			pdu_types.CommandId.submit_sm_resp)
		self.assertEqual(self.smppc_factory.lastProto.PDUReceived.call_args_list[0][0][0].status, 
			pdu_types.CommandStatus.ESME_RSYSERR)

	@defer.inlineCallbacks
	def test_rated_route_early_decrement_balance_percent(self):
		"""Note:
		Since this test case have no SMPPClientManagerPB set, message will not be sent
		to the routed connector, user will only be charged for earlier (on submit_sm).
		Complete test (with charging on submit_sm_resp) is done in 
		test_router_smpps.BillRequestSubmitSmRespCallbackingTestCases
		"""
		user = self.routerpb_factory.getUser('u1')
		user.mt_credential.setQuota('balance', 10.0)
		user.mt_credential.setQuota('early_decrement_balance_percent', 25)
		default_route = self.routerpb_factory.getMTRoutingTable().getAll()[0][0]
		default_route.rate = 2.0

		# Connect and bind
		yield self.smppc_factory.connectAndBind()
		self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.BOUND_TRX)

		# SMPPClient > SMPPServer
		yield self.smppc_factory.lastProto.sendDataRequest(self.SubmitSmPDU)

		# Unbind & Disconnect
 		yield self.smppc_factory.smpp.unbindAndDisconnect()
		self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.UNBOUND)

		# Assert quotas after SMS is sent
		self.assertEqual(user.mt_credential.getQuota('balance'), 9.5)

	@defer.inlineCallbacks
	def test_rated_route_early_decrement_balance_100_percent(self):
		"""Note:
		Since this test case have no SMPPClientManagerPB set, message will not be sent
		to the routed connector, user will only be charged for earlier (on submit_sm).
		Complete test (with charging on submit_sm_resp) is done in 
		test_router_smpps.BillRequestSubmitSmRespCallbackingTestCases
		"""
		user = self.routerpb_factory.getUser('u1')
		user.mt_credential.setQuota('balance', 10.0)
		user.mt_credential.setQuota('early_decrement_balance_percent', 100)
		default_route = self.routerpb_factory.getMTRoutingTable().getAll()[0][0]
		default_route.rate = 2.0

		# Connect and bind
		yield self.smppc_factory.connectAndBind()
		self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.BOUND_TRX)

		# SMPPClient > SMPPServer
		yield self.smppc_factory.lastProto.sendDataRequest(self.SubmitSmPDU)

		# Unbind & Disconnect
		yield self.smppc_factory.smpp.unbindAndDisconnect()
		self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.UNBOUND)

		# Assert quotas after SMS is sent
		self.assertEqual(user.mt_credential.getQuota('balance'), 8.0)

	@defer.inlineCallbacks
	def test_throughput_limit_rejection(self):
		user = self.routerpb_factory.getUser('u1')
		user.mt_credential.setQuota('smpps_throughput', 2)

		# Connect and bind
		yield self.smppc_factory.connectAndBind()
		self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.BOUND_TRX)

		# SMPPClient > SMPPServer
		# Send a bunch of MT messages
		# We should receive a ESME_ROK for success and ESME_RTHROTTLED when throughput is exceeded
		start_time = datetime.now()
		throughput_exceeded_errors = 0
		request_counter = 0
		for x in range(5000):
			responsePDU = yield self.smppc_factory.lastProto.sendDataRequest(self.SubmitSmPDU)

			request_counter+= 1
			if str(responsePDU.response.status) == 'ESME_RTHROTTLED':
				throughput_exceeded_errors+= 1
		end_time = datetime.now()

		# Unbind & Disconnect
		yield self.smppc_factory.smpp.unbindAndDisconnect()
		self.assertEqual(self.smppc_factory.smpp.sessionState, SMPPSessionStates.UNBOUND)

		# Asserts (tolerance of -/+ 3 messages)
		throughput = 1 / float(user.mt_credential.getQuota('smpps_throughput'))
		dt = end_time - start_time
		max_unsuccessfull_requests = request_counter - (dt.seconds / throughput)
		unsuccessfull_requests = throughput_exceeded_errors

		self.assertGreaterEqual(unsuccessfull_requests, max_unsuccessfull_requests - 3)
		self.assertLessEqual(unsuccessfull_requests, max_unsuccessfull_requests + 3)