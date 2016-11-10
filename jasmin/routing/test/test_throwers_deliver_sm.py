import binascii
import copy
from datetime import datetime, timedelta

import mock
from twisted.internet import reactor, defer
from twisted.trial import unittest
from twisted.web import server

from jasmin.queues.configs import AmqpConfig
from jasmin.queues.factory import AmqpFactory
from jasmin.routing.configs import deliverSmThrowerConfig
from jasmin.routing.content import RoutedDeliverSmContent
from jasmin.routing.jasminApi import HttpConnector, SmppServerSystemIdConnector
from jasmin.routing.proxies import RouterPBProxy
from jasmin.routing.test.http_server import TimeoutLeafServer, AckServer, NoAckServer, Error404Server
from jasmin.routing.test.test_router import SubmitSmTestCaseTools
from jasmin.routing.test.test_router_smpps import SMPPClientTestCases
from jasmin.routing.throwers import deliverSmThrower
from jasmin.vendor.smpp.pdu.operations import DeliverSM
from jasmin.vendor.smpp.pdu.pdu_types import *


@defer.inlineCallbacks
def waitFor(seconds):
    # Wait seconds
    waitDeferred = defer.Deferred()
    reactor.callLater(seconds, waitDeferred.callback, None)
    yield waitDeferred

class deliverSmThrowerTestCase(unittest.TestCase):
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
        deliverSmThrowerConfigInstance = deliverSmThrowerConfig()
        # Lower the timeout config to pass the timeout tests quickly
        deliverSmThrowerConfigInstance.timeout = 2
        deliverSmThrowerConfigInstance.retry_delay = 1
        deliverSmThrowerConfigInstance.max_retries = 2

        # Launch the deliverSmThrower
        self.deliverSmThrower = deliverSmThrower(deliverSmThrowerConfigInstance)

        # Add the broker to the deliverSmThrower
        yield self.deliverSmThrower.addAmqpBroker(self.amqpBroker)

        # Test vars:
        self.testDeliverSMPdu = DeliverSM(
            source_addr='1234',
            destination_addr='4567',
            short_message='hello !',
        )

    @defer.inlineCallbacks
    def publishRoutedDeliverSmContent(self, routing_key, DeliverSM, msgid, scid, routedConnector):
        content = RoutedDeliverSmContent(DeliverSM, msgid, scid, routedConnector)
        yield self.amqpBroker.publish(exchange='messaging', routing_key=routing_key, content=content)

    @defer.inlineCallbacks
    def tearDown(self):
        yield self.amqpBroker.disconnect()
        yield self.deliverSmThrower.stopService()

class HTTPDeliverSmThrowingTestCases(deliverSmThrowerTestCase):
    routingKey = 'deliver_sm_thrower.http'

    @defer.inlineCallbacks
    def setUp(self):
        yield deliverSmThrowerTestCase.setUp(self)

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
    def tearDown(self):
        yield deliverSmThrowerTestCase.tearDown(self)
        yield self.Error404Server.stopListening()
        yield self.AckServer.stopListening()
        yield self.NoAckServer.stopListening()
        yield self.TimeoutLeafServer.stopListening()

    @defer.inlineCallbacks
    def test_throwing_http_connector_with_ack(self):
        self.AckServerResource.render_GET = mock.Mock(wraps=self.AckServerResource.render_GET)

        routedConnector = HttpConnector('dst', 'http://127.0.0.1:%s/send' % self.AckServer.getHost().port)
        content = 'test_throwing_http_connector test content'
        self.testDeliverSMPdu.params['short_message'] = content
        self.publishRoutedDeliverSmContent(self.routingKey, self.testDeliverSMPdu, '1', 'src', routedConnector)

        yield waitFor(1)

        # No message retries must be made since ACK was received
        self.assertEqual(self.AckServerResource.render_GET.call_count, 1)

        callArgs = self.AckServerResource.render_GET.call_args_list[0][0][0].args
        self.assertEqual(callArgs['content'][0], self.testDeliverSMPdu.params['short_message'])
        self.assertEqual(callArgs['from'][0], self.testDeliverSMPdu.params['source_addr'])
        self.assertEqual(callArgs['to'][0], self.testDeliverSMPdu.params['destination_addr'])

    @defer.inlineCallbacks
    def test_throwing_http_connector_without_ack(self):
        self.NoAckServerResource.render_GET = mock.Mock(wraps=self.NoAckServerResource.render_GET)

        routedConnector = HttpConnector('dst', 'http://127.0.0.1:%s/send' % self.NoAckServer.getHost().port)
        content = 'test_throwing_http_connector test content'
        self.testDeliverSMPdu.params['short_message'] = content
        self.publishRoutedDeliverSmContent(self.routingKey, self.testDeliverSMPdu, '1', 'src', routedConnector)

        yield waitFor(4)

        # Retries must be made when ACK is not received
        self.assertTrue(self.NoAckServerResource.render_GET.call_count > 1)

        callArgs = self.NoAckServerResource.render_GET.call_args_list[0][0][0].args
        self.assertEqual(callArgs['content'][0], self.testDeliverSMPdu.params['short_message'])
        self.assertEqual(callArgs['from'][0], self.testDeliverSMPdu.params['source_addr'])
        self.assertEqual(callArgs['to'][0], self.testDeliverSMPdu.params['destination_addr'])

    @defer.inlineCallbacks
    def test_throwing_http_connector_timeout_retry(self):
        self.TimeoutLeafServerResource.render_GET = mock.Mock(wraps=self.TimeoutLeafServerResource.render_GET)

        routedConnector = HttpConnector('dst', 'http://127.0.0.1:%s/send' % self.TimeoutLeafServer.getHost().port)

        self.publishRoutedDeliverSmContent(self.routingKey, self.testDeliverSMPdu, '1', 'src', routedConnector)

        # Wait 12 seconds (timeout is set to 2 seconds in deliverSmThrowerTestCase.setUp(self)
        yield waitFor(12)

        self.assertEqual(self.TimeoutLeafServerResource.render_GET.call_count, 3)

    @defer.inlineCallbacks
    def test_throwing_http_connector_404_error_noretry(self):
        """When receiving a 404 error, no further retries shall be made
        """
        self.Error404ServerResource.render_GET = mock.Mock(wraps=self.Error404ServerResource.render_GET)

        routedConnector = HttpConnector('dst', 'http://127.0.0.1:%s/send' % self.Error404Server.getHost().port)

        self.publishRoutedDeliverSmContent(self.routingKey, self.testDeliverSMPdu, '1', 'src', routedConnector)

        # Wait 1 second
        yield waitFor(1)

        self.assertEqual(self.Error404ServerResource.render_GET.call_count, 1)

    @defer.inlineCallbacks
    def test_throwing_validity_parameter(self):
        self.AckServerResource.render_GET = mock.Mock(wraps=self.AckServerResource.render_GET)

        routedConnector = HttpConnector('dst', 'http://127.0.0.1:%s/send' % self.AckServer.getHost().port)
        content = 'test_throwing_http_connector test content'
        self.testDeliverSMPdu.params['short_message'] = content

        # Set validity_period in deliver_sm and send it
        deliver_sm = copy.copy(self.testDeliverSMPdu)
        vp = datetime.today() + timedelta(minutes=20)
        deliver_sm.params['validity_period'] = vp
        self.publishRoutedDeliverSmContent(self.routingKey, self.testDeliverSMPdu, '1', 'src', routedConnector)

        yield waitFor(1)

        # No message retries must be made since ACK was received
        self.assertEqual(self.AckServerResource.render_GET.call_count, 1)

        callArgs = self.AckServerResource.render_GET.call_args_list[0][0][0].args
        self.assertTrue('validity' in callArgs)
        self.assertEqual(str(vp), callArgs['validity'][0])

    @defer.inlineCallbacks
    def test_throwing_http_utf16(self):
        """Related to #320
        Send utf16-be content and check it was throwed while preserving the content as is"""
        self.AckServerResource.render_GET = mock.Mock(wraps=self.AckServerResource.render_GET)

        routedConnector = HttpConnector('dst', 'http://127.0.0.1:%s/send' % self.AckServer.getHost().port)
        content = "\x06\x2A\x06\x33\x06\x2A"
        self.testDeliverSMPdu.params['short_message'] = content
        self.testDeliverSMPdu.params['data_coding'] = DataCoding(schemeData=DataCodingDefault.UCS2)
        self.publishRoutedDeliverSmContent(self.routingKey, self.testDeliverSMPdu, '1', 'src', routedConnector)

        yield waitFor(1)

        # Assert throwed content is equal to original content
        callArgs = self.AckServerResource.render_GET.call_args_list[0][0][0].args
        self.assertEqual(callArgs['content'][0], content)
        self.assertEqual(callArgs['coding'][0], '8')
        self.assertEqual(callArgs['binary'][0], binascii.hexlify(content))

    @defer.inlineCallbacks
    def test_throwing_http_utf8(self):
        """Related to #320
        Send utf8 content and check it was throwed while preserving the content as is"""
        self.AckServerResource.render_GET = mock.Mock(wraps=self.AckServerResource.render_GET)

        routedConnector = HttpConnector('dst', 'http://127.0.0.1:%s/send' % self.AckServer.getHost().port)
        content = "\xd8\xaa\xd8\xb3\xd8\xaa"
        self.testDeliverSMPdu.params['short_message'] = content
        self.testDeliverSMPdu.params['data_coding'] = DataCoding(schemeData=DataCodingDefault.UCS2)
        self.publishRoutedDeliverSmContent(self.routingKey, self.testDeliverSMPdu, '1', 'src', routedConnector)

        yield waitFor(1)

        # Assert throwed content is equal to original content
        callArgs = self.AckServerResource.render_GET.call_args_list[0][0][0].args
        self.assertEqual(callArgs['content'][0], content)
        self.assertEqual(callArgs['coding'][0], '8')
        self.assertEqual(callArgs['binary'][0], binascii.hexlify(content))

    @defer.inlineCallbacks
    def test_throwing_http_with_message_payload(self):
        """Related to #380
        Will throw via http a pdu having 'message_payload' instead of 'short_message' parameter
        """
        self.AckServerResource.render_GET = mock.Mock(wraps=self.AckServerResource.render_GET)

        routedConnector = HttpConnector('dst', 'http://127.0.0.1:%s/send' % self.AckServer.getHost().port)
        content = 'test_throwing_http_with_message_payload test content'
        del self.testDeliverSMPdu.params['short_message']
        self.testDeliverSMPdu.params['message_payload'] = content
        self.publishRoutedDeliverSmContent(self.routingKey, self.testDeliverSMPdu, '1', 'src', routedConnector)

        yield waitFor(1)

        # No message retries must be made since ACK was received
        self.assertEqual(self.AckServerResource.render_GET.call_count, 1)

        callArgs = self.AckServerResource.render_GET.call_args_list[0][0][0].args
        self.assertEqual(callArgs['content'][0], content)
        self.assertEqual(callArgs['from'][0], self.testDeliverSMPdu.params['source_addr'])
        self.assertEqual(callArgs['to'][0], self.testDeliverSMPdu.params['destination_addr'])

    @defer.inlineCallbacks
    def test_throwing_http_without_priority(self):
        """Related to #380
        Will throw via http a pdu having no priority_flag parameter
        """
        self.AckServerResource.render_GET = mock.Mock(wraps=self.AckServerResource.render_GET)

        routedConnector = HttpConnector('dst', 'http://127.0.0.1:%s/send' % self.AckServer.getHost().port)
        content = 'test_throwing_http_without_priority test content'
        del self.testDeliverSMPdu.params['priority_flag']
        self.testDeliverSMPdu.params['short_message'] = content
        self.publishRoutedDeliverSmContent(self.routingKey, self.testDeliverSMPdu, '1', 'src', routedConnector)

        yield waitFor(1)

        # No message retries must be made since ACK was received
        self.assertEqual(self.AckServerResource.render_GET.call_count, 1)

    @defer.inlineCallbacks
    def test_throwing_http_without_coding(self):
        """Related to #380
        Will throw via http a pdu having no data_coding parameter
        """
        self.AckServerResource.render_GET = mock.Mock(wraps=self.AckServerResource.render_GET)

        routedConnector = HttpConnector('dst', 'http://127.0.0.1:%s/send' % self.AckServer.getHost().port)
        content = 'test_throwing_http_without_coding test content'
        del self.testDeliverSMPdu.params['data_coding']
        self.testDeliverSMPdu.params['short_message'] = content
        self.publishRoutedDeliverSmContent(self.routingKey, self.testDeliverSMPdu, '1', 'src', routedConnector)

        yield waitFor(1)

        # No message retries must be made since ACK was received
        self.assertEqual(self.AckServerResource.render_GET.call_count, 1)

    @defer.inlineCallbacks
    def test_throwing_http_without_validity(self):
        """Related to #380
        Will throw via http a pdu having no validity_period parameter
        """
        self.AckServerResource.render_GET = mock.Mock(wraps=self.AckServerResource.render_GET)

        routedConnector = HttpConnector('dst', 'http://127.0.0.1:%s/send' % self.AckServer.getHost().port)
        content = 'test_throwing_http_without_priority test content'
        del self.testDeliverSMPdu.params['validity_period']
        self.testDeliverSMPdu.params['short_message'] = content
        self.publishRoutedDeliverSmContent(self.routingKey, self.testDeliverSMPdu, '1', 'src', routedConnector)

        yield waitFor(1)

        # No message retries must be made since ACK was received
        self.assertEqual(self.AckServerResource.render_GET.call_count, 1)

class SMPPDeliverSmThrowerTestCases(RouterPBProxy, SMPPClientTestCases, SubmitSmTestCaseTools):
    routingKey = 'deliver_sm_thrower.smpps'

    @defer.inlineCallbacks
    def setUp(self):
        yield SMPPClientTestCases.setUp(self)

        # Initiating config objects without any filename
        # will lead to setting defaults and that's what we
        # need to run the tests
        deliverSmThrowerConfigInstance = deliverSmThrowerConfig()
        # Lower the timeout config to pass the timeout tests quickly
        deliverSmThrowerConfigInstance.timeout = 2
        deliverSmThrowerConfigInstance.retry_delay = 1
        deliverSmThrowerConfigInstance.max_retries = 2

        # Launch the deliverSmThrower
        self.deliverSmThrower = deliverSmThrower(deliverSmThrowerConfigInstance)

        # Add the broker to the deliverSmThrower
        yield self.deliverSmThrower.addAmqpBroker(self.amqpBroker)

        # Add SMPPs factory to DLRThrower
        self.deliverSmThrower.addSmpps(self.smpps_factory)

        # Test vars:
        self.testDeliverSMPdu = DeliverSM(
            source_addr='1234',
            destination_addr='4567',
            short_message='hello !',
        )

    @defer.inlineCallbacks
    def publishRoutedDeliverSmContent(self, routing_key, DeliverSM, msgid, scid, routedConnector):
        content = RoutedDeliverSmContent(DeliverSM, msgid, scid, routedConnector)
        yield self.amqpBroker.publish(exchange='messaging', routing_key=routing_key, content=content)

    @defer.inlineCallbacks
    def tearDown(self):
        yield SMPPClientTestCases.tearDown(self)
        yield self.deliverSmThrower.stopService()

    @defer.inlineCallbacks
    def test_throwing_smpps_to_bound_connection(self):
        self.deliverSmThrower.ackMessage = mock.Mock(wraps=self.deliverSmThrower.ackMessage)
        self.deliverSmThrower.rejectMessage = mock.Mock(wraps=self.deliverSmThrower.rejectMessage)
        self.deliverSmThrower.smpp_deliver_sm_callback = mock.Mock(wraps=self.deliverSmThrower.smpp_deliver_sm_callback)

        # Bind
        yield self.connect('127.0.0.1', self.pbPort)
        yield self.prepareRoutingsAndStartConnector()
        yield self.smppc_factory.connectAndBind()

        routedConnector = SmppServerSystemIdConnector('username')
        yield self.publishRoutedDeliverSmContent(self.routingKey,
            self.testDeliverSMPdu,
            '1',
            'src',
            routedConnector)

        yield waitFor(1)

        # Run tests
        self.assertEqual(self.deliverSmThrower.smpp_deliver_sm_callback.call_count, 1)
        self.assertEqual(self.deliverSmThrower.ackMessage.call_count, 1)
        self.assertEqual(self.deliverSmThrower.rejectMessage.call_count, 0)

        # Unbind & Disconnect
        yield self.smppc_factory.smpp.unbindAndDisconnect()
        yield self.stopSmppClientConnectors()

    @defer.inlineCallbacks
    def test_throwing_smpps_to_not_bound_connection(self):
        self.deliverSmThrower.ackMessage = mock.Mock(wraps=self.deliverSmThrower.ackMessage)
        self.deliverSmThrower.rejectMessage = mock.Mock(wraps=self.deliverSmThrower.rejectMessage)
        self.deliverSmThrower.rejectAndRequeueMessage = mock.Mock(wraps=self.deliverSmThrower.rejectAndRequeueMessage)
        self.deliverSmThrower.smpp_deliver_sm_callback = mock.Mock(wraps=self.deliverSmThrower.smpp_deliver_sm_callback)

        routedConnector = SmppServerSystemIdConnector('username')
        yield self.publishRoutedDeliverSmContent(self.routingKey,
            self.testDeliverSMPdu,
            '1',
            'src',
            routedConnector)

        yield waitFor(3)

        # Run tests
        self.assertEqual(self.deliverSmThrower.smpp_deliver_sm_callback.call_count, 3)
        self.assertEqual(self.deliverSmThrower.ackMessage.call_count, 0)
        self.assertEqual(self.deliverSmThrower.rejectMessage.call_count, 3)
        self.assertEqual(self.deliverSmThrower.rejectAndRequeueMessage.call_count, 2)

    @defer.inlineCallbacks
    def test_throwing_smpps_with_no_deliverers(self):
        self.deliverSmThrower.ackMessage = mock.Mock(wraps=self.deliverSmThrower.ackMessage)
        self.deliverSmThrower.rejectMessage = mock.Mock(wraps=self.deliverSmThrower.rejectMessage)
        self.deliverSmThrower.rejectAndRequeueMessage = mock.Mock(wraps=self.deliverSmThrower.rejectAndRequeueMessage)
        self.deliverSmThrower.smpp_deliver_sm_callback = mock.Mock(wraps=self.deliverSmThrower.smpp_deliver_sm_callback)

        # Bind (as a transmitter so we get no deliverers for DLR)
        yield self.connect('127.0.0.1', self.pbPort)
        yield self.prepareRoutingsAndStartConnector()
        self.smppc_config.bindOperation = 'transmitter'
        yield self.smppc_factory.connectAndBind()

        routedConnector = SmppServerSystemIdConnector('username')
        yield self.publishRoutedDeliverSmContent(self.routingKey,
            self.testDeliverSMPdu,
            '1',
            'src',
            routedConnector)

        yield waitFor(3)

        # Run tests
        self.assertEqual(self.deliverSmThrower.smpp_deliver_sm_callback.call_count, 3)
        self.assertEqual(self.deliverSmThrower.ackMessage.call_count, 0)
        self.assertEqual(self.deliverSmThrower.rejectMessage.call_count, 3)
        self.assertEqual(self.deliverSmThrower.rejectAndRequeueMessage.call_count, 2)

        # Unbind & Disconnect
        yield self.smppc_factory.smpp.unbindAndDisconnect()
        yield self.stopSmppClientConnectors()

    @defer.inlineCallbacks
    def test_throwing_smpps_without_smppsFactory(self):
        self.deliverSmThrower.ackMessage = mock.Mock(wraps=self.deliverSmThrower.ackMessage)
        self.deliverSmThrower.rejectMessage = mock.Mock(wraps=self.deliverSmThrower.rejectMessage)
        self.deliverSmThrower.rejectAndRequeueMessage = mock.Mock(wraps=self.deliverSmThrower.rejectAndRequeueMessage)
        self.deliverSmThrower.smpp_deliver_sm_callback = mock.Mock(wraps=self.deliverSmThrower.smpp_deliver_sm_callback)

        # Remove smpps from self.DLRThrower
        self.deliverSmThrower.smpps = None
        self.deliverSmThrower.smpps_access = None

        routedConnector = SmppServerSystemIdConnector('username')
        yield self.publishRoutedDeliverSmContent(self.routingKey,
            self.testDeliverSMPdu,
            '1',
            'src',
            routedConnector)

        yield waitFor(1)

        # Run tests
        self.assertEqual(self.deliverSmThrower.smpp_deliver_sm_callback.call_count, 1)
        self.assertEqual(self.deliverSmThrower.ackMessage.call_count, 0)
        self.assertEqual(self.deliverSmThrower.rejectMessage.call_count, 1)
        self.assertEqual(self.deliverSmThrower.rejectAndRequeueMessage.call_count, 0)
