import binascii
import cPickle as pickle
import logging
import urllib
from logging.handlers import TimedRotatingFileHandler

from twisted.application.service import Service
from twisted.internet import defer
from twisted.internet import reactor
from twisted.web.client import getPage
from txamqp.queue import Closed

from jasmin.protocols.smpp.factory import SMPPServerFactory
from jasmin.protocols.smpp.operations import SMPPOperationFactory
from jasmin.protocols.smpp.proxies import SMPPServerPBProxy
from jasmin.vendor.smpp.pdu.constants import data_coding_default_name_map, priority_flag_name_map


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


class DeliveringFailed(Exception):
    """Raised when delivering a pdu errored"""


class Thrower(Service):
    name = 'abstract thrower'
    log_category = 'abstract-thrower'
    exchangeName = 'messaging'
    consumerTag = 'abstractThrower'
    routingKey = 'abstract_thrower.*'
    queueName = 'abstract_thrower'
    callback = None
    errback = None
    requeueTimers = {}
    throwing_retrials = {}

    def __init__(self, config):
        self.config = config

        # Check if callbacks are defined in child class ?
        if self.callback is None:
            self.callback = self.throwing_callback
        if self.errback is None:
            self.errback = self.throwing_errback

        # For these values to None since they must be defined through .addSmpps()
        self.smpps = None
        self.smpps_access = None

        # Set up a dedicated logger
        self.log = logging.getLogger(self.log_category)
        if len(self.log.handlers) != 1:
            self.log.setLevel(self.config.log_level)
            handler = TimedRotatingFileHandler(filename=self.config.log_file,
                                               when=self.config.log_rotate)
            formatter = logging.Formatter(self.config.log_format, self.config.log_date_format)
            handler.setFormatter(formatter)
            self.log.addHandler(handler)
            self.log.propagate = False

        self.log.info('Thrower configured and ready.')

    def addSmpps(self, smpps):
        self.smpps = smpps

        if isinstance(smpps, SMPPServerPBProxy):
            self.smpps_access = 'perspectivebroker'
        elif isinstance(smpps, SMPPServerFactory):
            self.smpps_access = 'direct'

        self.log.info('Added a %s access to SMPPServerFactory', self.smpps_access)

    def getThrowingRetrials(self, message):
        return self.throwing_retrials.get(message.content.properties['message-id'], 0)

    def delThrowingRetrials(self, message):
        if message.content.properties['message-id'] in self.throwing_retrials:
            del self.throwing_retrials[message.content.properties['message-id']]
            return True
        else:
            return False

    def incThrowingRetrials(self, message):
        if message.content.properties['message-id'] in self.throwing_retrials:
            self.throwing_retrials[message.content.properties['message-id']] += 1
        else:
            self.throwing_retrials[message.content.properties['message-id']] = 1

    def throwing_callback(self, message):
        # Init retrial mechanism
        self.incThrowingRetrials(message)

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
            self.log.error("Error in throwing_errback_errback: %s", error)

    def clearRequeueTimer(self, msgid):
        if msgid in self.requeueTimers:
            timer = self.requeueTimers[msgid]
            if timer.active():
                timer.cancel()
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

    @defer.inlineCallbacks
    def addAmqpBroker(self, amqpBroker):
        self.amqpBroker = amqpBroker
        self.log.info('Added amqpBroker')

        if not self.amqpBroker.connected:
            self.log.warn('AMQP Broker channel is not yet ready, waiting for it to become ready.')
            yield self.amqpBroker.channelReady
            self.log.info("AMQP Broker channel is ready now, let's go !")

        # Declare exchange, queue and start consuming to self.callback
        yield self.amqpBroker.chan.exchange_declare(exchange=self.exchangeName,
                                                    type='topic')
        yield self.amqpBroker.named_queue_declare(queue=self.queueName)
        yield self.amqpBroker.chan.queue_bind(queue=self.queueName,
                                              exchange=self.exchangeName,
                                              routing_key=self.routingKey)
        yield self.amqpBroker.chan.basic_consume(queue=self.queueName,
                                                 no_ack=False,
                                                 consumer_tag=self.consumerTag)
        self.thrower_q = yield self.amqpBroker.client.queue(self.consumerTag)
        self.thrower_q.get().addCallback(self.callback).addErrback(self.errback)
        self.log.info('Consuming from routing key: %s', self.routingKey)

    @defer.inlineCallbacks
    def rejectAndRequeueMessage(self, message, delay=True):
        msgid = message.content.properties['message-id']

        if delay:
            self.log.debug("Requeuing Content[%s] with delay: %s seconds",
                           msgid, self.config.retry_delay)

            # Requeue the message with a delay
            timer = reactor.callLater(self.config.retry_delay,
                                      self.rejectMessage,
                                      message=message,
                                      requeue=1)

            # If any, clear timer before setting a new one
            self.clearRequeueTimer(msgid)

            self.requeueTimers[msgid] = timer
            defer.returnValue(timer)
        else:
            self.log.debug("Requeuing Content[%s] without delay", msgid)
            yield self.rejectMessage(message, requeue=1)

    @defer.inlineCallbacks
    def rejectMessage(self, message, requeue=0):
        if requeue == 0:
            # Remove retrial tracker
            self.delThrowingRetrials(message)

        yield self.amqpBroker.chan.basic_reject(delivery_tag=message.delivery_tag, requeue=requeue)

    @defer.inlineCallbacks
    def ackMessage(self, message):
        # Remove retrial tracker
        self.delThrowingRetrials(message)

        yield self.amqpBroker.chan.basic_ack(message.delivery_tag)


class deliverSmThrower(Thrower):
    name = 'deliverSmThrower'

    def __init__(self, config):
        self.log_category = "jasmin-deliversm-thrower"
        self.exchangeName = 'messaging'
        self.consumerTag = 'deliverSmThrower'
        self.routingKey = 'deliver_sm_thrower.*'
        self.queueName = 'deliver_sm_thrower'
        self.callback = self.deliver_sm_throwing_callback

        Thrower.__init__(self, config)

    @defer.inlineCallbacks
    def http_deliver_sm_callback(self, message):
        msgid = message.content.properties['message-id']
        route_type = message.content.properties['headers']['route-type']
        dcs = pickle.loads(message.content.properties['headers']['dst-connectors'])
        RoutedDeliverSmContent = pickle.loads(message.content.body)
        self.log.debug('Got one message (msgid:%s) to throw: %s', msgid, RoutedDeliverSmContent)

        # If any, clear requeuing timer
        self.clearRequeueTimer(msgid)

        if dcs[0].type != 'http':
            self.log.error(
                'Rejecting message [msgid:%s] because destination connector is not http (type were %s)',
                msgid,
                dcs[0].type)
            yield self.rejectMessage(message)
            defer.returnValue(None)

        # Build mandatory arguments
        args = {
            'id': msgid,
            'from': RoutedDeliverSmContent.params['source_addr'],
            'to': RoutedDeliverSmContent.params['destination_addr'],
            'origin-connector': message.content.properties['headers']['src-connector-id']}

        # Content can be short_message or message_payload:
        if 'short_message' in RoutedDeliverSmContent.params and len(RoutedDeliverSmContent.params['short_message']) > 0:
            args['content'] = RoutedDeliverSmContent.params['short_message']
        elif 'message_payload' in RoutedDeliverSmContent.params:
            args['content'] = RoutedDeliverSmContent.params['message_payload']
        elif 'short_message' in RoutedDeliverSmContent.params:
            args['content'] = RoutedDeliverSmContent.params['short_message']
        else:
            self.log.error('Cannot find content in pdu (msgid:%s): %s', msgid, RoutedDeliverSmContent)
            yield self.rejectMessage(message)
            defer.returnValue(None)

        # Set the binary arg after deciding where to pick the content from
        args['binary'] = binascii.hexlify(args['content'])

        # Build optional arguments
        if ('priority_flag' in RoutedDeliverSmContent.params and
                    RoutedDeliverSmContent.params['priority_flag'] is not None):
            args['priority'] = priority_flag_name_map[str(RoutedDeliverSmContent.params['priority_flag'])]
        if ('data_coding' in RoutedDeliverSmContent.params and
                    RoutedDeliverSmContent.params['data_coding'] is not None):
            args['coding'] = data_coding_default_name_map[
                str(RoutedDeliverSmContent.params['data_coding'].schemeData)]
        if ('validity_period' in RoutedDeliverSmContent.params and
                    RoutedDeliverSmContent.params['validity_period'] is not None):
            args['validity'] = RoutedDeliverSmContent.params['validity_period']

        counter = 0
        for dc in dcs:
            counter += 1
            self.log.debug('DCS Iteration %s/%s taking [cid:%s] (%s)', counter, len(dcs), dc.cid, dc)
            last_dc = True
            if route_type == 'failover' and counter < len(dcs):
                last_dc = False

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

                self.log.debug('Calling %s with args %s using %s method.', dc.baseurl, args, _method)
                content = yield getPage(
                    baseurl,
                    method=_method,
                    postdata=postdata,
                    timeout=self.config.timeout,
                    agent='Jasmin gateway/1.0 deliverSmHttpThrower',
                    headers={'Content-Type': 'application/x-www-form-urlencoded',
                             'Accept': 'text/plain'})
                self.log.info('Throwed message [msgid:%s] to connector (%s %s/%s)[cid:%s] using http to %s.',
                              msgid, route_type, counter, len(dcs), dc.cid, dc.baseurl)

                self.log.debug('Destination end replied to message [msgid:%s]: %r',
                               msgid, content)

                # Check for acknowledgement
                if content.strip() != 'ACK/Jasmin':
                    raise MessageAcknowledgementError(
                        'Destination end did not acknowledge receipt of the message.')
            except Exception, e:
                self.log.error('Throwing message [msgid:%s] to (%s %s/%s)[cid:%s] (%s), %s: %s.',
                               msgid, route_type, counter, len(dcs), dc.cid, dc.baseurl, type(e), e)

                # List of errors after which, no further retrying shall be made
                noRetryErrors = ['404 Not Found']

                if route_type == 'simple':
                    # Requeue message for later retry
                    if (str(e) not in noRetryErrors
                        and self.getThrowingRetrials(message) <= self.config.max_retries):
                        self.log.debug('Message try-count is %s [msgid:%s]: requeuing',
                                       self.getThrowingRetrials(message), msgid)
                        yield self.rejectAndRequeueMessage(message)
                    elif str(e) in noRetryErrors:
                        self.log.warn('Message [msgid:%s] is no more processed after receiving "%s" error',
                                      msgid, str(e))
                        yield self.rejectMessage(message)
                    else:
                        self.log.warn('Message try-count is %s [msgid:%s]: purged from queue',
                                      self.getThrowingRetrials(message), msgid)
                        yield self.rejectMessage(message)
                elif route_type == 'failover':
                    # The route has multiple connectors, we will not retry throwing to same connector
                    if last_dc:
                        self.log.warn(
                            'Message [msgid:%s] is no more processed after receiving "%s" error on this fo/connector',
                            msgid, str(e))
            else:
                # Everything is okay ? then:
                yield self.ackMessage(message)

                if route_type == 'failover':
                    self.log.debug('Stopping iteration for failover route.')
                    break
            finally:
                if route_type == 'simple':
                    # There's only one connector for simple routes
                    break
                elif route_type == 'failover' and last_dc:
                    self.log.debug('Break (last dc) iteration for failover route.')
                    break
                elif route_type == 'failover' and not last_dc:
                    self.log.debug('Continue iteration for failover route.')

    @defer.inlineCallbacks
    def smpp_deliver_sm_callback(self, message):
        msgid = message.content.properties['message-id']
        route_type = message.content.properties['headers']['route-type']
        dcs = pickle.loads(message.content.properties['headers']['dst-connectors'])
        pdu = pickle.loads(message.content.body)
        RoutedDeliverSmContent = pickle.loads(message.content.body)
        self.log.debug('Got one message (msgid:%s) to throw: %s', msgid, RoutedDeliverSmContent)

        # If any, clear requeuing timer
        self.clearRequeueTimer(msgid)

        if dcs[0].type != 'smpps':
            self.log.error(
                'Rejecting message [msgid:%s] because destination connector is not smpps (type were %s)',
                msgid,
                dcs[0].type)
            yield self.rejectMessage(message)
            defer.returnValue(None)

        counter = 0
        for dc in dcs:
            counter += 1
            self.log.debug('DCS Iteration %s/%s taking [cid:%s] (%s)', counter, len(dcs), dc.cid, dc)
            last_dc = True
            if route_type == 'failover' and counter < len(dcs):
                last_dc = False

            try:
                if self.smpps is None or self.smpps_access is None:
                    raise SmppsNotSetError()

                # Get bound connections (or systemids)
                if self.smpps_access == 'direct':
                    bound_systemdids = self.smpps.bound_connections
                else:
                    bound_systemdids = yield self.smpps.list_bound_systemids()

                if dc.cid not in bound_systemdids:
                    raise SystemIdNotBound(dc.cid)

                # Pick a deliverer and sendRequest
                if self.smpps_access == 'direct':
                    deliverer = bound_systemdids[dc.cid].getNextBindingForDelivery()

                    if deliverer is None:
                        raise NoDelivererForSystemId(dc.cid)

                    yield deliverer.sendRequest(pdu, deliverer.config().responseTimerSecs)
                else:
                    r = yield self.smpps.deliverer_send_request(dc.cid, pdu)
                    if not r:
                        raise DeliveringFailed('Delivering failed, check %s smpps logs for more details' % dc.cid)
            except Exception, e:
                self.log.error('Throwing SMPP/DELIVER_SM [msgid:%s] to (%s %s/%s)[cid:%s], %s: %s.',
                               msgid, route_type, counter, len(dcs), dc.cid, type(e), e)

                # List of exceptions after which, no further retrying shall be made
                noRetryExceptions = [SmppsNotSetError]

                if route_type == 'simple':
                    retry = True
                    for noRetryException in noRetryExceptions:
                        if isinstance(e, noRetryException):
                            retry = False
                            break

                    # Requeue message for later retry
                    if retry and self.getThrowingRetrials(message) <= self.config.max_retries:
                        self.log.debug('Message try-count is %s [msgid:%s]: requeuing',
                                       self.getThrowingRetrials(message), msgid)
                        yield self.rejectAndRequeueMessage(message)
                    elif retry and self.getThrowingRetrials(message) > self.config.max_retries:
                        self.log.warn('Message [msgid:%s] is no more processed after receiving "%s" error',
                                      msgid, str(e))
                        yield self.rejectMessage(message)
                    else:
                        self.log.warn('Message try-count is %s [msgid:%s]: purged from queue',
                                      self.getThrowingRetrials(message), msgid)
                        yield self.rejectMessage(message)
                elif route_type == 'failover':
                    # The route has multiple connectors, we will not retry throwing to same connector
                    if last_dc:
                        self.log.warn(
                            'Message [msgid:%s] is no more processed after receiving "%s" error on this fo/connector',
                            msgid, str(e))
            else:
                # Everything is okay ? then:
                yield self.ackMessage(message)

                self.log.info('Throwed message [msgid:%s] to connector (%s %s/%s)[cid:%s] using smpp.',
                              msgid, route_type, counter, len(dcs), dc.cid)

                if route_type == 'failover':
                    self.log.debug('Stopping iteration for failover route.')
                    break
            finally:
                if route_type == 'simple':
                    # There's only one connector for simple routes
                    break
                elif route_type == 'failover' and last_dc:
                    self.log.debug('Break (last dc) iteration for failover route.')
                    break
                elif route_type == 'failover' and not last_dc:
                    self.log.debug('Continue iteration for failover route.')

    @defer.inlineCallbacks
    def deliver_sm_throwing_callback(self, message):
        Thrower.throwing_callback(self, message)

        if message.routing_key == 'deliver_sm_thrower.http':
            yield self.http_deliver_sm_callback(message)
        elif message.routing_key == 'deliver_sm_thrower.smpps':
            yield self.smpp_deliver_sm_callback(message)
        else:
            self.log.error('Unknown routing_key in deliver_sm_throwing_callback: %s', message.routing_key)
            yield self.rejectMessage(message)


class DLRThrower(Thrower):
    name = 'DLRThrower'

    def __init__(self, config):
        self.log_category = "jasmin-dlr-thrower"
        self.exchangeName = 'messaging'
        self.consumerTag = 'DLRThrower'
        self.routingKey = 'dlr_thrower.*'
        self.queueName = 'dlr_thrower'
        self.callback = self.dlr_throwing_callback
        self.opFactory = SMPPOperationFactory()

        Thrower.__init__(self, config)

    @defer.inlineCallbacks
    def http_dlr_callback(self, message):
        msgid = message.content.properties['message-id']
        url = message.content.properties['headers']['url']
        method = message.content.properties['headers']['method']
        level = message.content.properties['headers']['level']
        self.log.debug('Got one message (msgid:%s) to throw', msgid)

        # If any, clear requeuing timer
        self.clearRequeueTimer(msgid)

        # Build mandatory arguments
        args = {'id': msgid, 'level': level,
                'message_status': message.content.properties['headers']['message_status']}

        # Level 2 extra args
        if level in [2, 3]:
            args['id_smsc'] = message.content.properties['headers']['id_smsc']
            args['sub'] = message.content.properties['headers']['sub']
            args['dlvrd'] = message.content.properties['headers']['dlvrd']
            args['subdate'] = message.content.properties['headers']['subdate']
            args['donedate'] = message.content.properties['headers']['donedate']
            args['err'] = message.content.properties['headers']['err']
            args['text'] = message.content.properties['headers']['text']

        try:
            # Throw the message to http endpoint
            encodedArgs = urllib.urlencode(args)
            postdata = None
            baseurl = url
            if method == 'GET':
                baseurl += '?%s' % encodedArgs
            else:
                postdata = encodedArgs

            self.log.debug('Calling %s with args %s using %s method.', baseurl, encodedArgs, method)
            content = yield getPage(
                baseurl,
                method=method,
                postdata=postdata,
                timeout=self.config.timeout,
                agent='Jasmin gateway/1.0 %s' % self.name,
                headers={'Content-Type': 'application/x-www-form-urlencoded',
                         'Accept': 'text/plain'})
            self.log.info('Throwed DLR [msgid:%s] to %s.', msgid, baseurl)

            self.log.debug('Destination end replied to message [msgid:%s]: %r', msgid, content)
            # Check for acknowledgement
            if content.strip() != 'ACK/Jasmin':
                raise MessageAcknowledgementError(
                    'Destination end did not acknowledge receipt of the DLR message.')

            # Everything is okay ? then:
            yield self.ackMessage(message)
        except Exception, e:
            self.log.error('Throwing HTTP/DLR [msgid:%s] to (%s): %r.', msgid, baseurl, e)

            # List of errors after which, no further retrying shall be made
            noRetryErrors = ['404 Not Found']

            # Requeue message for later retry
            if (str(e) not in noRetryErrors
                and self.getThrowingRetrials(message) <= self.config.max_retries):
                self.log.debug('Message try-count is %s [msgid:%s]: requeuing',
                               self.getThrowingRetrials(message), msgid)
                yield self.rejectAndRequeueMessage(message)
            elif str(e) in noRetryErrors:
                self.log.warn('Message is no more processed after receiving "%s" error', str(e))
                yield self.rejectMessage(message)
            else:
                self.log.warn('Message try-count is %s [msgid:%s]: purged from queue',
                              self.getThrowingRetrials(message), msgid)
                yield self.rejectMessage(message)

    @defer.inlineCallbacks
    def smpp_dlr_callback(self, message):
        msgid = message.content.properties['message-id']
        system_id = message.content.properties['headers']['system_id']
        message_status = message.content.properties['headers']['message_status']
        source_addr = '%s' % message.content.properties['headers']['source_addr']
        destination_addr = '%s' % message.content.properties['headers']['destination_addr']
        sub_date = message.content.properties['headers']['sub_date']
        source_addr_ton = message.content.properties['headers']['source_addr_ton']
        source_addr_npi = message.content.properties['headers']['source_addr_npi']
        dest_addr_ton = message.content.properties['headers']['dest_addr_ton']
        dest_addr_npi = message.content.properties['headers']['dest_addr_npi']
        self.log.debug('Got one message (msgid:%s) to throw', msgid)

        # If any, clear requeuing timer
        self.clearRequeueTimer(msgid)

        try:
            if self.smpps is None or self.smpps_access is None:
                raise SmppsNotSetError()

            # Get bound connections (or systemids)
            if self.smpps_access == 'direct':
                bound_systemdids = self.smpps.bound_connections
            else:
                bound_systemdids = yield self.smpps.list_bound_systemids()

            if system_id not in bound_systemdids:
                raise SystemIdNotBound(system_id)

            # Build the Receipt PDU (data_sm)
            pdu = self.opFactory.getReceipt(dlr_pdu=self.config.dlr_pdu,
                                            msgid=msgid,
                                            source_addr=source_addr,
                                            destination_addr=destination_addr,
                                            message_status=message_status,
                                            sub_date=sub_date,
                                            source_addr_ton=source_addr_ton,
                                            source_addr_npi=source_addr_npi,
                                            dest_addr_ton=dest_addr_ton,
                                            dest_addr_npi=dest_addr_npi)

            # Pick a deliverer and sendRequest
            if self.smpps_access == 'direct':
                deliverer = bound_systemdids[system_id].getNextBindingForDelivery()

                if deliverer is None:
                    raise NoDelivererForSystemId(system_id)

                yield deliverer.sendRequest(pdu, deliverer.config().responseTimerSecs)
            else:
                r = yield self.smpps.deliverer_send_request(system_id, pdu)
                if not r:
                    raise DeliveringFailed('Delivering failed, check %s smpps logs for more details' % system_id)
        except Exception, e:
            self.log.error('Throwing SMPP/DLR [msgid:%s] to (%s): %r.', msgid, system_id, e)

            # List of exceptions after which, no further retrying shall be made
            noRetryExceptions = [SmppsNotSetError]

            retry = True
            for noRetryException in noRetryExceptions:
                if isinstance(e, noRetryException):
                    retry = False
                    break

            # Requeue message for later retry
            if retry and self.getThrowingRetrials(message) <= self.config.max_retries:
                self.log.debug('Message try-count is %s [msgid:%s]: requeuing',
                               self.getThrowingRetrials(message), msgid)
                yield self.rejectAndRequeueMessage(message)
            elif retry and self.getThrowingRetrials(message) > self.config.max_retries:
                self.log.warn('Message is no more processed after receiving "%s" error', str(e))
                yield self.rejectMessage(message)
            else:
                self.log.warn('Message try-count is %s [msgid:%s]: purged from queue',
                              self.getThrowingRetrials(message), msgid)
                yield self.rejectMessage(message)
        else:
            # Everything is okay ? then:
            yield self.ackMessage(message)

    @defer.inlineCallbacks
    def dlr_throwing_callback(self, message):
        Thrower.throwing_callback(self, message)

        if message.routing_key == 'dlr_thrower.http':
            yield self.http_dlr_callback(message)
        elif message.routing_key == 'dlr_thrower.smpps':
            yield self.smpp_dlr_callback(message)
        else:
            self.log.error('Unknown routing_key in dlr_throwing_callback: %s', message.routing_key)
            yield self.rejectMessage(message)
