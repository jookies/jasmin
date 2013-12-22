# Copyright 2012 Fourat Zouari <fourat@gmail.com>
# See LICENSE for details.

import logging
import pickle
import uuid
from twisted.spread import pb
from twisted.internet import defer
from txamqp.queue import Closed
from jasmin.routing.content import RoutedDeliverSmContent
from jasmin.routing.RoutingTables import MORoutingTable, MTRoutingTable
from jasmin.routing.Routables import RoutableDeliverSm
from jasmin.routing.jasminApi import Connector

LOG_CATEGORY = "jasmin-router"

class RouterPB(pb.Root):
    def setConfig(self, RouterPBConfig):
        self.config = RouterPBConfig

        # Set up a dedicated logger
        self.log = logging.getLogger(LOG_CATEGORY)
        self.log.setLevel(self.config.log_level)
        handler = logging.FileHandler(filename=self.config.log_file)
        formatter = logging.Formatter(self.config.log_format, self.config.log_date_format)
        handler.setFormatter(formatter)
        self.log.addHandler(handler)
        
        # Set pickleProtocol
        self.pickleProtocol = self.config.pickle_protocol
        
        # Init routing-related objects
        self.mo_routing_table = MORoutingTable()
        self.mt_routing_table = MTRoutingTable()
        self.users = []
        
        self.log.info('Router configured and ready.')
    
    @defer.inlineCallbacks
    def addAmqpBroker(self, amqpBroker):
        self.amqpBroker = amqpBroker
        self.log.info('Added amqpBroker to RouterPB')
        
        if self.amqpBroker.connected == False:
            self.log.warn('AMQP Broker channel is not yet ready, waiting for it to become ready.')
            yield self.amqpBroker.channelReady
            self.log.info("AMQP Broker channel is ready now, let's go !")
         
        # Subscribe to deliver.sm.* queues
        yield self.amqpBroker.chan.exchange_declare(exchange='messaging', type='topic')
        consumerTag = 'RouterPB.%s' % str(uuid.uuid4())
        routingKey = 'deliver.sm.*'
        queueName = 'RouterPB_deliver_sm_all' # A local queue to RouterPB
        yield self.amqpBroker.named_queue_declare(queue=queueName)
        yield self.amqpBroker.chan.queue_bind(queue=queueName, exchange="messaging", routing_key=routingKey)
        yield self.amqpBroker.chan.basic_consume(queue=queueName, no_ack=False, consumer_tag=consumerTag)
        self.deliver_sm_q = yield self.amqpBroker.client.queue(consumerTag)
        self.deliver_sm_q.get().addCallback(self.deliver_sm_callback).addErrback(self.deliver_sm_errback)
        self.log.info('RouterPB is consuming from routing key: %s', routingKey)
        
    def rejectAndRequeueMessage(self, message):
        msgid = message.content.properties['message-id']
        
        self.log.debug("Requeuing DeliverSmPDU[%s] without delay" % msgid)
        return self.amqpBroker.chan.basic_reject(delivery_tag=message.delivery_tag, requeue=1)
    def rejectMessage(self, message):
        return self.amqpBroker.chan.basic_reject(delivery_tag=message.delivery_tag, requeue=0)
    def ackMessage(self, message):
        return self.amqpBroker.chan.basic_ack(message.delivery_tag)

    @defer.inlineCallbacks
    def deliver_sm_callback(self, message):
        """This callback is a queue listener
        It will only decide where to send the input message and republish it to the routedConnector
        The consumer will execute the remaining job of final delivery 
        c.f. test_router.DeliverSmDeliveryTestCases for use cases
        """
        msgid = message.content.properties['message-id']
        scid = message.content.properties['headers']['connector-id']
        connector = Connector(scid)
        DeliverSmPDU = pickle.loads(message.content.body)
        self.log.debug("Callbacked a deliver_sm with a DeliverSmPDU[%s] (?): %s" % (msgid, DeliverSmPDU))

        # @todo: Implement MO throttling here, same as in jasmin.managers.listeners.SMPPClientSMListener.submit_sm_callback
        self.deliver_sm_q.get().addCallback(self.deliver_sm_callback).addErrback(self.deliver_sm_errback)
        
        # Routing
        routable = RoutableDeliverSm(DeliverSmPDU, connector)
        routedConnector = self.getMORoutingTable().getConnectorFor(routable)
        if routedConnector is None:
            self.log.debug("No route matched this DeliverSmPDU with scid:%s and msgid:%s" % (scid, msgid))
            yield self.rejectMessage(message)
        else:
            self.log.debug("Connector '%s' is set to be a route for this DeliverSmPDU" % routedConnector.cid)
            yield self.ackMessage(message)
            
            # Enqueue DeliverSm for delivery through publishing it to deliver_sm_thrower.(type)
            content = RoutedDeliverSmContent(DeliverSmPDU, msgid, scid, routedConnector)
            self.log.debug("Publishing RoutedDeliverSmContent [msgid:%s] in deliver_sm_thrower.%s with [dcid:%s]" % (msgid, routedConnector.type, routedConnector.cid))
            yield self.amqpBroker.publish(exchange='messaging', routing_key='deliver_sm_thrower.%s' % routedConnector.type, content=content)
    
    def deliver_sm_errback(self, error):
        """It appears that when closing a queue with the close() method it errbacks with
        a txamqp.queue.Closed exception, didnt find a clean way to stop consuming a queue
        without errbacking here so this is a workaround to make it clean, it can be considered
        as a @TODO requiring knowledge of the queue api behaviour
        """
        if error.check(Closed) == None:
            #@todo: implement this errback
            # For info, this errback is called whenever:
            # - an error has occured inside deliver_sm_callback
            self.log.error("Error in deliver_sm_errback: %s" % error)

    def getMORoutingTable(self):
        return self.mo_routing_table
    def getMTRoutingTable(self):
        return self.mt_routing_table
    def authenticateUser(self, username, password, pickled = False):
        for _user in self.users:
            if _user.username == username and _user.password == password:
                if pickled:
                    return pickle.dumps(_user, self.pickleProtocol)
                else:
                    return _user
        
        return None
    
    def remote_user_add(self, user):
        user = pickle.loads(user)
        self.log.debug('Adding a User: %s' % user)
        self.log.info('Adding a User (id:%s)' % user.uid)

        # Replace existant users
        for _user in self.users:
            if user.uid == _user.uid or user.username == _user.username:
                self.users.remove(_user)
                break 

        return self.users.append(user)
    
    def remote_user_authenticate(self, username, password):
        self.log.debug('Authenticating with username:%s and password:%s' % (username, password))
        self.log.info('Authentication request with username:%s' % username)

        return self.authenticateUser(username, password, True)
    
    def remote_user_remove(self, user):
        user = pickle.loads(user)
        self.log.debug('Removing a User: %s' % user)
        self.log.info('Removing a User (id:%s)' % user.uid)

        # Remove user
        for _user in self.users:
            if user.uid == _user.uid or user.username == _user.username:
                self.users.remove(_user)
                break 

    def remote_user_remove_all(self):
        self.log.info('Removing all users')
        
        self.users = []
        
        return True

    def remote_user_get_all(self):
        self.log.info('Getting all users')
        self.log.debug('Getting all users: %s' % self.users)

        return pickle.dumps(self.users)
    
    def remote_mtroute_add(self, route, order):
        route = pickle.loads(route)
        self.log.debug('Adding a MT Route, order = %s, route = %s' % (order, route))
        self.log.info('Adding a MT Route with order %s', order)

        return self.mt_routing_table.add(route, order)
    
    def remote_moroute_add(self, route, order):
        route = pickle.loads(route)
        self.log.debug('Adding a MO Route, order = %s, route = %s' % (order, route))
        self.log.info('Adding a MO Route with order %s', order)

        return self.mo_routing_table.add(route, order)
    
    def remote_mtroute_flush(self):
        self.log.info('Flushing MT Routing table')

        return self.mt_routing_table.flush()
    
    def remote_moroute_flush(self):
        self.log.info('Flushing MO Routing table')

        return self.mo_routing_table.flush()
    
    def remote_mtroute_get_all(self):
        self.log.info('Getting MT Routing table')
        
        routes = self.mt_routing_table.getAll()
        self.log.debug('Getting MT Routing table: %s', routes)

        return pickle.dumps(routes, self.pickleProtocol)
    
    def remote_moroute_get_all(self):
        self.log.info('Getting MO Routing table')

        routes = self.mo_routing_table.getAll()
        self.log.debug('Getting MO Routing table: %s', routes)

        return pickle.dumps(routes, self.pickleProtocol)