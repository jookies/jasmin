import logging
from logging.handlers import TimedRotatingFileHandler

from twisted.internet import defer
from twisted.internet import reactor
from txamqp.queue import Closed

from jasmin.managers.content import DLRContentForHttpapi, DLRContentForSmpps
from jasmin.tools.singleton import Singleton

LOG_CATEGORY = "dlr"


class RedisError(Exception):
    """Raised for any Redis connectivity problem"""


class DLRMapError(Exception):
    """Raised when receiving an invalid dlr content from Redis"""


class DLRMapNotFound(Exception):
    """Raised if no dlr is found in Redis db"""


class DLRLookup(object):
    """
    Will consume dlr pdus (submit_sm, deliver_sm or data_sm), lookup for matching dlr maps in redis db
    and publish dlr for later throwing (http or smpp)
    """

    def __init__(self, config, amqpBroker, redisClient):
        self.pid = config.pid
        self.q = None
        self.config = config
        self.amqpBroker = amqpBroker
        self.redisClient = redisClient
        self.requeue_timers = {}
        self.lookup_retrials = {}

        # Set up a dedicated logger
        self.log = logging.getLogger(LOG_CATEGORY)
        if len(self.log.handlers) != 1:
            self.log.setLevel(self.config.log_level)
            handler = TimedRotatingFileHandler(filename=self.config.log_file,
                                               when=self.config.log_rotate)
            formatter = logging.Formatter(self.config.log_format, self.config.log_date_format)
            handler.setFormatter(formatter)
            self.log.addHandler(handler)
            self.log.propagate = False

        self.log.info('Started %s #%s.', self.__class__.__name__, self.pid)

        # Subscribe to dlr.* queues
        consumerTag = 'DLRLookup-%s' % self.pid
        queueName = 'DLRLookup-%s' % self.pid  # A local queue to this object
        routing_key = 'dlr.*'
        self.amqpBroker.chan.exchange_declare(exchange='messaging', type='topic').addCallback(
            lambda _: self.amqpBroker.named_queue_declare(queue=queueName).addCallback(
                lambda _: self.amqpBroker.chan.queue_bind(queue=queueName, exchange="messaging",
                                                          routing_key=routing_key).addCallback(
                    lambda _: self.amqpBroker.chan.basic_consume(queue=queueName, no_ack=False,
                                                                 consumer_tag=consumerTag).addCallback(
                        lambda _: self.amqpBroker.client.queue(consumerTag).addCallback(self.setup_callbacks)
                    )
                )
            )
        )

    @defer.inlineCallbacks
    def rejectAndRequeueMessage(self, message, delay=True):
        msgid = message.content.properties['message-id']

        if delay:
            self.log.debug("Requeuing Content[%s] with delay: %s seconds",
                           msgid, self.config.dlr_lookup_retry_delay)

            # Requeue the message with a delay
            timer = reactor.callLater(self.config.dlr_lookup_retry_delay,
                                      self.rejectMessage,
                                      message=message,
                                      requeue=1)

            # If any, clear timer before setting a new one
            if msgid in self.requeue_timers:
                timer = self.requeue_timers[msgid]
                if timer.active():
                    timer.cancel()
                del self.requeue_timers[msgid]

            # Set new timer
            self.requeue_timers[msgid] = timer
            defer.returnValue(timer)
        else:
            self.log.debug("Requeuing Content[%s] without delay", msgid)
            yield self.rejectMessage(message, requeue=1)

    @defer.inlineCallbacks
    def rejectMessage(self, message, requeue=0):
        if requeue == 0 and message.content.properties['message-id'] in self.lookup_retrials:
            # Remove retrial tracker
            del self.lookup_retrials[message.content.properties['message-id']]

        yield self.amqpBroker.chan.basic_reject(delivery_tag=message.delivery_tag, requeue=requeue)

    @defer.inlineCallbacks
    def ackMessage(self, message):
        # Remove retrial tracker
        if message.content.properties['message-id'] in self.lookup_retrials:
            # Remove retrial tracker
            del self.lookup_retrials[message.content.properties['message-id']]

        yield self.amqpBroker.chan.basic_ack(message.delivery_tag)

    def setup_callbacks(self, q):
        if self.q is None:
            self.q = q
            self.log.info('DLRLookup (%s) is ready.', self.pid)

        q.get().addCallback(self.dlr_callback_dispatcher).addErrback(self.dlr_errback)

    @defer.inlineCallbacks
    def dlr_callback_dispatcher(self, message):
        # Again ...
        self.setup_callbacks(self.q)

        # retrial tracking
        if message.content.properties['message-id'] in self.lookup_retrials:
            self.lookup_retrials[message.content.properties['message-id']] += 1
        else:
            self.lookup_retrials[message.content.properties['message-id']] = 1

        # Dispatching
        if message.routing_key == 'dlr.submit_sm_resp':
            yield self.submit_sm_resp_dlr_callback(message)
        elif message.routing_key == 'dlr.deliver_sm':
            yield self.deliver_sm_dlr_callback(message)
        else:
            self.log.error('Unknown routing_key in dlr_callback_dispatcher: %s', message.routing_key)
            yield self.rejectMessage(message)

    def dlr_errback(self, error):
        """It appears that when closing a queue with the close() method it errbacks with
        a txamqp.queue.Closed exception, didnt find a clean way to stop consuming a queue
        without errbacking here so this is a workaround to make it clean, it can be considered
        as a @TODO requiring knowledge of the queue api behaviour
        """
        if error.check(Closed) is None:
            # @todo: implement this errback
            # For info, this errback is called whenever:
            # - an error has occured inside dlr_callback_dispatcher
            self.log.error("Error in dlr_callback_dispatcher: %s", error)

    @defer.inlineCallbacks
    def submit_sm_resp_dlr_callback(self, message):
        msgid = message.content.properties['message-id']
        dlr_status = message.content.body

        try:
            if self.redisClient is None:
                raise RedisError('RC undefined !')
            if self.redisClient.connected != 1:
                raise RedisError('RC is offline !')

            # Check for DLR request from redis 'dlr' key
            # If there's a pending delivery receipt request then serve it
            # back by publishing a DLRContentForHttpapi to the messaging exchange
            dlr = yield self.redisClient.hgetall("dlr:%s" % msgid)

            if dlr is None or len(dlr) == 0:
                raise DLRMapNotFound('No dlr map for msgid[%s]' % msgid)
            if 'sc' not in dlr or dlr['sc'] not in ['httpapi', 'smppsapi']:
                raise DLRMapError('Fetched unknown dlr: %s' % dlr)

            if dlr['sc'] == 'httpapi':
                self.log.debug('There is a HTTP DLR request for msgid[%s] ...', msgid)
                dlr_url = dlr['url']
                dlr_level = dlr['level']
                dlr_method = dlr['method']
                dlr_expiry = dlr['expiry']

                if dlr['level'] in [1, 3]:
                    self.log.debug('Got DLR information for msgid[%s], url:%s, level:%s',
                                   msgid, dlr_url, dlr_level)

                    # The dlr_url in DLRContentForHttpapi indicates the level
                    # of the actual delivery receipt (1) and not the requested
                    # one (maybe 1 or 3)
                    self.log.debug("Publishing DLRContentForHttpapi[%s] with routing_key[%s]",
                                   msgid, 'dlr_thrower.http')
                    yield self.amqpBroker.publish(exchange='messaging',
                                                  routing_key='dlr_thrower.http',
                                                  content=DLRContentForHttpapi(dlr_status,
                                                                               msgid, dlr_url,
                                                                               dlr_level=1, method=dlr_method))

                    # DLR request is removed if:
                    # - If level 1 is requested (SMSC level only)
                    # - SubmitSmResp returned an error (no more delivery will be tracked)
                    #
                    # When level 3 is requested, the DLR will be removed when
                    # receiving a deliver_sm (terminal receipt)
                    if dlr_level == 1 or dlr_status != 'ESME_ROK':
                        self.log.debug('Removing DLR request for msgid[%s]', msgid)
                        yield self.redisClient.delete("dlr:%s" % msgid)
                else:
                    self.log.debug(
                        'Terminal level receipt is requested, will not send any DLR receipt at this level.')

                if dlr_level in [2, 3] and dlr_status == 'ESME_ROK':
                    smpp_msgid = message.content.properties['headers']['smpp_msgid']
                    # Map received submit_sm_resp's message_id to the msg for later receipt handling
                    self.log.debug('Mapping smpp msgid: %s to queue msgid: %s, expiring in %s',
                                   smpp_msgid, msgid, dlr_expiry)
                    hashKey = "queue-msgid:%s" % smpp_msgid
                    hashValues = {'msgid': msgid, 'connector_type': 'httpapi'}
                    yield self.redisClient.hmset(hashKey, hashValues)
                    yield self.redisClient.expire(hashKey, dlr_expiry)
            elif dlr['sc'] == 'smppsapi':
                self.log.debug('There is a SMPPs mapping for msgid[%s] ...', msgid)
                system_id = dlr['system_id']
                source_addr_ton = dlr['source_addr_ton']
                source_addr_npi = dlr['source_addr_npi']
                source_addr = str(dlr['source_addr'])
                dest_addr_ton = dlr['dest_addr_ton']
                dest_addr_npi = dlr['dest_addr_npi']
                destination_addr = str(dlr['destination_addr'])
                sub_date = dlr['sub_date']
                registered_delivery_receipt = dlr['rd_receipt']
                smpps_map_expiry = dlr['expiry']

                # Do we need to forward the receipt to the original sender ?
                if ((dlr_status == 'ESME_ROK' and registered_delivery_receipt in
                    ['SMSC_DELIVERY_RECEIPT_REQUESTED_FOR_FAILURE', 'SMSC_DELIVERY_RECEIPT_REQUESTED']) or
                        (dlr_status != 'ESME_ROK' and
                                 registered_delivery_receipt == 'SMSC_DELIVERY_RECEIPT_REQUESTED_FOR_FAILURE')):
                    self.log.debug('Got DLR information for msgid[%s], registered_deliver%s, system_id:%s',
                                   msgid, registered_delivery_receipt, system_id)

                    if (dlr_status != 'ESME_ROK' or (dlr_status == 'ESME_ROK' and
                                                         self.config.smpp_receipt_on_success_submit_sm_resp)):
                        # Send back a receipt (by throwing deliver_sm or data_sm)
                        self.log.debug("Publishing DLRContentForSmpps[%s] with routing_key[%s]",
                                       msgid, 'dlr_thrower.smpps')
                        yield self.amqpBroker.publish(exchange='messaging',
                                                      routing_key='dlr_thrower.smpps',
                                                      content=DLRContentForSmpps(dlr_status, msgid, system_id,
                                                                                 source_addr,
                                                                                 destination_addr, sub_date,
                                                                                 source_addr_ton,
                                                                                 source_addr_npi,
                                                                                 dest_addr_ton,
                                                                                 dest_addr_npi))

                    if dlr_status == 'ESME_ROK':
                        smpp_msgid = message.content.properties['headers']['smpp_msgid']
                        # Map received submit_sm_resp's message_id to the msg for later rceipt handling
                        self.log.debug('Mapping smpp msgid: %s to queue msgid: %s, expiring in %s',
                                       smpp_msgid, msgid, smpps_map_expiry)
                        hashKey = "queue-msgid:%s" % smpp_msgid
                        hashValues = {'msgid': msgid, 'connector_type': 'smppsapi'}
                        yield self.redisClient.hmset(hashKey, hashValues)
                        yield self.redisClient.expire(hashKey, smpps_map_expiry)
        except DLRMapError as e:
            self.log.error('[msgid:%s] DLR Content: %s', msgid, e)
            yield self.rejectMessage(message)
        except RedisError as e:
            if msgid in self.lookup_retrials and self.lookup_retrials[msgid] < self.config.dlr_lookup_max_retries:
                self.log.error('[msgid:%s] (retrials: %s/%s) RedisError: %s', msgid, self.lookup_retrials[msgid],
                               self.config.dlr_lookup_max_retries, e)
                yield self.rejectAndRequeueMessage(message)
            else:
                self.log.error('[msgid:%s] (final) RedisError: %s', msgid, e)
                yield self.rejectMessage(message)
        except DLRMapNotFound as e:
            self.log.debug('[msgid:%s] DLRMapNotFound: %s', msgid, e)
            yield self.rejectMessage(message)
        except Exception as e:
            self.log.error('[msgid:%s] Unknown error (%s): %s', msgid, type(e), e)
            yield self.rejectMessage(message)
        else:
            yield self.ackMessage(message)

    @defer.inlineCallbacks
    def deliver_sm_dlr_callback(self, message):
        msgid = message.content.properties['message-id']
        pdu_cid = message.content.properties['headers']['cid']
        pdu_dlr_id = message.content.properties['headers']['dlr_id']
        pdu_dlr_ddate = message.content.properties['headers']['dlr_ddate']
        pdu_dlr_sdate = message.content.properties['headers']['dlr_sdate']
        pdu_dlr_sub = message.content.properties['headers']['dlr_sub']
        pdu_dlr_err = message.content.properties['headers']['dlr_err']
        pdu_dlr_text = message.content.properties['headers']['dlr_text']
        pdu_dlr_dlvrd = message.content.properties['headers']['dlr_dlvrd']
        pdu_dlr_status = message.content.body

        try:
            if self.redisClient is None:
                raise RedisError('RC undefined !')
            if self.redisClient.connected != 1:
                raise RedisError('RC is offline !')

            q = yield self.redisClient.hgetall("queue-msgid:%s" % msgid)
            if len(q) != 2 or 'msgid' not in q or 'connector_type' not in q:
                raise DLRMapNotFound('Got a DLR for an unknown message id: %s (coded:%s)' % (pdu_dlr_id, msgid))

            submit_sm_queue_id = q['msgid']
            connector_type = q['connector_type']

            # Get dlr and ensure it's sc (source_connector) is same as q['connector_type']
            dlr = yield self.redisClient.hgetall("dlr:%s" % submit_sm_queue_id)
            if dlr is None or len(dlr) == 0:
                raise DLRMapNotFound('Got a DLR for an unknown message id: %s (coded:%s)' % (pdu_dlr_id, msgid))
            if len(dlr) > 0 and dlr['sc'] != connector_type:
                raise DLRMapError('Found a dlr for msgid:%s with diffrent sc: %s' % (submit_sm_queue_id, dlr['sc']))

            if connector_type == 'httpapi':
                self.log.debug('There is a HTTP DLR request for msgid[%s] ...', msgid)
                dlr_url = dlr['url']
                dlr_level = dlr['level']
                dlr_method = dlr['method']

                if dlr_level in [2, 3]:
                    self.log.debug('Got DLR information for msgid[%s], url:%s, level:%s',
                                   submit_sm_queue_id, dlr_url, dlr_level)
                    # The dlr_url in DLRContentForHttpapi indicates the level
                    # of the actual delivery receipt (2) and not the
                    # requested one (maybe 2 or 3)
                    self.log.debug("Publishing DLRContentForHttpapi[%s] with routing_key[%s]",
                                   submit_sm_queue_id, 'dlr_thrower.http')
                    yield self.amqpBroker.publish(exchange='messaging',
                                                  routing_key='dlr_thrower.http',
                                                  content=DLRContentForHttpapi(pdu_dlr_status,
                                                                               submit_sm_queue_id,
                                                                               dlr_url, dlr_level=2, id_smsc=msgid,
                                                                               sub=pdu_dlr_sub,
                                                                               dlvrd=pdu_dlr_dlvrd,
                                                                               subdate=pdu_dlr_sdate,
                                                                               donedate=pdu_dlr_ddate,
                                                                               err=pdu_dlr_err,
                                                                               text=pdu_dlr_text,
                                                                               method=dlr_method))

                    self.log.debug('Removing HTTP dlr map for msgid[%s]', submit_sm_queue_id)
                    yield self.redisClient.delete('dlr:%s' % submit_sm_queue_id)
            elif connector_type == 'smppsapi':
                self.log.debug('There is a SMPPs mapping for msgid[%s] ...', msgid)
                system_id = dlr['system_id']
                source_addr_ton = dlr['source_addr_ton']
                source_addr_npi = dlr['source_addr_npi']
                source_addr = str(dlr['source_addr'])
                dest_addr_ton = dlr['dest_addr_ton']
                dest_addr_npi = dlr['dest_addr_npi']
                destination_addr = str(dlr['destination_addr'])
                sub_date = dlr['sub_date']
                registered_delivery_receipt = dlr['rd_receipt']

                success_states = ['ACCEPTD', 'DELIVRD']
                final_states = ['DELIVRD', 'EXPIRED', 'DELETED', 'UNDELIV', 'REJECTD']
                # Do we need to forward the receipt to the original sender ?
                if ((pdu_dlr_status in success_states and
                             registered_delivery_receipt == 'SMSC_DELIVERY_RECEIPT_REQUESTED') or
                        (pdu_dlr_status not in success_states and
                                 registered_delivery_receipt in ['SMSC_DELIVERY_RECEIPT_REQUESTED',
                                                                 'SMSC_DELIVERY_RECEIPT_REQUESTED_FOR_FAILURE'])):
                    self.log.debug(
                        'Got DLR information for msgid[%s], registered_deliver%s, system_id:%s',
                        submit_sm_queue_id, registered_delivery_receipt, system_id)

                    self.log.debug("Publishing DLRContentForSmpps[%s] with routing_key[%s]",
                                   submit_sm_queue_id, 'dlr_thrower.smpps')
                    yield self.amqpBroker.publish(exchange='messaging',
                                                  routing_key='dlr_thrower.smpps',
                                                  content=DLRContentForSmpps(pdu_dlr_status,
                                                                             submit_sm_queue_id, system_id,
                                                                             source_addr, destination_addr, sub_date,
                                                                             source_addr_ton, source_addr_npi,
                                                                             dest_addr_ton, dest_addr_npi))

                    if pdu_dlr_status in final_states:
                        self.log.debug('Removing SMPPs dlr map for msgid[%s]', submit_sm_queue_id)
                        yield self.redisClient.delete('dlr:%s' % submit_sm_queue_id)
        except DLRMapError as e:
            self.log.error('[msgid:%s] DLRMapError: %s', msgid, e)
            yield self.rejectMessage(message)
        except RedisError as e:
            if msgid in self.lookup_retrials and self.lookup_retrials[msgid] < self.config.dlr_lookup_max_retries:
                self.log.error('[msgid:%s] (retrials: %s/%s) RedisError: %s', msgid, self.lookup_retrials[msgid],
                               self.config.dlr_lookup_max_retries, e)
                yield self.rejectAndRequeueMessage(message)
            else:
                self.log.error('[msgid:%s] (final) RedisError: %s', msgid, e)
                yield self.rejectMessage(message)
        except DLRMapNotFound as e:
            if msgid in self.lookup_retrials and self.lookup_retrials[msgid] < self.config.dlr_lookup_max_retries:
                self.log.error('[msgid:%s] (retrials: %s/%s) DLRMapNotFound: %s', msgid, self.lookup_retrials[msgid],
                               self.config.dlr_lookup_max_retries, e)
                yield self.rejectAndRequeueMessage(message)
            else:
                self.log.error('[msgid:%s] (final) DLRMapNotFound: %s', msgid, e)
                yield self.rejectMessage(message)
        except Exception as e:
            self.log.error('[msgid:%s] Unknown error (%s): %s', msgid, type(e), e)
            yield self.rejectMessage(message)
        else:
            yield self.ackMessage(message)

            self.log.info(
                "DLR [cid:%s] [smpp-msgid:%s] [status:%s] [submit date:%s] [done date:%s] [sub/dlvrd messages:%s/%s] \
[err:%s] [content:%r]",
                pdu_cid,
                msgid,
                pdu_dlr_status,
                pdu_dlr_sdate,
                pdu_dlr_ddate,
                pdu_dlr_sub,
                pdu_dlr_dlvrd,
                pdu_dlr_err,
                pdu_dlr_text)


class DLRLookupSingleton(object):
    """Used to launch only one DLRLookup object"""
    __metaclass__ = Singleton
    objects = {}

    def get(self, config, amqpBroker, redisClient):
        """Return a DLRLookup object or instanciate a new one"""
        name = 'singleton'
        if name not in self.objects:
            self.objects[name] = DLRLookup(name, config, amqpBroker, redisClient)

        return self.objects[name]
