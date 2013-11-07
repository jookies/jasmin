# Copyright 2012 Fourat Zouari <fourat@gmail.com>
# See LICENSE for details.

import copy
import time
import mock
import pickle
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

class SMPPClientPBTestCase(unittest.TestCase):
    @defer.inlineCallbacks
    def setUp(self):
        # Initiating config objects without any filename
        # will lead to setting defaults and that's what we
        # need to run the tests
        SMPPClientPBConfigInstance = SMPPClientPBConfig()
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
        pbRoot.setConfig(SMPPClientPBConfigInstance)
        pbRoot.addAmqpBroker(self.amqpBroker)
        self.PBServer = reactor.listenTCP(0, pb.PBServerFactory(pbRoot))
        self.pbPort = self.PBServer.getHost().port
        
        # Default SMPPClientConfig
        defaultSMPPClientId = '001-testconnector'

        self.defaultConfig = SMPPClientConfig(id=defaultSMPPClientId,
                                              username='smppclient1',
                                              reconnectOnConnectionFailure=True,
                                              port=9002
                                              )

    def tearDown(self):
        self.PBServer.stopListening()
        self.amqpClient.disconnect()
        
class SMPPClientPBProxyTestCase(SMPPClientManagerPBProxy, SMPPClientPBTestCase):    
    def tearDown(self):
        SMPPClientPBTestCase.tearDown(self)
        return self.disconnect()
    
class SMSCSimulator(SMPPClientPBProxyTestCase):
    @defer.inlineCallbacks
    def setUp(self):
        yield SMPPClientPBProxyTestCase.setUp(self)

        factory = Factory()
        factory.protocol = HappySMSC        
        self.SMSCPort = reactor.listenTCP(self.defaultConfig.port, factory)

    def tearDown(self):
        SMPPClientPBProxyTestCase.tearDown(self)
        return self.SMSCPort.stopListening()

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

    def tearDown(self):
        SMPPClientPBProxyTestCase.tearDown(self)
        return self.SMSCPort.stopListening()

class SMSCSimulatorDeliverSM(SMPPClientPBProxyTestCase):
    @defer.inlineCallbacks
    def setUp(self):
        yield SMPPClientPBProxyTestCase.setUp(self)

        factory = Factory()
        factory.protocol = DeliverSMSMSC
        self.SMSCPort = reactor.listenTCP(self.defaultConfig.port, factory)

    def tearDown(self):
        SMPPClientPBProxyTestCase.tearDown(self)
        return self.SMSCPort.stopListening()

class SMSCSimulatorDeliveryReceiptSM(SMPPClientPBProxyTestCase):
    @defer.inlineCallbacks
    def setUp(self):
        yield SMPPClientPBProxyTestCase.setUp(self)

        factory = Factory()
        factory.protocol = DeliveryReceiptSMSC      
        self.SMSCPort = reactor.listenTCP(self.defaultConfig.port, factory)

    def tearDown(self):
        SMPPClientPBProxyTestCase.tearDown(self)
        return self.SMSCPort.stopListening()

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
    receivedSubmitSmResp = None
    
    def submit_sm_callback(self, message):
        self.receivedSubmitSmResp = pickle.loads(message.content.body)
    
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
        while True:
            ssRet = yield self.session_state(self.defaultConfig.id)
            if ssRet == 'BOUND_TRX':
                break;
            else:
                time.sleep(0.2)

        # Listen on the submit.sm.resp queue
        routingKey_submit_sm_resp = 'submit.sm.resp.%s' % self.defaultConfig.id
        consumerTag = 'test_submitSm'
        yield self.amqpBroker.chan.basic_consume(queue=routingKey_submit_sm_resp, no_ack=True, consumer_tag=consumerTag)
        queue = yield self.amqpBroker.client.queue(consumerTag)
        queue.get().addCallback(self.submit_sm_callback)

        # Send submit_sm
        assertionKey = str(randint(10000, 99999999999))
        SentSubmitSmPDU = copy.copy(self.SubmitSmPDU)
        SentSubmitSmPDU.params['short_message'] = assertionKey
        msgid = yield self.submit_sm(self.defaultConfig.id, self.SubmitSmPDU)
        
        # Wait 2 seconds
        waitingDeferred = defer.Deferred()
        reactor.callLater(2, waitingDeferred.callback, None)
        yield waitingDeferred

        # We delete the queues before stopping the connector to let further
        # tests run without having old messages in the queues, in normal
        # situations, delQueues shall not be set to true
        yield self.stop(self.defaultConfig.id)

        # Wait for unbound state
        ssRet = yield self.session_state(self.defaultConfig.id)
        while ssRet != 'NONE':
            time.sleep(0.2)
            ssRet = yield self.session_state(self.defaultConfig.id)
            
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
        # Submit_sm_resp was returned
        self.assertTrue(self.receivedSubmitSmResp is not None)
        self.assertIsInstance(self.receivedSubmitSmResp, SubmitSMResp)

    @defer.inlineCallbacks
    def test_submitSm_priority(self):
        yield self.connect('127.0.0.1', self.pbPort)
    test_submitSm_priority.skip = 'Priority is not implemented in RabbitMQ, the broker on which jasmin tests are done.'

    @defer.inlineCallbacks
    def test_submitSm_throughput(self):
        yield self.connect('127.0.0.1', self.pbPort)

        localConfig = copy.copy(self.defaultConfig)
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
            # Yielding to let the reactor turn on
            yield self.session_state(localConfig.id)
            time.sleep(0.1)
            counter += 1
        endAt = datetime.now()
        
        # We delete the queues before stopping the connector to let further
        # tests run without having old messages in the queues, in normal
        # situations, delQueues shall not be set to true
        yield self.stop(localConfig.id)

        # Wait for unbound state
        ssRet = yield self.session_state(localConfig.id)
        while ssRet != 'NONE':
            time.sleep(0.2)
            ssRet = yield self.session_state(localConfig.id)

        # Assertions
        # Take the lastClient (and unique one) and assert received message
        self.assertEqual(len(self.SMSCPort.factory.lastClient.submitRecords), 5)
        # Delivery mut be delayed for around 5 seconds (+/- 1s) since we throughput is
        # 1 submitsm per second
        self.assertApproximates(endAt - startAt, timedelta( seconds = 5 ), timedelta( seconds = 1 )) 

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
        waitingDeferred = defer.Deferred()
        reactor.callLater(5, waitingDeferred.callback, None)
        yield waitingDeferred
        
        # We delete the queues before stopping the connector to let further
        # tests run without having old messages in the queues, in normal
        # situations, delQueues shall not be set to true
        yield self.stop(localConfig.id)

        # Wait for unbound state
        ssRet = yield self.session_state(localConfig.id)
        while ssRet != 'NONE':
            time.sleep(0.2)
            ssRet = yield self.session_state(localConfig.id)

        # Assertions
        # Take the lastClient (and unique one) and assert received message.
        # Setting validity period to only 2 seconds when throughput is 1 submit/s
        # will lead to rejecting 3 expired messages from the queue
        self.assertApproximates(len(self.SMSCPort.factory.lastClient.submitRecords), 2, 1)

class ClientConnectorDeliverSmTestCases(SMSCSimulatorDeliverSM):
    receivedDeliverSm = None
    
    def deliver_sm_callback(self, message):
        self.receivedDeliverSm = pickle.loads(message.content.body)
    
    @defer.inlineCallbacks
    def test_deliverSm(self):
        yield self.connect('127.0.0.1', self.pbPort)

        yield self.add(self.defaultConfig)
        yield self.start(self.defaultConfig.id)
        
        # Give the reactor a run
        yield self.session_state(self.defaultConfig.id)
        # Wait for 'BOUND_TRX' state
        while True:
            ssRet = yield self.session_state(self.defaultConfig.id)
            if ssRet == 'BOUND_TRX':
                break;
            else:
                time.sleep(0.2)

        # Listen on the deliver.sm queue
        queueName = 'deliver.sm.%s' % self.defaultConfig.id
        consumerTag = 'test_deliverSm'
        yield self.amqpBroker.chan.basic_consume(queue=queueName, consumer_tag=consumerTag, no_ack=True)
        deliver_sm_q = yield self.amqpBroker.client.queue(consumerTag)
        deliver_sm_q.get().addCallback(self.deliver_sm_callback)

        yield self.stop(self.defaultConfig.id)

        # Wait for unbound state
        ssRet = yield self.session_state(self.defaultConfig.id)
        while ssRet != 'NONE':
            time.sleep(0.2)
            ssRet = yield self.session_state(self.defaultConfig.id)

        # Assertions
        self.assertTrue(self.receivedDeliverSm is not None)
        self.assertIsInstance(self.receivedDeliverSm, DeliverSM)
        
class ClientConnectorDeliveryReceiptTestCases(SMSCSimulatorDeliveryReceiptSM):
    receivedDeliverSm = None
    
    def deliver_sm_callback(self, message):
        self.receivedDeliverSm = pickle.loads(message.content.body)
    
    @defer.inlineCallbacks
    def test_deliverSm_dlr(self):
        yield self.connect('127.0.0.1', self.pbPort)

        # Listen on the deliver.sm queue
        queueName = 'dlr.%s' % self.defaultConfig.id
        consumerTag = 'test_deliverSm_dlr'
        yield self.amqpBroker.chan.basic_consume(queue=queueName, consumer_tag=consumerTag, no_ack=True)
        deliver_sm_q = yield self.amqpBroker.client.queue(consumerTag)
        deliver_sm_q.get().addCallback(self.deliver_sm_callback)

        # Add & start
        yield self.add(self.defaultConfig)
        yield self.start(self.defaultConfig.id)
        # Wait for 'BOUND_TRX' state
        while True:
            ssRet = yield self.session_state(self.defaultConfig.id)
            if ssRet == 'BOUND_TRX':
                break;
            else:
                time.sleep(0.2)
        
        # Give the reactor a run
        yield self.session_state(self.defaultConfig.id)

        yield self.stop(self.defaultConfig.id)

        # Wait for unbound state
        ssRet = yield self.session_state(self.defaultConfig.id)
        while ssRet != 'NONE':
            time.sleep(0.2)
            ssRet = yield self.session_state(self.defaultConfig.id)

        # Assertions
        self.assertTrue(self.receivedDeliverSm is not None)
        self.assertTrue(self.receivedDeliverSm.dlr['stat'] == 'DELIVRD')

class ClientConnectorStatusTestCases(SMSCSimulator):
    
    @defer.inlineCallbacks
    def test_session_state_bound(self):
        yield self.connect('127.0.0.1', self.pbPort)

        yield self.add(self.defaultConfig)

        ssRet = yield self.session_state(self.defaultConfig.id)
        self.assertEqual(None, ssRet)
        
        yield self.start(self.defaultConfig.id)

        ssRet = yield self.session_state(self.defaultConfig.id)
        self.assertEqual('BIND_TRX_PENDING', ssRet)
        
        yield self.stop(self.defaultConfig.id)

        # Wait for unbound state
        ssRet = yield self.session_state(self.defaultConfig.id)
        while ssRet != 'NONE':
            time.sleep(0.2)
            ssRet = yield self.session_state(self.defaultConfig.id)
        
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
