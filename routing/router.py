# Copyright 2012 Fourat Zouari <fourat@gmail.com>
# See LICENSE for details.

import time
import logging
import pickle
import uuid
from twisted.spread import pb
from twisted.internet import defer
from txamqp.queue import Closed
from content import RoutedDeliverSmContent
from RoutingTables import MORoutingTable, MTRoutingTable, InvalidRoutingTableParameterError
from Routables import RoutableDeliverSm
from jasminApi import Connector
from copy import copy

LOG_CATEGORY = "jasmin-router"

class RouterPB(pb.Root):
    def setConfig(self, RouterPBConfig):
        self.config = RouterPBConfig

        # Set up a dedicated logger
        self.log = logging.getLogger(LOG_CATEGORY)
        if len(self.log.handlers) != 1:
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
        self.groups = []
        
        # Persistence flag, accessed through remote_is_persisted
        self.persistanceState = {'users': True, 'groups': True, 'moroutes': True, 'mtroutes': True}
        
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
    
    def getUser(self, uid):
        for u in self.users:
            if u.uid == uid:
                self.log.debug('getUser [%s] returned a User', uid)
                return u
        
        self.log.debug('getUser [%s] returned None', uid)
        return None
    
    def getGroup(self, gid):
        for g in self.groups:
            if g.gid == gid:
                self.log.debug('getGroup [%s] returned a Group', gid)
                return g
        
        self.log.debug('getGroup [%s] returned None', gid)
        return None
    
    def getMORoute(self, order):
        moroutes = self.mo_routing_table.getAll()
        
        for e in moroutes:
            if order == e.keys()[0]:
                self.log.debug('getMORoute [%s] returned a MORoute', order)
                return e[order]
        
        self.log.debug('getMORoute [%s] returned None', order)
        return None
    
    def getMTRoute(self, order):
        mtroutes = self.mt_routing_table.getAll()
        
        for e in mtroutes:
            if order == e.keys()[0]:
                self.log.debug('getMTRoute [%s] returned a MTRoute', order)
                return e[order]
        
        self.log.debug('getMTRoute [%s] returned None', order)
        return None
    
    def remote_persist(self, profile, scope = 'all'):
        try:
            if scope in ['all', 'groups']:
                # Persist groups configuration
                path = '%s/%s.router-groups' % (self.config.store_path, profile)
                self.log.info('Persisting current Groups configuration to [%s] profile in %s' % (profile, path))
    
                fh = open(path,'w')
                # Write configuration with datetime stamp
                fh.write('Persisted on %s\n' % time.strftime("%c"))
                fh.write(pickle.dumps(self.groups, self.pickleProtocol))
                fh.close()

                # Set persistance state to True
                self.persistanceState['users'] = True

            if scope in ['all', 'users']:
                # Persist users configuration
                path = '%s/%s.router-users' % (self.config.store_path, profile)
                self.log.info('Persisting current Users configuration to [%s] profile in %s' % (profile, path))
    
                fh = open(path,'w')
                # Write configuration with datetime stamp
                fh.write('Persisted on %s\n' % time.strftime("%c"))
                fh.write(pickle.dumps(self.users, self.pickleProtocol))
                fh.close()

                # Set persistance state to True
                self.persistanceState['groups'] = True

            if scope in ['all', 'moroutes']:
                # Persist moroutes configuration
                path = '%s/%s.router-moroutes' % (self.config.store_path, profile)
                self.log.info('Persisting current MORoutingTable to [%s] profile in %s' % (profile, path))
    
                fh = open(path,'w')
                # Write configuration with datetime stamp
                fh.write('Persisted on %s\n' % time.strftime("%c"))
                fh.write(pickle.dumps(self.mo_routing_table, self.pickleProtocol))
                fh.close()
                
                # Set persistance state to True
                self.persistanceState['moroutes'] = True

            if scope in ['all', 'mtroutes']:
                # Persist mtroutes configuration
                path = '%s/%s.router-mtroutes' % (self.config.store_path, profile)
                self.log.info('Persisting current MTRoutingTable to [%s] profile in %s' % (profile, path))
    
                fh = open(path,'w')
                # Write configuration with datetime stamp
                fh.write('Persisted on %s\n' % time.strftime("%c"))
                fh.write(pickle.dumps(self.mt_routing_table, self.pickleProtocol))
                fh.close()
                
                # Set persistance state to True
                self.persistanceState['mtroutes'] = True

        except IOError:
            self.log.error('Cannot persist to %s' % path)
            return False
        except Exception, e:
            self.log.error('Unknown error occurred while persisting configuration: %s' % e)
            return False

        return True
    
    def remote_load(self, profile, scope = 'all'):
        try:
            if scope in ['all', 'groups']:
                # Load groups configuration
                path = '%s/%s.router-groups' % (self.config.store_path, profile)
                self.log.info('Loading/Activating [%s] profile Groups configuration from %s' % (profile, path))
    
                # Load configuration from file
                fh = open(path,'r')
                lines = fh.readlines()
                fh.close()
    
                # Remove current configuration
                self.log.info('Removing current Groups (%d)' % len(self.groups))
                self.remote_group_remove_all()
    
                # Adding new groups
                self.groups = pickle.loads(''.join(lines[1:]))
                self.log.info('Added new Groups (%d)' % len(self.groups))

                # Set persistance state to True
                self.persistanceState['groups'] = True

            if scope in ['all', 'users']:
                # Load users configuration
                path = '%s/%s.router-users' % (self.config.store_path, profile)
                self.log.info('Loading/Activating [%s] profile Users configuration from %s' % (profile, path))
    
                # Load configuration from file
                fh = open(path,'r')
                lines = fh.readlines()
                fh.close()
    
                # Remove current configuration
                self.log.info('Removing current Users (%d)' % len(self.users))
                self.remote_user_remove_all()
    
                # Adding new groups
                self.users = pickle.loads(''.join(lines[1:]))
                self.log.info('Added new Users (%d)' % len(self.users))

                # Set persistance state to True
                self.persistanceState['users'] = True

            if scope in ['all', 'moroutes']:
                # Load moroutes configuration
                path = '%s/%s.router-moroutes' % (self.config.store_path, profile)
                self.log.info('Loading/Activating [%s] profile MO Routes configuration from %s' % (profile, path))
    
                # Load configuration from file
                fh = open(path,'r')
                lines = fh.readlines()
                fh.close()
    
                # Adding new MO Routes
                self.mo_routing_table = pickle.loads(''.join(lines[1:]))
                self.log.info('Added new MORoutingTable with %d routes' % len(self.mo_routing_table.getAll()))

                # Set persistance state to True
                self.persistanceState['moroutes'] = True

            if scope in ['all', 'mtroutes']:
                # Load mtroutes configuration
                path = '%s/%s.router-mtroutes' % (self.config.store_path, profile)
                self.log.info('Loading/Activating [%s] profile MT Routes configuration from %s' % (profile, path))
    
                # Load configuration from file
                fh = open(path,'r')
                lines = fh.readlines()
                fh.close()
    
                # Adding new MT Routes
                self.mt_routing_table = pickle.loads(''.join(lines[1:]))
                self.log.info('Added new MTRoutingTable with %d routes' % len(self.mt_routing_table.getAll()))

                # Set persistance state to True
                self.persistanceState['mtroutes'] = True

        except IOError, e:
            self.log.error('Cannot load configuration from %s: %s' % (path, str(e)))
            return False
        except Exception, e:
            self.log.error('Unknown error occurred while loading configuration: %s' % e)
            return False

        return True
        
    def remote_is_persisted(self):
        for k, v in self.persistanceState.iteritems():
            if not v:
                return False
            
        return True
        
    def remote_user_add(self, user):
        user = pickle.loads(user)
        self.log.debug('Adding a User: %s' % user)
        self.log.info('Adding a User (id:%s)' % user.uid)
        
        # Check if group exists
        foundGroup = False
        for _group in self.groups:
            if _group.gid == user.group.gid:
                foundGroup = True
        if not foundGroup:
            self.log.error("Group with id:%s not found, cancelling user adding." % user.group.gid)
            return False

        # Replace existant users
        for _user in self.users:
            if user.uid == _user.uid or user.username == _user.username:
                self.users.remove(_user)
                break 

        self.users.append(user)
        
        # Set persistance state to False (pending for persistance)
        self.persistanceState['users'] = False

        return True
    
    def remote_user_authenticate(self, username, password):
        self.log.debug('Authenticating with username:%s and password:%s' % (username, password))
        self.log.info('Authentication request with username:%s' % username)

        return self.authenticateUser(username, password, True)
    
    def remote_user_remove(self, uid):
        self.log.debug('Removing a User with uid: %s' % uid)
        self.log.info('Removing a User (id:%s)' % uid)

        # Remove user
        for _user in self.users:
            if uid == _user.uid:
                self.users.remove(_user)
                return True
        
        self.log.error("User with id:%s not found, not removing it." % uid)

        # Set persistance state to False (pending for persistance)
        self.persistanceState['users'] = False

        return False

    def remote_user_remove_all(self):
        self.log.info('Removing all users')
        
        self.users = []
        
        # Set persistance state to False (pending for persistance)
        self.persistanceState['users'] = False

        return True

    def remote_user_get_all(self, gid = None):
        self.log.info('Getting all users')
        self.log.debug('Getting all users: %s' % self.users)

        if gid is None:
            return pickle.dumps(self.users)
        else:
            _users = []
            for _user in self.users:
                if _user.group.gid == gid:
                    _users.append(_user)
            
            return pickle.dumps(_users)
            
    
    def remote_group_add(self, group):
        group = pickle.loads(group)
        self.log.debug('Adding a Group: %s' % group)
        self.log.info('Adding a Group (id:%s)' % group.gid)

        # Replace existant groups
        for _group in self.groups:
            if group.gid == _group.gid:
                self.groups.remove(_group)
                break 

        self.groups.append(group)
        
        # Set persistance state to False (pending for persistance)
        self.persistanceState['groups'] = False

        return True
    
    def remote_group_remove(self, gid):
        self.log.debug('Removing a Group with gid: %s' % gid)
        self.log.info('Removing a Group (id:%s)' % gid)

        # Remove group
        for _group in self.groups:
            if gid == _group.gid:
                # Remove users from this group
                _users = copy(self.users)
                for _user in _users:
                    if _user.group.gid == _group.gid:
                        self.log.info('Removing a User (id:%s) from the Group (id:%s)' % (_user.uid, gid))
                        self.users.remove(_user)
                        
                # Safely remove this group
                self.groups.remove(_group)
                return True
        
        self.log.error("Group with id:%s not found, not removing it." % gid)

        # Set persistance state to False (pending for persistance)
        self.persistanceState['groups'] = False

        return False

    def remote_group_remove_all(self):
        self.log.info('Removing all groups')
        
        # Remove group
        for _group in self.groups:
            self.log.debug('Removing a Group: %s' % _group)
            self.log.info('Removing a Group (id:%s)' % _group.gid)
            
            # Remove users from this group
            _users = copy(self.users)
            for _user in _users:
                if _user.group.gid == _group.gid:
                    self.log.info('Removing a User (id:%s) from the Group (id:%s)' % (_user.uid, _group.gid))
                    self.users.remove(_user)
        
        self.groups = []

        # Set persistance state to False (pending for persistance)
        self.persistanceState['groups'] = False

        return True

    def remote_group_get_all(self):
        self.log.info('Getting all groups')
        self.log.debug('Getting all groups: %s' % self.groups)

        return pickle.dumps(self.groups)
    
    def remote_mtroute_add(self, route, order):
        route = pickle.loads(route)
        self.log.debug('Adding a MT Route, order = %s, route = %s' % (order, route))
        self.log.info('Adding a MT Route with order %s', order)

        try:
            self.mt_routing_table.add(route, order)
        except InvalidRoutingTableParameterError, e:
            self.log.error('Cannot add MT Route: %s' % (str(e)))
            return False
        except Exception, e:
            self.log.error('Unknown error occurred while adding MT Route: %s' % (str(e)))
            return False
        
        # Set persistance state to False (pending for persistance)
        self.persistanceState['mtroutes'] = False

        return True
    
    def remote_moroute_add(self, route, order):
        route = pickle.loads(route)
        self.log.debug('Adding a MO Route, order = %s, route = %s' % (order, route))
        self.log.info('Adding a MO Route with order %s', order)

        try:
            self.mo_routing_table.add(route, order)
        except InvalidRoutingTableParameterError, e:
            self.log.error('Cannot add MO Route: %s' % (str(e)))
            return False
        except Exception, e:
            self.log.error('Unknown error occurred while adding MO Route: %s' % (str(e)))
            return False
        
        # Set persistance state to False (pending for persistance)
        self.persistanceState['moroutes'] = False

        return True
    
    def remote_moroute_remove(self, order):
        self.log.info('Removing MO Route [%s]', order)
        
        # Set persistance state to False (pending for persistance)
        self.persistanceState['moroutes'] = False

        return self.mo_routing_table.remove(order)

    def remote_mtroute_remove(self, order):
        self.log.info('Removing MT Route [%s]', order)
        
        # Set persistance state to False (pending for persistance)
        self.persistanceState['mtroutes'] = False

        return self.mt_routing_table.remove(order)

    def remote_mtroute_flush(self):
        self.log.info('Flushing MT Routing table')

        # Set persistance state to False (pending for persistance)
        self.persistanceState['mtroutes'] = False

        return self.mt_routing_table.flush()
    
    def remote_moroute_flush(self):
        self.log.info('Flushing MO Routing table')

        # Set persistance state to False (pending for persistance)
        self.persistanceState['moroutes'] = False

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