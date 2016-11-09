import datetime
from hashlib import md5

import mock
from twisted.cred import portal
from twisted.cred.checkers import InMemoryUsernamePasswordDatabaseDontUse
from twisted.internet import reactor, defer
from twisted.spread import pb

from jasmin.managers.content import DLRContentForSmpps
from jasmin.protocols.smpp.configs import SMPPServerPBConfig, SMPPServerPBClientConfig
from jasmin.protocols.smpp.pb import SMPPServerPB
from jasmin.protocols.smpp.proxies import SMPPServerPBProxy
from jasmin.routing.proxies import RouterPBProxy
from jasmin.routing.test.test_router import SubmitSmTestCaseTools
from jasmin.routing.test.test_router_smpps import SMPPClientTestCases
from jasmin.tools.cred.portal import JasminPBRealm
from jasmin.tools.spread.pb import JasminPBPortalRoot
from jasmin.vendor.smpp.pdu import pdu_types


@defer.inlineCallbacks
def waitFor(seconds):
    # Wait seconds
    waitDeferred = defer.Deferred()
    reactor.callLater(seconds, waitDeferred.callback, None)
    yield waitDeferred


class SMPPDLRThrowerTestCases(RouterPBProxy, SMPPClientTestCases, SubmitSmTestCaseTools):
    @defer.inlineCallbacks
    def setUp(self):
        yield SMPPClientTestCases.setUp(self)

        # Init SMPPServerPB
        SMPPServerPBConfigInstance = SMPPServerPBConfig()
        SMPPServerPBInstance = SMPPServerPB(SMPPServerPBConfigInstance)
        SMPPServerPBInstance.addSmpps(self.smpps_factory)

        p = portal.Portal(JasminPBRealm(SMPPServerPBInstance))
        c = InMemoryUsernamePasswordDatabaseDontUse()
        c.addUser('smppsadmin', md5('smppspwd').digest())
        p.registerChecker(c)
        jPBPortalRoot = JasminPBPortalRoot(p)
        self.SMPPServerPBInstanceServer = reactor.listenTCP(SMPPServerPBConfigInstance.port,
                                                            pb.PBServerFactory(jPBPortalRoot))

        # Init SMPPServerPBClient and connect it to SMPPServerPB
        SMPPServerPBClientConfigInstance = SMPPServerPBClientConfig()
        self.SMPPServerPBProxyInstance = SMPPServerPBProxy()
        yield self.SMPPServerPBProxyInstance.connect(
            SMPPServerPBClientConfigInstance.host,
            SMPPServerPBClientConfigInstance.port,
            SMPPServerPBClientConfigInstance.username,
            SMPPServerPBClientConfigInstance.password,
            retry=False)

        # Lower the timeout config to pass the timeout tests quickly
        self.DLRThrower.config.timeout = 2
        self.DLRThrower.config.retry_delay = 1
        self.DLRThrower.config.max_retries = 2

        # Most important thing:
        # Swap default direct smpps access to perspectivebroker smpps access:
        self.DLRThrower.addSmpps(self.SMPPServerPBProxyInstance)

    @defer.inlineCallbacks
    def tearDown(self):
        yield SMPPClientTestCases.tearDown(self)
        yield self.SMPPServerPBProxyInstance.disconnect()
        yield self.SMPPServerPBInstanceServer.stopListening()

    @defer.inlineCallbacks
    def publishDLRContentForSmppapi(self, message_status, msgid, system_id, source_addr, destination_addr,
                                    sub_date=None,
                                    source_addr_ton='UNKNOWN', source_addr_npi='UNKNOWN',
                                    dest_addr_ton='UNKNOWN', dest_addr_npi='UNKNOWN'):
        if sub_date is None:
            sub_date = datetime.datetime.now()

        content = DLRContentForSmpps(message_status, msgid, system_id, source_addr, destination_addr, sub_date,
                                     source_addr_ton, source_addr_npi, dest_addr_ton, dest_addr_npi)
        yield self.amqpBroker.publish(exchange='messaging', routing_key='dlr_thrower.smpps', content=content)

    @defer.inlineCallbacks
    def test_throwing_smpps_to_bound_connection_as_deliver_sm(self):
        self.DLRThrower.config.dlr_pdu = 'deliver_sm'

        self.DLRThrower.ackMessage = mock.Mock(wraps=self.DLRThrower.ackMessage)
        self.DLRThrower.rejectMessage = mock.Mock(wraps=self.DLRThrower.rejectMessage)
        self.DLRThrower.smpp_dlr_callback = mock.Mock(wraps=self.DLRThrower.smpp_dlr_callback)

        # Bind
        yield self.connect('127.0.0.1', self.pbPort)
        yield self.prepareRoutingsAndStartConnector()
        yield self.smppc_factory.connectAndBind()

        # Install mocks
        self.smppc_factory.lastProto.PDUDataRequestReceived = mock.Mock(
            wraps=self.smppc_factory.lastProto.PDUDataRequestReceived)

        sub_date = datetime.datetime.now()
        yield self.publishDLRContentForSmppapi('ESME_ROK', 'MSGID', 'username', '999', '000', sub_date)

        yield waitFor(1)

        # Run tests
        self.assertEqual(self.smppc_factory.lastProto.PDUDataRequestReceived.call_count, 1)
        # the received pdu must be a DeliverSM
        received_pdu_1 = self.smppc_factory.lastProto.PDUDataRequestReceived.call_args_list[0][0][0]
        self.assertEqual(received_pdu_1.id, pdu_types.CommandId.deliver_sm)
        self.assertEqual(received_pdu_1.params['source_addr'], '000')
        self.assertEqual(received_pdu_1.params['destination_addr'], '999')
        self.assertEqual(received_pdu_1.params['receipted_message_id'], 'MSGID')
        self.assertEqual(str(received_pdu_1.params['message_state']), 'ACCEPTED')
        self.assertEqual(received_pdu_1.params['short_message'],
                         'id:MSGID submit date:%s done date:%s stat:ACCEPTD err:000' % (
                             sub_date.strftime("%Y%m%d%H%M"),
                             sub_date.strftime("%Y%m%d%H%M"),
                         ))

        # Unbind & Disconnect
        yield self.smppc_factory.smpp.unbindAndDisconnect()
        yield self.stopSmppClientConnectors()

    @defer.inlineCallbacks
    def test_throwing_smpps_to_bound_connection(self):
        self.DLRThrower.ackMessage = mock.Mock(wraps=self.DLRThrower.ackMessage)
        self.DLRThrower.rejectMessage = mock.Mock(wraps=self.DLRThrower.rejectMessage)
        self.DLRThrower.smpp_dlr_callback = mock.Mock(wraps=self.DLRThrower.smpp_dlr_callback)

        # Bind
        yield self.connect('127.0.0.1', self.pbPort)
        yield self.prepareRoutingsAndStartConnector()
        yield self.smppc_factory.connectAndBind()

        yield self.publishDLRContentForSmppapi('ESME_ROK', 'MSGID', 'username', '999', '000')

        yield waitFor(1)

        # Run tests
        self.assertEqual(self.DLRThrower.smpp_dlr_callback.call_count, 1)
        self.assertEqual(self.DLRThrower.ackMessage.call_count, 1)
        self.assertEqual(self.DLRThrower.rejectMessage.call_count, 0)

        # Unbind & Disconnect
        yield self.smppc_factory.smpp.unbindAndDisconnect()
        yield self.stopSmppClientConnectors()

    @defer.inlineCallbacks
    def test_throwing_smpps_to_not_bound_connection(self):
        self.DLRThrower.ackMessage = mock.Mock(wraps=self.DLRThrower.ackMessage)
        self.DLRThrower.rejectMessage = mock.Mock(wraps=self.DLRThrower.rejectMessage)
        self.DLRThrower.rejectAndRequeueMessage = mock.Mock(wraps=self.DLRThrower.rejectAndRequeueMessage)
        self.DLRThrower.smpp_dlr_callback = mock.Mock(wraps=self.DLRThrower.smpp_dlr_callback)

        yield self.publishDLRContentForSmppapi('ESME_ROK', 'MSGID', 'username', '999', '000')

        yield waitFor(3)

        # Run tests
        self.assertEqual(self.DLRThrower.smpp_dlr_callback.call_count, 3)
        self.assertEqual(self.DLRThrower.ackMessage.call_count, 0)
        self.assertEqual(self.DLRThrower.rejectMessage.call_count, 3)
        self.assertEqual(self.DLRThrower.rejectAndRequeueMessage.call_count, 2)

    @defer.inlineCallbacks
    def test_throwing_smpps_with_no_deliverers(self):
        self.DLRThrower.ackMessage = mock.Mock(wraps=self.DLRThrower.ackMessage)
        self.DLRThrower.rejectMessage = mock.Mock(wraps=self.DLRThrower.rejectMessage)
        self.DLRThrower.rejectAndRequeueMessage = mock.Mock(wraps=self.DLRThrower.rejectAndRequeueMessage)
        self.DLRThrower.smpp_dlr_callback = mock.Mock(wraps=self.DLRThrower.smpp_dlr_callback)

        # Bind (as a transmitter so we get no deliverers for DLR)
        yield self.connect('127.0.0.1', self.pbPort)
        yield self.prepareRoutingsAndStartConnector()
        self.smppc_config.bindOperation = 'transmitter'
        yield self.smppc_factory.connectAndBind()

        yield self.publishDLRContentForSmppapi('ESME_ROK', 'MSGID', 'username', '999', '000')

        yield waitFor(3)

        # Run tests
        self.assertEqual(self.DLRThrower.smpp_dlr_callback.call_count, 3)
        self.assertEqual(self.DLRThrower.ackMessage.call_count, 0)
        self.assertEqual(self.DLRThrower.rejectMessage.call_count, 3)
        self.assertEqual(self.DLRThrower.rejectAndRequeueMessage.call_count, 2)

        # Unbind & Disconnect
        yield self.smppc_factory.smpp.unbindAndDisconnect()
        yield self.stopSmppClientConnectors()

    @defer.inlineCallbacks
    def test_throwing_smpps_without_smppsFactory(self):
        self.DLRThrower.ackMessage = mock.Mock(wraps=self.DLRThrower.ackMessage)
        self.DLRThrower.rejectMessage = mock.Mock(wraps=self.DLRThrower.rejectMessage)
        self.DLRThrower.rejectAndRequeueMessage = mock.Mock(wraps=self.DLRThrower.rejectAndRequeueMessage)
        self.DLRThrower.smpp_dlr_callback = mock.Mock(wraps=self.DLRThrower.smpp_dlr_callback)

        # Remove smpps from self.DLRThrower
        self.DLRThrower.smpps = None
        self.DLRThrower.smpps_access = None

        yield self.publishDLRContentForSmppapi('ESME_ROK', 'MSGID', 'username', '999', '000')

        yield waitFor(1)

        # Run tests
        self.assertEqual(self.DLRThrower.smpp_dlr_callback.call_count, 1)
        self.assertEqual(self.DLRThrower.ackMessage.call_count, 0)
        self.assertEqual(self.DLRThrower.rejectMessage.call_count, 1)
        self.assertEqual(self.DLRThrower.rejectAndRequeueMessage.call_count, 0)
