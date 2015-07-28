import pickle
import logging
import urllib
from logging.handlers import TimedRotatingFileHandler
from twisted.application.service import Service
from twisted.internet import defer
from twisted.web.client import getPage
from txamqp.queue import Closed
from twisted.internet import reactor
from jasmin.vendor.smpp.pdu.constants import data_coding_default_name_map, priority_flag_name_map
from jasmin.protocols.smpp.operations import SMPPOperationFactory

class MessageAcknowledgementError(Exception):
    """Raised when destination end does not return 'ACK/Jasmin' back to
    the thrower
    """

class SmppsNotSetError(Exception):
    """Raised whenever self.smppsFactory is needed but not already set
    """

class SystemIdNotBound(Exception):
    """Raised system_id have no binding in self.smppsFactory.bound_connections
    """

class NoDelivererForSystemId(Exception):
    """Raised when no valid binding found for system_id using getNextBindingForDelivery()
    """

class Thrower(Service):
    name = 'abstract thrower'
    
    def __init__(self):
        self.requeueTimers = {}
        self.log_category = "abstract-thrower"

        self.exchangeName = 'messaging'
        self.consumerTag = 'abstractThrower'
        self.routingKey = 'abstract_thrower.*'
        self.queueName = 'abstract_thrower'
        self.callback = self.throwing_callback
        self.errback = self.throwing_errback

        self.smppsFactory = None

    def addSmpps(self, smppsFactory):
        self.smppsFactory = smppsFactory

    def throwing_callback(self, message):
        self.thrower_q.get().addCallback(self.callback).addErrback(self.errback)
                
    def throwing_errback(self, error):
        """It appears that when closing a queue with the close() method it errbacks with
        a txamqp.queue.Closed exception, didnt find a clean way to stop consuming a queue
        without errbacking here so this is a workaround to make it clean, it can be considered
        as a @TODO requiring knowledge of the queue api behaviour
        """
        if error.check(Closed) is None:
            # @todo: implement this errback
            # For info, this errback is called whenever:
            # - an error has occured inside throwing_callback
            self.log.error("Error in throwing_errback_errback: %s" % error)
    
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

    def setConfig(self, config):
        self.config = config

        # Set up a dedicated logger
        self.log = logging.getLogger(self.log_category)
        if len(self.log.handlers) != 1:
            self.log.setLevel(self.config.log_level)
            handler = TimedRotatingFileHandler(filename=self.config.log_file, 
                when = self.config.log_rotate)
            formatter = logging.Formatter(self.config.log_format, self.config.log_date_format)
            handler.setFormatter(formatter)
            self.log.addHandler(handler)
            self.log.propagate = False
        
        self.log.info('Thrower configured and ready.')

    @defer.inlineCallbacks
    def addAmqpBroker(self, amqpBroker):
        self.amqpBroker = amqpBroker
        self.log.info('Added amqpBroker')
        
        if not self.amqpBroker.connected:
            self.log.warn('AMQP Broker channel is not yet ready, waiting for it to become ready.')
            yield self.amqpBroker.channelReady
            self.log.info("AMQP Broker channel is ready now, let's go !")
         
        # Declare exchange, queue and start consuming to self.callback
        yield self.amqpBroker.chan.exchange_declare(exchange = self.exchangeName, 
                                                    type='topic')
        yield self.amqpBroker.named_queue_declare(queue = self.queueName)
        yield self.amqpBroker.chan.queue_bind(queue = self.queueName, 
                                              exchange = self.exchangeName, 
                                              routing_key = self.routingKey)
        yield self.amqpBroker.chan.basic_consume(queue = self.queueName, 
                                                 no_ack = False, 
                                                 consumer_tag = self.consumerTag)
        self.thrower_q = yield self.amqpBroker.client.queue(self.consumerTag)
        self.thrower_q.get().addCallback(self.callback).addErrback(self.errback)
        self.log.info('Consuming from routing key: %s', self.routingKey)

    def rejectAndRequeueMessage(self, message, delay = True):
        msgid = message.content.properties['message-id']
        
        # Reject message
        self.amqpBroker.chan.basic_reject(delivery_tag=message.delivery_tag, requeue=0)
        
        # Publish it
        if delay:
            self.log.debug("Requeuing Content[%s] with delay: %s seconds" % (msgid, 
                                                                             self.config.retry_delay))
            t = reactor.callLater(self.config.retry_delay, 
                                  self.amqpBroker.publish, 
                                  exchange = 'messaging', 
                                  routing_key = message.routing_key, 
                                  content = message.content)

            # If any, clear timer before setting a new one
            self.clearRequeueTimer(msgid)

            self.requeueTimers[msgid] = t
            return t
        else:
            self.log.debug("Requeuing Content[%s] without delay" % msgid)
            return self.amqpBroker.publish(exchange = 'messaging', 
                                           routing_key = message.routing_key, 
                                           content = message.content)
    def rejectMessage(self, message):
        return self.amqpBroker.chan.basic_reject(delivery_tag=message.delivery_tag, requeue=0)
    def ackMessage(self, message):
        return self.amqpBroker.chan.basic_ack(message.delivery_tag)
            
class deliverSmThrower(Thrower):
    name = 'deliverSmThrower'
    
    def __init__(self):
        Thrower.__init__(self)

        self.log_category = "jasmin-deliversm-thrower"
        self.exchangeName = 'messaging'
        self.consumerTag = 'deliverSmThrower'
        self.routingKey = 'deliver_sm_thrower.*'
        self.queueName = 'deliver_sm_thrower'
        
        self.callback = self.deliver_sm_throwing_callback
    
    @defer.inlineCallbacks
    def http_deliver_sm_callback(self, message):
        msgid = message.content.properties['message-id']
        dc = pickle.loads(message.content.properties['headers']['dst-connector'])
        RoutedDeliverSmContent = pickle.loads(message.content.body)
        self.log.debug('Got one message (msgid:%s) to throw: %s' % (msgid, RoutedDeliverSmContent))
        
        # If any, clear requeuing timer
        self.clearRequeueTimer(msgid)
        
        if dc.type != 'http':
            self.log.error('Rejecting message [msgid:%s] because destination connector [dcid:%s] is not http (type were %s)' % (msgid, dc.cid, dc.type))
            yield self.rejectMessage(message)
            defer.returnValue(None)
        
        # Build mandatory arguments
        args = {'id': msgid, 'from': RoutedDeliverSmContent.params['source_addr'], 
                'to': RoutedDeliverSmContent.params['destination_addr'], 'content': RoutedDeliverSmContent.params['short_message'],
                'origin-connector': message.content.properties['headers']['src-connector-id']}
        # Build optional arguments
        if RoutedDeliverSmContent.params['priority_flag'] is not None:
            args['priority'] = priority_flag_name_map[str(RoutedDeliverSmContent.params['priority_flag'])]
        if RoutedDeliverSmContent.params['data_coding'] is not None:
            args['coding'] = data_coding_default_name_map[str(RoutedDeliverSmContent.params['data_coding'].schemeData)]
        if RoutedDeliverSmContent.params['validity_period'] is not None:
            args['validity'] = RoutedDeliverSmContent.params['validity_period']
        
        try:
            # Throw the message to http endpoint
            encodedArgs = urllib.urlencode(args)
            postdata = None
            baseurl = dc.baseurl
            _method = dc.method.upper()
            if _method == 'GET':
                baseurl += '?%s' % encodedArgs
            else:
                postdata = encodedArgs

            self.log.debug('Calling %s with args %s using %s method.' % (dc.baseurl, args, _method))
            content = yield getPage(baseurl, method = _method, postdata = postdata, 
                          timeout = self.config.timeout, agent = 'Jasmin gateway/1.0 deliverSmHttpThrower',
                          headers = {'Content-Type'     : 'application/x-www-form-urlencoded',
                                     'Accept'           : 'text/plain'})
            self.log.info('Throwed message [msgid:%s] to connector [cid:%s] using http to %s.' % (msgid, dc.cid, dc.baseurl))
            
            self.log.debug('Destination end replied to message [msgid:%s]: %r' % (msgid, content))
            # Check for acknowledgement
            if content.strip() != 'ACK/Jasmin':
                raise MessageAcknowledgementError('Destination end did not acknowledge receipt of the message.')

            yield self.ackMessage(message)
        except Exception, e:
            message.content.properties['headers']['try-count'] += 1
            self.log.error('Throwing message [msgid:%s] to [cid:%s] (%s): %r.' % (msgid, dc.cid, dc.baseurl, e))
            
            # List of errors after which, no further retrying shall be made
            noRetryErrors = ['404 Not Found']
            
            # Requeue message for later retry
            if str(e) not in noRetryErrors and message.content.properties['headers']['try-count'] <= self.config.max_retries:
                self.log.debug('Message try-count is %s [msgid:%s]: requeuing' % (message.content.properties['headers']['try-count'], msgid))
                yield self.rejectAndRequeueMessage(message)
            elif str(e) in noRetryErrors:
                self.log.warn('Message is no more processed after receiving "%s" error' % (str(e)))
                yield self.rejectMessage(message)
            else:
                self.log.warn('Message try-count is %s [msgid:%s]: purged from queue' % (message.content.properties['headers']['try-count'], msgid))
                yield self.rejectMessage(message)

    @defer.inlineCallbacks
    def smpp_deliver_sm_callback(self, message):
        msgid = message.content.properties['message-id']
        dc = pickle.loads(message.content.properties['headers']['dst-connector'])
        system_id = dc.cid
        pdu = pickle.loads(message.content.body)
        RoutedDeliverSmContent = pickle.loads(message.content.body)
        self.log.debug('Got one message (msgid:%s) to throw: %s' % (msgid, RoutedDeliverSmContent))

        # If any, clear requeuing timer
        self.clearRequeueTimer(msgid)
        
        if dc.type != 'smpps':
            self.log.error('Rejecting message [msgid:%s] because destination connector [dcid:%s] is not smpps (type were %s)' % (msgid, dc.cid, dc.type))
            yield self.rejectMessage(message)
            defer.returnValue(None)

        try:
            if self.smppsFactory is None:
                raise SmppsNotSetError()

            if system_id not in self.smppsFactory.bound_connections:
                raise SystemIdNotBound(system_id)
            
            deliverer = self.smppsFactory.bound_connections[system_id].getNextBindingForDelivery()
            if deliverer is None:
                raise NoDelivererForSystemId(system_id)

            # Deliver (or throw) the pdu through the deliverer
            yield deliverer.sendRequest(pdu, deliverer.config().responseTimerSecs)

            # Everything is okay ? then:
            yield self.ackMessage(message)
        except Exception, e:
            message.content.properties['headers']['try-count'] += 1
            self.log.error('Throwing SMPP/DELIVER_SM [msgid:%s] to (%s): %r.' % (msgid, system_id, e))

            # List of exceptions after which, no further retrying shall be made
            noRetryExceptions = [SmppsNotSetError]

            retry = True
            for noRetryException in noRetryExceptions:
                if isinstance(e, noRetryException):
                    retry = False
                    break
            
            # Requeue message for later retry
            if retry and message.content.properties['headers']['try-count'] <= self.config.max_retries:
                self.log.debug('Message try-count is %s [msgid:%s]: requeuing' % (message.content.properties['headers']['try-count'], msgid))
                yield self.rejectAndRequeueMessage(message)
            elif retry and message.content.properties['headers']['try-count'] > self.config.max_retries:
                self.log.warn('Message is no more processed after receiving "%s" error' % (str(e)))
                yield self.rejectMessage(message)
            else:
                self.log.warn('Message try-count is %s [msgid:%s]: purged from queue' % (message.content.properties['headers']['try-count'], msgid))
                yield self.rejectMessage(message)

    @defer.inlineCallbacks
    def deliver_sm_throwing_callback(self, message):
        Thrower.throwing_callback(self, message)

        if message.routing_key == 'deliver_sm_thrower.http':
            yield self.http_deliver_sm_callback(message)
        elif message.routing_key == 'deliver_sm_thrower.smpps':
            yield self.smpp_deliver_sm_callback(message)
        else:
            self.log.error('Unknown routing_key in deliver_sm_throwing_callback: %s' % message.routing_key)
            yield self.rejectMessage(message)

class DLRThrower(Thrower):
    name = 'DLRThrower'
    
    def __init__(self):
        Thrower.__init__(self)

        self.log_category = "jasmin-dlr-thrower"
        self.exchangeName = 'messaging'
        self.consumerTag = 'DLRThrower'
        self.routingKey = 'dlr_thrower.*'
        self.queueName = 'dlr_thrower'
        self.callback = self.dlr_throwing_callback

        self.opFactory = SMPPOperationFactory()
    
    @defer.inlineCallbacks
    def http_dlr_callback(self, message):
        msgid = message.content.properties['message-id']
        url = message.content.properties['headers']['url']
        method = message.content.properties['headers']['method']
        level = message.content.properties['headers']['level']
        self.log.debug('Got one message (msgid:%s) to throw' % (msgid))
        
        # If any, clear requeuing timer
        self.clearRequeueTimer(msgid)
        
        # Build mandatory arguments
        args = {'id': msgid, 'level': level, 'message_status': message.content.properties['headers']['message_status']}
        
        # Level 2 extra args
        if level in [2, 3]:
            args['id_smsc'] = message.content.properties['headers']['id_smsc'];
            args['sub'] = message.content.properties['headers']['sub'];
            args['dlvrd'] = message.content.properties['headers']['dlvrd'];
            args['subdate'] = message.content.properties['headers']['subdate'];
            args['donedate'] = message.content.properties['headers']['donedate'];
            args['err'] = message.content.properties['headers']['err'];
            args['text'] = message.content.properties['headers']['text'];
        
        try:
            # Throw the message to http endpoint
            encodedArgs = urllib.urlencode(args)
            postdata = None
            baseurl = url
            if method == 'GET':
                baseurl += '?%s' % encodedArgs
            else:
                postdata = encodedArgs

            self.log.debug('Calling %s with args %s using %s method.' % (baseurl, encodedArgs, method))
            content = yield getPage(baseurl, method = method, postdata = postdata, 
                          timeout = self.config.timeout, agent = 'Jasmin gateway/1.0 %s' % self.name,
                          headers = {'Content-Type'     : 'application/x-www-form-urlencoded',
                                     'Accept'           : 'text/plain'})
            self.log.info('Throwed DLR [msgid:%s] to %s.' % (msgid, baseurl))
            
            self.log.debug('Destination end replied to message [msgid:%s]: %r' % (msgid, content))
            # Check for acknowledgement
            if content.strip() != 'ACK/Jasmin':
                raise MessageAcknowledgementError('Destination end did not acknowledge receipt of the DLR message.')

            # Everything is okay ? then:
            yield self.ackMessage(message)
        except Exception, e:
            message.content.properties['headers']['try-count'] += 1
            self.log.error('Throwing HTTP/DLR [msgid:%s] to (%s): %r.' % (msgid, baseurl, e))
            
            # List of errors after which, no further retrying shall be made
            noRetryErrors = ['404 Not Found']
            
            # Requeue message for later retry
            if str(e) not in noRetryErrors and message.content.properties['headers']['try-count'] <= self.config.max_retries:
                self.log.debug('Message try-count is %s [msgid:%s]: requeuing' % (message.content.properties['headers']['try-count'], msgid))
                yield self.rejectAndRequeueMessage(message)
            elif str(e) in noRetryErrors:
                self.log.warn('Message is no more processed after receiving "%s" error' % (str(e)))
                yield self.rejectMessage(message)
            else:
                self.log.warn('Message try-count is %s [msgid:%s]: purged from queue' % (message.content.properties['headers']['try-count'], msgid))
                yield self.rejectMessage(message)

    @defer.inlineCallbacks
    def smpp_dlr_callback(self, message):
        msgid = message.content.properties['message-id']
        system_id = message.content.properties['headers']['system_id']
        message_status = message.content.properties['headers']['message_status']
        source_addr = message.content.properties['headers']['source_addr']
        destination_addr = message.content.properties['headers']['destination_addr']
        sub_date = message.content.properties['headers']['sub_date']
        self.log.debug('Got one message (msgid:%s) to throw' % (msgid))

        # If any, clear requeuing timer
        self.clearRequeueTimer(msgid)
        
        try:
            if self.smppsFactory is None:
                raise SmppsNotSetError()

            if system_id not in self.smppsFactory.bound_connections:
                raise SystemIdNotBound(system_id)
            
            deliverer = self.smppsFactory.bound_connections[system_id].getNextBindingForDelivery()
            if deliverer is None:
                raise NoDelivererForSystemId(system_id)

            # Build the Receipt PDU (data_sm)
            pdu = self.opFactory.getReceipt(dlr_pdu = self.config.dlr_pdu,
                                            msgid = msgid, 
                                            source_addr = source_addr, 
                                            destination_addr = destination_addr,
                                            message_status = message_status,
                                            sub_date = sub_date,
                                            )

            # Deliver (or throw) the receipt through the deliverer
            yield deliverer.sendRequest(pdu, deliverer.config().responseTimerSecs)

            # Everything is okay ? then:
            yield self.ackMessage(message)
        except Exception, e:
            message.content.properties['headers']['try-count'] += 1
            self.log.error('Throwing SMPP/DLR [msgid:%s] to (%s): %r.' % (msgid, system_id, e))

            # List of exceptions after which, no further retrying shall be made
            noRetryExceptions = [SmppsNotSetError]

            retry = True
            for noRetryException in noRetryExceptions:
                if isinstance(e, noRetryException):
                    retry = False
                    break
            
            # Requeue message for later retry
            if retry and message.content.properties['headers']['try-count'] <= self.config.max_retries:
                self.log.debug('Message try-count is %s [msgid:%s]: requeuing' % (message.content.properties['headers']['try-count'], msgid))
                yield self.rejectAndRequeueMessage(message)
            elif retry and message.content.properties['headers']['try-count'] > self.config.max_retries:
                self.log.warn('Message is no more processed after receiving "%s" error' % (str(e)))
                yield self.rejectMessage(message)
            else:
                self.log.warn('Message try-count is %s [msgid:%s]: purged from queue' % (message.content.properties['headers']['try-count'], msgid))
                yield self.rejectMessage(message)

    @defer.inlineCallbacks
    def dlr_throwing_callback(self, message):
        Thrower.throwing_callback(self, message)

        if message.routing_key == 'dlr_thrower.http':
            yield self.http_dlr_callback(message)
        elif message.routing_key == 'dlr_thrower.smpps':
            yield self.smpp_dlr_callback(message)
        else:
            self.log.error('Unknown routing_key in dlr_throwing_callback: %s' % message.routing_key)
            yield self.rejectMessage(message)