import pickle
import logging
import urllib
import copy
from twisted.application.service import Service
from twisted.internet import defer
from twisted.web.client import getPage
from txamqp.queue import Closed
from twisted.internet import reactor


LOG_CATEGORY = "jasmin-thrower"

class MessageAcknowledgementError(Exception):
    """Raised when destination end does not return 'ACK/Jasmin' back to
    the thrower
    """

class deliverSmThrower(Service):
    name = 'deliverSmThrower'
    
    def __init__(self):
        self.requeueTimers = {}
    
    def clearRequeueTimer(self, msgid):
        if msgid in self.requeueTimers:
            t = self.requeueTimers[msgid]
            if t.active():
                t.cancel()
            del self.requeueTimers[msgid]

    def clearRequeueTimers(self):
        for msgid, timer in self.requeueTimers.items():
            if timer.active():
                timer.cancel()
            del self.requeueTimers[msgid]

    def clearAllTimers(self):
        self.clearRequeueTimers()

    def startService(self):
        Service.startService(self)
    def stopService(self):
        Service.stopService(self)
        
        self.clearAllTimers()

    def setConfig(self, deliverSmThrowerConfig):
        self.config = deliverSmThrowerConfig

        # Set up a dedicated logger
        self.log = logging.getLogger(LOG_CATEGORY)
        self.log.setLevel(self.config.log_level)
        handler = logging.FileHandler(filename=self.config.log_file)
        formatter = logging.Formatter(self.config.log_format, self.config.log_date_format)
        handler.setFormatter(formatter)
        self.log.addHandler(handler)
        
        self.log.info('deliverSmThrower configured and ready.')

    @defer.inlineCallbacks
    def addAmqpBroker(self, amqpBroker):
        self.amqpBroker = amqpBroker
        self.log.info('Added amqpBroker to deliverSmThrower')
        
        if self.amqpBroker.connected == False:
            self.log.warn('AMQP Broker channel is not yet ready, waiting for it to become ready.')
            yield self.amqpBroker.channelReady
            self.log.info("AMQP Broker channel is ready now, let's go !")
         
        # Subscribe to deliver_sm_thrower.http queue
        exchangeName = 'messaging'
        consumerTag = 'deliverSmThrower'
        routingKey = 'deliver_sm_thrower.http'
        queueName = 'deliver_sm_thrower.http'
        yield self.amqpBroker.chan.exchange_declare(exchange=exchangeName, type='topic')
        yield self.amqpBroker.named_queue_declare(queue=queueName)
        yield self.amqpBroker.chan.queue_bind(queue=queueName, exchange=exchangeName, routing_key=routingKey)
        yield self.amqpBroker.chan.basic_consume(queue=queueName, no_ack=False, consumer_tag=consumerTag)
        self.httpthrower_q = yield self.amqpBroker.client.queue(consumerTag)
        self.httpthrower_q.get().addCallback(self.http_throwing_callback).addErrback(self.throwing_errback)
        self.log.info('deliverSmThrower is consuming from routing key: %s', routingKey)
        
    def rejectAndRequeueMessage(self, message, delay = True):
        msgid = message.content.properties['message-id']
        
        if delay:
            self.log.debug("Requeuing RoutedDeliverSmContent[%s] with delay: %s seconds" % (msgid, self.config.retryDelay))
            t = reactor.callLater(self.config.retryDelay, self.amqpBroker.chan.basic_reject, delivery_tag=message.delivery_tag, requeue=1)

            # If any, clear timer before setting a new one
            self.clearRequeueTimer(msgid)
            
            self.requeueTimers[msgid] = t
            return t
        else:
            self.log.debug("Requeuing RoutedDeliverSmContent[%s] without delay" % msgid)
            return self.amqpBroker.chan.basic_reject(delivery_tag=message.delivery_tag, requeue=1)
    def rejectAnRepublishMessage(self, message, routing_key, delay = True):
        msgid = message.content.properties['message-id']
        
        # Reject message
        self.amqpBroker.chan.basic_reject(delivery_tag=message.delivery_tag, requeue=0)
        
        # Publish it
        if delay:
            self.log.debug("Requeuing RoutedDeliverSmContent[%s] with delay: %s seconds" % (msgid, self.config.retryDelay))
            t = reactor.callLater(self.config.retryDelay, self.amqpBroker.publish, routing_key=routing_key, content=message.content)

            # If any, clear timer before setting a new one
            self.clearRequeueTimer(msgid)

            self.requeueTimers[msgid] = t
            return t
        else:
            self.log.debug("Requeuing RoutedDeliverSmContent[%s] without delay" % msgid)
            return self.amqpBroker.publish(exchange='messaging', routing_key=routing_key, content=message.content)
    def rejectMessage(self, message):
        return self.amqpBroker.chan.basic_reject(delivery_tag=message.delivery_tag, requeue=0)
    def ackMessage(self, message):
        return self.amqpBroker.chan.basic_ack(message.delivery_tag)

    @defer.inlineCallbacks
    def http_throwing_callback(self, message):
        self.httpthrower_q.get().addCallback(self.http_throwing_callback).addErrback(self.throwing_errback)
        
        msgid = message.content.properties['message-id']
        dc = pickle.loads(message.content.properties['headers']['dst-connector'])
        RoutedDeliverSmContent = pickle.loads(message.content.body)
        
        # If any, clear requeuing timer
        self.clearRequeueTimer(msgid)
        
        if dc.type != 'http':
            self.log.error('Rejecting message [msgid:%s] because destination connector [dcid:%s] is not http (type were %s)' % (msgid, dc.cid, dc.type))
            yield self.rejectMessage(message, 'http')
            defer.returnValue(None)
        
        # Build mandatory arguments
        args = {'id': msgid, 'from': RoutedDeliverSmContent.params['source_addr'], 
                'to': RoutedDeliverSmContent.params['destination_addr'], 'content': RoutedDeliverSmContent.params['short_message'],
                'origin-connector': message.content.properties['headers']['src-connector-id']}
        # Build optional arguments
        if RoutedDeliverSmContent.params['priority_flag'] is not None:
            args['priority'] = RoutedDeliverSmContent.params['priority_flag']
        if RoutedDeliverSmContent.params['data_coding'] is not None:
            args['coding'] = RoutedDeliverSmContent.params['data_coding']
        if RoutedDeliverSmContent.params['validity_period'] is not None:
            args['validity'] = RoutedDeliverSmContent.params['validity_period']
        
        try:
            # Throw the message to http endpoint
            encodedArgs = urllib.urlencode(args)
            postdata = None
            baseurl = dc.baseurl
            if dc.method == 'GET':
                baseurl += '?%s' % encodedArgs
            else:
                postdata = encodedArgs

            self.log.debug('Calling %s with args %s using %s method.' % (dc.baseurl, args, dc.method))
            content = yield getPage(baseurl, method = dc.method, postdata = postdata, 
                          timeout = self.config.timeout, agent = 'Jasmin gateway/1.0 deliverSmThrower')
            self.log.info('Throwed message [msgid:%s] to connector [cid:%s] using http to %s.' % (msgid, dc.cid, dc.baseurl))
            
            self.log.debug('Destination end replied to message [msgid:%s]: %s' % (msgid, content))
            # Check for acknowledgement
            if content != 'ACK/Jasmin':
                raise MessageAcknowledgementError('Destination end did not acknowledge receipt of the message.')

            yield self.ackMessage(message)
        except Exception, e:
            message.content.properties['headers']['try-count'] += 1
            self.log.error('Throwing message [msgid:%s] to [cid:%s] (%s): %s.' % (msgid, dc.cid, dc.baseurl, str(e)))
            
            # List of errors after which, no further retrying shall be made
            noRetryErrors = ['404 Not Found']
            
            # Requeue message for later retry
            if str(e) not in noRetryErrors and message.content.properties['headers']['try-count'] <= self.config.maxRetries:
                self.log.debug('Message try-count is %s [msgid:%s]: requeuing' % (message.content.properties['headers']['try-count'], msgid))
                yield self.rejectAnRepublishMessage(message, 'deliver_sm_thrower.http')
            elif str(e) in noRetryErrors:
                self.log.warn('Message is no more processed after receiving "%s" error' % (str(e)))
                yield self.rejectMessage(message)
            else:
                self.log.warn('Message try-count is %s [msgid:%s]: purged from queue' % (message.content.properties['headers']['try-count'], msgid))
                yield self.rejectMessage(message)
    
    def throwing_errback(self, error):
        """It appears that when closing a queue with the close() method it errbacks with
        a txamqp.queue.Closed exception, didnt find a clean way to stop consuming a queue
        without errbacking here so this is a workaround to make it clean, it can be considered
        as a @TODO requiring knowledge of the queue api behaviour
        """
        if error.check(Closed) == None:
            # @todo: implement this errback
            # For info, this errback is called whenever:
            # - an error has occured inside deliver_sm_callback
            self.log.error("Error in throwing_errback_errback: %s" % error)