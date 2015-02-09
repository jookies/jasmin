import mock
import copy
import time
import string
from twisted.internet import reactor, defer
from twisted.web import server
from jasmin.routing.test.http_server import AckServer
from jasmin.routing.jasminApi import *
from jasmin.routing.test.test_router import (SMPPClientManagerPBTestCase, LastClientFactory,
                                            id_generator)
from jasmin.protocols.smpp.test.smsc_simulator import *
from jasmin.routing.proxies import RouterPBProxy
from jasmin.protocols.smpp.configs import SMPPClientConfig
from jasmin.routing.configs import deliverSmThrowerConfig
from jasmin.routing.throwers import deliverSmThrower
from jasmin.vendor.smpp.pdu.operations import DeliverSM
from jasmin.routing.Routes import DefaultRoute

class DeliverSmSMSCTestCase(SMPPClientManagerPBTestCase):
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
        
class DeliverSmThrowingTestCases(RouterPBProxy, DeliverSmSMSCTestCase):
    
    @defer.inlineCallbacks
    def setUp(self):
        yield DeliverSmSMSCTestCase.setUp(self)
        
        # Start http servers
        self.AckServerResource = AckServer()
        self.AckServer = reactor.listenTCP(0, server.Site(self.AckServerResource))
        
        # Initiating config objects without any filename
        # will lead to setting defaults and that's what we
        # need to run the tests
        deliverSmThrowerConfigInstance = deliverSmThrowerConfig()
        # Lower the timeout config to pass the timeout tests quickly
        deliverSmThrowerConfigInstance.timeout = 2
        deliverSmThrowerConfigInstance.retryDelay = 1
        deliverSmThrowerConfigInstance.maxRetries = 2
        
        # Launch the deliverSmThrower
        self.deliverSmThrower = deliverSmThrower()
        self.deliverSmThrower.setConfig(deliverSmThrowerConfigInstance)
        
        # Add the broker to the deliverSmThrower
        yield self.deliverSmThrower.addAmqpBroker(self.amqpBroker)

    @defer.inlineCallbacks
    def tearDown(self):
        yield self.AckServer.stopListening()
        yield self.deliverSmThrower.stopService()
        yield DeliverSmSMSCTestCase.tearDown(self)
        
    @defer.inlineCallbacks
    def prepareRoutingsAndStartConnector(self, connector):
        self.AckServerResource.render_GET = mock.Mock(wraps=self.AckServerResource.render_GET)

        # Prepare for routing
        connector.port = self.SMSCPort.getHost().port
        c2_destination = HttpConnector(id_generator(), 'http://127.0.0.1:%s/send' % self.AckServer.getHost().port)
        # Set the route
        yield self.moroute_add(DefaultRoute(c2_destination), 0)
        
        # Now we'll create the connector 1
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
    def triggerDeliverSmFromSMSC(self, pdus):
        for pdu in pdus:
            yield self.SMSCPort.factory.lastClient.trigger_deliver_sm(pdu)

        # Wait 2 seconds
        exitDeferred = defer.Deferred()
        reactor.callLater(2, exitDeferred.callback, None)
        yield exitDeferred

    @defer.inlineCallbacks
    def test_delivery_HttpConnector(self):
        yield self.connect('127.0.0.1', self.pbPort)
        # Connect to SMSC
        source_connector = Connector(id_generator())
        yield self.prepareRoutingsAndStartConnector(source_connector)
        
        # Send a deliver_sm from the SMSC
        pdu = DeliverSM(
            source_addr='1234',
            destination_addr='4567',
            short_message='any content',
        )
        yield self.triggerDeliverSmFromSMSC([pdu])

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
        self.assertEqual(receivedHttpReq['content'], [pdu.params['short_message']])
        self.assertEqual(receivedHttpReq['origin-connector'], [source_connector.cid])

        # Disconnector from SMSC
        yield self.stopConnector(source_connector)

    @defer.inlineCallbacks
    def test_long_content_delivery_SAR_HttpConnector(self):
        yield self.connect('127.0.0.1', self.pbPort)
        # Connect to SMSC
        #source_connector = Connector(id_generator())
        source_connector = Connector(id_generator())
        yield self.prepareRoutingsAndStartConnector(source_connector)
        
        # Send a deliver_sm from the SMSC
        basePdu = DeliverSM(
            source_addr = '1234',
            destination_addr = '4567',
            short_message = '',
            sar_total_segments = 3,
            sar_msg_ref_num = int(id_generator(size = 2, chars=string.digits)),
        )
        pdu_part1 = copy.deepcopy(basePdu)
        pdu_part2 = copy.deepcopy(basePdu)
        pdu_part3 = copy.deepcopy(basePdu)
        pdu_part1.params['short_message'] = '__1st_part_with_153_char________________________________________________________________________________________________________________________________.'
        pdu_part1.params['sar_segment_seqnum'] = 1
        pdu_part2.params['short_message'] = '__2nd_part_with_153_char________________________________________________________________________________________________________________________________.'
        pdu_part2.params['sar_segment_seqnum'] = 2
        pdu_part3.params['short_message'] = '__3rd_part_end.'
        pdu_part3.params['sar_segment_seqnum'] = 3
        yield self.triggerDeliverSmFromSMSC([pdu_part1, pdu_part2, pdu_part3])

        # Run tests
        # Destination connector must receive the message one time (no retries)
        self.assertEqual(self.AckServerResource.render_GET.call_count, 1)
        # Assert received args
        receivedHttpReq = self.AckServerResource.last_request.args
        self.assertEqual(len(receivedHttpReq), 7)
        self.assertEqual(receivedHttpReq['from'], [basePdu.params['source_addr']])
        self.assertEqual(receivedHttpReq['to'], [basePdu.params['destination_addr']])
        self.assertEqual(receivedHttpReq['content'], [pdu_part1.params['short_message'] + pdu_part2.params['short_message'] + pdu_part3.params['short_message']])
        self.assertEqual(receivedHttpReq['origin-connector'], [source_connector.cid])

        # Disconnector from SMSC
        yield self.stopConnector(source_connector)

    @defer.inlineCallbacks
    def test_long_content_delivery_UDH_HttpConnector(self):
        yield self.connect('127.0.0.1', self.pbPort)
        # Connect to SMSC
        #source_connector = Connector(id_generator())
        source_connector = Connector(id_generator())
        yield self.prepareRoutingsAndStartConnector(source_connector)
        
        # Build a UDH
        baseUdh = []
        baseUdh.append(struct.pack('!B', 5)) # Length of User Data Header
        baseUdh.append(struct.pack('!B', 0)) # Information Element Identifier, equal to 00 (Concatenated short messages, 8-bit reference number)
        baseUdh.append(struct.pack('!B', 3)) # Length of the header, excluding the first two fields; equal to 03
        baseUdh.append(struct.pack('!B', int(id_generator(size = 2, chars=string.digits)))) # msg_ref_num
        baseUdh.append(struct.pack('!B', 3)) # total_segments

        # Send a deliver_sm from the SMSC
        basePdu = DeliverSM(
            source_addr = '1234',
            destination_addr = '4567',
            short_message = '',
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
        pdu_part1.params['short_message'] = ''.join(udh_part1)+'__1st_part_with_153_char________________________________________________________________________________________________________________________________.'
        udh_part2.append(struct.pack('!B', 2)) # segment_seqnum
        pdu_part2.params['more_messages_to_send'] = MoreMessagesToSend.MORE_MESSAGES
        pdu_part2.params['short_message'] = ''.join(udh_part2)+'__2nd_part_with_153_char________________________________________________________________________________________________________________________________.'
        udh_part3.append(struct.pack('!B', 3)) # segment_seqnum
        pdu_part3.params['more_messages_to_send'] = MoreMessagesToSend.NO_MORE_MESSAGES
        pdu_part3.params['short_message'] = ''.join(udh_part3)+'__3rd_part_end.'
        yield self.triggerDeliverSmFromSMSC([pdu_part1, pdu_part2, pdu_part3])

        # Run tests
        # Destination connector must receive the message one time (no retries)
        self.assertEqual(self.AckServerResource.render_GET.call_count, 1)
        # Assert received args
        receivedHttpReq = self.AckServerResource.last_request.args
        self.assertEqual(len(receivedHttpReq), 7)
        self.assertEqual(receivedHttpReq['from'], [basePdu.params['source_addr']])
        self.assertEqual(receivedHttpReq['to'], [basePdu.params['destination_addr']])
        self.assertEqual(receivedHttpReq['content'], [pdu_part1.params['short_message'][6:] + pdu_part2.params['short_message'][6:] + pdu_part3.params['short_message'][6:]])
        self.assertEqual(receivedHttpReq['origin-connector'], [source_connector.cid])

        # Disconnector from SMSC
        yield self.stopConnector(source_connector)

    @defer.inlineCallbacks
    def test_unordered_long_content_delivery_HttpConnector(self):
        yield self.connect('127.0.0.1', self.pbPort)
        # Connect to SMSC
        #source_connector = Connector(id_generator())
        source_connector = Connector(id_generator())
        yield self.prepareRoutingsAndStartConnector(source_connector)
        
        # Send a deliver_sm from the SMSC
        basePdu = DeliverSM(
            source_addr = '1234',
            destination_addr = '4567',
            short_message = '',
            sar_total_segments = 3,
            sar_msg_ref_num = int(id_generator(size = 2, chars=string.digits)),
        )
        pdu_part1 = copy.deepcopy(basePdu)
        pdu_part2 = copy.deepcopy(basePdu)
        pdu_part3 = copy.deepcopy(basePdu)
        pdu_part1.params['short_message'] = '__1st_part_with_153_char________________________________________________________________________________________________________________________________.'
        pdu_part1.params['sar_segment_seqnum'] = 1
        pdu_part2.params['short_message'] = '__2nd_part_with_153_char________________________________________________________________________________________________________________________________.'
        pdu_part2.params['sar_segment_seqnum'] = 2
        pdu_part3.params['short_message'] = '__3rd_part_end.'
        pdu_part3.params['sar_segment_seqnum'] = 3
        yield self.triggerDeliverSmFromSMSC([pdu_part1, pdu_part3, pdu_part2])

        # Run tests
        # Destination connector must receive the message one time (no retries)
        self.assertEqual(self.AckServerResource.render_GET.call_count, 1)
        # Assert received args
        receivedHttpReq = self.AckServerResource.last_request.args
        self.assertEqual(len(receivedHttpReq), 7)
        self.assertEqual(receivedHttpReq['from'], [basePdu.params['source_addr']])
        self.assertEqual(receivedHttpReq['to'], [basePdu.params['destination_addr']])
        self.assertEqual(receivedHttpReq['content'], [pdu_part1.params['short_message'] + pdu_part2.params['short_message'] + pdu_part3.params['short_message']])
        self.assertEqual(receivedHttpReq['origin-connector'], [source_connector.cid])

        # Disconnector from SMSC
        yield self.stopConnector(source_connector)

    def test_delivery_SmppClientConnector(self):
        pass
    test_delivery_SmppClientConnector.skip = 'TODO: When SMPP Server will be implemented ?'
