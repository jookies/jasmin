import mock
import copy
import time
import string
from twisted.internet import reactor, defer
from twisted.web import server
from jasmin.routing.test.http_server import AckServer
from jasmin.routing.jasminApi import *
from jasmin.routing.test.test_router import (SMPPClientManagerPBTestCase, LastClientFactory,
                                            SubmitSmTestCaseTools, id_generator)
from jasmin.vendor.smpp.pdu import pdu_types
from jasmin.protocols.smpp.test.smsc_simulator import *
from jasmin.routing.proxies import RouterPBProxy
from jasmin.protocols.smpp.configs import SMPPClientConfig
from jasmin.routing.configs import deliverSmThrowerConfig
from jasmin.routing.throwers import deliverSmThrower
from jasmin.vendor.smpp.pdu.operations import DataSM
from jasmin.routing.Routes import DefaultRoute
from jasmin.routing.test.test_router_smpps import SMPPClientTestCases

class DataSmSMSCTestCase(SMPPClientManagerPBTestCase):
    protocol = DeliverSmSMSC

    @defer.inlineCallbacks
    def setUp(self):
        yield SMPPClientManagerPBTestCase.setUp(self)

        self.smsc_f = LastClientFactory()
        self.smsc_f.protocol = self.protocol
        self.SMSCPort = reactor.listenTCP(0, self.smsc_f)

    @defer.inlineCallbacks
    def tearDown(self):
        yield self.SMSCPort.stopListening()
        yield SMPPClientManagerPBTestCase.tearDown(self)

class DataSmHttpThrowingTestCases(RouterPBProxy, DataSmSMSCTestCase):

    @defer.inlineCallbacks
    def setUp(self):
        yield DataSmSMSCTestCase.setUp(self)

        # Start http servers
        self.AckServerResource = AckServer()
        self.AckServer = reactor.listenTCP(0, server.Site(self.AckServerResource))

        # Initiating config objects without any filename
        # will lead to setting defaults and that's what we
        # need to run the tests
        deliverSmThrowerConfigInstance = deliverSmThrowerConfig()
        # Lower the timeout config to pass the timeout tests quickly
        deliverSmThrowerConfigInstance.timeout = 2
        deliverSmThrowerConfigInstance.retry_delay = 1
        deliverSmThrowerConfigInstance.max_retries = 2

        # Launch the deliverSmThrower
        self.deliverSmThrower = deliverSmThrower()
        self.deliverSmThrower.setConfig(deliverSmThrowerConfigInstance)

        # Add the broker to the deliverSmThrower
        yield self.deliverSmThrower.addAmqpBroker(self.amqpBroker)

    @defer.inlineCallbacks
    def tearDown(self):
        yield self.AckServer.stopListening()
        yield self.deliverSmThrower.stopService()
        yield DataSmSMSCTestCase.tearDown(self)

    @defer.inlineCallbacks
    def prepareRoutingsAndStartConnector(self, connector):
        self.AckServerResource.render_GET = mock.Mock(wraps=self.AckServerResource.render_GET)

        # Prepare for routing
        connector.port = self.SMSCPort.getHost().port
        c2_destination = HttpConnector(id_generator(), 'http://127.0.0.1:%s/send' % self.AckServer.getHost().port)
        # Set the route
        yield self.moroute_add(DefaultRoute(c2_destination), 0)

        # Now we'll create the connector 1 from which we'll receive DataSM PDUs before
        # throwing to http
        yield self.SMPPClientManagerPBProxy.connect('127.0.0.1', self.CManagerPort)
        c1Config = SMPPClientConfig(id=connector.cid, port=connector.port)
        yield self.SMPPClientManagerPBProxy.add(c1Config)

        # Start the connector
        yield self.SMPPClientManagerPBProxy.start(connector.cid)
        # Wait for 'BOUND_TRX' state
        while True:
            ssRet = yield self.SMPPClientManagerPBProxy.session_state(connector.cid)
            if ssRet == 'BOUND_TRX':
                break;
            else:
                time.sleep(0.2)

    @defer.inlineCallbacks
    def stopConnector(self, connector):
        # Disconnect the connector
        yield self.SMPPClientManagerPBProxy.stop(connector.cid)
        # Wait for 'BOUND_TRX' state
        while True:
            ssRet = yield self.SMPPClientManagerPBProxy.session_state(connector.cid)
            if ssRet == 'NONE':
                break;
            else:
                time.sleep(0.2)

    @defer.inlineCallbacks
    def triggerDataSmFromSMSC(self, pdus):
        for pdu in pdus:
            yield self.SMSCPort.factory.lastClient.trigger_deliver_sm(pdu)

        # Wait 4 seconds
        exitDeferred = defer.Deferred()
        reactor.callLater(4, exitDeferred.callback, None)
        yield exitDeferred

    @defer.inlineCallbacks
    def test_delivery_HttpConnector(self):
        yield self.connect('127.0.0.1', self.pbPort)
        # Connect to SMSC
        source_connector = Connector(id_generator())
        yield self.prepareRoutingsAndStartConnector(source_connector)

        # Send a data_sm from the SMSC
        pdu = DataSM(
            source_addr='1234',
            destination_addr='4567',
            message_payload='any content',
        )
        yield self.triggerDataSmFromSMSC([pdu])

        # Run tests
        # Test callback in router
        self.assertEquals(self.pbRoot_f.deliver_sm_callback.call_count, 1)
        # Destination connector must receive the message one time (no retries)
        self.assertEqual(self.AckServerResource.render_GET.call_count, 1)
        # Assert received args
        receivedHttpReq = self.AckServerResource.last_request.args
        self.assertEqual(len(receivedHttpReq), 7)
        self.assertEqual(receivedHttpReq['from'], [pdu.params['source_addr']])
        self.assertEqual(receivedHttpReq['to'], [pdu.params['destination_addr']])
        self.assertEqual(receivedHttpReq['content'], [pdu.params['message_payload']])
        self.assertEqual(receivedHttpReq['origin-connector'], [source_connector.cid])

        # Disconnector from SMSC
        yield self.stopConnector(source_connector)

    @defer.inlineCallbacks
    def test_long_content_delivery_SAR_HttpConnector(self):
        yield self.connect('127.0.0.1', self.pbPort)
        # Connect to SMSC
        source_connector = Connector(id_generator())
        yield self.prepareRoutingsAndStartConnector(source_connector)

        # Send a data_sm from the SMSC
        basePdu = DataSM(
            source_addr = '1234',
            destination_addr = '4567',
            message_payload = '',
            sar_total_segments = 3,
            sar_msg_ref_num = int(id_generator(size = 2, chars=string.digits)),
        )
        pdu_part1 = copy.deepcopy(basePdu)
        pdu_part2 = copy.deepcopy(basePdu)
        pdu_part3 = copy.deepcopy(basePdu)
        pdu_part1.params['message_payload'] = '__1st_part_with_153_char________________________________________________________________________________________________________________________________.'
        pdu_part1.params['sar_segment_seqnum'] = 1
        pdu_part2.params['message_payload'] = '__2nd_part_with_153_char________________________________________________________________________________________________________________________________.'
        pdu_part2.params['sar_segment_seqnum'] = 2
        pdu_part3.params['message_payload'] = '__3rd_part_end.'
        pdu_part3.params['sar_segment_seqnum'] = 3
        yield self.triggerDataSmFromSMSC([pdu_part1, pdu_part2, pdu_part3])

        # Run tests
        # Destination connector must receive the message one time (no retries)
        self.assertEqual(self.AckServerResource.render_GET.call_count, 1)
        # Assert received args
        receivedHttpReq = self.AckServerResource.last_request.args
        self.assertEqual(len(receivedHttpReq), 7)
        self.assertEqual(receivedHttpReq['from'], [basePdu.params['source_addr']])
        self.assertEqual(receivedHttpReq['to'], [basePdu.params['destination_addr']])
        self.assertEqual(receivedHttpReq['content'], [pdu_part1.params['message_payload'] + pdu_part2.params['message_payload'] + pdu_part3.params['message_payload']])
        self.assertEqual(receivedHttpReq['origin-connector'], [source_connector.cid])

        # Disconnector from SMSC
        yield self.stopConnector(source_connector)

    @defer.inlineCallbacks
    def test_long_content_delivery_UDH_HttpConnector(self):
        yield self.connect('127.0.0.1', self.pbPort)
        # Connect to SMSC
        source_connector = Connector(id_generator())
        yield self.prepareRoutingsAndStartConnector(source_connector)

        # Build a UDH
        baseUdh = []
        baseUdh.append(struct.pack('!B', 5)) # Length of User Data Header
        baseUdh.append(struct.pack('!B', 0)) # Information Element Identifier, equal to 00 (Concatenated short messages, 8-bit reference number)
        baseUdh.append(struct.pack('!B', 3)) # Length of the header, excluding the first two fields; equal to 03
        baseUdh.append(struct.pack('!B', int(id_generator(size = 2, chars=string.digits)))) # msg_ref_num
        baseUdh.append(struct.pack('!B', 3)) # total_segments

        # Send a data_sm from the SMSC
        basePdu = DataSM(
            source_addr = '1234',
            destination_addr = '4567',
            message_payload = '',
            esm_class = EsmClass(EsmClassMode.DEFAULT, EsmClassType.DEFAULT, [EsmClassGsmFeatures.UDHI_INDICATOR_SET]),
        )
        pdu_part1 = copy.deepcopy(basePdu)
        udh_part1 = copy.deepcopy(baseUdh)
        pdu_part2 = copy.deepcopy(basePdu)
        udh_part2 = copy.deepcopy(baseUdh)
        pdu_part3 = copy.deepcopy(basePdu)
        udh_part3 = copy.deepcopy(baseUdh)
        udh_part1.append(struct.pack('!B', 1)) # segment_seqnum
        pdu_part1.params['more_messages_to_send'] = MoreMessagesToSend.MORE_MESSAGES
        pdu_part1.params['message_payload'] = ''.join(udh_part1)+'__1st_part_with_153_char________________________________________________________________________________________________________________________________.'
        udh_part2.append(struct.pack('!B', 2)) # segment_seqnum
        pdu_part2.params['more_messages_to_send'] = MoreMessagesToSend.MORE_MESSAGES
        pdu_part2.params['message_payload'] = ''.join(udh_part2)+'__2nd_part_with_153_char________________________________________________________________________________________________________________________________.'
        udh_part3.append(struct.pack('!B', 3)) # segment_seqnum
        pdu_part3.params['more_messages_to_send'] = MoreMessagesToSend.NO_MORE_MESSAGES
        pdu_part3.params['message_payload'] = ''.join(udh_part3)+'__3rd_part_end.'
        yield self.triggerDataSmFromSMSC([pdu_part1, pdu_part2, pdu_part3])

        # Run tests
        # Destination connector must receive the message one time (no retries)
        self.assertEqual(self.AckServerResource.render_GET.call_count, 1)
        # Assert received args
        receivedHttpReq = self.AckServerResource.last_request.args
        self.assertEqual(len(receivedHttpReq), 7)
        self.assertEqual(receivedHttpReq['from'], [basePdu.params['source_addr']])
        self.assertEqual(receivedHttpReq['to'], [basePdu.params['destination_addr']])
        self.assertEqual(receivedHttpReq['content'], [pdu_part1.params['message_payload'][6:] + pdu_part2.params['message_payload'][6:] + pdu_part3.params['message_payload'][6:]])
        self.assertEqual(receivedHttpReq['origin-connector'], [source_connector.cid])

        # Disconnector from SMSC
        yield self.stopConnector(source_connector)

    @defer.inlineCallbacks
    def test_unordered_long_content_delivery_HttpConnector(self):
        yield self.connect('127.0.0.1', self.pbPort)
        # Connect to SMSC
        source_connector = Connector(id_generator())
        yield self.prepareRoutingsAndStartConnector(source_connector)

        # Send a data_sm from the SMSC
        basePdu = DataSM(
            source_addr = '1234',
            destination_addr = '4567',
            message_payload = '',
            sar_total_segments = 3,
            sar_msg_ref_num = int(id_generator(size = 2, chars=string.digits)),
        )
        pdu_part1 = copy.deepcopy(basePdu)
        pdu_part2 = copy.deepcopy(basePdu)
        pdu_part3 = copy.deepcopy(basePdu)
        pdu_part1.params['message_payload'] = '__1st_part_with_153_char________________________________________________________________________________________________________________________________.'
        pdu_part1.params['sar_segment_seqnum'] = 1
        pdu_part2.params['message_payload'] = '__2nd_part_with_153_char________________________________________________________________________________________________________________________________.'
        pdu_part2.params['sar_segment_seqnum'] = 2
        pdu_part3.params['message_payload'] = '__3rd_part_end.'
        pdu_part3.params['sar_segment_seqnum'] = 3
        yield self.triggerDataSmFromSMSC([pdu_part1, pdu_part3, pdu_part2])

        # Run tests
        # Destination connector must receive the message one time (no retries)
        self.assertEqual(self.AckServerResource.render_GET.call_count, 1)
        # Assert received args
        receivedHttpReq = self.AckServerResource.last_request.args
        self.assertEqual(len(receivedHttpReq), 7)
        self.assertEqual(receivedHttpReq['from'], [basePdu.params['source_addr']])
        self.assertEqual(receivedHttpReq['to'], [basePdu.params['destination_addr']])
        self.assertEqual(receivedHttpReq['content'], [pdu_part1.params['message_payload'] + pdu_part2.params['message_payload'] + pdu_part3.params['message_payload']])
        self.assertEqual(receivedHttpReq['origin-connector'], [source_connector.cid])

        # Disconnector from SMSC
        yield self.stopConnector(source_connector)

class DataSmSmppThrowingTestCases(RouterPBProxy, SMPPClientTestCases, SubmitSmTestCaseTools):

    @defer.inlineCallbacks
    def setUp(self):
        yield SMPPClientTestCases.setUp(self)

        # Initiating config objects without any filename
        # will lead to setting defaults and that's what we
        # need to run the tests
        deliverSmThrowerConfigInstance = deliverSmThrowerConfig()

        # Launch the deliverSmThrower
        self.deliverSmThrower = deliverSmThrower()
        self.deliverSmThrower.setConfig(deliverSmThrowerConfigInstance)

        # Add the broker to the deliverSmThrower
        yield self.deliverSmThrower.addAmqpBroker(self.amqpBroker)

        # Add SMPPs factory to DLRThrower
        self.deliverSmThrower.addSmpps(self.smpps_factory)

    @defer.inlineCallbacks
    def tearDown(self):
        yield self.deliverSmThrower.stopService()
        yield SMPPClientTestCases.tearDown(self)

    @defer.inlineCallbacks
    def prepareRoutingsAndStartConnector(self):
        yield SubmitSmTestCaseTools.prepareRoutingsAndStartConnector(self)

        # Add a MO Route to a SmppServerSystemIdConnector
        c2_destination = SmppServerSystemIdConnector(system_id = self.smppc_factory.config.username)
        # Set the route
        yield self.moroute_add(DefaultRoute(c2_destination), 0)

    @defer.inlineCallbacks
    def triggerDataSmFromSMSC(self, pdus):
        for pdu in pdus:
            yield self.SMSCPort.factory.lastClient.trigger_deliver_sm(pdu)

        # Wait 4 seconds
        exitDeferred = defer.Deferred()
        reactor.callLater(4, exitDeferred.callback, None)
        yield exitDeferred

    @defer.inlineCallbacks
    def test_delivery_SmppClientConnector(self):
        yield self.connect('127.0.0.1', self.pbPort)
        yield self.prepareRoutingsAndStartConnector()

        # Bind
        yield self.smppc_factory.connectAndBind()

        # Install mocks
        self.smppc_factory.lastProto.PDUDataRequestReceived = mock.Mock(wraps=self.smppc_factory.lastProto.PDUDataRequestReceived)

        # Send a deliver_sm from the SMSC
        yield self.triggerDataSmFromSMSC([self.DataSmPDU])

        # Run tests
        self.assertEqual(self.smppc_factory.lastProto.PDUDataRequestReceived.call_count, 1)
        # the received pdu must be our self.DataSmPDU
        received_pdu_1 = self.smppc_factory.lastProto.PDUDataRequestReceived.call_args_list[0][0][0]
        self.assertEqual(received_pdu_1.id, pdu_types.CommandId.data_sm)
        self.assertEqual(received_pdu_1.params['source_addr'], self.DataSmPDU.params['source_addr'])
        self.assertEqual(received_pdu_1.params['destination_addr'], self.DataSmPDU.params['destination_addr'])
        self.assertEqual(received_pdu_1.params['message_payload'], self.DataSmPDU.params['message_payload'])

        # Unbind and disconnect
        yield self.smppc_factory.smpp.unbindAndDisconnect()
        yield self.stopSmppClientConnectors()

    @defer.inlineCallbacks
    def test_delivery_multipart_SmppClientConnector(self):
        yield self.connect('127.0.0.1', self.pbPort)
        yield self.prepareRoutingsAndStartConnector()

        # Bind
        yield self.smppc_factory.connectAndBind()

        # Install mocks
        self.smppc_factory.lastProto.PDUDataRequestReceived = mock.Mock(wraps=self.smppc_factory.lastProto.PDUDataRequestReceived)

        # Send 10 deliver_sm from the SMSC
        for i in range(10):
            pdu = copy.copy(self.DataSmPDU)
            pdu.seqNum+= 1
            yield self.triggerDataSmFromSMSC([pdu])

        # Run tests
        self.assertEqual(self.smppc_factory.lastProto.PDUDataRequestReceived.call_count, 10)
        # the received pdus are ordered the same way they were sent
        for i in range(10):
            received_pdu = self.smppc_factory.lastProto.PDUDataRequestReceived.call_args_list[i][0][0]
            self.assertEqual(received_pdu.id, pdu_types.CommandId.data_sm)
            self.assertEqual(received_pdu.seqNum, i + 1)

        # Unbind and disconnect
        yield self.smppc_factory.smpp.unbindAndDisconnect()
        yield self.stopSmppClientConnectors()

    @defer.inlineCallbacks
    def test_long_content_delivery_SAR_SmppsConnector(self):
        yield self.connect('127.0.0.1', self.pbPort)
        yield self.prepareRoutingsAndStartConnector()

        # Bind
        yield self.smppc_factory.connectAndBind()

        # Install mocks
        self.smppc_factory.lastProto.PDUDataRequestReceived = mock.Mock(wraps=self.smppc_factory.lastProto.PDUDataRequestReceived)

        # Send a data_sm from the SMSC
        basePdu = DataSM(
            source_addr = '1234',
            destination_addr = '4567',
            message_payload = '',
            sar_total_segments = 3,
            sar_msg_ref_num = int(id_generator(size = 2, chars=string.digits)),
        )
        pdu_part1 = copy.deepcopy(basePdu)
        pdu_part2 = copy.deepcopy(basePdu)
        pdu_part3 = copy.deepcopy(basePdu)
        pdu_part1.params['message_payload'] = '__1st_part_with_153_char________________________________________________________________________________________________________________________________.'
        pdu_part1.params['sar_segment_seqnum'] = 1
        pdu_part2.params['message_payload'] = '__2nd_part_with_153_char________________________________________________________________________________________________________________________________.'
        pdu_part2.params['sar_segment_seqnum'] = 2
        pdu_part3.params['message_payload'] = '__3rd_part_end.'
        pdu_part3.params['sar_segment_seqnum'] = 3
        yield self.triggerDataSmFromSMSC([pdu_part1, pdu_part2, pdu_part3])

        # Run tests
        self.assertEqual(self.smppc_factory.lastProto.PDUDataRequestReceived.call_count, 3)
        # First received pdu
        received_pdu_1 = self.smppc_factory.lastProto.PDUDataRequestReceived.call_args_list[0][0][0]
        self.assertEqual(received_pdu_1.seqNum, 1)
        self.assertEqual(received_pdu_1.id, pdu_types.CommandId.data_sm)
        self.assertEqual(received_pdu_1.params['source_addr'], basePdu.params['source_addr'])
        self.assertEqual(received_pdu_1.params['destination_addr'], basePdu.params['destination_addr'])
        self.assertEqual(received_pdu_1.params['message_payload'], pdu_part1.params['message_payload'])
        self.assertEqual(received_pdu_1.params['sar_segment_seqnum'], pdu_part1.params['sar_segment_seqnum'])
        # Second received pdu
        received_pdu_2 = self.smppc_factory.lastProto.PDUDataRequestReceived.call_args_list[1][0][0]
        self.assertEqual(received_pdu_2.seqNum, 2)
        self.assertEqual(received_pdu_2.id, pdu_types.CommandId.data_sm)
        self.assertEqual(received_pdu_2.params['source_addr'], basePdu.params['source_addr'])
        self.assertEqual(received_pdu_2.params['destination_addr'], basePdu.params['destination_addr'])
        self.assertEqual(received_pdu_2.params['message_payload'], pdu_part2.params['message_payload'])
        self.assertEqual(received_pdu_2.params['sar_segment_seqnum'], pdu_part2.params['sar_segment_seqnum'])
        self.assertNotEqual(received_pdu_2.params['message_payload'], received_pdu_1.params['message_payload'])
        self.assertNotEqual(received_pdu_2.params['sar_segment_seqnum'], received_pdu_1.params['sar_segment_seqnum'])
        # Third received pdu
        received_pdu_3 = self.smppc_factory.lastProto.PDUDataRequestReceived.call_args_list[2][0][0]
        self.assertEqual(received_pdu_3.seqNum, 3)
        self.assertEqual(received_pdu_3.id, pdu_types.CommandId.data_sm)
        self.assertEqual(received_pdu_3.params['source_addr'], basePdu.params['source_addr'])
        self.assertEqual(received_pdu_3.params['destination_addr'], basePdu.params['destination_addr'])
        self.assertEqual(received_pdu_3.params['message_payload'], pdu_part3.params['message_payload'])
        self.assertEqual(received_pdu_3.params['sar_segment_seqnum'], pdu_part3.params['sar_segment_seqnum'])
        self.assertNotEqual(received_pdu_3.params['message_payload'], received_pdu_2.params['message_payload'])
        self.assertNotEqual(received_pdu_3.params['sar_segment_seqnum'], received_pdu_2.params['sar_segment_seqnum'])

        # Unbind and disconnect
        yield self.smppc_factory.smpp.unbindAndDisconnect()
        yield self.stopSmppClientConnectors()

    @defer.inlineCallbacks
    def test_long_content_delivery_UDH_SmppsConnector(self):
        yield self.connect('127.0.0.1', self.pbPort)
        yield self.prepareRoutingsAndStartConnector()

        # Bind
        yield self.smppc_factory.connectAndBind()

        # Install mocks
        self.smppc_factory.lastProto.PDUDataRequestReceived = mock.Mock(wraps=self.smppc_factory.lastProto.PDUDataRequestReceived)

        # Build a UDH
        baseUdh = []
        baseUdh.append(struct.pack('!B', 5)) # Length of User Data Header
        baseUdh.append(struct.pack('!B', 0)) # Information Element Identifier, equal to 00 (Concatenated short messages, 8-bit reference number)
        baseUdh.append(struct.pack('!B', 3)) # Length of the header, excluding the first two fields; equal to 03
        baseUdh.append(struct.pack('!B', int(id_generator(size = 2, chars=string.digits)))) # msg_ref_num
        baseUdh.append(struct.pack('!B', 3)) # total_segments

        # Send a data_sm from the SMSC
        basePdu = DataSM(
            source_addr = '1234',
            destination_addr = '4567',
            message_payload = '',
            esm_class = EsmClass(EsmClassMode.DEFAULT, EsmClassType.DEFAULT, [EsmClassGsmFeatures.UDHI_INDICATOR_SET]),
        )
        pdu_part1 = copy.deepcopy(basePdu)
        udh_part1 = copy.deepcopy(baseUdh)
        pdu_part2 = copy.deepcopy(basePdu)
        udh_part2 = copy.deepcopy(baseUdh)
        pdu_part3 = copy.deepcopy(basePdu)
        udh_part3 = copy.deepcopy(baseUdh)
        udh_part1.append(struct.pack('!B', 1)) # segment_seqnum
        pdu_part1.params['more_messages_to_send'] = MoreMessagesToSend.MORE_MESSAGES
        pdu_part1.params['message_payload'] = ''.join(udh_part1)+'__1st_part_with_153_char________________________________________________________________________________________________________________________________.'
        udh_part2.append(struct.pack('!B', 2)) # segment_seqnum
        pdu_part2.params['more_messages_to_send'] = MoreMessagesToSend.MORE_MESSAGES
        pdu_part2.params['message_payload'] = ''.join(udh_part2)+'__2nd_part_with_153_char________________________________________________________________________________________________________________________________.'
        udh_part3.append(struct.pack('!B', 3)) # segment_seqnum
        pdu_part3.params['more_messages_to_send'] = MoreMessagesToSend.NO_MORE_MESSAGES
        pdu_part3.params['message_payload'] = ''.join(udh_part3)+'__3rd_part_end.'
        yield self.triggerDataSmFromSMSC([pdu_part1, pdu_part2, pdu_part3])

        # Run tests
        self.assertEqual(self.smppc_factory.lastProto.PDUDataRequestReceived.call_count, 3)
        # First received pdu
        received_pdu_1 = self.smppc_factory.lastProto.PDUDataRequestReceived.call_args_list[0][0][0]
        self.assertEqual(received_pdu_1.seqNum, 1)
        self.assertEqual(received_pdu_1.id, pdu_types.CommandId.data_sm)
        self.assertEqual(received_pdu_1.params['source_addr'], basePdu.params['source_addr'])
        self.assertEqual(received_pdu_1.params['destination_addr'], basePdu.params['destination_addr'])
        self.assertEqual(received_pdu_1.params['esm_class'], basePdu.params['esm_class'])
        self.assertEqual(received_pdu_1.params['message_payload'][6:], pdu_part1.params['message_payload'][6:])
        # Second received pdu
        received_pdu_2 = self.smppc_factory.lastProto.PDUDataRequestReceived.call_args_list[1][0][0]
        self.assertEqual(received_pdu_2.seqNum, 2)
        self.assertEqual(received_pdu_2.id, pdu_types.CommandId.data_sm)
        self.assertEqual(received_pdu_2.params['source_addr'], basePdu.params['source_addr'])
        self.assertEqual(received_pdu_2.params['destination_addr'], basePdu.params['destination_addr'])
        self.assertEqual(received_pdu_2.params['esm_class'], basePdu.params['esm_class'])
        self.assertEqual(received_pdu_2.params['message_payload'][6:], pdu_part2.params['message_payload'][6:])
        # Third received pdu
        received_pdu_3 = self.smppc_factory.lastProto.PDUDataRequestReceived.call_args_list[2][0][0]
        self.assertEqual(received_pdu_3.seqNum, 3)
        self.assertEqual(received_pdu_3.id, pdu_types.CommandId.data_sm)
        self.assertEqual(received_pdu_3.params['source_addr'], basePdu.params['source_addr'])
        self.assertEqual(received_pdu_3.params['destination_addr'], basePdu.params['destination_addr'])
        self.assertEqual(received_pdu_3.params['esm_class'], basePdu.params['esm_class'])
        self.assertEqual(received_pdu_3.params['message_payload'][6:], pdu_part3.params['message_payload'][6:])

        # Unbind and disconnect
        yield self.smppc_factory.smpp.unbindAndDisconnect()
        yield self.stopSmppClientConnectors()

    @defer.inlineCallbacks
    def test_delivery_SmppClientConnector_with_network_error_code(self):
        "Related to #117"
        yield self.connect('127.0.0.1', self.pbPort)
        yield self.prepareRoutingsAndStartConnector()

        # Bind
        yield self.smppc_factory.connectAndBind()

        # Install mocks
        self.smppc_factory.lastProto.PDUDataRequestReceived = mock.Mock(wraps=self.smppc_factory.lastProto.PDUDataRequestReceived)

        # Send a deliver_sm from the SMSC
        DeliverSmPDU = copy.deepcopy(self.DataSmPDU)
        DeliverSmPDU.params['network_error_code'] = '\x03\x00\x00'
        yield self.triggerDataSmFromSMSC([DeliverSmPDU])

        # Run tests
        self.assertEqual(self.smppc_factory.lastProto.PDUDataRequestReceived.call_count, 1)
        # the received pdu must be our DeliverSmPDU
        received_pdu_1 = self.smppc_factory.lastProto.PDUDataRequestReceived.call_args_list[0][0][0]
        self.assertEqual(received_pdu_1.id, pdu_types.CommandId.data_sm)
        self.assertEqual(received_pdu_1.params['network_error_code'], DeliverSmPDU.params['network_error_code'])

        # Unbind and disconnect
        yield self.smppc_factory.smpp.unbindAndDisconnect()
        yield self.stopSmppClientConnectors()
