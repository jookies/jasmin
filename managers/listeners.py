import logging
import pickle
from datetime import datetime, timedelta
from dateutil import parser
from twisted.internet import defer
from smpp.pdu.operations import SubmitSM
from txamqp.queue import Closed
from twisted.internet import reactor, task
from jasmin.managers.content import SubmitSmRespContent, DeliverSmContent
from jasmin.managers.configs import SMPPClientPBConfig
from jasmin.protocols.smpp.operations import SMPPOperationFactory

LOG_CATEGORY = "jasmin-sm-listener"

class SMPPClientSMListener:
    def __init__(self, SMPPClientSMListenerConfig, SMPPClientFactory, amqpBroker, submit_sm_q):
        self.config = SMPPClientSMListenerConfig
        self.SMPPClientFactory = SMPPClientFactory
        self.SMPPOperationFactory = SMPPOperationFactory(self.SMPPClientFactory.config)
        self.amqpBroker = amqpBroker
        self.submit_sm_q = submit_sm_q
        self.qos_last_submit_sm_at = None
        self.rejectTimers = {}
        self.qosTimer = None
        
        # Set pickleProtocol
        self.pickleProtocol = SMPPClientPBConfig(self.config.config_file).pickle_protocol

        # Set up a dedicated logger
        self.log = logging.getLogger(LOG_CATEGORY)
        self.log.setLevel(self.config.log_level)
        handler = logging.FileHandler(filename=self.config.log_file)
        formatter = logging.Formatter(self.config.log_format, self.config.log_date_format)
        handler.setFormatter(formatter)
        self.log.addHandler(handler)
        
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
        if self.qosTimer is not None and self.qosTimer.called == False:
            self.qosTimer.cancel()
            self.qosTimer = None
        
    def clearAllTimers(self):
        self.clearQosTimer()
        self.clearRejectTimers()
        
    def rejectAndRequeueMessage(self, message, delay = True):
        msgid = message.content.properties['message-id']
        
        if delay:
            self.log.debug("Requeuing SubmitSmPDU[%s] in %s seconds" % (msgid, self.SMPPClientFactory.config.requeue_delay))
            t = reactor.callLater(self.SMPPClientFactory.config.requeue_delay, self.amqpBroker.chan.basic_reject, delivery_tag=message.delivery_tag, requeue=1)

            self.rejectTimers[msgid] = t
            return t
        else:
            self.log.debug("Requeuing SubmitSmPDU[%s] without delay" % msgid)
            return self.amqpBroker.chan.basic_reject(delivery_tag=message.delivery_tag, requeue=1)
    def rejectMessage(self, message):
        return self.amqpBroker.chan.basic_reject(delivery_tag=message.delivery_tag, requeue=0)
    def ackMessage(self, message):
        return self.amqpBroker.chan.basic_ack(message.delivery_tag)
    
    @defer.inlineCallbacks
    def submit_sm_callback(self, message):
        """This callback is a queue listener
        it is called whenever a message was consumed from queue
        c.f. test_amqp.ConsumeTestCase for use cases
        """
        msgid = message.content.properties['message-id']
        SubmitSmPDU = pickle.loads(message.content.body)
        self.log.debug("Callbacked a submit_sm with a SubmitSmPDU[%s] (?): %s" % (msgid, SubmitSmPDU))

        if self.qos_last_submit_sm_at is None:
            self.qos_last_submit_sm_at = datetime(1970, 1, 1)    
            
        if self.SMPPClientFactory.config.submit_sm_throughput > 0:
            # QoS throttling
            qos_throughput_second = 1 / float(self.SMPPClientFactory.config.submit_sm_throughput)
            qos_throughput_ysecond_td = timedelta( microseconds = qos_throughput_second * 1000000)
            qos_delay = datetime.now() - self.qos_last_submit_sm_at
            if qos_delay < qos_throughput_ysecond_td:
                #qos_slow_down = 1 / float(((qos_throughput_ysecond_td - qos_delay).microseconds / 1000))
                qos_slow_down = float((qos_throughput_ysecond_td - qos_delay).microseconds) / 1000000
                # We're faster than submit_sm_throughput, slow down before taking a new message from the queue
                self.log.debug("QoS: submit_sm_callback is faster (%s) than fixed throughput (%s), slowing down by %s seconds (message will be requeued)." % (
                                qos_delay,
                                qos_throughput_ysecond_td,
                                qos_slow_down
                                ))

                # Relaunch queue callbacking after qos_slow_down seconds
                self.qosTimer = task.deferLater(reactor, qos_slow_down, self.submit_sm_q.get)
                self.qosTimer.addCallback(self.submit_sm_callback).addErrback(self.submit_sm_errback)
                # Requeue the message
                yield self.rejectAndRequeueMessage(message, delay = False)
                defer.returnValue(False)
            else:
                # We're slower than submit_sm_throughput, let's take a new message from the queue NOW !
                self.qos_last_submit_sm_at = datetime.now()
                self.submit_sm_q.get().addCallback(self.submit_sm_callback).addErrback(self.submit_sm_errback)
            
            self.qos_last_submit_sm_at = datetime.now()
        else:
            # No throttling
            self.submit_sm_q.get().addCallback(self.submit_sm_callback).addErrback(self.submit_sm_errback)
        
        # Verify if message is a SubmitSm PDU
        if isinstance(SubmitSmPDU, SubmitSM) == False:
            self.log.error("Received an object[%s] which is not an instance of SubmitSm" % msgid)
            self.log.info("Discarding this unkown object from the queue")
            yield self.rejectMessage(message)
            defer.returnValue(False)
        # If the message has expired in the queue
        if 'expiration' in message.content.properties:
            expiration_datetime = parser.parse(message.content.properties['expiration'])
            if expiration_datetime < datetime.now():
                self.log.info("Discarding expired message[%s]: expiration is %s" % (msgid, expiration_datetime))
                yield self.rejectMessage(message)
                defer.returnValue(False)
        # SMPP Client should be already connected
        if self.SMPPClientFactory.smpp == None:
            self.log.error("SMPP Client is not connected")
            self.log.info("Requeuing SubmitSmPDU[%s]" % msgid)
            yield self.rejectAndRequeueMessage(message)
            defer.returnValue(False)
        # SMPP Client should be already bound as transceiver or transmitter
        if self.SMPPClientFactory.smpp.isBound() == False:
            self.log.error("SMPP Client is not bound")
            self.log.info("Requeuing SubmitSmPDU[%s]" % msgid)
            yield self.rejectAndRequeueMessage(message)
            defer.returnValue(False)

        self.log.debug("Sending SubmitSmPDU through SMPPClientFactory")
        yield self.SMPPClientFactory.smpp.sendDataRequest(SubmitSmPDU).addCallback(self.submit_sm_resp_callback, message)

    @defer.inlineCallbacks
    def submit_sm_resp_callback(self, r, amqpMessage):
        msgid = amqpMessage.content.properties['message-id']
        
        self.log.info("SMS-MT [cid:%s] [queue-msgid:%s] [smpp-msgid:%s] [status:%s] [prio:%s] [validity:%s] [from:%s] [to:%s] [content:%s]" % 
                      (
                       self.SMPPClientFactory.config.id,
                       msgid,
                       r.response.params['message_id'],
                       r.response.status,
                       amqpMessage.content.properties['priority'],
                       'none' if 'expiration' not in amqpMessage.content.properties else amqpMessage.content.properties['expiration'],
                       r.request.params['source_addr'],
                       r.request.params['destination_addr'],
                       r.request.params['short_message']
                       ))

        # Cancel any mapped rejectTimer to this message (in case this message was rejected in the past)
        self.clearRejectTimer(msgid)

        self.log.debug("ACKing amqpMessage[%s]" % msgid)
        # ACK the message in queue, this will remove it from the queue
        yield self.ackMessage(amqpMessage)
            
        # Send back submit_sm_resp to submit.sm.resp.CID queue
        content = SubmitSmRespContent(r.response, msgid, pickleProtocol = self.pickleProtocol)
        self.log.debug("Sending back SubmitSmRespContent[%s] with routing_key[%s]" % (msgid, amqpMessage.content.properties['reply-to']))
        yield self.amqpBroker.publish(routing_key=amqpMessage.content.properties['reply-to'], content=content)

    def submit_sm_errback(self, error):
        """It appears that when closing a queue with the close() method it errbacks with
        a txamqp.queue.Closed exception, didnt find a clean way to stop consuming a queue
        without errbacking here so this is a workaround to make it clean, it can be considered
        as a @TODO requiring knowledge of the queue api behaviour
        """
        if error.check(Closed) == None:
            #@todo: implement this errback
            # For info, this errback is called whenever:
            # - an error has occured inside submit_sm_callback
            # - the qosTimer has been cancelled (self.clearQosTimer())
            print "Error in submit_sm_errback: %s" % (error)
            #return error
    
    @defer.inlineCallbacks        
    def deliver_sm_callback(self, smpp, pdu):
        isDlr =  self.SMPPOperationFactory.isDeliveryReceipt(pdu)
        
        if isDlr is None:
            destination_queue = 'deliver.sm.%s' % self.SMPPClientFactory.config.id
            content = DeliverSmContent(pdu, pickleProtocol = self.pickleProtocol)
            self.log.info("SMS-MO [cid:%s] [queue-msgid:%s] [status:%s] [prio:%s] [validity:%s] [from:%s] [to:%s] [content:%s]" % 
                      (
                       self.SMPPClientFactory.config.id,
                       content.properties['message-id'],
                       pdu.status,
                       pdu.params['priority_flag'],
                       pdu.params['validity_period'],
                       pdu.params['source_addr'],
                       pdu.params['destination_addr'],
                       pdu.params['short_message']
                       ))
        else:
            destination_queue = 'dlr.%s' % self.SMPPClientFactory.config.id
            pdu.dlr = isDlr
            content = DeliverSmContent(pdu, pickleProtocol = self.pickleProtocol)
            self.log.info("DLR [cid:%s] [queue-msgid:%s] [smpp-msgid:%s] [stat:%s] [sdate:%s] [ddate:%s] [sub:%s] [dlvrd:%s] [err:%s] [content:%s]" % 
                      (
                       self.SMPPClientFactory.config.id,
                       content.properties['message-id'],
                       isDlr['id'],
                       isDlr['stat'],
                       isDlr['sdate'],
                       isDlr['ddate'],
                       isDlr['sub'],
                       isDlr['dlvrd'],
                       isDlr['err'],
                       isDlr['text'],
                       ))

        # Send back submit_sm_resp to submit.sm.resp.CID queue
        self.log.debug("Sending DeliverSmContent[%s] with routing_key[%s]" % (content.properties['message-id'], destination_queue))
        yield self.amqpBroker.publish(routing_key=destination_queue, content=content)