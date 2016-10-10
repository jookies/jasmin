import cPickle as pickle
import logging
import time
from copy import copy
from hashlib import md5
from logging.handlers import TimedRotatingFileHandler

from twisted.internet import defer, reactor
from twisted.spread import pb
from txamqp.queue import Closed

import jasmin
from jasmin.routing.InterceptionTables import (MOInterceptionTable,
                                               MTInterceptionTable,
                                               InvalidInterceptionTableParameterError)
from jasmin.routing.Routables import RoutableDeliverSm
from jasmin.routing.RoutingTables import MORoutingTable, MTRoutingTable, InvalidRoutingTableParameterError
from jasmin.routing.content import RoutedDeliverSmContent
from jasmin.routing.jasminApi import Connector
from jasmin.tools.migrations.configuration import ConfigurationMigrator

LOG_CATEGORY = "jasmin-router"


class RouterPB(pb.Avatar):
    def setConfig(self, RouterPBConfig, persistenceTimer=True):
        self.config = RouterPBConfig
        self.persistenceTimer = None

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

        # Set pickleProtocol
        self.pickleProtocol = self.config.pickle_protocol

        # Init routing-related objects
        self.mo_routing_table = MORoutingTable()
        self.mt_routing_table = MTRoutingTable()
        self.users = []
        self.groups = []

        # Init interception-related objects
        self.mo_interception_table = MOInterceptionTable()
        self.mt_interception_table = MTInterceptionTable()

        if persistenceTimer:
            # Activate persistenceTimer, used for persisting users and groups whenever critical updates
            # occured
            self.activatePersistenceTimer()

        # Persistence flag, accessed through perspective_is_persisted
        self.persistenceState = {'users': True, 'groups': True, 'moroutes': True, 'mtroutes': True}

        self.log.info('Router configured and ready.')

    def setAvatar(self, avatar):
        if isinstance(avatar, str):
            self.log.info('Authenticated Avatar: %s', avatar)
        else:
            self.log.info('Anonymous connection')

        self.avatar = avatar

    @defer.inlineCallbacks
    def addAmqpBroker(self, amqpBroker):
        self.amqpBroker = amqpBroker
        self.log.info('Added amqpBroker to RouterPB')

        if not self.amqpBroker.connected:
            self.log.warn('AMQP Broker channel is not yet ready, waiting for it to become ready.')
            yield self.amqpBroker.channelReady
            self.log.info("AMQP Broker channel is ready now, let's go !")

        # Subscribe to deliver.sm.* queues
        yield self.amqpBroker.chan.exchange_declare(exchange='messaging', type='topic')
        consumerTag = 'RouterPB-delivers'
        routingKey = 'deliver.sm.*'
        queueName = 'RouterPB_deliver_sm_all'  # A local queue to RouterPB
        yield self.amqpBroker.named_queue_declare(queue=queueName)
        yield self.amqpBroker.chan.queue_bind(queue=queueName, exchange="messaging", routing_key=routingKey)
        yield self.amqpBroker.chan.basic_consume(queue=queueName, no_ack=False, consumer_tag=consumerTag)
        self.deliver_sm_q = yield self.amqpBroker.client.queue(consumerTag)
        self.deliver_sm_q.get().addCallback(self.deliver_sm_callback).addErrback(self.deliver_sm_errback)
        self.log.info('RouterPB is consuming from routing key: %s', routingKey)

        # Subscribe to bill_request.submit_sm_resp.* queues
        yield self.amqpBroker.chan.exchange_declare(exchange='billing', type='topic')
        consumerTag = 'RouterPB-billrequests'
        routingKey = 'bill_request.submit_sm_resp.*'
        queueName = 'RouterPB_bill_request_submit_sm_resp_all'  # A local queue to RouterPB
        yield self.amqpBroker.named_queue_declare(queue=queueName)
        yield self.amqpBroker.chan.queue_bind(queue=queueName, exchange="billing", routing_key=routingKey)
        yield self.amqpBroker.chan.basic_consume(queue=queueName, no_ack=False, consumer_tag=consumerTag)
        self.bill_request_submit_sm_resp_q = yield self.amqpBroker.client.queue(consumerTag)
        self.bill_request_submit_sm_resp_q.get().addCallback(
            self.bill_request_submit_sm_resp_callback).addErrback(
            self.bill_request_submit_sm_resp_errback)
        self.log.info('RouterPB is consuming from routing key: %s', routingKey)

    @defer.inlineCallbacks
    def rejectMessage(self, message):
        yield self.amqpBroker.chan.basic_reject(delivery_tag=message.delivery_tag, requeue=0)

    @defer.inlineCallbacks
    def ackMessage(self, message):
        yield self.amqpBroker.chan.basic_ack(message.delivery_tag)

    def activatePersistenceTimer(self):
        if self.persistenceTimer and self.persistenceTimer.active():
            self.log.debug('Reseting persistenceTimer with %ss', self.config.persistence_timer_secs)
            self.persistenceTimer.reset(self.config.persistence_timer_secs)
        else:
            self.log.debug('Activating persistenceTimer with %ss', self.config.persistence_timer_secs)
            self.persistenceTimer = reactor.callLater(
                self.config.persistence_timer_secs,
                self.persistenceTimerExpired
            )

    def cancelPersistenceTimer(self):
        if self.persistenceTimer and self.persistenceTimer.active():
            self.log.debug('Cancelling persistenceTimer')
            self.persistenceTimer.cancel()
            self.persistenceTimer = None

    def persistenceTimerExpired(self):
        'This is run every self.config.persistence_timer_secs seconds'
        self.log.debug('persistenceTimerExpired called')

        # If at least one user have its quotas updated, then persist
        # groups and users to disk
        for u in self.users:
            if u.mt_credential.quotas_updated:
                self.log.info('Detected a user quota update, users and groups will be persisted.')
                self.perspective_persist(scope='groups')
                self.perspective_persist(scope='users')
                u.mt_credential.quotas_updated = False
                self.log.debug('Persisted successfully')
                break

        self.activatePersistenceTimer()

    @defer.inlineCallbacks
    def deliver_sm_callback(self, message):
        """This callback is a queue listener
        It will only decide where to send the input message and republish it to the routedConnector
        The consumer will execute the remaining job of final delivery
        c.f. test_router.DeliverSmDeliveryTestCases for use cases
        """
        msgid = message.content.properties['message-id']
        scid = message.content.properties['headers']['connector-id']
        concatenated = message.content.properties['headers']['concatenated']
        will_be_concatenated = message.content.properties['headers']['will_be_concatenated']
        connector = Connector(scid)
        DeliverSmPDU = pickle.loads(message.content.body)
        self.log.debug("Callbacked a deliver_sm with a DeliverSmPDU[%s] (?): %s", msgid, DeliverSmPDU)

        # @todo: Implement MO throttling here, same as in
        # jasmin.managers.listeners.SMPPClientSMListener.submit_sm_callback
        self.deliver_sm_q.get().addCallback(self.deliver_sm_callback).addErrback(self.deliver_sm_errback)

        # Routing
        routable = RoutableDeliverSm(DeliverSmPDU, connector)
        route = self.getMORoutingTable().getRouteFor(routable)
        if route is None:
            self.log.debug("No route matched this DeliverSmPDU with scid:%s and msgid:%s", scid, msgid)
            yield self.rejectMessage(message)
        else:
            # Get connector from selected route
            self.log.debug("RouterPB selected %s for this SubmitSmPDU", route)
            if repr(route) == 'FailoverMORoute':
                # The failover route will return all connectors, we don't care about
                #   connectors statuses, this will be the thrower responsability
                routedConnectors = route.getConnectors()
                route_type = 'failover'
            else:
                routedConnectors = [route.getConnector()]
                route_type = 'simple'

            # Smpps will not route any concatenated content, it must instead route
            # multiparted messages
            # Only http connector needs concatenated content
            if concatenated and routedConnectors[0].type != 'http':
                self.log.debug(
                    "DeliverSmPDU [msgid:%s] not routed because its content is concatenated and the routedConnector is not http: %s",
                    msgid, routedConnectors[0].type)
                yield self.rejectMessage(message)

            # Http will not route any multipart messages, it must instead route
            # concatenated messages
            # Only smpps connector needs multipart content
            elif will_be_concatenated and routedConnectors[0].type == 'http':
                self.log.debug(
                    "DeliverSmPDU [msgid:%s] not routed because there will be a one concatenated message for all parts",
                    msgid)
                yield self.rejectMessage(message)

            else:
                if len(routedConnectors) == 1:
                    self.log.debug("Connector '%s'(%s) is set to be a route for this DeliverSmPDU",
                                   routedConnectors[0].cid, routedConnectors[0].type)
                else:
                    self.log.debug("%s %s connectors (failover route) are set to be a route for this DeliverSmPDU",
                                   len(routedConnectors), routedConnectors[0].type)
                yield self.ackMessage(message)

                # Enqueue DeliverSm for delivery through publishing it to deliver_sm_thrower.(type)
                content = RoutedDeliverSmContent(DeliverSmPDU, msgid, scid, routedConnectors, route_type)
                self.log.debug("Publishing RoutedDeliverSmContent [msgid:%s] in deliver_sm_thrower.%s",
                               msgid, routedConnectors[0].type)
                yield self.amqpBroker.publish(exchange='messaging', routing_key='deliver_sm_thrower.%s' %
                                                                                routedConnectors[0].type,
                                              content=content)

    def deliver_sm_errback(self, error):
        """It appears that when closing a queue with the close() method it errbacks with
        a txamqp.queue.Closed exception, didnt find a clean way to stop consuming a queue
        without errbacking here so this is a workaround to make it clean, it can be considered
        as a @TODO requiring knowledge of the queue api behaviour
        """
        if error.check(Closed) == None:
            # @todo: implement this errback
            # For info, this errback is called whenever:
            # - an error has occured inside deliver_sm_callback
            self.log.error("Error in deliver_sm_errback: %s", error)

    @defer.inlineCallbacks
    def bill_request_submit_sm_resp_callback(self, message):
        """This callback is a queue listener
        """
        bid = message.content.properties['message-id']
        amount = float(message.content.properties['headers']['amount'])
        uid = message.content.properties['headers']['user-id']
        self.log.debug("Callbacked a bill_request_submit_sm_resp [uid:%s] [amount:%s] [related-bid:%s]",
                       uid, amount, bid)

        self.bill_request_submit_sm_resp_q.get().addCallback(
            self.bill_request_submit_sm_resp_callback).addErrback(
            self.bill_request_submit_sm_resp_errback)

        _user = self.getUser(uid)
        if _user is None:
            self.log.error("User [uid:%s] not found, billing request [bid:%s] rejected", uid, bid)
            yield self.rejectMessage(message)
        elif _user.mt_credential.getQuota('balance') is not None:
            if _user.mt_credential.getQuota('balance') < amount:
                self.log.error(
                    'User [uid:%s] have no sufficient balance (%s/%s) for this billing [bid:%s] request: rejected',
                    uid, _user.mt_credential.getQuota('balance'), amount, bid)
                yield self.rejectMessage(message)
            else:
                _user.mt_credential.updateQuota('balance', -amount)
                self.log.info('User [uid:%s] charged for amount: %s (bid:%s)', uid, amount, bid)
                yield self.ackMessage(message)

    def bill_request_submit_sm_resp_errback(self, error):
        """It appears that when closing a queue with the close() method it errbacks with
        a txamqp.queue.Closed exception, didnt find a clean way to stop consuming a queue
        without errbacking here so this is a workaround to make it clean, it can be considered
        as a @TODO requiring knowledge of the queue api behaviour
        """
        if error.check(Closed) == None:
            # @todo: implement this errback
            # For info, this errback is called whenever:
            # - an error has occured inside deliver_sm_callback
            self.log.error("Error in bill_request_submit_sm_resp_errback: %s", error)
            self.log.critical("User were not charged !")

    def getMOInterceptionTable(self):
        return self.mo_interception_table

    def getMTInterceptionTable(self):
        return self.mt_interception_table

    def getMORoutingTable(self):
        return self.mo_routing_table

    def getMTRoutingTable(self):
        return self.mt_routing_table

    def authenticateUser(self, username, password, return_pickled=False):
        """Authenticate a user agains username and password and return user object or None
        """
        # Find user having correct username/password
        for _user in self.users:
            if _user.username == username and _user.password == md5(password).digest():
                self.log.debug('authenticateUser [username:%s] returned a User', username)

                # Check if user's group is enabled
                _group = self.getGroup(_user.group.gid)
                if _group is not None and not _group.enabled:
                    self.log.info('authenticateUser [username:%s] returned None (group %s is disabled)',
                                  username, _user.group)
                    return None

                # Check if user is enabled
                if not _user.enabled:
                    self.log.info('authenticateUser [username:%s] returned None (user is disabled)',
                                  username)
                    return None

                # If user/group are enabled:
                if return_pickled:
                    return pickle.dumps(_user, self.pickleProtocol)
                else:
                    return _user

        self.log.info('authenticateUser [username:%s] returned None', username)
        return None

    def chargeUserForSubmitSms(self, user, bill, submit_sm_count=1, requirements=None):
        """Will charge the user using the bill object after checking requirements
        """
        if requirements is None:
            # Default:
            requirements = []

        # Check if User is already existent in Router ?
        _user = self.getUser(user.uid)
        if _user is None:
            self.log.error("User [uid:%s] not found for charging", user.uid)

        # Verify user-defined requirements
        for requirement in requirements:
            if not requirement['condition']:
                self.log.warn(requirement['error_message'])
                return None

        # Charge _user
        if (bill.getAmount('submit_sm') * submit_sm_count > 0
            and _user.mt_credential.getQuota('balance') is not None):
            if _user.mt_credential.getQuota('balance') < bill.getAmount('submit_sm') * submit_sm_count:
                self.log.info('User [uid:%s] have no sufficient balance (%s) for submit_sm charging: %s',
                              user.uid, _user.mt_credential.getQuota('balance'),
                              bill.getAmount('submit_sm') * submit_sm_count)
                return None
            _user.mt_credential.updateQuota('balance', -(bill.getAmount('submit_sm') * submit_sm_count))
            self.log.info('User [uid:%s] charged for submit_sm amount: %s',
                          user.uid, bill.getAmount('submit_sm') * submit_sm_count)
        # Decrement counts
        if (bill.getAction('decrement_submit_sm_count') * submit_sm_count > 0
            and _user.mt_credential.getQuota('submit_sm_count') is not None):
            if _user.mt_credential.getQuota('submit_sm_count') < bill.getAction(
                    'decrement_submit_sm_count') * submit_sm_count:
                self.log.info('User [uid:%s] have no sufficient submit_sm_count (%s) for submit_sm charging: %s',
                              user.uid, _user.mt_credential.getQuota('submit_sm_count'),
                              bill.getAction('decrement_submit_sm_count') * submit_sm_count)
                return None
            _user.mt_credential.updateQuota(
                'submit_sm_count',
                -(bill.getAction('decrement_submit_sm_count') * submit_sm_count))
            self.log.info('User\'s [uid:%s] submit_sm_count decremented for submit_sm: %s',
                          user.uid, bill.getAction('decrement_submit_sm_count') * submit_sm_count)

        return True

    def getUser(self, uid):
        for _user in self.users:
            if str(_user.uid) == str(uid):
                self.log.debug('getUser [uid:%s] returned a User', uid)
                return _user

        self.log.debug('getUser [uid:%s] returned None', uid)
        return None

    def getGroup(self, gid):
        for _group in self.groups:
            if str(_group.gid) == str(gid):
                self.log.debug('getGroup [gid:%s] returned a Group', gid)
                return _group

        self.log.debug('getGroup [gid:%s] returned None', gid)
        return None

    def getMOInterceptor(self, order):
        mointerceptors = self.mo_interception_table.getAll()

        for e in mointerceptors:
            if order == e.keys()[0]:
                self.log.debug('getMOInterceptor [order:%s] returned a MOInterceptor', order)
                return e[order]

        self.log.debug('getMOInterceptor [order:%s] returned None', order)
        return None

    def getMTInterceptor(self, order):
        mtinterceptors = self.mt_interception_table.getAll()

        for e in mtinterceptors:
            if order == e.keys()[0]:
                self.log.debug('getMTInterceptor [order:%s] returned a MTInterceptor', order)
                return e[order]

        self.log.debug('getMTInterceptor [order:%s] returned None', order)
        return None

    def getMORoute(self, order):
        moroutes = self.mo_routing_table.getAll()

        for e in moroutes:
            if order == e.keys()[0]:
                self.log.debug('getMORoute [order:%s] returned a MORoute', order)
                return e[order]

        self.log.debug('getMORoute [order:%s] returned None', order)
        return None

    def getMTRoute(self, order):
        mtroutes = self.mt_routing_table.getAll()

        for e in mtroutes:
            if order == e.keys()[0]:
                self.log.debug('getMTRoute [order:%s] returned a MTRoute', order)
                return e[order]

        self.log.debug('getMTRoute [order:%s] returned None', order)
        return None

    def perspective_version_release(self):
        return jasmin.get_release()

    def perspective_version(self):
        return jasmin.get_version()

    def perspective_persist(self, profile='jcli-prod', scope='all'):
        try:
            if scope in ['all', 'groups']:
                # Persist groups configuration
                path = '%s/%s.router-groups' % (self.config.store_path, profile)
                self.log.info('Persisting current Groups configuration to [%s] profile in %s',
                              profile, path)

                fh = open(path, 'w')
                # Write configuration with datetime stamp
                fh.write('Persisted on %s [Jasmin %s]\n' % (time.strftime("%c"), jasmin.get_release()))
                fh.write(pickle.dumps(self.groups, self.pickleProtocol))
                fh.close()

                # Set persistance state to True
                self.persistenceState['groups'] = True

            if scope in ['all', 'users']:
                # Persist users configuration
                path = '%s/%s.router-users' % (self.config.store_path, profile)
                self.log.info('Persisting current Users configuration to [%s] profile in %s',
                              profile, path)

                fh = open(path, 'w')
                # Write configuration with datetime stamp
                fh.write('Persisted on %s [Jasmin %s]\n' % (time.strftime("%c"), jasmin.get_release()))
                fh.write(pickle.dumps(self.users, self.pickleProtocol))
                fh.close()

                # Set persistance state to True
                self.persistenceState['users'] = True
                for u in self.users:
                    u.mt_credential.quotas_updated = False

            if scope in ['all', 'moroutes']:
                # Persist moroutes configuration
                path = '%s/%s.router-moroutes' % (self.config.store_path, profile)
                self.log.info('Persisting current MORoutingTable to [%s] profile in %s', profile, path)

                fh = open(path, 'w')
                # Write configuration with datetime stamp
                fh.write('Persisted on %s [Jasmin %s]\n' % (time.strftime("%c"), jasmin.get_release()))
                fh.write(pickle.dumps(self.mo_routing_table, self.pickleProtocol))
                fh.close()

                # Set persistance state to True
                self.persistenceState['moroutes'] = True

            if scope in ['all', 'mtroutes']:
                # Persist mtroutes configuration
                path = '%s/%s.router-mtroutes' % (self.config.store_path, profile)
                self.log.info('Persisting current MTRoutingTable to [%s] profile in %s', profile, path)

                fh = open(path, 'w')
                # Write configuration with datetime stamp
                fh.write('Persisted on %s [Jasmin %s]\n' % (time.strftime("%c"), jasmin.get_release()))
                fh.write(pickle.dumps(self.mt_routing_table, self.pickleProtocol))
                fh.close()

                # Set persistance state to True
                self.persistenceState['mtroutes'] = True

            if scope in ['all', 'mointerceptors']:
                # Persist mointerceptors configuration
                path = '%s/%s.router-mointerceptors' % (self.config.store_path, profile)
                self.log.info('Persisting current MOInterceptionTable to [%s] profile in %s',
                              profile, path)

                fh = open(path, 'w')
                # Write configuration with datetime stamp
                fh.write('Persisted on %s [Jasmin %s]\n' % (time.strftime("%c"), jasmin.get_release()))
                fh.write(pickle.dumps(self.mo_interception_table, self.pickleProtocol))
                fh.close()

                # Set persistance state to True
                self.persistenceState['mointerceptors'] = True

            if scope in ['all', 'mtinterceptors']:
                # Persist mtinterceptors configuration
                path = '%s/%s.router-mtinterceptors' % (self.config.store_path, profile)
                self.log.info('Persisting current MTInterceptionTable to [%s] profile in %s',
                              profile, path)

                fh = open(path, 'w')
                # Write configuration with datetime stamp
                fh.write('Persisted on %s [Jasmin %s]\n' % (time.strftime("%c"), jasmin.get_release()))
                fh.write(pickle.dumps(self.mt_interception_table, self.pickleProtocol))
                fh.close()

                # Set persistance state to True
                self.persistenceState['mtinterceptors'] = True

        except IOError:
            self.log.error('Cannot persist to %s', path)
            return False
        except Exception, e:
            self.log.error('Unknown error occurred while persisting configuration: %s', e)
            return False

        return True

    def perspective_load(self, profile='jcli-prod', scope='all'):
        try:
            if scope in ['all', 'groups']:
                # Load groups configuration
                path = '%s/%s.router-groups' % (self.config.store_path, profile)
                self.log.info('Loading/Activating [%s] profile Groups configuration from %s',
                              profile, path)

                # Load configuration from file
                fh = open(path, 'r')
                lines = fh.readlines()
                fh.close()

                # Init migrator
                cf = ConfigurationMigrator(context='groups', header=lines[0], data=''.join(lines[1:]))

                # Remove current configuration
                self.log.info('Removing current Groups (%d)', len(self.groups))
                self.perspective_group_remove_all()

                # Adding new groups
                self.groups = cf.getMigratedData()
                self.log.info('Added new Groups (%d)', len(self.groups))

                # Set persistance state to True
                self.persistenceState['groups'] = True

            if scope in ['all', 'users']:
                # Load users configuration
                path = '%s/%s.router-users' % (self.config.store_path, profile)
                self.log.info('Loading/Activating [%s] profile Users configuration from %s',
                              profile, path)

                # Load configuration from file
                fh = open(path, 'r')
                lines = fh.readlines()
                fh.close()

                # Init migrator
                cf = ConfigurationMigrator(context='users', header=lines[0], data=''.join(lines[1:]))

                # Remove current configuration
                self.log.info('Removing current Users (%d)', len(self.users))
                self.perspective_user_remove_all()

                # Adding new users
                self.users = cf.getMigratedData()
                self.log.info('Added new Users (%d)', len(self.users))

                # Set persistance state to True
                self.persistenceState['users'] = True
                for u in self.users:
                    u.mt_credential.quotas_updated = False

            if scope in ['all', 'mointerceptors']:
                # Load mointerceptors configuration
                path = '%s/%s.router-mointerceptors' % (self.config.store_path, profile)
                self.log.info('Loading/Activating [%s] profile MO Interceptors configuration from %s',
                              profile, path)

                # Load configuration from file
                fh = open(path, 'r')
                lines = fh.readlines()
                fh.close()

                # Init migrator
                cf = ConfigurationMigrator(context='mointerceptors',
                                           header=lines[0], data=''.join(lines[1:]))

                # Adding new MO Interceptors
                self.mo_interception_table = cf.getMigratedData()
                self.log.info('Added new MOInterceptionTable with %d routes',
                              len(self.mo_interception_table.getAll()))

                # Set persistance state to True
                self.persistenceState['mointerceptors'] = True

            if scope in ['all', 'mtinterceptors']:
                # Load mtinterceptors configuration
                path = '%s/%s.router-mtinterceptors' % (self.config.store_path, profile)
                self.log.info('Loading/Activating [%s] profile MT Interceptors configuration from %s',
                              profile, path)

                # Load configuration from file
                fh = open(path, 'r')
                lines = fh.readlines()
                fh.close()

                # Init migrator
                cf = ConfigurationMigrator(context='mtinterceptors',
                                           header=lines[0], data=''.join(lines[1:]))

                # Adding new MT Interceptors
                self.mt_interception_table = cf.getMigratedData()
                self.log.info('Added new MTInterceptionTable with %d routes',
                              len(self.mt_interception_table.getAll()))

                # Set persistance state to True
                self.persistenceState['mtinterceptors'] = True

            if scope in ['all', 'moroutes']:
                # Load moroutes configuration
                path = '%s/%s.router-moroutes' % (self.config.store_path, profile)
                self.log.info('Loading/Activating [%s] profile MO Routes configuration from %s',
                              profile, path)

                # Load configuration from file
                fh = open(path, 'r')
                lines = fh.readlines()
                fh.close()

                # Init migrator
                cf = ConfigurationMigrator(context='moroutes',
                                           header=lines[0], data=''.join(lines[1:]))

                # Adding new MO Routes
                self.mo_routing_table = cf.getMigratedData()
                self.log.info('Added new MORoutingTable with %d routes',
                              len(self.mo_routing_table.getAll()))

                # Set persistance state to True
                self.persistenceState['moroutes'] = True

            if scope in ['all', 'mtroutes']:
                # Load mtroutes configuration
                path = '%s/%s.router-mtroutes' % (self.config.store_path, profile)
                self.log.info('Loading/Activating [%s] profile MT Routes configuration from %s',
                              profile, path)

                # Load configuration from file
                fh = open(path, 'r')
                lines = fh.readlines()
                fh.close()

                # Init migrator
                cf = ConfigurationMigrator(context='mtroutes',
                                           header=lines[0], data=''.join(lines[1:]))

                # Adding new MT Routes
                self.mt_routing_table = cf.getMigratedData()
                self.log.info('Added new MTRoutingTable with %d routes',
                              len(self.mt_routing_table.getAll()))

                # Set persistance state to True
                self.persistenceState['mtroutes'] = True

        except IOError, e:
            self.log.error('Cannot load configuration from %s: %s', path, str(e))
            return False
        except Exception, e:
            self.log.error('Unknown error occurred while loading configuration: %s', e)
            return False

        return True

    def perspective_is_persisted(self):
        for _, v in self.persistenceState.iteritems():
            if not v:
                return False

        return True

    def perspective_user_add(self, user):
        user = pickle.loads(user)
        self.log.debug('Adding a User: %s', user)
        self.log.info('Adding a User (id:%s)', user.uid)

        # Check if group exists
        foundGroup = False
        for _group in self.groups:
            if _group.gid == user.group.gid:
                foundGroup = True
        if not foundGroup:
            self.log.error("Group with id:%s not found, cancelling user adding.", user.group.gid)
            return False

        # Replace existant users
        for _user in self.users:
            if user.uid == _user.uid or user.username == _user.username:
                self.log.warn('User (id:%s) already existant, will be replaced !', user.uid)
                self.users.remove(_user)

                # Save old CnxStatus in new user
                user.setCnxStatus(_user.getCnxStatus())
                break

        self.users.append(user)

        # Set persistance state to False (pending for persistance)
        self.persistenceState['users'] = False

        return True

    def perspective_user_authenticate(self, username, password):
        self.log.debug('Authenticating with username:%s and password:%s', username, password)
        self.log.info('Authentication request with username:%s', username)

        return self.authenticateUser(username, password, True)

    def perspective_user_enable(self, uid):
        self.log.info('Enabling a User (id:%s)', uid)

        # Enable user
        for _user in self.users:
            if uid == _user.uid:
                _user.enable()

                # Set persistance state to False (pending for persistance)
                self.persistenceState['users'] = False
                return True

        self.log.error("User with id:%s not found, not enabling it.", uid)
        return False

    def perspective_user_disable(self, uid):
        self.log.info('Disabling a User (id:%s)', uid)

        # Disable user
        for _user in self.users:
            if uid == _user.uid:
                _user.disable()

                # Set persistance state to False (pending for persistance)
                self.persistenceState['users'] = False
                return True

        self.log.error("User with id:%s not found, not disabling it.", uid)
        return False

    def perspective_user_remove(self, uid):
        self.log.info('Removing a User (id:%s)', uid)

        # Remove user
        for _user in self.users:
            if uid == _user.uid:
                self.users.remove(_user)

                # Set persistance state to False (pending for persistance)
                self.persistenceState['users'] = False
                return True

        self.log.error("User with id:%s not found, not removing it.", uid)
        return False

    def perspective_user_remove_all(self):
        self.log.info('Removing all users')

        self.users = []

        # Set persistance state to False (pending for persistance)
        self.persistenceState['users'] = False

        return True

    def perspective_user_get_all(self, gid=None):
        self.log.info('Getting all users')
        self.log.debug('Getting all users: %s', self.users)

        if gid is None:
            return pickle.dumps(self.users)
        else:
            _users = []
            for _user in self.users:
                if _user.group.gid == gid:
                    _users.append(_user)

            return pickle.dumps(_users)

    def perspective_user_update_quota(self, uid, cred, quota, value):
        self.log.info('Updating a User (id:%s) quota: %s/%s %s', uid, cred, quota, value)

        # Find user
        for _user in self.users:
            if uid == _user.uid:
                try:
                    if not hasattr(_user, cred):
                        raise Exception("Invalid cred: %s", cred)
                    else:
                        _cred = getattr(_user, cred)

                    if quota not in _cred.quotas:
                        raise Exception("Unknown quota: %s", quota)

                    # Update the quota
                    _cred.updateQuota(quota, value)

                except Exception, e:
                    self.log.error("Error updating user (id:%s): %s", uid, e)
                    return False
                else:
                    # Successful update !
                    # Set persistance state to False (pending for persistance)
                    self.persistenceState['users'] = False
                    return True

        self.log.error("User with id:%s not found, not updating it.", uid)

        return False

    def perspective_group_add(self, group):
        group = pickle.loads(group)
        self.log.info('Adding a Group (id:%s)', group.gid)

        # Replace existant groups
        for _group in self.groups:
            if group.gid == _group.gid:
                self.groups.remove(_group)
                break

        self.groups.append(group)

        # Set persistance state to False (pending for persistance)
        self.persistenceState['groups'] = False

        return True

    def perspective_group_enable(self, gid):
        self.log.info('Enabling a Group (id:%s)', gid)

        # Enable group
        for _group in self.groups:
            if gid == _group.gid:
                _group.enable()

                # Set persistance state to False (pending for persistance)
                self.persistenceState['groups'] = False
                return True

        self.log.error("Group with id:%s not found, not enabling it.", gid)
        return False

    def perspective_group_disable(self, gid):
        self.log.info('Disabling a Group (id:%s)', gid)

        # Disable group
        for _group in self.groups:
            if gid == _group.gid:
                _group.disable()

                # Set persistance state to False (pending for persistance)
                self.persistenceState['groups'] = False
                return True

        self.log.error("Group with id:%s not found, not disabling it.", gid)
        return False

    def perspective_group_remove(self, gid):
        self.log.info('Removing a Group (id:%s)', gid)

        # Remove group
        for _group in self.groups:
            if gid == _group.gid:
                # Remove users from this group
                _users = copy(self.users)
                for _user in _users:
                    if _user.group.gid == _group.gid:
                        self.log.info('Removing a User (id:%s) from the Group (id:%s)', _user.uid, gid)
                        self.users.remove(_user)

                # Safely remove this group
                self.groups.remove(_group)
                return True

        self.log.error("Group with id:%s not found, not removing it.", gid)

        # Set persistance state to False (pending for persistance)
        self.persistenceState['groups'] = False

        return False

    def perspective_group_remove_all(self):
        self.log.info('Removing all groups')

        # Remove group
        for _group in self.groups:
            self.log.debug('Removing a Group: %s', _group)
            self.log.info('Removing a Group (id:%s)', _group.gid)

            # Remove users from this group
            _users = copy(self.users)
            for _user in _users:
                if _user.group.gid == _group.gid:
                    self.log.info('Removing a User (id:%s) from the Group (id:%s)',
                                  _user.uid, _group.gid)
                    self.users.remove(_user)

        self.groups = []

        # Set persistance state to False (pending for persistance)
        self.persistenceState['groups'] = False

        return True

    def perspective_group_get_all(self):
        self.log.info('Getting all groups')
        self.log.debug('Getting all groups: %s', self.groups)

        return pickle.dumps(self.groups)

    def perspective_mtinterceptor_add(self, interceptor, order):
        interceptor = pickle.loads(interceptor)
        self.log.debug('Adding a MT Interceptor, order = %s, interceptor = %s', order, interceptor)
        self.log.info('Adding a MT Interceptor with order %s', order)

        try:
            self.mt_interception_table.add(interceptor, order)
        except InvalidInterceptionTableParameterError, e:
            self.log.error('Cannot add MT Interceptor: %s', str(e))
            return False
        except Exception, e:
            self.log.error('Unknown error occurred while adding MT Interceptor: %s', str(e))
            return False

        # Set persistance state to False (pending for persistance)
        self.persistenceState['mtinterceptors'] = False

        return True

    def perspective_mointerceptor_add(self, interceptor, order):
        interceptor = pickle.loads(interceptor)
        self.log.debug('Adding a MO Interceptor, order = %s, interceptor = %s', order, interceptor)
        self.log.info('Adding a MO Interceptor with order %s', order)

        try:
            self.mo_interception_table.add(interceptor, order)
        except InvalidInterceptionTableParameterError, e:
            self.log.error('Cannot add MO Interceptor: %s', str(e))
            return False
        except Exception, e:
            self.log.error('Unknown error occurred while adding MO Interceptor: %s', str(e))
            return False

        # Set persistance state to False (pending for persistance)
        self.persistenceState['mointerceptors'] = False

        return True

    def perspective_mointerceptor_remove(self, order):
        self.log.info('Removing MO Interceptor [%s]', order)

        # Set persistance state to False (pending for persistance)
        self.persistenceState['mointerceptors'] = False

        return self.mo_interception_table.remove(order)

    def perspective_mtinterceptor_remove(self, order):
        self.log.info('Removing MT Interceptor [%s]', order)

        # Set persistance state to False (pending for persistance)
        self.persistenceState['mtinterceptors'] = False

        return self.mt_interception_table.remove(order)

    def perspective_mtinterceptor_flush(self):
        self.log.info('Flushing MT Interceptor table')

        # Set persistance state to False (pending for persistance)
        self.persistenceState['mtinterceptors'] = False

        return self.mt_interception_table.flush()

    def perspective_mointerceptor_flush(self):
        self.log.info('Flushing MO Interceptor table')

        # Set persistance state to False (pending for persistance)
        self.persistenceState['mointerceptors'] = False

        return self.mo_interception_table.flush()

    def perspective_mtinterceptor_get_all(self):
        self.log.info('Getting MT Interceptor table')

        interceptors = self.mt_interception_table.getAll()
        self.log.debug('Getting MT Interceptor table: %s', interceptors)

        return pickle.dumps(interceptors, self.pickleProtocol)

    def perspective_mointerceptor_get_all(self):
        self.log.info('Getting MO Interceptor table')

        interceptors = self.mo_interception_table.getAll()
        self.log.debug('Getting MO Interceptor table: %s', interceptors)

        return pickle.dumps(interceptors, self.pickleProtocol)

    def perspective_mtroute_add(self, route, order):
        route = pickle.loads(route)
        self.log.debug('Adding a MT Route, order = %s, route = %s', order, route)
        self.log.info('Adding a MT Route with order %s', order)

        try:
            self.mt_routing_table.add(route, order)
        except InvalidRoutingTableParameterError, e:
            self.log.error('Cannot add MT Route: %s', str(e))
            return False
        except Exception, e:
            self.log.error('Unknown error occurred while adding MT Route: %s', str(e))
            return False

        # Set persistance state to False (pending for persistance)
        self.persistenceState['mtroutes'] = False

        return True

    def perspective_moroute_add(self, route, order):
        route = pickle.loads(route)
        self.log.debug('Adding a MO Route, order = %s, route = %s', order, route)
        self.log.info('Adding a MO Route with order %s', order)

        try:
            self.mo_routing_table.add(route, order)
        except InvalidRoutingTableParameterError, e:
            self.log.error('Cannot add MO Route: %s', str(e))
            return False
        except Exception, e:
            self.log.error('Unknown error occurred while adding MO Route: %s', str(e))
            return False

        # Set persistance state to False (pending for persistance)
        self.persistenceState['moroutes'] = False

        return True

    def perspective_moroute_remove(self, order):
        self.log.info('Removing MO Route [%s]', order)

        # Set persistance state to False (pending for persistance)
        self.persistenceState['moroutes'] = False

        return self.mo_routing_table.remove(order)

    def perspective_mtroute_remove(self, order):
        self.log.info('Removing MT Route [%s]', order)

        # Set persistance state to False (pending for persistance)
        self.persistenceState['mtroutes'] = False

        return self.mt_routing_table.remove(order)

    def perspective_mtroute_flush(self):
        self.log.info('Flushing MT Routing table')

        # Set persistance state to False (pending for persistance)
        self.persistenceState['mtroutes'] = False

        return self.mt_routing_table.flush()

    def perspective_moroute_flush(self):
        self.log.info('Flushing MO Routing table')

        # Set persistance state to False (pending for persistance)
        self.persistenceState['moroutes'] = False

        return self.mo_routing_table.flush()

    def perspective_mtroute_get_all(self):
        self.log.info('Getting MT Routing table')

        routes = self.mt_routing_table.getAll()
        self.log.debug('Getting MT Routing table: %s', routes)

        return pickle.dumps(routes, self.pickleProtocol)

    def perspective_moroute_get_all(self):
        self.log.info('Getting MO Routing table')

        routes = self.mo_routing_table.getAll()
        self.log.debug('Getting MO Routing table: %s', routes)

        return pickle.dumps(routes, self.pickleProtocol)
