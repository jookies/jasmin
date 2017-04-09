"""
Test cases for AmqpFactory
These are test cases for only Jasmin's code, smpp.twisted tests are not included here
"""

import logging
import time
import uuid

from twisted.internet import defer, reactor
from twisted.trial.unittest import TestCase
from txamqp.content import Content
from txamqp.queue import Closed

from jasmin.queues.configs import AmqpConfig
from jasmin.queues.factory import AmqpFactory


@defer.inlineCallbacks
def waitFor(seconds):
    # Wait seconds
    waitDeferred = defer.Deferred()
    reactor.callLater(seconds, waitDeferred.callback, None)
    yield waitDeferred


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
    
    @defer.inlineCallbacks
    def _callback(self, message, queue, ack = False):
        queue.get().addCallback(self._callback, queue, ack = ack).addErrback(self._errback)
        #print " [x] Received %r" % (message,)
        self.consumedMessages = self.consumedMessages + 1

        if ack:
            yield self.amqp.chan.basic_ack(message.delivery_tag)
    
    def _errback(self, error):
        """It appears that when closing a queue with the close() method it errbacks with
        a txamqp.queue.Closed exception, didnt find a clean way to stop consuming a queue
        without errbacking here so this is a workaround to make it clean, it can be considered
        as a @TODO requiring knowledge of the queue api behaviour
        """
        if error.check(Closed) is None:
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
        yield waitFor(2)
        
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
        yield waitFor(2)

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
        yield waitFor(2)
        
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
        yield waitFor(2)

        yield self.submit_sm_q.close()
        yield self.deliver_sm_q.close()

        yield self.amqp.disconnect()
        
        self.assertEqual(self.consumedMessages, 2)

    @defer.inlineCallbacks
    def test_start_consuming_later(self):
        """Related to #67, will start consuming after publishing messages, this will imitate
        starting a connector with some pending messages for it"""
        yield self.connect()
        
        yield self.amqp.chan.exchange_declare(exchange='messaging', type='topic')

        # Consume
        consumerTag = 'lateConsumerTest-%s' % (str(uuid.uuid4()))
        yield self.amqp.named_queue_declare(queue="submit.sm.connector01")
        yield self.amqp.chan.queue_bind(queue="submit.sm.connector01", exchange="messaging", routing_key="submit.sm.*")
        yield self.amqp.chan.basic_consume(queue="submit.sm.connector01", no_ack=False, consumer_tag=consumerTag)
        queue = yield self.amqp.client.queue(consumerTag)
        
        # Publish
        for i in range(5000):
            yield self.amqp.publish(exchange='messaging', routing_key="submit.sm.connector01", 
                content=Content(str(i)))

        # Start consuming (same as starting a connector)
        queue.get().addCallback(self._callback, queue, ack = True).addErrback(self._errback)

        # Wait for 15 seconds
        # (give some time to the consumer to get its work done)
        yield waitFor(15)
        
        yield queue.close()

        yield self.amqp.disconnect()
        
        self.assertEqual(self.consumedMessages, 5000)


class RejectAndRequeueTestCase(ConsumeTools):
    rejectedMessages = 0
    # Used to store rejected messages:
    data = []

    @defer.inlineCallbacks
    def _callback_reject_once(self, message, queue, reject = False, requeue = 1):
        queue.get().addCallback(self._callback_reject_once, queue, reject, requeue).addErrback(self._errback)

        if reject and message.content.body not in self.data:
            self.rejectedMessages = self.rejectedMessages + 1
            self.data.append(message.content.body)
            yield self.amqp.chan.basic_reject(delivery_tag=message.delivery_tag, requeue = requeue)
        else:
            self.data.remove(message.content.body)
            self.consumedMessages = self.consumedMessages + 1
            yield self.amqp.chan.basic_ack(message.delivery_tag)

    @defer.inlineCallbacks
    def _callback_reject_and_requeue_all(self, message, queue, requeue = 1):
        queue.get().addCallback(self._callback_reject_and_requeue_all, queue, requeue).addErrback(self._errback)

        self.rejectedMessages = self.rejectedMessages + 1
        yield self.amqp.chan.basic_reject(delivery_tag=message.delivery_tag, requeue = requeue)

    @defer.inlineCallbacks
    def test_consume_all_requeued_messages(self):
        "Related to #67, test for consuming all requeued messages"
        yield self.connect()
        
        yield self.amqp.chan.exchange_declare(exchange='messaging', type='topic')

        # Consume
        yield self.amqp.named_queue_declare(queue="submit.sm_all_1")
        yield self.amqp.chan.queue_bind(queue="submit.sm_all_1", exchange="messaging", routing_key="submit.sm.*")
        yield self.amqp.chan.basic_consume(queue="submit.sm_all_1", no_ack=False, consumer_tag='qtag')
        queue = yield self.amqp.client.queue('qtag')
        queue.get().addCallback(self._callback_reject_once, queue, reject = True).addErrback(self._errback)
        
        # Publish
        for i in range(3000):
            yield self.amqp.publish(exchange='messaging', routing_key="submit.sm.connector01", 
                content=Content(str(i)))

        # Wait for 20 seconds
        # (give some time to the consumer to get its work done)
        yield waitFor(20)
        
        yield queue.close()

        yield self.amqp.disconnect()
        
        self.assertEqual(self.rejectedMessages, 3000)
        self.assertEqual(self.consumedMessages, 3000)

    @defer.inlineCallbacks
    def test_requeue_all_restart_then_reconsume(self):
        """Related to #67, Starting consuming with a """
        yield self.connect()
        
        yield self.amqp.chan.exchange_declare(exchange='messaging', type='topic')

        # Setup Consumer
        yield self.amqp.named_queue_declare(queue="submit.sm_all_2")
        yield self.amqp.chan.queue_bind(queue="submit.sm_all_2", exchange="messaging", routing_key="submit.sm.*")
        yield self.amqp.chan.basic_consume(queue="submit.sm_all_2", no_ack=False, consumer_tag='qtag')
        queue = yield self.amqp.client.queue('qtag')
        # Start consuming through _callback_reject_and_requeue_all
        queue.get().addCallback(self._callback_reject_and_requeue_all, queue).addErrback(self._errback)

        # Publish
        for i in range(50):
            yield self.amqp.publish(exchange='messaging', routing_key="submit.sm.connector01", 
                content=Content(str(i)))
        
        # Wait for 2 seconds
        # (give some time to the consumer to get its work done)
        yield waitFor(2)
        
        # Stop consuming and assert
        yield self.amqp.chan.basic_cancel(consumer_tag='qtag')
        self.assertGreaterEqual(self.rejectedMessages, 50)
        self.assertEqual(self.consumedMessages, 0)

        # Wait for 2 seconds
        # (give some time to the consumer to get its work done)
        yield waitFor(2)

        # Start consuming again
        yield self.amqp.chan.basic_consume(queue="submit.sm_all_2", no_ack=False, consumer_tag='qtag')
        queue = yield self.amqp.client.queue('qtag')
        # Consuming through _callback
        queue.get().addCallback(self._callback, queue).addErrback(self._errback)
        
        # Wait for 2 seconds
        # (give some time to the consumer to get its work done)
        yield waitFor(2)

        # Stop consuming and assert
        yield queue.close()
        self.assertEqual(self.consumedMessages, 50)

        yield self.amqp.disconnect()
