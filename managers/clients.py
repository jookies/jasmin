# Copyright 2012 Fourat Zouari <fourat@gmail.com>
# See LICENSE for details.

import logging
import pickle
from twisted.spread import pb
from twisted.internet import defer
from jasmin.protocols.smpp.services import SMPPClientService
from jasmin.managers.listeners import SMPPClientSMListener
from jasmin.managers.configs import SMPPClientSMListenerConfig
from jasmin.managers.content import SubmitSmContent

LOG_CATEGORY = "jasmin-pb-client-mgmt"

class SMPPClientManagerPB(pb.Root):
    def __init__(self):
        self.rc = None
        self.amqpBroker = None
        self.connectors = []
        self.declared_queues = []
        self.pickleProtocol = 2
        
    def setConfig(self, SMPPClientPBConfig):
        self.config = SMPPClientPBConfig

        # Set up a dedicated logger
        self.log = logging.getLogger(LOG_CATEGORY)
        self.log.setLevel(self.config.log_level)
        handler = logging.FileHandler(filename=self.config.log_file)
        formatter = logging.Formatter(self.config.log_format, self.config.log_date_format)
        handler.setFormatter(formatter)
        self.log.addHandler(handler)
        
        # Set pickleProtocol
        self.pickleProtocol = self.config.pickle_protocol
        
        self.log.info('SMPP Client manager configured and ready.')
        
    def addAmqpBroker(self, amqpBroker):
        self.amqpBroker = amqpBroker

        self.log.info('Added amqpBroker to SMPPClientManagerPB')
        
    def addRedisClient(self, rc):
        self.rc = rc

        self.log.info('Added Redis Client to SMPPClientManagerPB')

    def getConnector(self, cid):
        for c in self.connectors:
            if c['id'] == cid:
                self.log.debug('getConnector [%s] returned a connector', cid)
                return c
        
        self.log.debug('getConnector [%s] returned None', cid)
        return None
    
    def getConnectorDetails(self, cid):
        c = self.getConnector(cid)
        if c == None:
            self.log.debug('getConnectorDetails [%s] returned None', cid)
            return None
        
        details = {}
        details['id'] = c['id']
        details['session_state'] = str(c['service'].SMPPClientFactory.getSessionState())
        details['service_status'] = c['service'].running
        details['start_count'] = c['service'].startCounter
        details['stop_count'] = c['service'].stopCounter
        
        self.log.debug('getConnectorDetails [%s] returned details', cid)
        return details
    
    def delConnector(self, cid):
        for i in range (len(self.connectors)):
            if self.connectors[i]['id'] == cid:
                del self.connectors[i]
                self.log.debug('Deleted connector [%s].', cid)
                return True
        
        self.log.debug('Deleting connector [%s] failed.', cid)
        return False
    
    @defer.inlineCallbacks
    def remote_connector_add(self, ClientConfig):
        """This will add a new connector to self.connectors
        and get a listener on submit.sm.%cid queue, this listener will be
        started and stopped when the connector will get started and stopped
        through this API"""
        
        c = pickle.loads(ClientConfig)

        self.log.debug('Adding a new connector %s', c.id)
        
        if self.getConnector(c.id) != None:
            self.log.error('Trying to add a new connector with an already existant cid: %s', c.id)
            defer.returnValue(False)
        if self.amqpBroker == None:
            self.log.error('AMQP Broker is not added')
            defer.returnValue(False)
        if self.amqpBroker.connected == False:
            self.log.error('AMQP Broker channel is not yet ready')
            defer.returnValue(False)

        # Declare queues
        # First declare the messaging exchange
        yield self.amqpBroker.chan.exchange_declare(exchange='messaging', type='topic')
        # submit.sm queue declaration and binding
        routingKey_submit_sm = 'submit.sm.%s' % c.id
        self.log.info('Declaring submit_sm queue to listen to: %s', routingKey_submit_sm)
        yield self.amqpBroker.named_queue_declare(queue=routingKey_submit_sm)
        yield self.amqpBroker.chan.queue_bind(queue=routingKey_submit_sm, exchange="messaging", routing_key=routingKey_submit_sm)
        # submit.sm.resp queue declaration and binding
        routingKey_submit_sm_resp = 'submit.sm.resp.%s' % c.id
        self.log.info('Declaring submit_sm_resp queue to publish to: %s', routingKey_submit_sm_resp)
        yield self.amqpBroker.named_queue_declare(queue=routingKey_submit_sm_resp)
        yield self.amqpBroker.chan.queue_bind(queue=routingKey_submit_sm_resp, exchange="messaging", routing_key=routingKey_submit_sm_resp)
        # deliver.sm queue declaration and binding
        routingKey_deliver_sm = 'deliver.sm.%s' % c.id
        self.log.info('Declaring deliver_sm queue to publish to: %s', routingKey_deliver_sm)
        yield self.amqpBroker.named_queue_declare(queue=routingKey_deliver_sm)
        yield self.amqpBroker.chan.queue_bind(queue=routingKey_deliver_sm, exchange="messaging", routing_key=routingKey_deliver_sm)
        # dlr queue declaration and binding
        routingKey_dlr = 'dlr.%s' % c.id
        self.log.info('Declaring dlr queue to publish to: %s', routingKey_dlr)
        yield self.amqpBroker.named_queue_declare(queue=routingKey_dlr)
        yield self.amqpBroker.chan.queue_bind(queue=routingKey_dlr, exchange="messaging", routing_key=routingKey_dlr)
                
        # Subscribe to submit.sm.%cid queue
        # check jasmin.queues.test.test_amqp.PublishConsumeTestCase.test_simple_publish_consume_by_topic
        consumerTag = 'SMPPClientFactory.%s' % c.id
        yield self.amqpBroker.chan.basic_consume(queue=routingKey_submit_sm, no_ack=False, consumer_tag=consumerTag)
        submit_sm_q = yield self.amqpBroker.client.queue(consumerTag)
        self.log.info('SMPPClientFactory.%s is consuming from routing key: %s', c.id, routingKey_submit_sm)
        
        # Instanciate smpp client service manager
        serviceManager = SMPPClientService(c, self.config)
        
        # Instanciate a SM listener
        print self.rc
        smListener = SMPPClientSMListener(SMPPClientSMListenerConfig(self.config.config_file), serviceManager.SMPPClientFactory, self.amqpBroker, self.rc, submit_sm_q)
        
        # Deliver_sm are sent to smListener's deliver_sm callback method
        serviceManager.SMPPClientFactory.msgHandler = smListener.deliver_sm_callback

        self.connectors.append({'id':c.id,
                                'config':c,
                                'service':serviceManager,
                                'submit_sm_q': submit_sm_q,
                                'sm_listener': smListener
                        })
       
        self.log.info('Added a new connector: %s', c.id)
        defer.returnValue(True)
    
    @defer.inlineCallbacks
    def remote_connector_remove(self, cid):
        """This will stop and remove a connector from self.connectors"""
        
        self.log.debug('Removing connector [%s]', cid)

        connector = self.getConnector(cid)
        if connector == None:
            self.log.error('Trying to remove a connector with an unknown cid: %s', cid)
            defer.returnValue(False)
        if connector['service'].running == 1:
            self.log.debug('Stopping service for connector [%s] before removing it', cid)
            connector['service'].stopService()
            
        # Stop the queue consumer
        self.log.debug('Stopping submit_sm_q consumer in connecotr [%s]', cid)
        yield connector['submit_sm_q'].close()
        
        if self.delConnector(cid):
            self.log.info('Removed connector [%s]', cid)
            defer.returnValue(True)
        else:
            self.log.error('Error removing connector [%s], cid not found', cid)
            defer.returnValue(False)
            
        defer.returnValue(True)
    
    def remote_connector_list(self):
        """This will return only connector IDs since returning an already copyed SMPPClientConfig
        would be a headache"""
        
        self.log.debug('Connector list requested, returning %s', self.connectors)
        
        connectorList = []
        for connector in self.connectors:
            c = self.getConnectorDetails(connector['id'])
            
            connectorList.append(c)

        self.log.info('Returning a list of %s connectors', len(connectorList))
        return connectorList
    
    def remote_connector_start(self, cid):
        """This will start a service by adding IService to IServiceCollection
        """
        
        self.log.debug('Starting connector [%s]', cid)

        connector = self.getConnector(cid)
        if connector == None:
            self.log.error('Trying to start a connector with an unknown cid: %s', cid)
            return False
        if connector['service'].running == 1:
            self.log.error('Connector [%s] is already running.', cid)
            return False
        
        connector['service'].startService()
        
        # Start the queue consumer
        self.log.debug('Starting submit_sm_q consumer in connector [%s]', cid)
        d = connector['submit_sm_q'].get()
        d.addCallback(
                    connector['sm_listener'].submit_sm_callback
                ).addErrback(
                    connector['sm_listener'].submit_sm_errback
                )

        self.log.info('Started connector [%s]', cid)
        return True
    
    @defer.inlineCallbacks
    def remote_connector_stop(self, cid, delQueues = False):
        """This will stop a service by detaching IService to IServiceCollection
        """

        self.log.debug('Stopping connector [%s]', cid)

        connector = self.getConnector(cid)
        if connector == None:
            self.log.error('Trying to stop a connector with an unknown cid: %s', cid)
            defer.returnValue(False)
        if connector['service'].running == 0:
            self.log.error('Connector [%s] is already stopped.', cid)
            defer.returnValue(False)
            
        if delQueues:
            submitSmQueueName = 'submit.sm.%s' % cid
            self.log.debug('Deleting queue [%s]', submitSmQueueName)
            yield self.amqpBroker.chan.queue_delete(queue=submitSmQueueName)

        # Stop timers in message listeners
        self.log.debug('Clearing sm_listener timers in connecotr [%s]', cid)
        yield connector['sm_listener'].clearAllTimers()
        
        # Stop the queue consumer
        self.log.debug('Stopping submit_sm_q consumer in connecotr [%s]', cid)
        yield connector['submit_sm_q'].close()
        
        # Stop SMPP connector
        connector['service'].stopService()
        
        self.log.info('Stopped connector [%s]', cid)
        defer.returnValue(True)
    
    def remote_connector_stopall(self, delQueues = False):
        """This will stop all services by detaching IService to IServiceCollection
        """

        self.log.debug('Stopping all connectors')

        for connector in self.connectors:
            self.remote_connector_stop(connector['id'], delQueues)

        return True
    
    def remote_service_status(self, cid):
        """This will return the IService running status
        """

        self.log.debug('Requested service status %s', cid)

        connector = self.getConnector(cid)
        if connector == None:
            self.log.error('Trying to get service status of a connector with an unknown cid: %s', cid)
            return False
        
        service_status = connector['service'].running
        self.log.info('Connector [%s] service status is: %s', cid, str(service_status))

        return service_status
        
    def remote_session_state(self, cid):
        """This will return the session state of a client connector
        """

        self.log.debug('Requested session state for connector [%s]', cid)

        connector = self.getConnector(cid)
        if connector == None:
            self.log.error('Trying to get session state of a connector with an unknown cid: %s', cid)
            return False
        
        session_state = connector['service'].SMPPClientFactory.getSessionState()
        self.log.info('Connector [%s] session state is: %s', cid, str(session_state))

        if session_state == None:
            return None
        else:
            # returning Enum would raise this on the client side:
            # Unpersistable data: instance of class enum.EnumValue deemed insecure
            # So we just return back the string of it
            return str(session_state)
        
    def remote_connector_details(self, cid):
        """This will return the connector details
        """

        self.log.debug('Requested details for connector [%s]', cid)

        connector = self.getConnector(cid)
        if connector == None:
            self.log.error('Trying to get details of a connector with an unknown cid: %s', cid)
            return False
        
        return self.getConnectorDetails(cid)
    
    def remote_connector_config(self, cid):
        """This will return the connector SMPPClientConfig object
        """

        self.log.debug('Requested config for connector [%s]', cid)

        connector = self.getConnector(cid)
        if connector == None:
            self.log.error('Trying to get config of a connector with an unknown cid: %s', cid)
            return False
        
        return pickle.dumps(connector['config'], self.pickleProtocol)
    
    @defer.inlineCallbacks
    def setKeyExpiry(self, callbackArg, key, expiry):
        yield self.rc.expire(key, expiry)
    
    @defer.inlineCallbacks
    def remote_submit_sm(self, cid, SubmitSmPDU, priority = 1, validity_period = None, pickled = True, 
                         dlr_url = None, dlr_level = 1, dlr_method = 'POST'):
        """This will enqueue a submit_sm to a connector
        """

        self.log.debug('Enqueued a SUBMIT_SM to connector [%s]', cid)

        connector = self.getConnector(cid)
        if connector == None:
            self.log.error('Trying to enqueue a SUBMIT_SM to a connector with an unknown cid: %s', cid)
            defer.returnValue(False)
        if self.amqpBroker == None:
            self.log.error('AMQP Broker is not added')
            defer.returnValue(False)
        if self.amqpBroker == None:
            self.log.error('Trying to enqueue a SUBMIT_SM when no broker were added')
            defer.returnValue(False)
        
        # TODO: Future implementation, submitting a sm to a disconnected broker would be possible
        #    through a local in memory queue
        if self.amqpBroker.connected == False:
            self.log.error('AMQP Broker is not connected')
            defer.returnValue(False)
        
        # Define the destination and response queue names
        pubQueueName = "submit.sm.%s" % cid
        responseQueueName = "submit.sm.resp.%s" % cid
        
        # Pickle SubmitSmPDU if it's not pickled
        if not pickled:
            SubmitSmPDU = pickle.dumps(SubmitSmPDU, self.pickleProtocol)
        
        # Publishing a pickled PDU
        self.log.info('Publishing SubmitSmPDU with routing_key=%s, priority=%s' % (pubQueueName, priority))
        c = SubmitSmContent(SubmitSmPDU, responseQueueName, priority, validity_period)
        yield self.amqpBroker.publish(exchange='messaging', routing_key=pubQueueName, content=c)
        
        # Enqueue DLR request
        if dlr_url is not None:
            if self.rc is None or str(self.rc) == '<Redis Connection: Not connected>':
                self.log.warn("DLR is not enqueued for SubmitSmPDU [msgid:%s], RC is not connected." % c.properties['message-id'])
            else:
                self.log.debug('Setting DLR url (%s) and level (%s) for message id:%s, expiring in %s' % (dlr_url, dlr_level, c.properties['message-id'], connector['config'].dlr_expiry))
                # Set values and callback expiration setting
                hashKey = "dlr:%s" % (c.properties['message-id'])
                hashValues = {'url': dlr_url, 
                              'level':dlr_level, 
                              'method':dlr_method, 
                              'expiry':connector['config'].dlr_expiry}
                self.rc.set(hashKey, pickle.dumps(hashValues, self.pickleProtocol)).addCallback(
                            self.setKeyExpiry, hashKey, connector['config'].dlr_expiry)
        
        defer.returnValue(c.properties['message-id'])