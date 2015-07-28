#pylint: disable-msg=W0401,W0611
import re
import logging
import pickle
import struct
from logging.handlers import TimedRotatingFileHandler
from datetime import datetime, timedelta
from dateutil import parser
from twisted.internet import defer
from jasmin.vendor.smpp.pdu.pdu_types import CommandStatus, CommandId
from jasmin.vendor.smpp.pdu.operations import SubmitSM
from txamqp.queue import Closed
from twisted.internet import reactor, task
from jasmin.managers.content import (SubmitSmRespContent, DeliverSmContent, 
                                    DLRContentForHttpapi, DLRContentForSmpps,
                                    SubmitSmRespBillContent)
from jasmin.managers.configs import SMPPClientPBConfig
from jasmin.protocols.smpp.error import *
from jasmin.protocols.smpp.operations import SMPPOperationFactory

LOG_CATEGORY = "jasmin-sm-listener"

def SubmitSmPDUUpdate(fCallback):
    '''Will extract SubmitSmPDU and update it (if needed) then pass it to fCallback'''
    def update_submit_sm_pdu(self, *args, **kwargs):
        message = args[0]
        SubmitSmPDU = pickle.loads(message.content.body)

        if 'headers' in message.content.properties:
            headers = message.content.properties['headers']
            """SubmitSmPDU is sent through httpapi, in this case, some params that cannot be defined
            through the api must be set here (from the connector config):"""
            if 'source_connector' in headers and headers['source_connector'] == 'httpapi':
                update_params = [
                    'protocol_id', 
                    'replace_if_present_flag', 
                    'dest_addr_ton',
                    'source_addr_npi',
                    'dest_addr_npi',
                    'service_type',
                    'source_addr_ton',
                    'sm_default_msg_id',
                ]

                for param in update_params:
                    _pdu = SubmitSmPDU

                    # Set param in main pdu
                    _pdu.params[param] = getattr(self.SMPPClientFactory.config, param)

                    # Set param in sub-pdus (multipart use case)
                    while hasattr(_pdu, 'nextPdu'):
                        _pdu = _pdu.nextPdu
                        _pdu.params[param] = getattr(self.SMPPClientFactory.config, param)

        return fCallback(self, message, SubmitSmPDU)
    return update_submit_sm_pdu

class SMPPClientSMListener:
    debug_it = {'rejectCount': 0}
    '''
    This is a listener object instanciated for every new SMPP connection, it is responsible of handling 
    SubmitSm, DeliverSm and SubmitSm PDUs for a given SMPP connection
    '''
    
    def __init__(self, SMPPClientSMListenerConfig, SMPPClientFactory, amqpBroker, redisClient):
        self.config = SMPPClientSMListenerConfig
        self.SMPPClientFactory = SMPPClientFactory
        self.SMPPOperationFactory = SMPPOperationFactory(self.SMPPClientFactory.config)
        self.amqpBroker = amqpBroker
        self.redisClient = redisClient
        self.submit_sm_q = None
        self.qos_last_submit_sm_at = None
        self.rejectTimers = {}
        self.submit_retrials = {}
        self.qosTimer = None
        self.re_patterns = {}

        # Set pickleProtocol
        self.pickleProtocol = SMPPClientPBConfig(self.config.config_file).pickle_protocol

        # Set up a dedicated logger
        self.log = logging.getLogger(LOG_CATEGORY)
        if len(self.log.handlers) != 1:
            self.log.setLevel(self.config.log_level)
            handler = TimedRotatingFileHandler(filename=self.config.log_file, 
                when = self.config.log_rotate)
            formatter = logging.Formatter(self.config.log_format, self.config.log_date_format)
            handler.setFormatter(formatter)
            self.log.addHandler(handler)
            self.log.propagate = False

    def setSubmitSmQ(self, queue):
        self.log.debug('Setting a new submit_sm_q: %s' % queue)
        self.submit_sm_q = queue
        
    def clearRejectTimer(self, msgid):
        if msgid in self.rejectTimers:
            t = self.rejectTimers[msgid]
            if t.active():
                t.cancel()
            del self.rejectTimers[msgid]

    def clearRejectTimers(self):
        for msgid, timer in self.rejectTimers.items():
            if timer.active():
                timer.cancel()
            del self.rejectTimers[msgid]
                
    def clearQosTimer(self):
        if self.qosTimer is not None and self.qosTimer.called is False:
            self.qosTimer.cancel()
            self.qosTimer = None
        
    def clearAllTimers(self):
        self.clearQosTimer()
        self.clearRejectTimers()
    
    @defer.inlineCallbacks
    def rejectAndRequeueMessage(self, message, delay = True):
        msgid = message.content.properties['message-id']
        
        if delay != False:
            # Use configured requeue_delay or specific one
            if type(delay) != bool:
                requeue_delay = delay
            else:
                requeue_delay = self.SMPPClientFactory.config.requeue_delay

            self.log.debug("Requeuing SubmitSmPDU[%s] in %s seconds" % 
                           (msgid, requeue_delay))

            # Requeue the message with a delay
            t = reactor.callLater(requeue_delay, 
                                  self.rejectMessage,
                                  message = message, 
                                  requeue = 1)

            # If any, clear timer before setting a new one
            self.clearRejectTimer(msgid)
            
            self.rejectTimers[msgid] = t
            defer.returnValue(t)
        else:
            self.log.debug("Requeuing SubmitSmPDU[%s] without delay" % msgid)
            yield self.rejectMessage(message, requeue = 1)
    @defer.inlineCallbacks
    def rejectMessage(self, message, requeue = 0):
        yield self.amqpBroker.chan.basic_reject(delivery_tag=message.delivery_tag, requeue = requeue)
    @defer.inlineCallbacks
    def ackMessage(self, message):
        yield self.amqpBroker.chan.basic_ack(message.delivery_tag)

    @SubmitSmPDUUpdate
    @defer.inlineCallbacks
    def submit_sm_callback(self, message, SubmitSmPDU):
        """This callback is a queue listener
        it is called whenever a message was consumed from queue
        c.f. test_amqp.ConsumeTestCase for use cases
        """
        try:
            msgid = message.content.properties['message-id']

            self.submit_sm_q.get().addCallback(self.submit_sm_callback).addErrback(self.submit_sm_errback)

            self.log.debug("Callbacked a submit_sm with a SubmitSmPDU[%s] (?): %s" % (msgid, SubmitSmPDU))

            # Update submit_sm retrial tracker
            if msgid in self.submit_retrials:
                self.submit_retrials[msgid]+= 1
            else:
                self.submit_retrials[msgid] = 1

            if self.qos_last_submit_sm_at is None:
                self.qos_last_submit_sm_at = datetime(1970, 1, 1)    
                
            if self.SMPPClientFactory.config.submit_sm_throughput > 0:
                # QoS throttling
                qos_throughput_second = 1 / float(self.SMPPClientFactory.config.submit_sm_throughput)
                qos_throughput_ysecond_td = timedelta( microseconds = qos_throughput_second * 1000000)
                qos_delay = datetime.now() - self.qos_last_submit_sm_at
                if qos_delay < qos_throughput_ysecond_td:
                    qos_slow_down = float((qos_throughput_ysecond_td - qos_delay).microseconds) / 1000000
                    # We're faster than submit_sm_throughput, slow down before taking a new message from the queue
                    self.log.debug("QoS: submit_sm_callback is faster (%s) than fixed throughput (%s), slowing down by %s seconds (message will be requeued)." % (
                                    qos_delay,
                                    qos_throughput_ysecond_td,
                                    qos_slow_down
                                    ))

                    # Relaunch queue callbacking after qos_slow_down seconds
                    #self.qosTimer = task.deferLater(reactor, qos_slow_down, self.submit_sm_q.get)
                    #self.qosTimer.addCallback(self.submit_sm_callback).addErrback(self.submit_sm_errback)
                    # Requeue the message
                    yield self.rejectAndRequeueMessage(message, delay = qos_slow_down)
                    defer.returnValue(False)
                
                self.qos_last_submit_sm_at = datetime.now()
            
            # Verify if message is a SubmitSm PDU
            if isinstance(SubmitSmPDU, SubmitSM) is False:
                self.log.error("Received an object[%s] which is not an instance of SubmitSm: discarding this unkown object from the queue" % msgid)
                yield self.rejectMessage(message)
                defer.returnValue(False)
            # If the message has expired in the queue
            if 'headers' in message.content.properties and 'expiration' in message.content.properties['headers']:
                expiration_datetime = parser.parse(message.content.properties['headers']['expiration'])
                if expiration_datetime < datetime.now():
                    self.log.info("Discarding expired message[%s]: expiration is %s" % (msgid, expiration_datetime))
                    yield self.rejectMessage(message)
                    defer.returnValue(False)
            # SMPP Client should be already connected
            if self.SMPPClientFactory.smpp is None:
                created_at = parser.parse(message.content.properties['headers']['created_at'])
                msgAge = datetime.now() - created_at
                if msgAge.seconds > self.config.submit_max_age_smppc_not_ready:
                    self.log.error("SMPPC [cid:%s] is not connected: Discarding (#%s) SubmitSmPDU[%s], over-aged %s seconds." % (
                        self.SMPPClientFactory.config.id, 
                        self.submit_retrials[msgid],
                        msgid,
                        msgAge.seconds,
                        )
                    )
                    yield self.rejectMessage(message)
                    defer.returnValue(False)
                else:
                    if self.config.submit_retrial_delay_smppc_not_ready != False:
                        delay_str = ' with delay %s seconds' % self.config.submit_retrial_delay_smppc_not_ready
                    else:
                        delay_str = ''
                    self.log.error("SMPPC [cid:%s] is not connected: Requeuing (#%s) SubmitSmPDU[%s]%s, aged %s seconds." % (
                        self.SMPPClientFactory.config.id, 
                        self.submit_retrials[msgid],
                        msgid,
                        delay_str,
                        msgAge.seconds,
                        )
                    )
                    yield self.rejectAndRequeueMessage(message, delay = self.config.submit_retrial_delay_smppc_not_ready)
                    defer.returnValue(False)
            # SMPP Client should be already bound as transceiver or transmitter
            if self.SMPPClientFactory.smpp.isBound() is False:
                created_at = parser.parse(message.content.properties['headers']['created_at'])
                msgAge = datetime.now() - created_at
                if msgAge.seconds > self.config.submit_max_age_smppc_not_ready:
                    self.log.error("SMPPC [cid:%s] is not bound: Discarding (#%s) SubmitSmPDU[%s], over-aged %s seconds." % (
                        self.SMPPClientFactory.config.id, 
                        self.submit_retrials[msgid],
                        msgid,
                        msgAge.seconds,
                        )
                    )
                    yield self.rejectMessage(message)
                    defer.returnValue(False)
                else:
                    if self.config.submit_retrial_delay_smppc_not_ready != False:
                        delay_str = ' with delay %s seconds' % self.config.submit_retrial_delay_smppc_not_ready
                    else:
                        delay_str = ''
                    self.log.error("SMPPC [cid:%s] is not bound: Requeuing (#%s) SubmitSmPDU[%s]%s, aged %s seconds."% (
                        self.SMPPClientFactory.config.id, 
                        self.submit_retrials[msgid],
                        msgid,
                        delay_str,
                        msgAge,
                        )
                    )
                    yield self.rejectAndRequeueMessage(message, delay = self.config.submit_retrial_delay_smppc_not_ready)
                    defer.returnValue(False)

            # Finally: send the sms !
            self.log.debug("Sending SubmitSmPDU[%s] through SMPPClientFactory [cid:%s]" % (
                msgid,
                self.SMPPClientFactory.config.id
            ))
            d = self.SMPPClientFactory.smpp.sendDataRequest(SubmitSmPDU)
            d.addCallback(self.submit_sm_resp_event, message)
            yield d
        except SMPPRequestTimoutError:
            self.log.error("SubmitSmPDU[%s] request timed out through [cid:%s], message requeued." % (
                msgid,
                self.SMPPClientFactory.config.id
            ))
            self.rejectAndRequeueMessage(message)
            defer.returnValue(False)
        except LongSubmitSmTransactionError, e:
            self.log.error("Long SubmitSmPDU[%s] error in [cid:%s], message requeued: %s" % (
                msgid,
                self.SMPPClientFactory.config.id,
                e.message
            ))
            self.rejectAndRequeueMessage(message)
            defer.returnValue(False)
        except Exception, e:
            self.log.critical("Rejecting SubmitSmPDU[%s] through [cid:%s] for an unknown error (%s): %s" % (
                msgid,
                self.SMPPClientFactory.config.id,
                type(e), 
                e
            ))
            self.rejectMessage(message)
            defer.returnValue(False)

    @defer.inlineCallbacks
    def submit_sm_resp_event(self, r, amqpMessage):
        msgid = amqpMessage.content.properties['message-id']
        total_bill_amount = None
        will_be_retried = False
        
        if ('headers' not in amqpMessage.content.properties or 
            'submit_sm_resp_bill' not in amqpMessage.content.properties['headers']):
            submit_sm_resp_bill = None
        else:  
            submit_sm_resp_bill = pickle.loads(amqpMessage.content.properties['headers']['submit_sm_resp_bill'])
        
        if r.response.status == CommandStatus.ESME_ROK:
            # No more retrials !
            del self.submit_retrials[msgid]

            # Get bill information
            total_bill_amount = 0.0
            if submit_sm_resp_bill is not None and submit_sm_resp_bill.getTotalAmounts() > 0:
                total_bill_amount = submit_sm_resp_bill.getTotalAmounts()

            # UDH is set ?
            UDHI_INDICATOR_SET = False
            if hasattr(r.request.params['esm_class'], 'gsmFeatures'):
                for gsmFeature in r.request.params['esm_class'].gsmFeatures:
                    if str(gsmFeature) == 'UDHI_INDICATOR_SET':
                        UDHI_INDICATOR_SET = True
                        break

            # What type of splitting ?
            splitMethod = None
            if 'sar_msg_ref_num' in r.request.params:
                splitMethod = 'sar'
            elif UDHI_INDICATOR_SET and r.request.params['short_message'][:3] == '\x05\x00\x03':
                splitMethod = 'udh'
            
            # Concatenate short_message
            if splitMethod is not None:
                _pdu = r.request
                if splitMethod == 'sar':
                    short_message = _pdu.params['short_message']
                else:
                    short_message = _pdu.params['short_message'][6:]
                
                while hasattr(_pdu, 'nextPdu'):
                    _pdu = _pdu.nextPdu
                    if splitMethod == 'sar':
                        short_message += _pdu.params['short_message']
                    else:
                        short_message += _pdu.params['short_message'][6:]
                    
                    # Increase bill amount for each submit_sm_resp
                    if submit_sm_resp_bill is not None and submit_sm_resp_bill.getTotalAmounts() > 0:
                        total_bill_amount+= submit_sm_resp_bill.getTotalAmounts()
            else:
                short_message = r.request.params['short_message']
            
            self.log.info("SMS-MT [cid:%s] [queue-msgid:%s] [smpp-msgid:%s] [status:%s] [prio:%s] [dlr:%s] [validity:%s] [from:%s] [to:%s] [content:%s]" % 
                          (
                           self.SMPPClientFactory.config.id,
                           msgid,
                           r.response.params['message_id'],
                           r.response.status,
                           amqpMessage.content.properties['priority'],
                           r.request.params['registered_delivery'].receipt,
                           'none' if ('headers' not in amqpMessage.content.properties or
                                      'expiration' not in amqpMessage.content.properties['headers']) 
                                  else amqpMessage.content.properties['headers']['expiration'],
                           r.request.params['source_addr'],
                           r.request.params['destination_addr'],
                           re.sub(r'[^\x20-\x7E]+','.', short_message)
                           ))
        else:
            # Message must be retried ?
            if str(r.response.status) in self.config.submit_error_retrial:
                retrial = self.config.submit_error_retrial[str(r.response.status)]

                # Still have some retrys to go ?
                if self.submit_retrials[msgid] < retrial['count']:
                    # Requeue the message for later redelivery
                    yield self.rejectAndRequeueMessage(amqpMessage, delay = retrial['delay'])
                    will_be_retried = True

            # Log the message
            self.log.info("SMS-MT [cid:%s] [queue-msgid:%s] [status:ERROR/%s] [retry:%s] [prio:%s] [dlr:%s] [validity:%s] [from:%s] [to:%s] [content:%s]" % 
                          (
                           self.SMPPClientFactory.config.id,
                           msgid,
                           r.response.status,
                           will_be_retried,
                           amqpMessage.content.properties['priority'],
                           r.request.params['registered_delivery'].receipt,
                           'none' if ('headers' not in amqpMessage.content.properties or
                                      'expiration' not in amqpMessage.content.properties['headers']) 
                                  else amqpMessage.content.properties['headers']['expiration'],
                           r.request.params['source_addr'],
                           r.request.params['destination_addr'],
                           re.sub(r'[^\x20-\x7E]+','.', r.request.params['short_message'])
                           ))

        # It is a final submit_sm_resp !
        if not will_be_retried:
            # Cancel any mapped rejectTimer to this message (in case this message was rejected in the past)
            self.clearRejectTimer(msgid)
            self.log.debug("ACKing amqpMessage [%s] having routing_key [%s]", msgid, amqpMessage.routing_key)
            # ACK the message in queue, this will remove it from the queue
            yield self.ackMessage(amqpMessage)
        
        # Redis client is connected ?
        # Check DLR mappings and publish receipt for later throwing
        if self.redisClient is not None:
            # Check for HTTP DLR request from redis 'dlr' key
            # If there's a pending delivery receipt request then serve it
            # back by publishing a DLRContentForHttpapi to the messaging exchange
            pickledDlr = None
            pickledSmppsMap = None
            pickledDlr = yield self.redisClient.get("dlr:%s" % msgid)
            if pickledDlr is None:
                pickledSmppsMap = yield self.redisClient.get("smppsmap:%s" % msgid)

            if pickledDlr is not None:
                self.log.debug('There is a HTTP DLR request for msgid[%s] ...' % (msgid))

                dlr = pickle.loads(pickledDlr)
                dlr_url = dlr['url']
                dlr_level = dlr['level']
                dlr_method = dlr['method']
                dlr_expiry = dlr['expiry']

                if dlr_level in [1, 3]:
                    self.log.debug('Got DLR information for msgid[%s], url:%s, level:%s' % (msgid, 
                                                                                            dlr_url, 
                                                                                            dlr_level))
                    content = DLRContentForHttpapi(str(r.response.status), 
                                         msgid, 
                                         dlr_url, 
                                         # The dlr_url in DLRContentForHttpapi indicates the level
                                         # of the actual delivery receipt (1) and not the requested
                                         # one (maybe 1 or 3)
                                         dlr_level = 1, 
                                         method = dlr_method)
                    routing_key = 'dlr_thrower.http'
                    self.log.debug("Publishing DLRContentForHttpapi[%s] with routing_key[%s]" % (msgid, routing_key))
                    yield self.amqpBroker.publish(exchange='messaging', 
                                                  routing_key=routing_key, 
                                                  content=content)
                    
                    # DLR request is removed if:
                    # - If level 1 is requested (SMSC level only)
                    # - SubmitSmResp returned an error (no more delivery will be tracked)
                    #
                    # When level 3 is requested, the DLR will be removed when 
                    # receiving a deliver_sm (terminal receipt)
                    if dlr_level == 1 or r.response.status != CommandStatus.ESME_ROK:
                        self.log.debug('Removing DLR request for msgid[%s]' % msgid)
                        yield self.redisClient.delete("dlr:%s" % msgid)
                else:
                    self.log.debug('Terminal level receipt is requested, will not send any DLR receipt at this level.')
                
                if dlr_level in [2, 3] and r.response.status == CommandStatus.ESME_ROK:
                    # Map received submit_sm_resp's message_id to the msg for later receipt handling
                    self.log.debug('Mapping smpp msgid: %s to queue msgid: %s, expiring in %s' % (
                                    r.response.params['message_id'],
                                    msgid, 
                                    dlr_expiry
                                    )
                                   )
                    hashKey = "queue-msgid:%s" % r.response.params['message_id']
                    hashValues = {'msgid': msgid, 
                                  'connector_type': 'httpapi',}
                    self.redisClient.setex(hashKey, 
                        dlr_expiry, 
                        pickle.dumps(hashValues, self.pickleProtocol))
            elif pickledSmppsMap is not None:
                self.log.debug('There is a SMPPs mapping for msgid[%s] ...' % (msgid))

                smpps_map = pickle.loads(pickledSmppsMap)
                system_id = smpps_map['system_id']
                source_addr = smpps_map['source_addr']
                destination_addr = smpps_map['destination_addr']
                sub_date = smpps_map['sub_date']
                registered_delivery = smpps_map['registered_delivery']
                smpps_map_expiry = smpps_map['expiry']

                # Do we need to forward the receipt to the original sender ?
                if ((r.response.status == CommandStatus.ESME_ROK and 
                        str(registered_delivery.receipt) in ['SMSC_DELIVERY_RECEIPT_REQUESTED', 
                                                             'SMSC_DELIVERY_RECEIPT_REQUESTED_FOR_FAILURE'])
                    or (r.response.status != CommandStatus.ESME_ROK and 
                        str(registered_delivery.receipt) == 'SMSC_DELIVERY_RECEIPT_REQUESTED_FOR_FAILURE')):
                    self.log.debug('Got DLR information for msgid[%s], registered_deliver%s, system_id:%s' % (msgid, 
                                                                                                       registered_delivery,
                                                                                                       system_id))
                    
                    if (r.response.status != CommandStatus.ESME_ROK or 
                        (r.response.status == CommandStatus.ESME_ROK and self.config.smpp_receipt_on_success_submit_sm_resp)):
                        # Send back a receipt (by throwing deliver_sm or data_sm)
                        content = DLRContentForSmpps(str(r.response.status), 
                                                     msgid, 
                                                     system_id,
                                                     source_addr,
                                                     destination_addr,
                                                     sub_date)

                        routing_key = 'dlr_thrower.smpps'
                        self.log.debug("Publishing DLRContentForSmpps[%s] with routing_key[%s]" % (msgid, routing_key))
                        yield self.amqpBroker.publish(exchange='messaging', 
                                                      routing_key=routing_key, 
                                                      content=content)

                    if r.response.status == CommandStatus.ESME_ROK:
                        # Map received submit_sm_resp's message_id to the msg for later rceipt handling
                        self.log.debug('Mapping smpp msgid: %s to queue msgid: %s, expiring in %s' % (
                                        r.response.params['message_id'],
                                        msgid, 
                                        smpps_map_expiry
                                        )
                                       )
                        hashKey = "queue-msgid:%s" % r.response.params['message_id']
                        hashValues = {'msgid': msgid, 
                                      'connector_type': 'smpps',}
                        self.redisClient.setex(hashKey, 
                            smpps_map_expiry, 
                            pickle.dumps(hashValues, self.pickleProtocol))
        else:
            self.log.warn('No valid RC were found while checking msg[%s] !' % msgid)
        
        # Bill will be charged by bill_request.submit_sm_resp.UID queue consumer
        if total_bill_amount > 0:
            pubQueueName = 'bill_request.submit_sm_resp.%s' % submit_sm_resp_bill.user.uid
            content = SubmitSmRespBillContent(submit_sm_resp_bill.bid, submit_sm_resp_bill.user.uid, total_bill_amount)
            self.log.debug("Requesting a SubmitSmRespBillContent from a bill [bid:%s] with routing_key[%s]: %s" % 
                           (submit_sm_resp_bill.bid, pubQueueName, total_bill_amount))
            yield self.amqpBroker.publish(exchange='billing', 
                                          routing_key=pubQueueName, 
                                          content=content)
        
        if self.config.publish_submit_sm_resp:
            # Send back submit_sm_resp to submit.sm.resp.CID queue
            # There's no actual listeners on this queue, it can be used to 
            # track submit_sm_resp messages from a 3rd party app
            content = SubmitSmRespContent(r.response, msgid, pickleProtocol = self.pickleProtocol)
            self.log.debug("Sending back SubmitSmRespContent[%s] with routing_key[%s]" % 
                           (msgid, amqpMessage.content.properties['reply-to']))
            yield self.amqpBroker.publish(exchange='messaging', 
                                          routing_key=amqpMessage.content.properties['reply-to'], 
                                          content=content)

        if will_be_retried:
            defer.returnValue(False)

    def submit_sm_errback(self, error):
        """It appears that when closing a queue with the close() method it errbacks with
        a txamqp.queue.Closed exception, didnt find a clean way to stop consuming a queue
        without errbacking here so this is a workaround to make it clean, it can be considered
        as a @TODO requiring knowledge of the queue api behaviour
        """
        if error.check(Closed) is None:
            #@todo: implement this errback
            # For info, this errback is called whenever:
            # - an error has occured inside submit_sm_callback
            # - the qosTimer has been cancelled (self.clearQosTimer())
            try:
                error.raiseException()
            except Exception, e:
                self.log.error("Error in submit_sm_errback (%s): %s" % (type(e), e))
       
    @defer.inlineCallbacks     
    def concatDeliverSMs(self, HSetReturn, splitMethod, total_segments, msg_ref_num, segment_seqnum):
        hashKey = "longDeliverSm:%s" % (msg_ref_num)
        if HSetReturn != 1:
            self.log.warn('Error (%s) when trying to set hashKey %s' % (HSetReturn, hashKey))
            return

        # @TODO: longDeliverSm part expiry must be configurable
        yield self.redisClient.expire(hashKey, 300)
        
        # This is the last part
        if segment_seqnum == total_segments:
            hvals = yield self.redisClient.hvals(hashKey)
            if len(hvals) != total_segments:
                self.log.warn('Received the last part (msg_ref_num:%s) and did not find all parts in redis, data lost !' % msg_ref_num)
                return
            
            # Get PDUs
            pdus = {}
            for pickledValue in hvals:
                value = pickle.loads(pickledValue)
                
                pdus[value['segment_seqnum']] = value['pdu']

            # Build short_message
            short_message = ''
            for i in range(total_segments):
                if splitMethod == 'sar':
                    short_message += pdus[i+1].params['short_message']
                else:
                    short_message += pdus[i+1].params['short_message'][6:]
                
            # Build the final pdu and return it back to deliver_sm_event
            pdu = pdus[1] # Take the first part as a base of work
            # 1. Remove message splitting information from pdu
            if splitMethod == 'sar':
                del(pdu.params['sar_segment_seqnum'])
                del(pdu.params['sar_total_segments'])
                del(pdu.params['sar_msg_ref_num'])
            else:
                pdu.params['esm_class'] = None
            # 2. Set the new short_message
            pdu.params['short_message'] = short_message
            yield self.deliver_sm_event(smpp = None, pdu = pdu, concatenated = True)

    def code_dlr_msgid(self, pdu):
        "Code the dlr msg id accordingly to SMPPc's dlr_msg_id_bases value"

        try:
            if pdu.id == CommandId.deliver_sm:
                if self.SMPPClientFactory.config.dlr_msg_id_bases == 1:
                    ret = '%x' % int(pdu.dlr['id'])
                elif self.SMPPClientFactory.config.dlr_msg_id_bases == 2:
                    ret = int(str(pdu.dlr['id']), 16)
                else:
                    ret = pdu.dlr['id']
            else:
                # TODO: code dlr for submit_sm_resp maybe ? TBC
                ret = pdu.dlr['id']
        except Exception, e:
            self.log.error('code_dlr_msgid, cannot code msgid [%s] with dlr_msg_id_bases:%s' % (
                pdu.dlr['id'],
                self.SMPPClientFactory.config.dlr_msg_id_bases,
            ))
            self.log.error('code_dlr_msgid, error details: %s' % e)
            ret = pdu.dlr['id']

        self.log.debug('code_dlr_msgid: %s coded to %s' % (pdu.dlr['id'], ret))
        return ret
    
    @defer.inlineCallbacks
    def deliver_sm_event(self, smpp, pdu, concatenated = False):
        """This event is called whenever a deliver_sm pdu is received through a SMPPc
        It will hand the pdu to the router or a dlr thrower (depending if its a DLR or not).
        
        Note: this event will catch data_sm pdus as well
        """

        pdu.dlr =  self.SMPPOperationFactory.isDeliveryReceipt(pdu)
        content = DeliverSmContent(pdu, 
                                   self.SMPPClientFactory.config.id, 
                                   pickleProtocol = self.pickleProtocol,
                                   concatenated = concatenated)
        msgid = content.properties['message-id']
        
        if pdu.dlr is None:
            # We have a SMS-MO

            # UDH is set ?
            UDHI_INDICATOR_SET = False
            if hasattr(pdu.params['esm_class'], 'gsmFeatures'):
                for gsmFeature in pdu.params['esm_class'].gsmFeatures:
                    if str(gsmFeature) == 'UDHI_INDICATOR_SET':
                        UDHI_INDICATOR_SET = True
                        break

            splitMethod = None
            # Is it a part of a long message ?
            if 'sar_msg_ref_num' in pdu.params:
                splitMethod = 'sar'
                total_segments = pdu.params['sar_total_segments']
                segment_seqnum = pdu.params['sar_segment_seqnum']
                msg_ref_num = pdu.params['sar_msg_ref_num']
                self.log.debug('Received a part of SMS-MO [queue-msgid:%s] using SAR options: total_segments=%s, segmen_seqnum=%s, msg_ref_num=%s' % (
                    msgid, total_segments, segment_seqnum, msg_ref_num))
            elif UDHI_INDICATOR_SET and pdu.params['short_message'][:3] == '\x05\x00\x03':
                splitMethod = 'udh'
                total_segments = struct.unpack('!B', pdu.params['short_message'][4])[0]
                segment_seqnum = struct.unpack('!B', pdu.params['short_message'][5])[0]
                msg_ref_num = struct.unpack('!B', pdu.params['short_message'][3])[0]
                self.log.debug('Received a part of SMS-MO [queue-msgid:%s] using UDH options: total_segments=%s, segmen_seqnum=%s, msg_ref_num=%s' % (
                    msgid, total_segments, segment_seqnum, msg_ref_num))
            
            if splitMethod is None:
                # It's a simple short message or a part of a concatenated message
                routing_key = 'deliver.sm.%s' % self.SMPPClientFactory.config.id
                self.log.debug("Publishing DeliverSmContent[%s] with routing_key[%s]" % (msgid, routing_key))
                yield self.amqpBroker.publish(exchange='messaging', routing_key=routing_key, content=content)
                
                self.log.info("SMS-MO [cid:%s] [queue-msgid:%s] [status:%s] [prio:%s] [validity:%s] [from:%s] [to:%s] [content:%s]" % 
                          (
                           self.SMPPClientFactory.config.id,
                           msgid,
                           pdu.status,
                           pdu.params['priority_flag'],
                           pdu.params['validity_period'],
                           pdu.params['source_addr'],
                           pdu.params['destination_addr'],
                           re.sub(r'[^\x20-\x7E]+','.', pdu.params['short_message'])
                           ))
            else:
                # Long message part received
                if self.redisClient is None:
                    self.warn('No valid RC were found while receiving a part of a long DeliverSm [queue-msgid:%s], MESSAGE IS LOST !' % msgid)
                
                # Save it to redis
                hashKey = "longDeliverSm:%s" % (msg_ref_num)
                hashValues = {'pdu': pdu, 
                              'total_segments':total_segments, 
                              'msg_ref_num':msg_ref_num, 
                              'segment_seqnum':segment_seqnum}
                self.redisClient.hset(hashKey, segment_seqnum, pickle.dumps(hashValues, 
                                                                           self.pickleProtocol
                                                                           )
                                      ).addCallback(self.concatDeliverSMs, 
                                                    splitMethod, 
                                                    total_segments, 
                                                    msg_ref_num, 
                                                    segment_seqnum)
                
                self.log.info("DeliverSmContent[%s] is a part of a long message of %s parts, will be sent to queue after concatenation." % (
                    msgid, total_segments))

                # Flag it as "will_be_concatenated" and publish it to router
                routing_key = 'deliver.sm.%s' % self.SMPPClientFactory.config.id
                self.log.debug("Publishing DeliverSmContent[%s](flagged:wbc) with routing_key[%s]" % (msgid, routing_key))
                content.properties['headers']['will_be_concatenated'] = True
                yield self.amqpBroker.publish(exchange='messaging', routing_key=routing_key, content=content)
        else:
            # This is a DLR !
            # Check for DLR request
            if self.redisClient is not None:
                _coded_dlr_id = self.code_dlr_msgid(pdu)

                q = yield self.redisClient.get("queue-msgid:%s" % _coded_dlr_id)
                submit_sm_queue_id = None
                connector_type = None
                if q is not None:
                    q = pickle.loads(q)
                    submit_sm_queue_id = q['msgid']
                    connector_type = q['connector_type']


                if submit_sm_queue_id is not None and connector_type == 'httpapi':
                    pickledDlr = yield self.redisClient.get("dlr:%s" % submit_sm_queue_id)
                    
                    if pickledDlr is not None:
                        dlr = pickle.loads(pickledDlr)
                        dlr_url = dlr['url']
                        dlr_level = dlr['level']
                        dlr_method = dlr['method']

                        if dlr_level in [2, 3]:
                            self.log.debug('Got DLR information for msgid[%s], url:%s, level:%s' % 
                                           (submit_sm_queue_id, dlr_url, dlr_level))
                            content = DLRContentForHttpapi(pdu.dlr['stat'], 
                                                 submit_sm_queue_id, 
                                                 dlr_url, 
                                                 # The dlr_url in DLRContentForHttpapi indicates the level
                                                 # of the actual delivery receipt (2) and not the 
                                                 # requested one (maybe 2 or 3)
                                                 dlr_level = 2, 
                                                 id_smsc = _coded_dlr_id, 
                                                 sub = pdu.dlr['sub'], 
                                                 dlvrd = pdu.dlr['dlvrd'], 
                                                 subdate = pdu.dlr['sdate'], 
                                                 donedate = pdu.dlr['ddate'], 
                                                 err = pdu.dlr['err'], 
                                                 text = pdu.dlr['text'],
                                                 method = dlr_method)
                            routing_key = 'dlr_thrower.http'
                            self.log.debug("Publishing DLRContentForHttpapi[%s] with routing_key[%s]" % 
                                           (submit_sm_queue_id, routing_key))
                            yield self.amqpBroker.publish(exchange='messaging', 
                                                          routing_key=routing_key, 
                                                          content=content)
                            
                            self.log.debug('Removing DLR request for msgid[%s]' % submit_sm_queue_id)
                            yield self.redisClient.delete('dlr:%s' % submit_sm_queue_id)
                        else:
                            self.log.debug('SMS-C receipt is requested, will not send any DLR receipt at this level.')
                    else:
                        self.log.warn('DLR for msgid[%s] not found !' % 
                                      (submit_sm_queue_id))
                elif submit_sm_queue_id is not None and connector_type == 'smpps':
                    pickledSmppsMap = yield self.redisClient.get("smppsmap:%s" % submit_sm_queue_id)
                    
                    if pickledSmppsMap is not None:
                        smpps_map = pickle.loads(pickledSmppsMap)
                        system_id = smpps_map['system_id']
                        source_addr = smpps_map['source_addr']
                        destination_addr = smpps_map['destination_addr']
                        sub_date = smpps_map['sub_date']
                        registered_delivery = smpps_map['registered_delivery']
                        smpps_map_expiry = smpps_map['expiry']

                        success_states = ['ACCEPTD', 'DELIVRD']
                        final_states = ['DELIVRD', 'EXPIRED', 'DELETED', 'UNDELIV', 'REJECTD']
                        # Do we need to forward the receipt to the original sender ?
                        if ((pdu.dlr['stat'] in success_states and 
                                str(registered_delivery.receipt) == 'SMSC_DELIVERY_RECEIPT_REQUESTED')
                            or (pdu.dlr['stat'] not in success_states and 
                                str(registered_delivery.receipt) in ['SMSC_DELIVERY_RECEIPT_REQUESTED', 
                                                                     'SMSC_DELIVERY_RECEIPT_REQUESTED_FOR_FAILURE'])):

                            self.log.debug('Got DLR information for msgid[%s], registered_deliver%s, system_id:%s' % (submit_sm_queue_id, 
                                                                                                                      registered_delivery,
                                                                                                                      system_id))
                            content = DLRContentForSmpps(pdu.dlr['stat'], 
                                                         submit_sm_queue_id, 
                                                         system_id,
                                                         source_addr,
                                                         destination_addr,
                                                         sub_date)

                            routing_key = 'dlr_thrower.smpps'
                            self.log.debug("Publishing DLRContentForSmpps[%s] with routing_key[%s]" % (submit_sm_queue_id, routing_key))
                            yield self.amqpBroker.publish(exchange='messaging', 
                                                          routing_key=routing_key, 
                                                          content=content)

                            if pdu.dlr['stat'] in final_states:
                                self.log.debug('Removing SMPPs map for msgid[%s]' % submit_sm_queue_id)
                                yield self.redisClient.delete('smppsmap:%s' % submit_sm_queue_id)
                else:
                    self.log.warn('Got a DLR for an unknown message id: %s (coded:%s)' % (pdu.dlr['id'], _coded_dlr_id))
            else:
                self.log.warn('DLR for msgid[%s] is not checked, no valid RC were found' % msgid)

            self.log.info("DLR [cid:%s] [smpp-msgid:%s] [status:%s] [submit date:%s] [done date:%s] [sub/dlvrd messages:%s/%s] [err:%s] [content:%s]" % 
                      (
                       self.SMPPClientFactory.config.id,
                       _coded_dlr_id,
                       pdu.dlr['stat'],
                       pdu.dlr['sdate'],
                       pdu.dlr['ddate'],
                       pdu.dlr['sub'],
                       pdu.dlr['dlvrd'],
                       pdu.dlr['err'],
                       pdu.dlr['text'],
                       ))