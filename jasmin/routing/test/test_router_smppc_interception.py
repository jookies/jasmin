import mock
from twisted.internet import defer, reactor
from jasmin.routing.proxies import RouterPBProxy
from jasmin.routing.test.test_router_smpps import SMPPClientTestCases
from jasmin.routing.test.test_router import SubmitSmTestCaseTools
from jasmin.routing.configs import deliverSmThrowerConfig
from jasmin.routing.throwers import deliverSmThrower
from jasmin.routing.jasminApi import *
from jasmin.routing.Routes import DefaultRoute
from jasmin.vendor.smpp.pdu import pdu_types
from jasmin.routing.Interceptors import DefaultInterceptor
from jasmin.routing.jasminApi import *

@defer.inlineCallbacks
def waitFor(seconds):
    # Wait seconds
    waitDeferred = defer.Deferred()
    reactor.callLater(seconds, waitDeferred.callback, None)
    yield waitDeferred

class ProvisionWithoutInterceptorPB:
    script = 'Default script that generates a syntax error !'

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

        # Connect to RouterPB
        yield self.connect('127.0.0.1', self.pbPort)
        # Provision mt interceptor
        self.mo_interceptor = MOInterceptorScript(self.script)
        yield self.mointerceptor_add(DefaultInterceptor(self.mo_interceptor), 0)
        # Disconnect from RouterPB
        self.disconnect()

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
    def triggerDeliverSmFromSMSC(self, pdus):
        for pdu in pdus:
            yield self.SMSCPort.factory.lastClient.trigger_deliver_sm(pdu)

        # Wait 2 seconds
        yield waitFor(2)

class SmppcDeliverSmNoInterceptorPBTestCases(ProvisionWithoutInterceptorPB, RouterPBProxy, SMPPClientTestCases, SubmitSmTestCaseTools):

    @defer.inlineCallbacks
    def test_interceptorpb_not_set(self):
        # Work in progress
        raise NotImplementedError
        
        yield self.connect('127.0.0.1', self.pbPort)
        yield self.prepareRoutingsAndStartConnector()

        # Bind
        yield self.smppc_factory.connectAndBind()

        # Install mocks
        self.smppc_factory.lastProto.PDUDataRequestReceived = mock.Mock(wraps=self.smppc_factory.lastProto.PDUDataRequestReceived)

        # Send a deliver_sm from the SMSC
        yield self.triggerDeliverSmFromSMSC([self.DeliverSmPDU])

        # Run tests on downstream smpp client
        self.assertEqual(self.smppc_factory.lastProto.PDUDataRequestReceived.call_count, 1)
        received_pdu_1 = self.smppc_factory.lastProto.PDUDataRequestReceived.call_args_list[0][0][0]
        self.assertEqual(received_pdu_1.id, pdu_types.CommandId.deliver_sm)
        # Run tests on upstream smpp client
        self.assertEqual(len(self.SMSCPort.factory.lastClient.pduRecords), 2)
        sent_back_resp = self.SMSCPort.factory.lastClient.pduRecords[1]
        self.assertEqual(sent_back_resp.id, pdu_types.CommandId.deliver_sm_resp)

        # Unbind and disconnect
        yield self.smppc_factory.smpp.unbindAndDisconnect()
        yield self.stopSmppClientConnectors()
