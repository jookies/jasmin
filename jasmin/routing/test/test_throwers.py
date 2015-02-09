import mock
from twisted.internet import reactor, defer
from twisted.trial import unittest
from jasmin.queues.factory import AmqpFactory
from jasmin.queues.configs import AmqpConfig
from jasmin.routing.configs import deliverSmThrowerConfig, DLRThrowerConfig
from jasmin.routing.throwers import deliverSmThrower, DLRThrower
from jasmin.routing.content import RoutedDeliverSmContent
from jasmin.managers.content import DLRContentForHttpapi
from jasmin.routing.jasminApi import HttpConnector, SmppClientConnector
from jasmin.vendor.smpp.pdu.operations import DeliverSM
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
        deliverSmThrowerConfigInstance.retry_delay = 1
        deliverSmThrowerConfigInstance.max_retries = 2
        
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

        # Wait 4 seconds
        exitDeferred = defer.Deferred()
        reactor.callLater(4, exitDeferred.callback, None)
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

        # Wait 4 seconds
        exitDeferred = defer.Deferred()
        reactor.callLater(4, exitDeferred.callback, None)
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

        # Wait 12 seconds (timeout is set to 2 seconds in deliverSmThrowerTestCase.setUp(self)
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

        # Wait 4 seconds
        exitDeferred = defer.Deferred()
        reactor.callLater(4, exitDeferred.callback, None)
        yield exitDeferred
        
        self.assertEqual(self.Error404ServerResource.render_GET.call_count, 1)

class DLRThrowerTestCase(unittest.TestCase):
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
        
        # Launch the deliverSmThrower
        self.DLRThrower = DLRThrower()
        self.DLRThrower.setConfig(DLRThrowerConfigInstance)
        
        # Add the broker to the deliverSmThrower
        yield self.DLRThrower.addAmqpBroker(self.amqpBroker)

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
    def publishDLRContentForHttpapi(self, message_status, msgid, dlr_url, dlr_level, id_smsc = '', sub = '', 
                 dlvrd = '', subdate = '', donedate = '', err = '', text = '', method = 'POST', trycount = 0):
        content = DLRContentForHttpapi(message_status, msgid, dlr_url, dlr_level, id_smsc, sub, dlvrd, subdate, 
                             donedate, err, text, method, trycount)
        yield self.amqpBroker.publish(exchange='messaging', routing_key='dlr_thrower.http', content=content)
    
    @defer.inlineCallbacks
    def tearDown(self):
        yield self.amqpBroker.disconnect()
        yield self.DLRThrower.stopService()
        
        yield self.Error404Server.stopListening()
        yield self.AckServer.stopListening()
        yield self.NoAckServer.stopListening()
        yield self.TimeoutLeafServer.stopListening()
        
    @defer.inlineCallbacks
    def test_throwing_http_connector_with_ack(self):
        self.AckServerResource.render_POST = mock.Mock(wraps=self.AckServerResource.render_POST)

        dlr_url = 'http://127.0.0.1:%s/dlr' % self.AckServer.getHost().port
        dlr_level = 1
        msgid = 'anything'
        message_status = 'DELIVRD'
        self.publishDLRContentForHttpapi(message_status, msgid, dlr_url, dlr_level)

        # Wait 3 seconds
        exitDeferred = defer.Deferred()
        reactor.callLater(3, exitDeferred.callback, None)
        yield exitDeferred
        
        # No message retries must be made since ACK was received
        self.assertEqual(self.AckServerResource.render_POST.call_count, 1)

    @defer.inlineCallbacks
    def test_throwing_http_connector_without_ack(self):
        self.NoAckServerResource.render_POST = mock.Mock(wraps=self.NoAckServerResource.render_POST)

        dlr_url = 'http://127.0.0.1:%s/dlr' % self.NoAckServer.getHost().port
        dlr_level = 1
        msgid = 'anything'
        message_status = 'DELIVRD'
        self.publishDLRContentForHttpapi(message_status, msgid, dlr_url, dlr_level)

        # Wait 3 seconds
        exitDeferred = defer.Deferred()
        reactor.callLater(3, exitDeferred.callback, None)
        yield exitDeferred
        
        # Retries must be made when ACK is not received
        self.assertTrue(self.NoAckServerResource.render_POST.call_count > 1)

    @defer.inlineCallbacks
    def test_throwing_http_connector_timeout_retry(self):
        self.TimeoutLeafServerResource.render_POST = mock.Mock(wraps=self.TimeoutLeafServerResource.render_POST)

        dlr_url = 'http://127.0.0.1:%s/dlr' % self.TimeoutLeafServer.getHost().port
        dlr_level = 1
        msgid = 'anything'
        message_status = 'DELIVRD'
        self.publishDLRContentForHttpapi(message_status, msgid, dlr_url, dlr_level)

        # Wait 9 seconds (timeout is set to 2 seconds in deliverSmThrowerTestCase.setUp(self)
        exitDeferred = defer.Deferred()
        reactor.callLater(12, exitDeferred.callback, None)
        yield exitDeferred
        
        self.assertEqual(self.TimeoutLeafServerResource.render_POST.call_count, 3)
        
    @defer.inlineCallbacks
    def test_throwing_http_connector_404_error_noretry(self):
        """When receiving a 404 error, no further retries shall be made
        """
        self.Error404ServerResource.render_POST = mock.Mock(wraps=self.Error404ServerResource.render_POST)

        dlr_url = 'http://127.0.0.1:%s/dlr' % self.Error404Server.getHost().port
        dlr_level = 1
        msgid = 'anything'
        message_status = 'DELIVRD'
        self.publishDLRContentForHttpapi(message_status, msgid, dlr_url, dlr_level)

        # Wait 3 seconds
        exitDeferred = defer.Deferred()
        reactor.callLater(3, exitDeferred.callback, None)
        yield exitDeferred
        
        self.assertEqual(self.Error404ServerResource.render_POST.call_count, 1)
        
    @defer.inlineCallbacks
    def test_throwing_http_connector_dlr_level1(self):
        self.AckServerResource.render_GET = mock.Mock(wraps=self.AckServerResource.render_GET)

        dlr_url = 'http://127.0.0.1:%s/dlr' % self.AckServer.getHost().port
        dlr_level = 1
        msgid = 'anything'
        message_status = 'DELIVRD'
        self.publishDLRContentForHttpapi(message_status, msgid, dlr_url, dlr_level, method = 'GET')

        # Wait 3 seconds
        exitDeferred = defer.Deferred()
        reactor.callLater(3, exitDeferred.callback, None)
        yield exitDeferred
        
        # No message retries must be made since ACK was received
        self.assertEqual(self.AckServerResource.render_GET.call_count, 1)
        callArgs = self.AckServerResource.render_GET.call_args_list[0][0][0].args
        self.assertEqual(callArgs['message_status'][0], message_status)
        self.assertEqual(callArgs['id'][0], msgid)
        self.assertEqual(callArgs['level'][0], str(dlr_level))

    @defer.inlineCallbacks
    def test_throwing_http_connector_dlr_level2(self):
        self.AckServerResource.render_GET = mock.Mock(wraps=self.AckServerResource.render_GET)

        dlr_url = 'http://127.0.0.1:%s/dlr' % self.AckServer.getHost().port
        dlr_level = 2
        msgid = 'anything'
        message_status = 'DELIVRD'
        self.publishDLRContentForHttpapi(message_status, msgid, dlr_url, dlr_level, id_smsc = 'abc', sub = '3', 
                 dlvrd = '3', subdate = 'anydate', donedate = 'anydate', err = '', text = 'Any text', method = 'GET')

        # Wait 3 seconds
        exitDeferred = defer.Deferred()
        reactor.callLater(3, exitDeferred.callback, None)
        yield exitDeferred
        
        # No message retries must be made since ACK was received
        self.assertEqual(self.AckServerResource.render_GET.call_count, 1)
        callArgs = self.AckServerResource.render_GET.call_args_list[0][0][0].args
        self.assertEqual(callArgs['message_status'][0], message_status)
        self.assertEqual(callArgs['id'][0], msgid)
        self.assertEqual(callArgs['level'][0], str(dlr_level))
        self.assertEqual(callArgs['id_smsc'][0], 'abc')
        self.assertEqual(callArgs['sub'][0], '3')
        self.assertEqual(callArgs['dlvrd'][0], '3')
        self.assertEqual(callArgs['subdate'][0], 'anydate')
        self.assertEqual(callArgs['donedate'][0], 'anydate')
        self.assertEqual(callArgs['err'][0], '')
        self.assertEqual(callArgs['text'][0], 'Any text')
