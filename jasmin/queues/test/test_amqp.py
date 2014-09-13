"""
Test cases for AmqpFactory
These are test cases for only Jasmin's code, smpp.twisted tests are not included here
"""

import time
import logging
from twisted.trial.unittest import TestCase
from twisted.internet import defer, reactor
from jasmin.queues.configs import AmqpConfig
from jasmin.queues.factory import AmqpFactory
from txamqp.queue import Closed
from txamqp.content import Content

class AmqpTestCase(TestCase):
    exchange_name = "CONNECTOR-00"
    message = "Any Message"

    configArgs = {
        'reconnectOnConnectionFailure': False,
        'reconnectOnConnectionLoss': False,
    }
    
    def setUp(self):

        self.config = AmqpConfig()
        self.config.host = self.configArgs.get('amqp_host', 'localhost')
        self.config.port = self.configArgs.get('amqp_port', 5672)
        self.config.username = self.configArgs.get('amqp_username', 'guest')
        self.config.password = self.configArgs.get('amqp_password', 'guest')
        self.config.log_level = self.configArgs.get('amqp_log_level', logging.DEBUG)
        self.config.reconnectOnConnectionFailure = self.configArgs.get('reconnectOnConnectionFailure', True)
        self.config.reconnectOnConnectionLoss = self.configArgs.get('reconnectOnConnectionLoss', True)
        
        self.amqp = None
    
    def requirement_disclaimer(self):
        print   "failed to connect to an AMQP broker; These tests are designed" \
                " to run against a running instance of a AMQP broker"
                
    @defer.inlineCallbacks
    def connect(self):
        self.amqp = AmqpFactory(self.config)
        
        try:
            yield self.amqp.connect()
        except:
            self.requirement_disclaimer()
            raise
        
        yield self.amqp.getChannelReadyDeferred()
    
class ConnectTestCase(AmqpTestCase):
    
    @defer.inlineCallbacks
    def test_connect(self):
        yield self.connect()
        
        yield self.amqp.disconnect()
        
    @defer.inlineCallbacks
    def test_connect_and_exchange_declare(self):
        yield self.connect()
        
        exchange_name = '%s_randomName' % time.time()
        
        yield self.amqp.chan.exchange_declare(exchange=exchange_name, type="fanout")

        yield self.amqp.disconnect()
        
class PublishTestCase(AmqpTestCase):
    
    @defer.inlineCallbacks
    def test_publish_to_topic_exchange(self):
        yield self.connect()
        
        yield self.amqp.chan.exchange_declare(exchange='%s_topic' % self.exchange_name, type="topic", durable=True)

        yield self.amqp.publish(exchange=self.exchange_name, routing_key="submit.sm", content=Content(self.message))
        
        yield self.amqp.disconnect()
    
    @defer.inlineCallbacks
    def test_publish_to_direct_exchange(self):
        yield self.connect()
        
        yield self.amqp.chan.exchange_declare(exchange='%s_direct' % self.exchange_name, type="direct", durable=True)

        yield self.amqp.publish(exchange=self.exchange_name, routing_key="submit_sm", content=Content(self.message))
        
        yield self.amqp.disconnect()
    
    @defer.inlineCallbacks
    def test_publish_to_fanout_exchange(self):
        yield self.connect()
        
        yield self.amqp.chan.exchange_declare(exchange='%s_fanout' % self.exchange_name, type="fanout", durable=True)

        yield self.amqp.publish(exchange=self.exchange_name, routing_key="submit_sm", content=Content(self.message))
        
        yield self.amqp.disconnect()
    
    @defer.inlineCallbacks
    def test_publish_to_queue(self):
        yield self.connect()
        
        yield self.amqp.named_queue_declare(queue="submit.sm.connector01")

        yield self.amqp.publish(routing_key="submit.sm.connector01", content=Content(self.message))
        
        yield self.amqp.disconnect()

class ConsumeTools(AmqpTestCase):
    consumedMessages = 0
    
    def _callback(self, message, queue):
        queue.get().addCallback(self._callback, queue).addErrback(self._errback)
        #print " [x] Received %r" % (message,)
        self.consumedMessages = self.consumedMessages + 1
    
    def _errback(self, error):
        """It appears that when closing a queue with the close() method it errbacks with
        a txamqp.queue.Closed exception, didnt find a clean way to stop consuming a queue
        without errbacking here so this is a workaround to make it clean, it can be considered
        as a @TODO requiring knowledge of the queue api behaviour
        """
        if error.check(Closed) == None:
            print "Error in _errback %s" % (error)
            return error

class ConsumeTestCase(ConsumeTools):    
    @defer.inlineCallbacks
    def test_consume_queue(self):
        yield self.connect()
        
        yield self.amqp.named_queue_declare(queue="submit.sm.connector01")
        
        yield self.amqp.chan.basic_consume(queue="submit.sm.connector01", no_ack=True, consumer_tag='qtag')
        self.queue = yield self.amqp.client.queue('qtag')
        self.queue.get().addCallback(self._callback, self.queue).addErrback(self._errback)
        
        # Wait for 2 seconds
        exitDeferred = defer.Deferred()
        reactor.callLater(2, exitDeferred.callback, None)        
        yield exitDeferred                
        
        yield self.queue.close()
        yield self.amqp.disconnect()

class PublishConsumeTestCase(ConsumeTools):    
    @defer.inlineCallbacks
    def test_simple_publish_consume(self):
        yield self.connect()
        
        yield self.amqp.named_queue_declare(queue="submit.sm.connector01")
        
        # Consume
        yield self.amqp.chan.basic_consume(queue="submit.sm.connector01", no_ack=True, consumer_tag='qtag')
        queue = yield self.amqp.client.queue('qtag')
        queue.get().addCallback(self._callback, queue).addErrback(self._errback)
        
        # Publish
        yield self.amqp.publish(routing_key="submit.sm.connector01", content=Content(self.message))

        # Wait for 2 seconds
        # (give some time to the consumer to get its work done)
        exitDeferred = defer.Deferred()
        reactor.callLater(2, exitDeferred.callback, None)        
        yield exitDeferred        

        yield queue.close()

        yield self.amqp.disconnect()
        
        self.assertEqual(self.consumedMessages, 1)
        
    @defer.inlineCallbacks
    def test_simple_publish_consume_by_topic(self):
        yield self.connect()
        
        yield self.amqp.chan.exchange_declare(exchange='messaging', type='topic')

        # Consume
        yield self.amqp.named_queue_declare(queue="submit.sm_all")
        yield self.amqp.chan.queue_bind(queue="submit.sm_all", exchange="messaging", routing_key="submit.sm.*")
        yield self.amqp.chan.basic_consume(queue="submit.sm_all", no_ack=True, consumer_tag='qtag')
        queue = yield self.amqp.client.queue('qtag')
        queue.get().addCallback(self._callback, queue).addErrback(self._errback)
        
        # Publish
        yield self.amqp.publish(exchange='messaging', routing_key="submit.sm.connector01", content=Content(self.message))

        # Wait for 2 seconds
        # (give some time to the consumer to get its work done)
        exitDeferred = defer.Deferred()
        reactor.callLater(2, exitDeferred.callback, None)        
        yield exitDeferred        
        
        yield queue.close()

        yield self.amqp.disconnect()
        
        self.assertEqual(self.consumedMessages, 1)

    @defer.inlineCallbacks
    def test_publish_consume_from_different_queues(self):
        yield self.connect()
        
        yield self.amqp.named_queue_declare(queue="submit.sm.connector01")
        yield self.amqp.named_queue_declare(queue="deliver.sm.connector01")
        
        # Consume
        yield self.amqp.chan.basic_consume(queue="submit.sm.connector01", no_ack=True, consumer_tag='submit_sm_consumer')
        yield self.amqp.chan.basic_consume(queue="deliver.sm.connector01", no_ack=True, consumer_tag='deliver_sm_consumer')
        self.submit_sm_q = yield self.amqp.client.queue('submit_sm_consumer')
        self.deliver_sm_q = yield self.amqp.client.queue('deliver_sm_consumer')
        self.submit_sm_q.get().addCallback(self._callback, self.submit_sm_q).addErrback(self._errback)
        self.deliver_sm_q.get().addCallback(self._callback, self.deliver_sm_q).addErrback(self._errback)
        
        # Publish
        yield self.amqp.publish(routing_key="submit.sm.connector01", content=Content(self.message))
        yield self.amqp.publish(routing_key="deliver.sm.connector01", content=Content(self.message))

        # Wait for 2 seconds
        # (give some time to the consumer to get its work done)
        exitDeferred = defer.Deferred()
        reactor.callLater(2, exitDeferred.callback, None)        
        yield exitDeferred                

        yield self.submit_sm_q.close()
        yield self.deliver_sm_q.close()

        yield self.amqp.disconnect()
        
        self.assertEqual(self.consumedMessages, 2)
