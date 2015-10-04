import copy
import time
import mock
import pickle
import glob
import os
import jasmin
from hashlib import md5
from testfixtures import LogCapture
from twisted.internet import reactor, defer
from twisted.trial import unittest
from twisted.python import log
from twisted.spread import pb
from jasmin.managers.clients import SMPPClientManagerPB
from jasmin.managers.proxies import SMPPClientManagerPBProxy
from jasmin.managers.configs import SMPPClientPBConfig
from jasmin.protocols.smpp.test.smsc_simulator import *
from jasmin.protocols.smpp.configs import SMPPClientConfig
from jasmin.protocols.smpp.operations import SMPPOperationFactory
from jasmin.queues.factory import AmqpFactory
from jasmin.queues.configs import AmqpConfig
from random import randint
from datetime import datetime, timedelta
from twisted.cred import portal
from jasmin.tools.cred.portal import JasminPBRealm
from jasmin.tools.spread.pb import JasminPBPortalRoot
from twisted.cred.checkers import AllowAnonymousAccess, InMemoryUsernamePasswordDatabaseDontUse
from jasmin.managers.proxies import ConnectError

@defer.inlineCallbacks
def waitFor(seconds):
    # Wait seconds
    waitDeferred = defer.Deferred()
    reactor.callLater(seconds, waitDeferred.callback, None)
    yield waitDeferred

class SMPPClientPBTestCase(unittest.TestCase):
    @defer.inlineCallbacks
    def setUp(self, authentication = False):
        # Initiating config objects without any filename
        # will lead to setting defaults and that's what we
        # need to run the tests
        self.SMPPClientPBConfigInstance = SMPPClientPBConfig()
        self.SMPPClientPBConfigInstance.authentication = authentication
        AMQPServiceConfigInstance = AmqpConfig()
        AMQPServiceConfigInstance.reconnectOnConnectionLoss = False

        # Launch AMQP Broker
        self.amqpBroker = AmqpFactory(AMQPServiceConfigInstance)
        self.amqpBroker.preConnect()
        self.amqpClient = reactor.connectTCP(AMQPServiceConfigInstance.host, AMQPServiceConfigInstance.port, self.amqpBroker)

        # Wait for AMQP Broker connection to get ready
        yield self.amqpBroker.getChannelReadyDeferred()

        # Launch the client manager server
        pbRoot = SMPPClientManagerPB()
        pbRoot.setConfig(self.SMPPClientPBConfigInstance)
        yield pbRoot.addAmqpBroker(self.amqpBroker)
        p = portal.Portal(JasminPBRealm(pbRoot))
        if not authentication:
            p.registerChecker(AllowAnonymousAccess())
        else:
            c = InMemoryUsernamePasswordDatabaseDontUse()
            c.addUser('test_user', md5('test_password').digest())
            p.registerChecker(c)
        jPBPortalRoot = JasminPBPortalRoot(p)
        self.PBServer = reactor.listenTCP(0, pb.PBServerFactory(jPBPortalRoot))
        self.pbPort = self.PBServer.getHost().port

        # Default SMPPClientConfig
        defaultSMPPClientId = '001-testconnector'

        self.defaultConfig = SMPPClientConfig(id=defaultSMPPClientId,
                                              username='smppclient1',
                                              reconnectOnConnectionFailure=True,
                                              port=9002,
                                              )

    @defer.inlineCallbacks
    def tearDown(self):
        yield self.PBServer.stopListening()
        yield self.amqpClient.disconnect()

class SMPPClientPBProxyTestCase(SMPPClientManagerPBProxy, SMPPClientPBTestCase):
    @defer.inlineCallbacks
    def tearDown(self):
        yield SMPPClientPBTestCase.tearDown(self)
        yield self.disconnect()

class SMSCSimulator(SMPPClientPBProxyTestCase):
    @defer.inlineCallbacks
    def setUp(self):
        yield SMPPClientPBProxyTestCase.setUp(self)

        factory = Factory()
        factory.protocol = HappySMSC
        self.SMSCPort = reactor.listenTCP(self.defaultConfig.port, factory)

    @defer.inlineCallbacks
    def tearDown(self):
        yield SMPPClientPBProxyTestCase.tearDown(self)
        yield self.SMSCPort.stopListening()

class LastClientFactory(Factory):
    lastClient = None
    def buildProtocol(self, addr):
        self.lastClient = Factory.buildProtocol(self, addr)
        self.lastClient.submitRecords = []
        return self.lastClient

class SMSCSimulatorRecorder(SMPPClientPBProxyTestCase):
    @defer.inlineCallbacks
    def setUp(self):
        yield SMPPClientPBProxyTestCase.setUp(self)

        factory = LastClientFactory()
        factory.protocol = HappySMSCRecorder
        self.SMSCPort = reactor.listenTCP(self.defaultConfig.port, factory)

    @defer.inlineCallbacks
    def tearDown(self):
        yield SMPPClientPBProxyTestCase.tearDown(self)
        yield self.SMSCPort.stopListening()

class SMSCSimulatorDeliverSM(SMPPClientPBProxyTestCase):
    @defer.inlineCallbacks
    def setUp(self):
        yield SMPPClientPBProxyTestCase.setUp(self)

        factory = Factory()
        factory.protocol = DeliverSMSMSC
        self.SMSCPort = reactor.listenTCP(self.defaultConfig.port, factory)

    @defer.inlineCallbacks
    def tearDown(self):
        yield SMPPClientPBProxyTestCase.tearDown(self)
        yield self.SMSCPort.stopListening()

class AuthenticatedTestCases(SMPPClientPBProxyTestCase):
    @defer.inlineCallbacks
    def setUp(self, authentication=False):
        yield SMPPClientPBProxyTestCase.setUp(self, authentication=True)

    @defer.inlineCallbacks
    def test_connect_success(self):
        yield self.connect('127.0.0.1', self.pbPort, 'test_user', 'test_password')

    @defer.inlineCallbacks
    def test_connect_failure(self):
        try:
            yield self.connect('127.0.0.1', self.pbPort, 'test_anyuser', 'test_wrongpassword')
        except ConnectError, e:
            self.assertEqual(str(e), 'Authentication error test_anyuser')
        except Exception, e:
            self.assertTrue(False, "ConnectError not raised, got instead a %s" % type(e))
        else:
            self.assertTrue(False, "ConnectError not raised")

        self.assertFalse(self.isConnected)

    @defer.inlineCallbacks
    def test_connect_non_anonymous(self):
        try:
            yield self.connect('127.0.0.1', self.pbPort)
        except ConnectError, e:
            self.assertEqual(str(e), 'Anonymous connection is not authorized !')
        except Exception, e:
            self.assertTrue(False, "ConnectError not raised, got instead a %s" % type(e))
        else:
            self.assertTrue(False, "ConnectError not raised")

        self.assertFalse(self.isConnected)

class BasicTestCases(SMPPClientPBProxyTestCase):
    @defer.inlineCallbacks
    def test_version_release(self):
        yield self.connect('127.0.0.1', self.pbPort)

        version_release = yield self.version_release()

        self.assertEqual(version_release, jasmin.get_release())

class ConfigurationPersistenceTestCases(SMPPClientPBProxyTestCase):
    @defer.inlineCallbacks
    def tearDown(self):
        # Remove persisted configurations
        filelist = glob.glob("%s/*" % self.SMPPClientPBConfigInstance.store_path)
        for f in filelist:
            os.remove(f)

        yield SMPPClientPBProxyTestCase.tearDown(self)

    @defer.inlineCallbacks
    def test_persist_default(self):
        yield self.connect('127.0.0.1', self.pbPort)

        persistRet = yield self.persist()

        self.assertTrue(persistRet)

    @defer.inlineCallbacks
    def test_load_undefined_profile(self):
        yield self.connect('127.0.0.1', self.pbPort)

        loadRet = yield self.load()

        self.assertFalse(loadRet)

    @defer.inlineCallbacks
    def test_add_start_persist_and_load_default(self):
        yield self.connect('127.0.0.1', self.pbPort)

        # Add, start and persist
        yield self.add(self.defaultConfig)
        yield self.start(self.defaultConfig.id)
        yield self.persist()

        # Remove and assert
        remRet = yield self.remove(self.defaultConfig.id)
        self.assertTrue(remRet)

        # List and assert
        listRet = yield self.connector_list()
        self.assertEqual(0, len(listRet))

        # Load, list and assert service status is started
        yield self.load()
        listRet = yield self.connector_list()
        self.assertEqual(1, len(listRet))
        self.assertEqual(1, listRet[0]['service_status'])

        # Stop (to avoid 'Reactor was unclean' error)
        yield self.stop(self.defaultConfig.id)

    @defer.inlineCallbacks
    def test_add_persist_and_load_default(self):
        yield self.connect('127.0.0.1', self.pbPort)

        # Add and persist
        yield self.add(self.defaultConfig)
        yield self.persist()

        # Remove and assert
        remRet = yield self.remove(self.defaultConfig.id)
        self.assertTrue(remRet)

        # List and assert
        listRet = yield self.connector_list()
        self.assertEqual(0, len(listRet))

        # Load, list and assert
        yield self.load()
        listRet = yield self.connector_list()
        self.assertEqual(1, len(listRet))
        self.assertEqual(self.defaultConfig.id, listRet[0]['id'])

    @defer.inlineCallbacks
    def test_add_persist_and_load_profile(self):
        yield self.connect('127.0.0.1', self.pbPort)

        # Add and persist
        yield self.add(self.defaultConfig)
        yield self.persist('profile')

        # Remove and assert
        remRet = yield self.remove(self.defaultConfig.id)
        self.assertTrue(remRet)

        # List and assert
        listRet = yield self.connector_list()
        self.assertEqual(0, len(listRet))

        # Load, list and assert
        yield self.load('profile')
        listRet = yield self.connector_list()
        self.assertEqual(1, len(listRet))
        self.assertEqual(self.defaultConfig.id, listRet[0]['id'])

    @defer.inlineCallbacks
    def test_persitance_flag(self):
        yield self.connect('127.0.0.1', self.pbPort)

        # Initially, all config is already persisted
        isPersisted = yield self.is_persisted()
        self.assertTrue(isPersisted)

        # Make modifications and assert
        yield self.add(self.defaultConfig)

        # Config is not persisted, waiting for persistance
        isPersisted = yield self.is_persisted()
        self.assertFalse(isPersisted)
        yield self.persist('profile')

        # Now it's persisted
        isPersisted = yield self.is_persisted()
        self.assertTrue(isPersisted)

        # Remove and assert
        yield self.remove(self.defaultConfig.id)

        # Config is not persisted, waiting for persistance
        isPersisted = yield self.is_persisted()
        self.assertFalse(isPersisted)
        yield self.persist('profile')

class ClientConnectorTestCases(SMPPClientPBProxyTestCase):
    @defer.inlineCallbacks
    def test_add_and_list(self):
        yield self.connect('127.0.0.1', self.pbPort)

        addRet = yield self.add(self.defaultConfig)
        listRet = yield self.connector_list()

        self.assertTrue(addRet)
        self.assertEqual(1, len(listRet))
        self.assertEqual(self.defaultConfig.id, listRet[0]['id'])

    @defer.inlineCallbacks
    def test_remove_and_list(self):
        yield self.connect('127.0.0.1', self.pbPort)

        yield self.add(self.defaultConfig)
        remRet = yield self.remove(self.defaultConfig.id)
        listRet = yield self.connector_list()

        self.assertTrue(remRet)
        self.assertEqual(0, len(listRet))

    @defer.inlineCallbacks
    def test_add_duplicates(self):
        yield self.connect('127.0.0.1', self.pbPort)

        yield self.add(self.defaultConfig)
        addRet = yield self.add(self.defaultConfig)
        listRet = yield self.connector_list()

        self.assertEqual(False, addRet)
        self.assertEqual(1, len(listRet))
        self.assertEqual(self.defaultConfig.id, listRet[0]['id'])

    @defer.inlineCallbacks
    def test_empty_list(self):
        yield self.connect('127.0.0.1', self.pbPort)

        listRet = yield self.connector_list()

        self.assertEqual(0, len(listRet))

    @defer.inlineCallbacks
    def test_list(self):
        yield self.connect('127.0.0.1', self.pbPort)

        self.defaultConfig.reconnectOnConnectionFailure = True;
        yield self.add(self.defaultConfig)

        listRet = yield self.connector_list()
        self.assertEqual(1, len(listRet))

        localConfig = copy.copy(self.defaultConfig)
        localConfig.id = 'secondConnector'
        yield self.add(localConfig)

        listRet = yield self.connector_list()
        self.assertEqual(2, len(listRet))

        yield self.start(self.defaultConfig.id)

        listRet = yield self.connector_list()
        self.assertEqual(1, listRet[0]['service_status'])
        self.assertEqual(0, listRet[1]['service_status'])

        yield self.stopall()

    @defer.inlineCallbacks
    def test_start_nonexistentconnector(self):
        yield self.connect('127.0.0.1', self.pbPort)

        startRet = yield self.start('anything')

        self.assertEqual(False, startRet)

    @defer.inlineCallbacks
    def test_stop_nonexistentconnector(self):
        yield self.connect('127.0.0.1', self.pbPort)

        stopRet = yield self.stop('anything')

        self.assertEqual(False, stopRet)

    @defer.inlineCallbacks
    def test_startconnector(self):
        yield self.connect('127.0.0.1', self.pbPort)

        yield self.add(self.defaultConfig)
        startRet = yield self.start(self.defaultConfig.id)

        self.assertEqual(True, startRet)

        yield self.stopall()

    @defer.inlineCallbacks
    def test_add_start_remove_add(self):
        """Resolving issue/bug #1
        """
        yield self.connect('127.0.0.1', self.pbPort)

        yield self.add(self.defaultConfig)
        yield self.start(self.defaultConfig.id)
        yield self.remove(self.defaultConfig.id)
        addRet = yield self.add(self.defaultConfig)

        self.assertEqual(True, addRet)

        yield self.stopall()

    @defer.inlineCallbacks
    def test_start_stop_iteration(self):
        """Resolving issue/bug #5
        """
        yield self.connect('127.0.0.1', self.pbPort)

        yield self.add(self.defaultConfig)

        i = 0
        while i < 150:
            yield self.start(self.defaultConfig.id)
            yield self.stop(self.defaultConfig.id)
            i+= 1

        yield self.stopall()

    @defer.inlineCallbacks
    def test_startconnector_with_noretry_on_con_failure(self):
        """When starting a connector that will fail connecting to a server
        the service shall be stopped since no reconnection is set
        """

        yield self.connect('127.0.0.1', self.pbPort)

        localConfig = copy.copy(self.defaultConfig)
        localConfig.reconnectOnConnectionFailure = False
        yield self.add(localConfig)
        yield self.start(localConfig.id)

        # It takes a moment to stop the service after a connection failure
        while True:
            ssRet = yield self.service_status(localConfig.id)
            if ssRet != 1:
                break;
            else:
                time.sleep(1)

        self.assertEqual(0, ssRet)

        yield self.stop(localConfig.id)

    @defer.inlineCallbacks
    def test_start_sameconnector_twice_with_reconnecting_on_failure(self):
        yield self.connect('127.0.0.1', self.pbPort)

        localConfig = copy.copy(self.defaultConfig)
        localConfig.reconnectOnConnectionFailure = True
        yield self.add(localConfig)
        yield self.start(localConfig.id)
        startRet = yield self.start(localConfig.id)

        self.assertEqual(False, startRet)

        yield self.stopall()

    @defer.inlineCallbacks
    def test_start_sameconnector_twice_with_noreconnecting_on_failure(self):
        """It was discovered that starting the connector twice would lead
        to a multiple consumers on same queue, as of now, starting connector
        twice will cancel current consumer and reconsume again
        Related to #234"""

        yield self.connect('127.0.0.1', self.pbPort)

        localConfig = copy.copy(self.defaultConfig)
        localConfig.reconnectOnConnectionFailure = False
        yield self.add(localConfig)
        yield self.start(localConfig.id)
        startRet = yield self.start(localConfig.id)

        self.assertEqual(True, startRet)

        yield self.stopall()

    @defer.inlineCallbacks
    def test_stopconnector(self):
        yield self.connect('127.0.0.1', self.pbPort)

        yield self.add(self.defaultConfig)
        yield self.start(self.defaultConfig.id)
        stopRet = yield self.stop(self.defaultConfig.id)

        self.assertEqual(True, stopRet)

    @defer.inlineCallbacks
    def test_stop_unstartedconnector(self):
        yield self.connect('127.0.0.1', self.pbPort)

        yield self.add(self.defaultConfig)
        stopRet = yield self.stop(self.defaultConfig.id)

        self.assertEqual(False, stopRet)

    @defer.inlineCallbacks
    def test_service_status(self):
        yield self.connect('127.0.0.1', self.pbPort)

        yield self.add(self.defaultConfig)

        ssRet = yield self.service_status(self.defaultConfig.id)
        self.assertEqual(0, ssRet)

        yield self.start(self.defaultConfig.id)

        ssRet = yield self.service_status(self.defaultConfig.id)
        self.assertEqual(1, ssRet)

        yield self.stop(self.defaultConfig.id)

        ssRet = yield self.service_status(self.defaultConfig.id)
        self.assertEqual(0, ssRet)

    @defer.inlineCallbacks
    def test_connector_details(self):
        yield self.connect('127.0.0.1', self.pbPort)

        localConfig = copy.copy(self.defaultConfig)
        localConfig.reconnectOnConnectionFailure = False
        yield self.add(localConfig)

        ssRet = yield self.connector_details(self.defaultConfig.id)
        self.assertEqual(0, ssRet['start_count'])
        self.assertEqual(0, ssRet['stop_count'])
        self.assertEqual(self.defaultConfig.id, ssRet['id'])
        self.assertEqual(0, ssRet['service_status'])

        yield self.start(self.defaultConfig.id)

        ssRet = yield self.connector_details(self.defaultConfig.id)
        self.assertEqual(1, ssRet['start_count'])

    @defer.inlineCallbacks
    def test_connector_config(self):
        yield self.connect('127.0.0.1', self.pbPort)

        localConfig = copy.copy(self.defaultConfig)
        localConfig.reconnectOnConnectionFailure = False
        yield self.add(localConfig)

        pickledCnfRet = yield self.connector_config(self.defaultConfig.id)
        cnfRet = self.unpickle(pickledCnfRet)
        self.assertIsInstance(cnfRet, SMPPClientConfig)

        # SMPPClientConfig has no __eq__ method, we do a simple comparaison
        # of id only
        self.assertEqual(cnfRet.id, localConfig.id)

class ClientConnectorSubmitSmTestCases(SMSCSimulatorRecorder):
    @defer.inlineCallbacks
    def setUp(self):
        yield SMSCSimulatorRecorder.setUp(self)

        self.SMSCPort.factory.buildProtocol = mock.Mock(wraps=self.SMSCPort.factory.buildProtocol)

        config = SMPPClientConfig(id='defaultId')
        opFactory = SMPPOperationFactory(config)
        self.SubmitSmPDU = opFactory.SubmitSM(
            source_addr='1423',
            destination_addr='98700177',
            short_message='Hello world !',
        )

    @defer.inlineCallbacks
    def test_submitSm(self):
        yield self.connect('127.0.0.1', self.pbPort)

        yield self.add(self.defaultConfig)
        yield self.start(self.defaultConfig.id)

        # Wait for 'BOUND_TRX' state
        yield waitFor(2)

        # Send submit_sm
        assertionKey = str(randint(10000, 99999999999))
        SentSubmitSmPDU = copy.copy(self.SubmitSmPDU)
        SentSubmitSmPDU.params['short_message'] = assertionKey
        msgid = yield self.submit_sm(self.defaultConfig.id, self.SubmitSmPDU)

        # Wait 2 seconds
        yield waitFor(2)

        yield self.stop(self.defaultConfig.id)

        # Wait for unbound state
        yield waitFor(2)

        # Assertions
        # There were a connection to the SMSC
        self.assertTrue(self.SMSCPort.factory.buildProtocol.called)
        self.assertEqual(self.SMSCPort.factory.buildProtocol.call_count, 1)
        # Take the lastClient (and unique one) and assert received message
        self.assertEqual(len(self.SMSCPort.factory.lastClient.submitRecords), 1)
        ReceivedSubmitSmPDU = self.SMSCPort.factory.lastClient.submitRecords[0]
        self.assertEqual(ReceivedSubmitSmPDU.params['short_message'], assertionKey)
        # @todo: Should be a real uuid pattern testing
        self.assertApproximates(len(msgid), 35, 5)

    @defer.inlineCallbacks
    def test_submitSm_priority(self):
        yield self.connect('127.0.0.1', self.pbPort)
    test_submitSm_priority.skip = 'Priority is not implemented in RabbitMQ, the broker on which jasmin tests are done.'

    @defer.inlineCallbacks
    def test_submitSm_throughput(self):
        yield self.connect('127.0.0.1', self.pbPort)

        localConfig = copy.copy(self.defaultConfig)
        localConfig.id = 'throughput-%s' % randint(10, 99)
        localConfig.submit_sm_throughput = 1
        yield self.add(localConfig)
        yield self.start(localConfig.id)

        # Send 5 messages to the queue
        startAt = datetime.now()
        submitCounter = 0
        while submitCounter < 5:
            yield self.submit_sm(localConfig.id, self.SubmitSmPDU)
            submitCounter += 1

        receivedSubmits = self.SMSCPort.factory.lastClient.submitRecords
        counter = 0
        # Wait for delivery (of 5 submits) in 6 seconds max time
        while len(receivedSubmits) < 5 and counter < 60:
            receivedSubmits = self.SMSCPort.factory.lastClient.submitRecords

            # Wait some time
            yield waitFor(1)

            counter += 1
        endAt = datetime.now()

        yield self.stop(localConfig.id)

        # Wait for unbound state
        yield waitFor(2)

        # Assertions
        # Take the lastClient (and unique one) and assert received message
        self.assertEqual(len(self.SMSCPort.factory.lastClient.submitRecords), 5)
        # Delivery mut be delayed for around 5 seconds (+/- 1s) since we throughput is
        # 1 submitsm per second
        self.assertApproximates(endAt - startAt, timedelta( seconds = 5 ), timedelta( seconds = 1 ))

    @defer.inlineCallbacks
    def test_redelivery_of_rejected_messages(self):
        """Related to #67
        Test if all rejected messages due to throughput limit are resent after a delay"""
        yield self.connect('127.0.0.1', self.pbPort)

        localConfig = copy.copy(self.defaultConfig)
        localConfig.id = '#67-%s' % randint(10, 99)
        localConfig.submit_sm_throughput = 3
        yield self.add(localConfig)
        yield self.start(localConfig.id)

        # Send 60 messages to the queue
        startAt = datetime.now()
        submitCounter = 0
        submit_sm_pdu = copy.copy(self.SubmitSmPDU)
        while submitCounter < 60:
            submit_sm_pdu.params['short_message'] = '%s' % submitCounter
            yield self.submit_sm(localConfig.id, submit_sm_pdu)
            submitCounter += 1

        receivedSubmits = self.SMSCPort.factory.lastClient.submitRecords
        counter = 0
        _receivedSubmitsCount = 0
        # Wait for 40 seconds before checking if all submits were delivered
        # It will check for throughput in each iteration
        while counter < 30:
            receivedSubmits = self.SMSCPort.factory.lastClient.submitRecords

            _receivedSubmitsCount = len(receivedSubmits)

            # Wait some time
            yield waitFor(1)

            counter += 1
        endAt = datetime.now()

        yield self.stop(localConfig.id)

        # Wait for unbound state
        yield waitFor(2)

        # Assertions
        # Take the lastClient (and unique one) and assert received message
        self.assertEqual(len(self.SMSCPort.factory.lastClient.submitRecords), 60)

    @defer.inlineCallbacks
    def test_redelivery_of_rejected_messages_after_restart(self):
        """Related to #67
        Test if all rejected messages are resent after a delay, connector will be restarted
        when sending message flow"""
        yield self.connect('127.0.0.1', self.pbPort)

        localConfig = copy.copy(self.defaultConfig)
        localConfig.id = '#67-%s' % randint(10, 9999)
        localConfig.requeue_delay = 1
        localConfig.submit_sm_throughput = 1
        yield self.add(localConfig)
        yield self.start(localConfig.id)

        # Send 4 messages to the queue
        submitCounter = 0
        submit_sm_pdu = copy.copy(self.SubmitSmPDU)
        while submitCounter < 4:
            submit_sm_pdu.params['short_message'] = '%s' % submitCounter
            msgid = yield self.submit_sm(localConfig.id, submit_sm_pdu)
            submitCounter += 1

        # Wait for 2 seconds before stopping
        yield waitFor(2)

        yield self.stop(localConfig.id)

        # Wait for unbound state
        yield waitFor(2)

        # Save the count before starting the connector
        _submitRecordsCount = len(self.SMSCPort.factory.lastClient.submitRecords)

        # Wait for 3 seconds before starting again
        yield waitFor(3)

        # Start the connector again
        yield self.start(localConfig.id)

        # Wait for 5 seconds before stopping , all the rest of the queue must be sent
        yield waitFor(5)

        yield self.stop(localConfig.id)

        # Wait for unbound state
        yield waitFor(2)

        # Update the counter
        _submitRecordsCount+= len(self.SMSCPort.factory.lastClient.submitRecords)

        # Assertions
        self.assertEqual(_submitRecordsCount, 4)

    @defer.inlineCallbacks
    def test_delivery_of_queued_messages(self):
        """Related to #67
        Test if queued messages when connector is down are sent when connector
        goes up"""
        yield self.connect('127.0.0.1', self.pbPort)

        localConfig = copy.copy(self.defaultConfig)
        localConfig.id = str(randint(10, 99))
        localConfig.requeue_delay = 2
        localConfig.submit_sm_throughput = 20
        yield self.add(localConfig)

        # Send 180 messages to the queue
        submitCounter = 0
        submit_sm_pdu = copy.copy(self.SubmitSmPDU)
        while submitCounter < 180:
            submit_sm_pdu.params['short_message'] = '%s' % submitCounter
            yield self.submit_sm(localConfig.id, submit_sm_pdu)
            submitCounter += 1

        # Wait for 3 seconds
        yield waitFor(3)

        # Start the connector again
        yield self.start(localConfig.id)

        # Wait for 15 seconds, all the rest of the queue must be sent
        yield waitFor(15)

        yield self.stop(localConfig.id)

        # Wait for unbound state
        yield waitFor(2)

        # Assertions
        # Take the lastClient (and unique one) and assert received message
        self.assertEqual(len(self.SMSCPort.factory.lastClient.submitRecords), 180)

    @defer.inlineCallbacks
    def test_submitSm_validity(self):
        yield self.connect('127.0.0.1', self.pbPort)

        localConfig = copy.copy(self.defaultConfig)
        localConfig.submit_sm_throughput = 1
        yield self.add(localConfig)
        yield self.start(localConfig.id)

        # Send 5 messages to the queue
        submitCounter = 0
        while submitCounter < 5:
            delta = timedelta(seconds=2)
            SubmitSmPDU = copy.copy(self.SubmitSmPDU)
            SubmitSmPDU.params['validity_period'] = datetime.today() + delta

            c = yield self.submit_sm(localConfig.id, SubmitSmPDU)
            submitCounter += 1

        # Wait 5 seconds
        yield waitFor(5)

        yield self.stop(localConfig.id)

        # Wait for unbound state
        yield waitFor(2)

        # Assertions
        # Take the lastClient (and unique one) and assert received message.
        # Setting validity period to only 2 seconds when throughput is 1 submit/s
        # will lead to rejecting 3 expired messages from the queue
        self.assertApproximates(len(self.SMSCPort.factory.lastClient.submitRecords), 2, 1)

class ClientConnectorSubmitSmRetrialTestCases(SMSCSimulatorRecorder):
    @defer.inlineCallbacks
    def setUp(self):
        yield SMSCSimulatorRecorder.setUp(self)

        self.SMSCPort.factory.buildProtocol = mock.Mock(wraps=self.SMSCPort.factory.buildProtocol)

        config = SMPPClientConfig(id='defaultId')
        opFactory = SMPPOperationFactory(config)
        self.SubmitSmPDU = opFactory.SubmitSM(
            source_addr='1423',
            destination_addr='98700177',
            short_message='Hello world !',
        )

    @defer.inlineCallbacks
    def test_ESME_RSYSERR(self):
        """Ensure that errors specified in jasmin.cfg's submit_error_retrial parameter
        are retried.
        """
        yield self.connect('127.0.0.1', self.pbPort)

        yield self.add(self.defaultConfig)
        yield self.start(self.defaultConfig.id)

        # Wait for 'BOUND_TRX' state
        yield waitFor(2)

        # Send submit_sm
        SentSubmitSmPDU = copy.copy(self.SubmitSmPDU)
        SentSubmitSmPDU.params['short_message'] = 'test_error: ESME_RSYSERR'
        msgid = yield self.submit_sm(self.defaultConfig.id, self.SubmitSmPDU)

        # Wait
        yield waitFor(70)

        yield self.stop(self.defaultConfig.id)

        # Wait for unbound state
        yield waitFor(2)

        # Assertions
        receivedSubmits = self.SMSCPort.factory.lastClient.submitRecords
        # By default, ESME_RSYSERR is retried 2 times in 70s
        self.assertEqual(len(receivedSubmits), 2)

    @defer.inlineCallbacks
    def test_ESME_RREPLACEFAIL(self):
        """Ensure that errors NOT specified in jasmin.cfg's submit_error_retrial parameter
        are not retried.
        """
        yield self.connect('127.0.0.1', self.pbPort)

        yield self.add(self.defaultConfig)
        yield self.start(self.defaultConfig.id)

        # Wait for 'BOUND_TRX' state
        yield waitFor(2)

        # Send submit_sm
        SentSubmitSmPDU = copy.copy(self.SubmitSmPDU)
        SentSubmitSmPDU.params['short_message'] = 'test_error: ESME_RREPLACEFAIL'
        msgid = yield self.submit_sm(self.defaultConfig.id, self.SubmitSmPDU)

        # Wait
        yield waitFor(70)

        yield self.stop(self.defaultConfig.id)

        # Wait for unbound state
        yield waitFor(2)

        # Assertions
        receivedSubmits = self.SMSCPort.factory.lastClient.submitRecords
        # By default, ESME_RREPLACEFAIL is not retried !
        self.assertEqual(len(receivedSubmits), 1)

class LoggingTestCases(SMSCSimulatorRecorder):
    @defer.inlineCallbacks
    def setUp(self):
        yield SMSCSimulatorRecorder.setUp(self)

        self.SMSCPort.factory.buildProtocol = mock.Mock(wraps=self.SMSCPort.factory.buildProtocol)

    @defer.inlineCallbacks
    def send_long_submit_sm(self, long_content_split):
        """Reference to #27:
        When sending a long SMS, logger must write concatenated content
        """
        lc = LogCapture("jasmin-sm-listener")
        yield self.connect('127.0.0.1', self.pbPort)

        yield self.add(self.defaultConfig)
        yield self.start(self.defaultConfig.id)

        # Wait for 'BOUND_TRX' state
        yield waitFor(2)

        # Build a long submit_sm
        assertionKey = str(randint(10, 99)) * 100 + 'EOF' # 203 chars
        config = SMPPClientConfig(id='defaultId')
        opFactory = SMPPOperationFactory(config, long_content_split = long_content_split)
        SubmitSmPDU = opFactory.SubmitSM(
            source_addr='1423',
            destination_addr='98700177',
            short_message=assertionKey,
        )

        # Send submit_sm
        yield self.submit_sm(self.defaultConfig.id, SubmitSmPDU)

        # Wait 2 seconds
        yield waitFor(2)

        yield self.stop(self.defaultConfig.id)

        # Wait for unbound state
        yield waitFor(2)


        # Assertions
        # Take the lastClient (and unique one) and assert received messages
        self.assertEqual(len(self.SMSCPort.factory.lastClient.submitRecords), 2)
        if long_content_split == 'udh':
            concatenatedShortMessage = self.SMSCPort.factory.lastClient.submitRecords[0].params['short_message'][6:]
            concatenatedShortMessage+= self.SMSCPort.factory.lastClient.submitRecords[1].params['short_message'][6:]
        else:
            concatenatedShortMessage = self.SMSCPort.factory.lastClient.submitRecords[0].params['short_message']
            concatenatedShortMessage+= self.SMSCPort.factory.lastClient.submitRecords[1].params['short_message']
        self.assertEqual(concatenatedShortMessage, assertionKey)
        # Logged concatenated message
        loggedSms = False
        for record in lc.records:
            if record.getMessage()[:6] == 'SMS-MT':
                loggedSms = True
                # Will raise ValueError if concatenatedShortMessage is not logged
                record.getMessage().index('[content:%s]' % concatenatedShortMessage)
                break
        # This will assert if we had a SMS-MT logged
        self.assertTrue(loggedSms)
        # There were a connection to the SMSC
        self.assertTrue(self.SMSCPort.factory.buildProtocol.called)
        self.assertEqual(self.SMSCPort.factory.buildProtocol.call_count, 1)


    def test_long_submitSm_sar(self):
        return self.send_long_submit_sm('sar')

    def test_long_submitSm_udh(self):
        return self.send_long_submit_sm('udh')

class ClientConnectorDeliverSmTestCases(SMSCSimulatorDeliverSM):
    receivedDeliverSm = None

    def deliver_sm_callback(self, message):
        self.receivedDeliverSm = pickle.loads(message.content.body)

    @defer.inlineCallbacks
    def test_deliverSm(self):
        yield self.connect('127.0.0.1', self.pbPort)

        # Bind to deliver.sm.CID
        routingKey = 'deliver.sm.%s' % self.defaultConfig.id
        queueName = 'test_deliverSm'
        yield self.amqpBroker.chan.exchange_declare(exchange='messaging', type='topic')
        yield self.amqpBroker.named_queue_declare(queue=queueName, exclusive = True, auto_delete = True)
        yield self.amqpBroker.chan.queue_bind(queue=queueName, exchange="messaging", routing_key=routingKey)

        yield self.add(self.defaultConfig)
        yield self.start(self.defaultConfig.id)

        # Give the reactor a run
        yield self.session_state(self.defaultConfig.id)
        # Wait for 'BOUND_TRX' state
        yield waitFor(2)

        # Listen on the deliver.sm queue
        consumerTag = 'test_deliverSm'
        yield self.amqpBroker.chan.basic_consume(queue=queueName, no_ack=True, consumer_tag=consumerTag)
        deliver_sm_q = yield self.amqpBroker.client.queue(consumerTag)
        deliver_sm_q.get().addCallback(self.deliver_sm_callback)


        yield self.stop(self.defaultConfig.id)

        # Wait for unbound state
        yield waitFor(2)

        # Assertions
        self.assertTrue(self.receivedDeliverSm is not None)
        self.assertIsInstance(self.receivedDeliverSm, DeliverSM)

class ClientConnectorStatusTestCases(SMSCSimulator):

    @defer.inlineCallbacks
    def test_session_state_bound(self):
        yield self.connect('127.0.0.1', self.pbPort)

        yield self.add(self.defaultConfig)

        ssRet = yield self.session_state(self.defaultConfig.id)
        self.assertEqual(None, ssRet)

        yield self.start(self.defaultConfig.id)

        ssRet = yield self.session_state(self.defaultConfig.id)
        self.assertEqual('BOUND_TRX', ssRet)

        yield self.stop(self.defaultConfig.id)

        # Wait for unbound state
        yield waitFor(2)

        cDetails = yield self.connector_details(self.defaultConfig.id)
        self.assertEqual(1, cDetails['stop_count'])

        yield self.stopall()

    @defer.inlineCallbacks
    def test_session_state_none(self):
        yield self.connect('127.0.0.1', self.pbPort)

        localConfig = copy.copy(self.defaultConfig)
        localConfig.port = 8888; # This is a non responsive port
        yield self.add(localConfig)

        ssRet = yield self.session_state(localConfig.id)
        self.assertEqual(None, ssRet)

        yield self.start(localConfig.id)

        ssRet = yield self.session_state(localConfig.id)
        self.assertEqual(None, ssRet)

        yield self.stop(localConfig.id)
