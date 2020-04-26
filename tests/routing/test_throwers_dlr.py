import datetime

from unittest.mock import Mock
from twisted.internet import reactor, defer
from twisted.trial.unittest import TestCase
from twisted.web import server

from jasmin.managers.content import DLRContentForHttpapi, DLRContentForSmpps
from jasmin.queues.configs import AmqpConfig
from jasmin.queues.factory import AmqpFactory
from jasmin.routing.configs import DLRThrowerConfig
from jasmin.routing.proxies import RouterPBProxy
from tests.routing.http_server import TimeoutLeafServer, AckServer, NoAckServer, Error404Server
from tests.routing.test_router import SubmitSmTestCaseTools
from tests.routing.test_router_smpps import SMPPClientTestCases
from jasmin.routing.throwers import DLRThrower
from smpp.pdu.pdu_types import MessageState, CommandId


@defer.inlineCallbacks
def waitFor(seconds):
    # Wait seconds
    waitDeferred = defer.Deferred()
    reactor.callLater(seconds, waitDeferred.callback, None)
    yield waitDeferred


class DLRThrowerTestCases(TestCase):
    @defer.inlineCallbacks
    def setUp(self):
        # Initiating config objects without any filename
        # will lead to setting defaults and that's what we
        # need to run the tests
        AMQPServiceConfigInstance = AmqpConfig()
        AMQPServiceConfigInstance.reconnectOnConnectionLoss = False

        self.amqpBroker = AmqpFactory(AMQPServiceConfigInstance)
        yield self.amqpBroker.connect()
        yield self.amqpBroker.getChannelReadyDeferred()

        # Initiating config objects without any filename
        # will lead to setting defaults and that's what we
        # need to run the tests
        DLRThrowerConfigInstance = DLRThrowerConfig()
        # Lower the timeout config to pass the timeout tests quickly
        DLRThrowerConfigInstance.timeout = 2
        DLRThrowerConfigInstance.retry_delay = 1
        DLRThrowerConfigInstance.max_retries = 2

        # Launch the DLRThrower
        self.DLRThrower = DLRThrower(DLRThrowerConfigInstance)

        # Add the broker to the DLRThrower
        yield self.DLRThrower.addAmqpBroker(self.amqpBroker)

    @defer.inlineCallbacks
    def tearDown(self):
        for q in self.amqpBroker.queues:
            yield self.amqpBroker.chan.queue_delete(queue=q)
        yield self.amqpBroker.disconnect()
        yield self.DLRThrower.stopService()


class HTTPDLRThrowerTestCase(DLRThrowerTestCases):
    @defer.inlineCallbacks
    def setUp(self):
        yield DLRThrowerTestCases.setUp(self)

        # Start http servers
        self.Error404ServerResource = Error404Server()
        self.Error404Server = reactor.listenTCP(0, server.Site(self.Error404ServerResource))

        self.AckServerResource = AckServer()
        self.AckServer = reactor.listenTCP(0, server.Site(self.AckServerResource))

        self.NoAckServerResource = NoAckServer()
        self.NoAckServer = reactor.listenTCP(0, server.Site(self.NoAckServerResource))

        self.TimeoutLeafServerResource = TimeoutLeafServer()
        self.TimeoutLeafServerResource.hangTime = 3
        self.TimeoutLeafServer = reactor.listenTCP(0, server.Site(self.TimeoutLeafServerResource))

    @defer.inlineCallbacks
    def publishDLRContentForHttpapi(self, message_status, msgid, dlr_url, dlr_level, dlr_connector='unknown', id_smsc='', sub='',
                                    dlvrd='', subdate='', donedate='', err='', text='', method='POST', trycount=0):
        content = DLRContentForHttpapi(message_status, msgid, dlr_url, dlr_level, dlr_connector, id_smsc, sub, dlvrd, subdate,
                                       donedate, err, text, method, trycount)
        yield self.amqpBroker.publish(exchange='messaging', routing_key='dlr_thrower.http', content=content)

    @defer.inlineCallbacks
    def tearDown(self):
        yield DLRThrowerTestCases.tearDown(self)

        yield self.Error404Server.stopListening()
        yield self.AckServer.stopListening()
        yield self.NoAckServer.stopListening()
        yield self.TimeoutLeafServer.stopListening()

    @defer.inlineCallbacks
    def test_throwing_http_connector_with_ack(self):
        self.AckServerResource.render_POST = Mock(wraps=self.AckServerResource.render_POST)

        dlr_url = 'http://127.0.0.1:%s/dlr' % self.AckServer.getHost().port
        dlr_level = 1
        msgid = 'anything'
        message_status = 'DELIVRD'
        self.publishDLRContentForHttpapi(message_status, msgid, dlr_url, dlr_level)

        yield waitFor(1)

        # No message retries must be made since ACK was received
        self.assertEqual(self.AckServerResource.render_POST.call_count, 1)

    @defer.inlineCallbacks
    def test_throwing_http_connector_without_ack(self):
        self.NoAckServerResource.render_POST = Mock(wraps=self.NoAckServerResource.render_POST)

        dlr_url = 'http://127.0.0.1:%s/dlr' % self.NoAckServer.getHost().port
        dlr_level = 1
        msgid = 'anything'
        message_status = 'DELIVRD'
        self.publishDLRContentForHttpapi(message_status, msgid, dlr_url, dlr_level)

        yield waitFor(4)

        # Retries must be made when ACK is not received
        self.assertTrue(self.NoAckServerResource.render_POST.call_count > 1)

    @defer.inlineCallbacks
    def test_throwing_http_connector_timeout_retry(self):
        self.TimeoutLeafServerResource.render_POST = Mock(wraps=self.TimeoutLeafServerResource.render_POST)

        dlr_url = 'http://127.0.0.1:%s/dlr' % self.TimeoutLeafServer.getHost().port
        dlr_level = 1
        msgid = 'anything'
        message_status = 'DELIVRD'
        self.publishDLRContentForHttpapi(message_status, msgid, dlr_url, dlr_level)

        yield waitFor(12)

        self.assertEqual(self.TimeoutLeafServerResource.render_POST.call_count, 3)

    @defer.inlineCallbacks
    def test_throwing_http_connector_404_error_noretry(self):
        """When receiving a 404 error, no further retries shall be made
        """
        self.Error404ServerResource.render_POST = Mock(wraps=self.Error404ServerResource.render_POST)

        dlr_url = 'http://127.0.0.1:%s/dlr' % self.Error404Server.getHost().port
        dlr_level = 1
        msgid = 'anything'
        message_status = 'DELIVRD'
        self.publishDLRContentForHttpapi(message_status, msgid, dlr_url, dlr_level)

        yield waitFor(1)

        self.assertEqual(self.Error404ServerResource.render_POST.call_count, 1)

    @defer.inlineCallbacks
    def test_throwing_http_connector_dlr_level1(self):
        self.AckServerResource.render_GET = Mock(wraps=self.AckServerResource.render_GET)

        dlr_url = 'http://127.0.0.1:%s/dlr' % self.AckServer.getHost().port
        dlr_level = 1
        dlr_connector = 'test_cid'
        msgid = 'anything'
        message_status = 'DELIVRD'
        self.publishDLRContentForHttpapi(message_status, msgid, dlr_url, dlr_level, dlr_connector, method='GET')

        yield waitFor(1)

        # No message retries must be made since ACK was received
        self.assertEqual(self.AckServerResource.render_GET.call_count, 1)
        callArgs = self.AckServerResource.render_GET.call_args_list[0][0][0].args
        self.assertEqual(callArgs[b'message_status'][0], message_status.encode())
        self.assertEqual(callArgs[b'id'][0], msgid.encode())
        self.assertEqual(callArgs[b'level'][0], str(dlr_level).encode())
        self.assertEqual(callArgs[b'connector'][0], dlr_connector.encode())

    @defer.inlineCallbacks
    def test_throwing_http_connector_dlr_level2(self):
        self.AckServerResource.render_GET = Mock(wraps=self.AckServerResource.render_GET)

        dlr_url = 'http://127.0.0.1:%s/dlr' % self.AckServer.getHost().port
        dlr_level = 2
        dlr_connector = 'test_cid'
        msgid = 'anything'
        message_status = 'DELIVRD'
        self.publishDLRContentForHttpapi(message_status, msgid, dlr_url, dlr_level, dlr_connector, id_smsc='abc', sub='3',
                                         dlvrd='3', subdate='anydate', donedate='anydate', err='', text='Any text',
                                         method='GET')

        yield waitFor(1)

        # No message retries must be made since ACK was received
        self.assertEqual(self.AckServerResource.render_GET.call_count, 1)
        callArgs = self.AckServerResource.render_GET.call_args_list[0][0][0].args
        self.assertEqual(callArgs[b'message_status'][0], message_status.encode())
        self.assertEqual(callArgs[b'id'][0], msgid.encode())
        self.assertEqual(callArgs[b'level'][0], str(dlr_level).encode())
        self.assertEqual(callArgs[b'id_smsc'][0], b'abc')
        self.assertEqual(callArgs[b'sub'][0], b'3')
        self.assertEqual(callArgs[b'dlvrd'][0], b'3')
        self.assertEqual(callArgs[b'subdate'][0], b'anydate')
        self.assertEqual(callArgs[b'donedate'][0], b'anydate')
        self.assertEqual(callArgs[b'err'][0], b'')
        self.assertEqual(callArgs[b'text'][0], b'Any text')
        self.assertEqual(callArgs[b'connector'][0], dlr_connector.encode())


class SMPPDLRThrowerTestCases(RouterPBProxy, SMPPClientTestCases, SubmitSmTestCaseTools):
    @defer.inlineCallbacks
    def setUp(self):
        yield SMPPClientTestCases.setUp(self)

        # Lower the timeout config to pass the timeout tests quickly
        self.DLRThrower.config.timeout = 2
        self.DLRThrower.config.retry_delay = 1
        self.DLRThrower.config.max_retries = 2

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

        self.DLRThrower.ackMessage = Mock(wraps=self.DLRThrower.ackMessage)
        self.DLRThrower.rejectMessage = Mock(wraps=self.DLRThrower.rejectMessage)
        self.DLRThrower.smpp_dlr_callback = Mock(wraps=self.DLRThrower.smpp_dlr_callback)

        # Bind
        yield self.connect('127.0.0.1', self.pbPort)
        yield self.prepareRoutingsAndStartConnector()
        yield self.smppc_factory.connectAndBind()

        # Install mocks
        self.smppc_factory.lastProto.PDUDataRequestReceived = Mock(
            wraps=self.smppc_factory.lastProto.PDUDataRequestReceived)

        sub_date = datetime.datetime.now()
        yield self.publishDLRContentForSmppapi('ESME_ROK', 'MSGID', 'username', '999', '000', sub_date)

        yield waitFor(1)

        # Run tests
        self.assertEqual(self.smppc_factory.lastProto.PDUDataRequestReceived.call_count, 1)
        # the received pdu must be a DeliverSM
        received_pdu_1 = self.smppc_factory.lastProto.PDUDataRequestReceived.call_args_list[0][0][0]
        self.assertEqual(received_pdu_1.id, CommandId.deliver_sm)
        self.assertEqual(received_pdu_1.params['source_addr'], b'000')
        self.assertEqual(received_pdu_1.params['destination_addr'], b'999')
        self.assertEqual(received_pdu_1.params['receipted_message_id'], b'MSGID')
        self.assertEqual(received_pdu_1.params['message_state'], MessageState.ACCEPTED)
        self.assertEqual(received_pdu_1.params['short_message'],
                         ('id:MSGID submit date:%s done date:%s stat:ACCEPTD err:99' % (
                             sub_date.strftime("%y%m%d%H%M"),
                             sub_date.strftime("%y%m%d%H%M"),
                         )).encode())

        # Unbind & Disconnect
        yield self.smppc_factory.smpp.unbindAndDisconnect()
        yield self.stopSmppClientConnectors()

    @defer.inlineCallbacks
    def test_throwing_smpps_to_bound_connection(self):
        self.DLRThrower.ackMessage = Mock(wraps=self.DLRThrower.ackMessage)
        self.DLRThrower.rejectMessage = Mock(wraps=self.DLRThrower.rejectMessage)
        self.DLRThrower.smpp_dlr_callback = Mock(wraps=self.DLRThrower.smpp_dlr_callback)

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
        self.DLRThrower.ackMessage = Mock(wraps=self.DLRThrower.ackMessage)
        self.DLRThrower.rejectMessage = Mock(wraps=self.DLRThrower.rejectMessage)
        self.DLRThrower.rejectAndRequeueMessage = Mock(wraps=self.DLRThrower.rejectAndRequeueMessage)
        self.DLRThrower.smpp_dlr_callback = Mock(wraps=self.DLRThrower.smpp_dlr_callback)

        yield self.publishDLRContentForSmppapi('ESME_ROK', 'MSGID', 'username', '999', '000')

        yield waitFor(3)

        # Run tests
        self.assertEqual(self.DLRThrower.smpp_dlr_callback.call_count, 3)
        self.assertEqual(self.DLRThrower.ackMessage.call_count, 0)
        self.assertEqual(self.DLRThrower.rejectMessage.call_count, 3)
        self.assertEqual(self.DLRThrower.rejectAndRequeueMessage.call_count, 2)

    @defer.inlineCallbacks
    def test_throwing_smpps_with_no_deliverers(self):
        self.DLRThrower.ackMessage = Mock(wraps=self.DLRThrower.ackMessage)
        self.DLRThrower.rejectMessage = Mock(wraps=self.DLRThrower.rejectMessage)
        self.DLRThrower.rejectAndRequeueMessage = Mock(wraps=self.DLRThrower.rejectAndRequeueMessage)
        self.DLRThrower.smpp_dlr_callback = Mock(wraps=self.DLRThrower.smpp_dlr_callback)

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
        self.DLRThrower.ackMessage = Mock(wraps=self.DLRThrower.ackMessage)
        self.DLRThrower.rejectMessage = Mock(wraps=self.DLRThrower.rejectMessage)
        self.DLRThrower.rejectAndRequeueMessage = Mock(wraps=self.DLRThrower.rejectAndRequeueMessage)
        self.DLRThrower.smpp_dlr_callback = Mock(wraps=self.DLRThrower.smpp_dlr_callback)

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
