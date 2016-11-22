import cPickle as pickle
import datetime
import logging
import time
from logging.handlers import TimedRotatingFileHandler

from twisted.internet import defer
from twisted.spread import pb

import jasmin
from jasmin.protocols.smpp.protocol import SMPPServerProtocol
from jasmin.protocols.smpp.services import SMPPClientService
from jasmin.tools.migrations.configuration import ConfigurationMigrator
from jasmin.vendor.smpp.pdu.pdu_types import RegisteredDeliveryReceipt
from jasmin.vendor.smpp.twisted.protocol import SMPPSessionStates
from .configs import SMPPClientSMListenerConfig
from .content import SubmitSmContent
from .listeners import SMPPClientSMListener

LOG_CATEGORY = "jasmin-pb-client-mgmt"

class ConfigProfileLoadingError(Exception):
    """
    Raised for any error occurring while loading a configuration
    profile with perspective_load
    """

class SMPPClientManagerPB(pb.Avatar):
    def __init__(self, SMPPClientPBConfig):
        self.config = SMPPClientPBConfig
        self.avatar = None
        self.redisClient = None
        self.amqpBroker = None
        self.interceptorpb_client = None
        self.RouterPB = None
        self.connectors = []
        self.declared_queues = []
        self.pickleProtocol = pickle.HIGHEST_PROTOCOL

        # Persistence flag, accessed through perspective_is_persisted
        self.persisted = True

        # Set up a dedicated logger
        self.log = logging.getLogger(LOG_CATEGORY)
        if len(self.log.handlers) != 1:
            self.log.setLevel(self.config.log_level)
            handler = TimedRotatingFileHandler(filename=self.config.log_file,
                                               when=self.config.log_rotate)
            formatter = logging.Formatter(self.config.log_format,
                                          self.config.log_date_format)
            handler.setFormatter(formatter)
            self.log.addHandler(handler)
            self.log.propagate = False

        # Set pickleProtocol
        self.pickleProtocol = self.config.pickle_protocol

        self.log.info('SMPP Client manager configured and ready.')

    def setAvatar(self, avatar):
        if type(avatar) is str:
            self.log.info('Authenticated Avatar: %s', avatar)
        else:
            self.log.info('Anonymous connection')

        self.avatar = avatar

    def addAmqpBroker(self, amqpBroker):
        self.amqpBroker = amqpBroker

        self.log.info('Added amqpBroker to SMPPClientManagerPB')

    def addRedisClient(self, redisClient):
        self.redisClient = redisClient

        self.log.info('Added Redis Client to SMPPClientManagerPB')

    def addInterceptorPBClient(self, interceptorpb_client):
        self.interceptorpb_client = interceptorpb_client

        self.log.info('Added interceptorpb_client to SMPPClientManagerPB')

    def addRouterPB(self, RouterPB):
        self.RouterPB = RouterPB

        self.log.info('Added RouterPB to SMPPClientManagerPB')

    def getConnector(self, cid):
        for c in self.connectors:
            if str(c['id']) == str(cid):
                self.log.debug('getConnector [%s] returned a connector', cid)
                return c

        self.log.debug('getConnector [%s] returned None', cid)
        return None

    def getConnectorDetails(self, cid):
        c = self.getConnector(cid)
        if c is None:
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
        for i in range(len(self.connectors)):
            if str(self.connectors[i]['id']) == str(cid):
                del self.connectors[i]
                self.log.debug('Deleted connector [%s].', cid)
                return True

        self.log.debug('Deleting connector [%s] failed.', cid)
        return False

    def perspective_version_release(self):
        return jasmin.get_release()

    def perspective_version(self):
        return jasmin.get_version()

    def perspective_persist(self, profile='jcli-prod'):
        path = '%s/%s.smppccs' % (self.config.store_path, profile)
        self.log.info('Persisting current configuration to [%s] profile in %s', profile, path)

        try:
            # Prepare connectors for persistence
            # Will persist config and service status only
            connectors = []
            for c in self.connectors:
                connectors.append({
                    'id': c['id'],
                    'config': c['config'],
                    'service_status':c['service'].running})

            # Write configuration with datetime stamp
            fh = open(path, 'w')
            fh.write('Persisted on %s [Jasmin %s]\n' % (time.strftime("%c"), jasmin.get_release()))
            fh.write(pickle.dumps(connectors, self.pickleProtocol))
            fh.close()

            # Set persistance state to True
            self.persisted = True
        except IOError:
            self.log.error('Cannot persist to %s', path)
            return False
        except Exception, e:
            self.log.error('Unknown error occurred while persisting configuration: %s', e)
            return False

        return True

    @defer.inlineCallbacks
    def perspective_load(self, profile='jcli-prod'):
        path = '%s/%s.smppccs' % (self.config.store_path, profile)
        self.log.info('Loading/Activating [%s] profile configuration from %s', profile, path)

        try:
            # Load configuration from file
            fh = open(path, 'r')
            lines = fh.readlines()
            fh.close()

            # Init migrator
            cf = ConfigurationMigrator(context='smppccs', header=lines[0], data=''.join(lines[1:]))

            # Remove current configuration
            CIDs = []
            for c in self.connectors:
                CIDs.append(c['id'])
            for cid in CIDs:
                remRet = yield self.perspective_connector_remove(cid)
                if not remRet:
                    raise ConfigProfileLoadingError('Error removing connector %s' % cid)
                self.log.info('Removed connector [%s]', cid)

            # Apply configuration
            loadedConnectors = cf.getMigratedData()
            for loadedConnector in loadedConnectors:
                # Add connector
                addRet = yield self.perspective_connector_add(
                    pickle.dumps(loadedConnector['config'],
                                 self.pickleProtocol))
                if not addRet:
                    raise ConfigProfileLoadingError('Error adding connector %s' % loadedConnector['id'])

                # Start it if it's service where started when persisted
                if loadedConnector['service_status'] == 1:
                    startRet = yield self.perspective_connector_start(loadedConnector['id'])
                    if not startRet:
                        self.log.error('Error starting connector %s', loadedConnector['id'])

            # Set persistance state to True
            self.persisted = True
        except IOError, e:
            self.log.error('Cannot load configuration from %s: %s', path, str(e))
            defer.returnValue(False)
        except ConfigProfileLoadingError, e:
            self.log.error('Error while loading configuration: %s', e)
            defer.returnValue(False)
        except Exception, e:
            self.log.error('Unknown error occurred while loading configuration: %s', e)
            defer.returnValue(False)

        defer.returnValue(True)

    def perspective_is_persisted(self):
        return self.persisted

    @defer.inlineCallbacks
    def perspective_connector_add(self, ClientConfig):
        """This will add a new connector to self.connectors
        and get a listener on submit.sm.%cid queue, this listener will be
        started and stopped when the connector will get started and stopped
        through this API"""

        c = pickle.loads(ClientConfig)

        self.log.debug('Adding a new connector %s', c.id)

        if self.getConnector(c.id) is not None:
            self.log.error('Trying to add a new connector with an already existant cid: %s', c.id)
            defer.returnValue(False)
        if self.amqpBroker is None:
            self.log.error('AMQP Broker is not added')
            defer.returnValue(False)
        if self.amqpBroker.connected == False:
            self.log.error('AMQP Broker channel is not yet ready')
            defer.returnValue(False)

        # Declare queues
        # First declare the messaging exchange (has no effect if its already declared)
        yield self.amqpBroker.chan.exchange_declare(exchange='messaging', type='topic')
        # submit.sm queue declaration and binding
        submit_sm_queue = 'submit.sm.%s' % c.id
        routing_key = 'submit.sm.%s' % c.id
        self.log.info('Binding %s queue to %s route_key', submit_sm_queue, routing_key)
        yield self.amqpBroker.named_queue_declare(queue=submit_sm_queue)
        yield self.amqpBroker.chan.queue_bind(queue=submit_sm_queue,
                                              exchange="messaging",
                                              routing_key=routing_key)

        # Instanciate smpp client service manager
        serviceManager = SMPPClientService(c, self.config)

        # Instanciate a SM listener
        smListener = SMPPClientSMListener(
            config=SMPPClientSMListenerConfig(self.config.config_file),
            SMPPClientFactory=serviceManager.SMPPClientFactory,
            amqpBroker=self.amqpBroker,
            redisClient=self.redisClient,
            RouterPB=self.RouterPB,
            interceptorpb_client=self.interceptorpb_client)

        # Deliver_sm are sent to smListener's deliver_sm callback method
        serviceManager.SMPPClientFactory.msgHandler = smListener.deliver_sm_event_interceptor

        self.connectors.append({
            'id':           c.id,
            'config':       c,
            'service':      serviceManager,
            'consumer_tag': None,
            'submit_sm_q':  None,
            'sm_listener':  smListener})

        self.log.info('Added a new connector: %s', c.id)

        # Set persistance state to False (pending for persistance)
        self.persisted = False

        defer.returnValue(True)

    @defer.inlineCallbacks
    def perspective_connector_remove(self, cid):
        """This will stop and remove a connector from self.connectors"""

        self.log.debug('Removing connector [%s]', cid)

        connector = self.getConnector(cid)
        if connector is None:
            self.log.error('Trying to remove a connector with an unknown cid: %s', cid)
            defer.returnValue(False)
        if connector['service'].running == 1:
            self.log.debug('Stopping service for connector [%s] before removing it', cid)
            connector['service'].stopService()

        # Stop the queue consumer
        self.log.debug('Stopping submit_sm_q consumer in connector [%s]', cid)
        yield self.perspective_connector_stop(cid)

        if self.delConnector(cid):
            self.log.info('Removed connector [%s]', cid)
            # Set persistance state to False (pending for persistance)
            self.persisted = False
            defer.returnValue(True)
        else:
            self.log.error('Error removing connector [%s], cid not found', cid)
            defer.returnValue(False)

        # Set persistance state to False (pending for persistance)
        self.persisted = False
        defer.returnValue(True)

    def perspective_connector_list(self):
        """This will return only connector IDs since returning an already copyed SMPPClientConfig
        would be a headache"""

        self.log.debug('Connector list requested, returning %s', self.connectors)

        connectorList = []
        for connector in self.connectors:
            c = self.getConnectorDetails(connector['id'])

            connectorList.append(c)

        self.log.info('Returning a list of %s connectors', len(connectorList))
        return connectorList

    @defer.inlineCallbacks
    def perspective_connector_start(self, cid):
        """This will start a service by adding IService to IServiceCollection
        """

        self.log.debug('Starting connector [%s]', cid)

        connector = self.getConnector(cid)
        if connector is None:
            self.log.error('Trying to start a connector with an unknown cid: %s', cid)
            defer.returnValue(False)
        if self.amqpBroker is None:
            self.log.error('AMQP Broker is not added')
            defer.returnValue(False)
        if self.amqpBroker.connected == False:
            self.log.error('AMQP Broker channel is not yet ready')
            defer.returnValue(False)
        if connector['service'].running == 1:
            self.log.error('Connector [%s] is already running.', cid)
            defer.returnValue(False)
        acceptedStartStates = [None, SMPPSessionStates.NONE, SMPPSessionStates.UNBOUND]
        if connector['service'].SMPPClientFactory.getSessionState() not in acceptedStartStates:
            self.log.error(
                'Connector [%s] cannot be started when in session_state: %s',
                cid,
                connector['service'].SMPPClientFactory.getSessionState())
            defer.returnValue(False)

        connector['service'].startService()

        # Start the queue consumer
        self.log.debug('Starting submit_sm_q consumer in connector [%s]', cid)

        # Subscribe to submit.sm.%cid queue
        # check jasmin.queues.test.test_amqp.PublishConsumeTestCase.test_simple_publish_consume_by_topic
        submit_sm_queue = 'submit.sm.%s' % connector['id']
        consumerTag = 'SMPPClientFactory-%s' % (connector['id'])

        try:
            # Using the same consumerTag will prevent getting multiple consumers on the same queue
            # This can resolve the dark hole issue #234

            # Stop the queue consumer if any
            if connector['consumer_tag'] is not None:
                self.log.debug('Stopping submit_sm_q consumer in connector [%s]', cid)
                yield self.amqpBroker.chan.basic_cancel(consumer_tag=connector['consumer_tag'])

            # Start a new consumer
            yield self.amqpBroker.chan.basic_consume(queue=submit_sm_queue,
                                                     no_ack=False, consumer_tag=consumerTag)
        except Exception, e:
            self.log.error('Error consuming from queue %s: %s', submit_sm_queue, e)
            defer.returnValue(False)

        submit_sm_q = yield self.amqpBroker.client.queue(consumerTag)
        self.log.info('%s is consuming from queue: %s', consumerTag, submit_sm_queue)

        # Set callbacks for every consumed message from submit_sm_queue queue
        d = submit_sm_q.get()
        d.addCallback(connector['sm_listener'].submit_sm_callback).addErrback(
            connector['sm_listener'].submit_sm_errback)

        self.log.info('Started connector [%s]', cid)

        # Set connector data
        connector['sm_listener'].setSubmitSmQ(submit_sm_q)
        connector['consumer_tag'] = consumerTag
        connector['submit_sm_q'] = submit_sm_q

        # Set persistance state to False (pending for persistance)
        self.persisted = False

        defer.returnValue(True)

    @defer.inlineCallbacks
    def perspective_connector_stop(self, cid, delQueues=False):
        """This will stop a service by detaching IService to IServiceCollection
        """

        self.log.debug('Stopping connector [%s]', cid)

        connector = self.getConnector(cid)
        if connector is None:
            self.log.error('Trying to stop a connector with an unknown cid: %s', cid)
            defer.returnValue(False)

        # Stop the queue consumer
        if connector['consumer_tag'] is not None:
            self.log.debug('Stopping submit_sm_q consumer in connector [%s]', cid)
            yield self.amqpBroker.chan.basic_cancel(consumer_tag=connector['consumer_tag'])

            # Cleaning
            self.log.debug('Cleaning objects in connector [%s]', cid)
            connector['submit_sm_q'] = None
            connector['consumer_tag'] = None

        if connector['service'].running == 0:
            self.log.error('Connector [%s] is already stopped.', cid)
            defer.returnValue(False)

        if delQueues:
            submitSmQueueName = 'submit.sm.%s' % cid
            self.log.debug('Deleting queue [%s]', submitSmQueueName)
            yield self.amqpBroker.chan.queue_delete(queue=submitSmQueueName)

        # Reject & requeue any pending message to avoid loosing messages after
        # clearing timers
        if len(connector['sm_listener'].rejectTimers) > 0:
            for msgid, timer in connector['sm_listener'].rejectTimers.items():
                if timer.active():
                    func = timer.func
                    kw = timer.kw
                    timer.cancel()
                    del connector['sm_listener'].rejectTimers[msgid]

                    self.log.debug('Rejecting/requeuing msgid [%s] before stopping connector', msgid)
                    yield func(**kw)

        # Stop timers in message listeners
        self.log.debug('Clearing sm_listener timers in connector [%s]', cid)
        connector['sm_listener'].clearAllTimers()
        connector['sm_listener'].submit_sm_q = None

        # Stop SMPP connector
        connector['service'].stopService()

        self.log.info('Stopped connector [%s]', cid)

        # Set persistance state to False (pending for persistance)
        self.persisted = False

        defer.returnValue(True)

    @defer.inlineCallbacks
    def perspective_connector_stopall(self, delQueues=False):
        """This will stop all services by detaching IService to IServiceCollection
        """

        self.log.debug('Stopping all connectors')

        for connector in self.connectors:
            yield self.perspective_connector_stop(connector['id'], delQueues)

        # Set persistance state to False (pending for persistance)
        self.persisted = False

        defer.returnValue(True)

    def perspective_service_status(self, cid):
        """This will return the IService running status
        """

        self.log.debug('Requested service status %s', cid)

        connector = self.getConnector(cid)
        if connector is None:
            self.log.error('Trying to get service status of a connector with an unknown cid: %s', cid)
            return False

        service_status = connector['service'].running
        self.log.info('Connector [%s] service status is: %s', cid, str(service_status))

        return service_status

    def perspective_session_state(self, cid):
        """This will return the session state of a client connector
        """

        self.log.debug('Requested session state for connector [%s]', cid)

        connector = self.getConnector(cid)
        if connector is None:
            self.log.error('Trying to get session state of a connector with an unknown cid: %s', cid)
            return False

        session_state = connector['service'].SMPPClientFactory.getSessionState()
        self.log.info('Connector [%s] session state is: %s', cid, str(session_state))

        if session_state is None:
            return None
        else:
            # returning Enum would raise this on the client side:
            # Unpersistable data: instance of class enum.EnumValue deemed insecure
            # So we just return back the string of it
            return str(session_state)

    def perspective_connector_details(self, cid):
        """This will return the connector details
        """

        self.log.debug('Requested details for connector [%s]', cid)

        connector = self.getConnector(cid)
        if connector is None:
            self.log.error('Trying to get details of a connector with an unknown cid: %s', cid)
            return False

        return self.getConnectorDetails(cid)

    def perspective_connector_config(self, cid):
        """This will return the connector SMPPClientConfig object
        """

        self.log.debug('Requested config for connector [%s]', cid)

        connector = self.getConnector(cid)
        if connector is None:
            self.log.error('Trying to get config of a connector with an unknown cid: %s', cid)
            return False

        return pickle.dumps(connector['config'], self.pickleProtocol)

    @defer.inlineCallbacks
    def perspective_submit_sm(self, cid, SubmitSmPDU, submit_sm_bill, priority=1, validity_period=None,
                              pickled=True, dlr_url=None, dlr_level=1, dlr_method='POST',
                              source_connector='httpapi'):
        """This will enqueue a submit_sm to a connector
        """
        connector = self.getConnector(cid)
        if connector is None:
            self.log.error('Trying to enqueue a SUBMIT_SM to a connector with an unknown cid: %s', cid)
            defer.returnValue(False)
        if self.amqpBroker is None:
            self.log.error('AMQP Broker is not added')
            defer.returnValue(False)
        if self.amqpBroker is None:
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
            # Remove schedule_delivery_time / not supported right now
            if SubmitSmPDU.params['schedule_delivery_time'] is not None:
                SubmitSmPDU.params['schedule_delivery_time'] = None
                self.log.warn('Removing schedule_delivery_time from SubmitSmPDU.')

            PickledSubmitSmPDU = pickle.dumps(SubmitSmPDU, self.pickleProtocol)
            submit_sm_bill = pickle.dumps(submit_sm_bill, self.pickleProtocol)
        else:
            PickledSubmitSmPDU = SubmitSmPDU
            SubmitSmPDU = pickle.loads(PickledSubmitSmPDU)

        # Publishing a pickled PDU
        self.log.debug('Publishing SubmitSmPDU with routing_key=%s, priority=%s', pubQueueName, priority)
        c = SubmitSmContent(
            body=PickledSubmitSmPDU,
            replyto=responseQueueName,
            submit_sm_bill=submit_sm_bill,
            priority=priority,
            expiration=validity_period,
            source_connector='httpapi' if source_connector == 'httpapi' else 'smppsapi')
        yield self.amqpBroker.publish(exchange='messaging', routing_key=pubQueueName, content=c)

        if source_connector == 'httpapi' and dlr_url is not None:
            # Enqueue DLR request in redis 'dlr' key if it is a httpapi request
            if self.redisClient is None or str(self.redisClient) == '<Redis Connection: Not connected>':
                self.log.warn("DLR is not enqueued for SubmitSmPDU [msgid:%s], RC is not connected.",
                              c.properties['message-id'])
            else:
                self.log.debug('Setting DLR url (%s) and level (%s) for message id:%s, expiring in %s',
                               dlr_url,
                               dlr_level,
                               c.properties['message-id'],
                               connector['config'].dlr_expiry)
                # Set values and callback expiration setting
                hashKey = "dlr:%s" % (c.properties['message-id'])
                hashValues = {'sc': 'httpapi',
                              'url': dlr_url,
                              'level': dlr_level,
                              'method': dlr_method,
                              'expiry': connector['config'].dlr_expiry}
                self.redisClient.hmset(hashKey, hashValues).addCallback(
                    lambda response: self.redisClient.expire(
                        hashKey, connector['config'].dlr_expiry))
        elif (isinstance(source_connector, SMPPServerProtocol) and
              SubmitSmPDU.params['registered_delivery'].receipt != RegisteredDeliveryReceipt.NO_SMSC_DELIVERY_RECEIPT_REQUESTED):
            # If submit_sm is successfully sent from a SMPPServerProtocol connector and DLR is
            # requested, then map message-id to the source_connector to permit related deliver_sm
            # messages holding further receipts to be sent back to the right connector
            if self.redisClient is None or str(self.redisClient) == '<Redis Connection: Not connected>':
                self.log.warn("SMPPs mapping is not done for SubmitSmPDU [msgid:%s], RC is not connected.",
                              c.properties['message-id'])
            else:
                self.log.debug(
                    'Setting SMPPs connector (%s) mapping for msgid:%s, registered_dlr: %s, expiring in %s',
                    source_connector.system_id,
                    c.properties['message-id'],
                    SubmitSmPDU.params['registered_delivery'],
                    source_connector.factory.config.dlr_expiry)
                # Set values and callback expiration setting
                hashKey = "dlr:%s" % (c.properties['message-id'])
                hashValues = {'sc': 'smppsapi',
                              'system_id': source_connector.system_id,
                              'source_addr_ton': SubmitSmPDU.params['source_addr_ton'],
                              'source_addr_npi': SubmitSmPDU.params['source_addr_npi'],
                              'source_addr': SubmitSmPDU.params['source_addr'],
                              'dest_addr_ton': SubmitSmPDU.params['dest_addr_ton'],
                              'dest_addr_npi': SubmitSmPDU.params['dest_addr_npi'],
                              'destination_addr': SubmitSmPDU.params['destination_addr'],
                              'sub_date': datetime.datetime.now(),
                              'rd_receipt': '%s' % SubmitSmPDU.params['registered_delivery'].receipt,
                              'expiry': source_connector.factory.config.dlr_expiry}
                self.redisClient.hmset(hashKey, hashValues).addCallback(
                    lambda response: self.redisClient.expire(
                        hashKey, source_connector.factory.config.dlr_expiry))

        defer.returnValue(c.properties['message-id'])
