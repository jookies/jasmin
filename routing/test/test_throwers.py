# Copyright 2012 Fourat Zouari <fourat@gmail.com>
# See LICENSE for details.

import mock
from twisted.internet import reactor, defer
from twisted.trial import unittest
from jasmin.queues.factory import AmqpFactory
from jasmin.queues.configs import AmqpConfig
from jasmin.routing.configs import deliverSmThrowerConfig
from jasmin.routing.throwers import deliverSmThrower
from jasmin.routing.content import RoutedDeliverSmContent
from jasmin.routing.jasminApi import HttpConnector, SmppConnector
from smpp.pdu.operations import DeliverSM
from twisted.web.resource import Resource
from jasmin.routing.test.http_server import LeafServer, TimeoutLeafServer, AckServer, NoAckServer, Error404Server
from twisted.web import server

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
        deliverSmThrowerConfigInstance.retryDelay = 1
        deliverSmThrowerConfigInstance.maxRetries = 2
        
        # Launch the deliverSmThrower
        self.deliverSmThrower = deliverSmThrower()
        self.deliverSmThrower.setConfig(deliverSmThrowerConfigInstance)
        
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

class HTTPThrowingTestCases(deliverSmThrowerTestCase):
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
        self.Error404Server.stopListening()
        self.AckServer.stopListening()
        self.NoAckServer.stopListening()
        self.TimeoutLeafServer.stopListening()
    
    @defer.inlineCallbacks
    def test_throwing_http_connector_with_ack(self):
        self.AckServerResource.render_GET = mock.Mock(wraps=self.AckServerResource.render_GET)

        routedConnector = HttpConnector('dst', 'http://127.0.0.1:%s/send' % self.AckServer.getHost().port)
        content = 'test_throwing_http_connector test content'
        self.testDeliverSMPdu.params['short_message'] = content
        self.publishRoutedDeliverSmContent(self.routingKey, self.testDeliverSMPdu, '1', 'src', routedConnector)

        # Wait 3 seconds
        exitDeferred = defer.Deferred()
        reactor.callLater(3, exitDeferred.callback, None)
        yield exitDeferred
        
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

        # Wait 3 seconds
        exitDeferred = defer.Deferred()
        reactor.callLater(3, exitDeferred.callback, None)
        yield exitDeferred
        
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

        # Wait 9 seconds (timeout is set to 2 seconds in deliverSmThrowerTestCase.setUp(self)
        exitDeferred = defer.Deferred()
        reactor.callLater(12, exitDeferred.callback, None)
        yield exitDeferred
        
        self.assertEqual(self.TimeoutLeafServerResource.render_GET.call_count, 3)
        
    @defer.inlineCallbacks
    def test_throwing_http_connector_404_error_noretry(self):
        """When receiving a 404 error, no further retries shall be made
        """
        self.Error404ServerResource.render_GET = mock.Mock(wraps=self.Error404ServerResource.render_GET)

        routedConnector = HttpConnector('dst', 'http://127.0.0.1:%s/send' % self.Error404Server.getHost().port)
        
        self.publishRoutedDeliverSmContent(self.routingKey, self.testDeliverSMPdu, '1', 'src', routedConnector)

        # Wait 3 seconds
        exitDeferred = defer.Deferred()
        reactor.callLater(3, exitDeferred.callback, None)
        yield exitDeferred
        
        self.assertEqual(self.Error404ServerResource.render_GET.call_count, 1)

class SMPPThrowingTestCases(deliverSmThrowerTestCase):
    routingKey = 'deliver_sm_thrower.smpp'

    def test_throwing_smpp_connector(self):
        pass
    test_throwing_smpp_connector.skip = 'TODO'