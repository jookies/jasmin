import mock
from twisted.cred import portal
from twisted.cred.checkers import AllowAnonymousAccess, InMemoryUsernamePasswordDatabaseDontUse
from twisted.internet import defer, reactor
from twisted.spread import pb

from jasmin.interceptor.configs import InterceptorPBConfig, InterceptorPBClientConfig
from jasmin.interceptor.interceptor import InterceptorPB
from jasmin.interceptor.proxies import InterceptorPBProxy
from jasmin.protocols.smpp.stats import SMPPClientStatsCollector
from jasmin.routing.Filters import TagFilter
from jasmin.routing.Interceptors import DefaultInterceptor
from jasmin.routing.Routes import DefaultRoute
from jasmin.routing.Routes import StaticMORoute
from jasmin.routing.configs import deliverSmThrowerConfig
from jasmin.routing.jasminApi import *
from jasmin.routing.proxies import RouterPBProxy
from jasmin.routing.test.test_router import SubmitSmTestCaseTools
from jasmin.routing.test.test_router_smpps import SMPPClientTestCases
from jasmin.routing.throwers import deliverSmThrower
from jasmin.tools.cred.portal import JasminPBRealm
from jasmin.tools.spread.pb import JasminPBPortalRoot
from jasmin.vendor.smpp.pdu import pdu_types


@defer.inlineCallbacks
def waitFor(seconds):
    # Wait seconds
    waitDeferred = defer.Deferred()
    reactor.callLater(seconds, waitDeferred.callback, None)
    yield waitDeferred


class ProvisionWithoutInterceptorPB(object):
    script = 'Default script that generates a syntax error !'

    @defer.inlineCallbacks
    def setUp(self):
        if hasattr(self, 'ipb_client'):
            yield SMPPClientTestCases.setUp(self, interceptorpb_client=self.ipb_client)
        else:
            yield SMPPClientTestCases.setUp(self)

        # Initiating config objects without any filename
        # will lead to setting defaults and that's what we
        # need to run the tests
        deliverSmThrowerConfigInstance = deliverSmThrowerConfig()

        # Launch the deliverSmThrower
        self.deliverSmThrower = deliverSmThrower(deliverSmThrowerConfigInstance)

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
        c2_destination = SmppServerSystemIdConnector(system_id=self.smppc_factory.config.username)
        # Set the route
        yield self.moroute_add(DefaultRoute(c2_destination), 0)

        # Get stats singletons
        self.stats_smppc = SMPPClientStatsCollector().get(self.c1.cid)

    @defer.inlineCallbacks
    def triggerDeliverSmFromSMSC(self, pdus):
        for pdu in pdus:
            yield self.SMSCPort.factory.lastClient.trigger_deliver_sm(pdu)

        # Wait 2 seconds
        yield waitFor(2)


class ProvisionInterceptorPB(ProvisionWithoutInterceptorPB):
    @defer.inlineCallbacks
    def setUp(self, authentication=False):
        "This will launch InterceptorPB and provide a client connected to it."
        # Launch a client in a disconnected state
        # it will be connected on demand through the self.ipb_connect() method
        self.ipb_client = InterceptorPBProxy()

        yield ProvisionWithoutInterceptorPB.setUp(self)

        # Initiating config objects without any filename
        # will lead to setting defaults and that's what we
        # need to run the tests
        InterceptorPBConfigInstance = InterceptorPBConfig()

        # Launch the interceptor server
        pbInterceptor_factory = InterceptorPB(InterceptorPBConfigInstance)

        # Configure portal
        p = portal.Portal(JasminPBRealm(pbInterceptor_factory))
        if not authentication:
            p.registerChecker(AllowAnonymousAccess())
        else:
            c = InMemoryUsernamePasswordDatabaseDontUse()
            c.addUser('test_user', md5('test_password').digest())
            p.registerChecker(c)
        jPBPortalRoot = JasminPBPortalRoot(p)
        self.pbInterceptor_server = reactor.listenTCP(0, pb.PBServerFactory(jPBPortalRoot))
        self.pbInterceptor_port = self.pbInterceptor_server.getHost().port

    @defer.inlineCallbacks
    def ipb_connect(self, config=None):
        if config is None:
            # Default test config (username is None for anonymous connection)
            config = InterceptorPBClientConfig()
            config.username = None
            config.port = self.pbInterceptor_port

        if config.username is not None:
            yield self.ipb_client.connect(
                config.host,
                config.port,
                config.username,
                config.password
            )
        else:
            yield self.ipb_client.connect(
                config.host,
                config.port
            )

    @defer.inlineCallbacks
    def tearDown(self):
        yield ProvisionWithoutInterceptorPB.tearDown(self)

        # Disconnect ipb and shutdown pbInterceptor_server
        if self.ipb_client.isConnected:
            self.ipb_client.disconnect()
        yield self.pbInterceptor_server.stopListening()


class SmppcDeliverSmNoInterceptorPBTestCases(ProvisionWithoutInterceptorPB, RouterPBProxy, SMPPClientTestCases,
                                             SubmitSmTestCaseTools):
    @defer.inlineCallbacks
    def test_interceptorpb_not_set(self):
        yield self.connect('127.0.0.1', self.pbPort)
        yield self.prepareRoutingsAndStartConnector()

        _ic = self.stats_smppc.get('interceptor_count')
        _iec = self.stats_smppc.get('interceptor_error_count')

        # Bind
        yield self.smppc_factory.connectAndBind()

        # Install mocks
        self.smppc_factory.lastProto.PDUDataRequestReceived = mock.Mock(
            wraps=self.smppc_factory.lastProto.PDUDataRequestReceived)

        # Send a deliver_sm from the SMSC
        yield self.triggerDeliverSmFromSMSC([self.DeliverSmPDU])

        # Run tests on downstream smpp client
        self.assertEqual(self.smppc_factory.lastProto.PDUDataRequestReceived.call_count, 0)
        # Run tests on upstream smpp client
        self.assertEqual(len(self.SMSCPort.factory.lastClient.pduRecords), 2)
        sent_back_resp = self.SMSCPort.factory.lastClient.pduRecords[1]
        self.assertEqual(sent_back_resp.id, pdu_types.CommandId.deliver_sm_resp)
        self.assertEqual(sent_back_resp.status, pdu_types.CommandStatus.ESME_RSYSERR)
        self.assertEqual(_ic, self.stats_smppc.get('interceptor_count'))
        self.assertEqual(_iec + 1, self.stats_smppc.get('interceptor_error_count'))

        # Unbind and disconnect
        yield self.smppc_factory.smpp.unbindAndDisconnect()
        yield self.stopSmppClientConnectors()


class SmppcDeliverSmInterceptorPBTestCases(ProvisionInterceptorPB, RouterPBProxy, SMPPClientTestCases,
                                           SubmitSmTestCaseTools):
    update_message_sript = "routable.pdu.params['short_message'] = 'Intercepted message'"
    raise_any_exception = "raise Exception('Exception from interceptor script')"
    return_ESME_RINVESMCLASS = "smpp_status = 67"
    return_HTTP_300 = "http_status = 300"

    @defer.inlineCallbacks
    def test_interceptorpb_not_connected(self):
        yield self.connect('127.0.0.1', self.pbPort)
        yield self.prepareRoutingsAndStartConnector()

        _ic = self.stats_smppc.get('interceptor_count')
        _iec = self.stats_smppc.get('interceptor_error_count')

        # Bind
        yield self.smppc_factory.connectAndBind()

        # Install mocks
        self.smppc_factory.lastProto.PDUDataRequestReceived = mock.Mock(
            wraps=self.smppc_factory.lastProto.PDUDataRequestReceived)

        # Send a deliver_sm from the SMSC
        yield self.triggerDeliverSmFromSMSC([self.DeliverSmPDU])

        # Run tests on downstream smpp client
        self.assertEqual(self.smppc_factory.lastProto.PDUDataRequestReceived.call_count, 0)
        # Run tests on upstream smpp client
        self.assertEqual(len(self.SMSCPort.factory.lastClient.pduRecords), 2)
        sent_back_resp = self.SMSCPort.factory.lastClient.pduRecords[1]
        self.assertEqual(sent_back_resp.id, pdu_types.CommandId.deliver_sm_resp)
        self.assertEqual(sent_back_resp.status, pdu_types.CommandStatus.ESME_RSYSERR)
        self.assertEqual(_ic, self.stats_smppc.get('interceptor_count'))
        self.assertEqual(_iec + 1, self.stats_smppc.get('interceptor_error_count'))

        # Unbind and disconnect
        yield self.smppc_factory.smpp.unbindAndDisconnect()
        yield self.stopSmppClientConnectors()

    @defer.inlineCallbacks
    def test_syntax_error(self):
        # Connect to InterceptorPB
        yield self.ipb_connect()

        yield self.connect('127.0.0.1', self.pbPort)
        yield self.prepareRoutingsAndStartConnector()

        _ic = self.stats_smppc.get('interceptor_count')
        _iec = self.stats_smppc.get('interceptor_error_count')

        # Bind
        yield self.smppc_factory.connectAndBind()

        # Install mocks
        self.smppc_factory.lastProto.PDUDataRequestReceived = mock.Mock(
            wraps=self.smppc_factory.lastProto.PDUDataRequestReceived)

        # Send a deliver_sm from the SMSC
        yield self.triggerDeliverSmFromSMSC([self.DeliverSmPDU])

        # Run tests on downstream smpp client
        self.assertEqual(self.smppc_factory.lastProto.PDUDataRequestReceived.call_count, 0)
        # Run tests on upstream smpp client
        self.assertEqual(len(self.SMSCPort.factory.lastClient.pduRecords), 2)
        sent_back_resp = self.SMSCPort.factory.lastClient.pduRecords[1]
        self.assertEqual(sent_back_resp.id, pdu_types.CommandId.deliver_sm_resp)
        self.assertEqual(sent_back_resp.status, pdu_types.CommandStatus.ESME_RSYSERR)
        self.assertEqual(_ic, self.stats_smppc.get('interceptor_count'))
        self.assertEqual(_iec + 1, self.stats_smppc.get('interceptor_error_count'))

        # Unbind and disconnect
        yield self.smppc_factory.smpp.unbindAndDisconnect()
        yield self.stopSmppClientConnectors()

    @defer.inlineCallbacks
    def test_success(self):
        # Re-provision interceptor with correct script
        # Connect to RouterPB
        yield self.connect('127.0.0.1', self.pbPort)
        mo_interceptor = MOInterceptorScript(self.update_message_sript)
        yield self.mointerceptor_add(DefaultInterceptor(mo_interceptor), 0)
        # Disconnect from RouterPB
        self.disconnect()

        # Connect to InterceptorPB
        yield self.ipb_connect()

        yield self.connect('127.0.0.1', self.pbPort)
        yield self.prepareRoutingsAndStartConnector()

        _ic = self.stats_smppc.get('interceptor_count')
        _iec = self.stats_smppc.get('interceptor_error_count')

        # Bind
        yield self.smppc_factory.connectAndBind()

        # Install mocks
        self.smppc_factory.lastProto.PDUDataRequestReceived = mock.Mock(
            wraps=self.smppc_factory.lastProto.PDUDataRequestReceived)

        # Send a deliver_sm from the SMSC
        yield self.triggerDeliverSmFromSMSC([self.DeliverSmPDU])

        # Run tests on downstream smpp client
        self.assertEqual(self.smppc_factory.lastProto.PDUDataRequestReceived.call_count, 1)
        received_pdu_1 = self.smppc_factory.lastProto.PDUDataRequestReceived.call_args_list[0][0][0]
        self.assertEqual(received_pdu_1.id, pdu_types.CommandId.deliver_sm)
        self.assertEqual(received_pdu_1.params['short_message'], 'Intercepted message')
        # Run tests on upstream smpp client
        self.assertEqual(len(self.SMSCPort.factory.lastClient.pduRecords), 2)
        sent_back_resp = self.SMSCPort.factory.lastClient.pduRecords[1]
        self.assertEqual(sent_back_resp.id, pdu_types.CommandId.deliver_sm_resp)
        self.assertEqual(sent_back_resp.status, pdu_types.CommandStatus.ESME_ROK)
        self.assertEqual(_ic + 1, self.stats_smppc.get('interceptor_count'))
        self.assertEqual(_iec, self.stats_smppc.get('interceptor_error_count'))

        # Unbind and disconnect
        yield self.smppc_factory.smpp.unbindAndDisconnect()
        yield self.stopSmppClientConnectors()

    @defer.inlineCallbacks
    def test_any_exception_from_script(self):
        # Re-provision interceptor with script raising an exception
        # Connect to RouterPB
        yield self.connect('127.0.0.1', self.pbPort)
        mo_interceptor = MOInterceptorScript(self.raise_any_exception)
        yield self.mointerceptor_add(DefaultInterceptor(mo_interceptor), 0)
        # Disconnect from RouterPB
        self.disconnect()

        # Connect to InterceptorPB
        yield self.ipb_connect()

        yield self.connect('127.0.0.1', self.pbPort)
        yield self.prepareRoutingsAndStartConnector()

        _ic = self.stats_smppc.get('interceptor_count')
        _iec = self.stats_smppc.get('interceptor_error_count')

        # Bind
        yield self.smppc_factory.connectAndBind()

        # Install mocks
        self.smppc_factory.lastProto.PDUDataRequestReceived = mock.Mock(
            wraps=self.smppc_factory.lastProto.PDUDataRequestReceived)

        # Send a deliver_sm from the SMSC
        yield self.triggerDeliverSmFromSMSC([self.DeliverSmPDU])

        # Run tests on downstream smpp client
        self.assertEqual(self.smppc_factory.lastProto.PDUDataRequestReceived.call_count, 0)
        # Run tests on upstream smpp client
        self.assertEqual(len(self.SMSCPort.factory.lastClient.pduRecords), 2)
        sent_back_resp = self.SMSCPort.factory.lastClient.pduRecords[1]
        self.assertEqual(sent_back_resp.id, pdu_types.CommandId.deliver_sm_resp)
        self.assertEqual(sent_back_resp.status, pdu_types.CommandStatus.ESME_RSYSERR)
        self.assertEqual(_ic, self.stats_smppc.get('interceptor_count'))
        self.assertEqual(_iec + 1, self.stats_smppc.get('interceptor_error_count'))

        # Unbind and disconnect
        yield self.smppc_factory.smpp.unbindAndDisconnect()
        yield self.stopSmppClientConnectors()

    @defer.inlineCallbacks
    def test_ESME_RINVESMCLASS_from_script(self):
        # Re-provision interceptor with script returning a ESME_RINVESMCLASS
        # Connect to RouterPB
        yield self.connect('127.0.0.1', self.pbPort)
        mo_interceptor = MOInterceptorScript(self.return_ESME_RINVESMCLASS)
        yield self.mointerceptor_add(DefaultInterceptor(mo_interceptor), 0)
        # Disconnect from RouterPB
        self.disconnect()

        # Connect to InterceptorPB
        yield self.ipb_connect()

        yield self.connect('127.0.0.1', self.pbPort)
        yield self.prepareRoutingsAndStartConnector()

        _ic = self.stats_smppc.get('interceptor_count')
        _iec = self.stats_smppc.get('interceptor_error_count')

        # Bind
        yield self.smppc_factory.connectAndBind()

        # Install mocks
        self.smppc_factory.lastProto.PDUDataRequestReceived = mock.Mock(
            wraps=self.smppc_factory.lastProto.PDUDataRequestReceived)

        # Send a deliver_sm from the SMSC
        yield self.triggerDeliverSmFromSMSC([self.DeliverSmPDU])

        # Run tests on downstream smpp client
        self.assertEqual(self.smppc_factory.lastProto.PDUDataRequestReceived.call_count, 0)
        # Run tests on upstream smpp client
        self.assertEqual(len(self.SMSCPort.factory.lastClient.pduRecords), 2)
        sent_back_resp = self.SMSCPort.factory.lastClient.pduRecords[1]
        self.assertEqual(sent_back_resp.id, pdu_types.CommandId.deliver_sm_resp)
        self.assertEqual(sent_back_resp.status, pdu_types.CommandStatus.ESME_RINVESMCLASS)
        self.assertEqual(_ic, self.stats_smppc.get('interceptor_count'))
        self.assertEqual(_iec + 1, self.stats_smppc.get('interceptor_error_count'))

        # Unbind and disconnect
        yield self.smppc_factory.smpp.unbindAndDisconnect()
        yield self.stopSmppClientConnectors()

    @defer.inlineCallbacks
    def test_HTTP_300_from_script(self):
        "Will ensure if script defines only http error it will implicitly cause a smpp ESME_RUNKNOWNERR error"

        # Re-provision interceptor with script returning a HTTP 300
        # Connect to RouterPB
        yield self.connect('127.0.0.1', self.pbPort)
        mo_interceptor = MOInterceptorScript(self.return_HTTP_300)
        yield self.mointerceptor_add(DefaultInterceptor(mo_interceptor), 0)
        # Disconnect from RouterPB
        self.disconnect()

        # Connect to InterceptorPB
        yield self.ipb_connect()

        yield self.connect('127.0.0.1', self.pbPort)
        yield self.prepareRoutingsAndStartConnector()

        _ic = self.stats_smppc.get('interceptor_count')
        _iec = self.stats_smppc.get('interceptor_error_count')

        # Bind
        yield self.smppc_factory.connectAndBind()

        # Install mocks
        self.smppc_factory.lastProto.PDUDataRequestReceived = mock.Mock(
            wraps=self.smppc_factory.lastProto.PDUDataRequestReceived)

        # Send a deliver_sm from the SMSC
        yield self.triggerDeliverSmFromSMSC([self.DeliverSmPDU])

        # Run tests on downstream smpp client
        self.assertEqual(self.smppc_factory.lastProto.PDUDataRequestReceived.call_count, 0)
        # Run tests on upstream smpp client
        self.assertEqual(len(self.SMSCPort.factory.lastClient.pduRecords), 2)
        sent_back_resp = self.SMSCPort.factory.lastClient.pduRecords[1]
        self.assertEqual(sent_back_resp.id, pdu_types.CommandId.deliver_sm_resp)
        self.assertEqual(sent_back_resp.status, pdu_types.CommandStatus.ESME_RUNKNOWNERR)
        self.assertEqual(_ic, self.stats_smppc.get('interceptor_count'))
        self.assertEqual(_iec + 1, self.stats_smppc.get('interceptor_error_count'))

        # Unbind and disconnect
        yield self.smppc_factory.smpp.unbindAndDisconnect()
        yield self.stopSmppClientConnectors()

    @defer.inlineCallbacks
    def test_tagging(self):
        """Refs #495
        Will tag message inside interceptor script and assert
        routing based tagfilter were correctly done
        """
        yield self.connect('127.0.0.1', self.pbPort)
        mo_interceptor = MOInterceptorScript("routable.addTag(10)")
        yield self.mointerceptor_add(DefaultInterceptor(mo_interceptor), 0)
        # Disconnect from RouterPB
        self.disconnect()

        # Connect to InterceptorPB
        yield self.ipb_connect()

        yield self.connect('127.0.0.1', self.pbPort)
        yield self.prepareRoutingsAndStartConnector()

        # Change routing rules by shadowing (high order value) default route with a
        # static route having a tagfilter
        c2_destination = SmppServerSystemIdConnector(system_id=self.smppc_factory.config.username)
        yield self.moroute_flush()
        yield self.moroute_add(StaticMORoute([TagFilter(10)], c2_destination), 1000)

        # Bind
        yield self.smppc_factory.connectAndBind()

        # Install mocks
        self.smppc_factory.lastProto.PDUDataRequestReceived = mock.Mock(
            wraps=self.smppc_factory.lastProto.PDUDataRequestReceived)

        # Send a deliver_sm from the SMSC
        yield self.triggerDeliverSmFromSMSC([self.DeliverSmPDU])

        # Run tests on downstream smpp client
        self.assertEqual(self.smppc_factory.lastProto.PDUDataRequestReceived.call_count, 1)
        received_pdu_1 = self.smppc_factory.lastProto.PDUDataRequestReceived.call_args_list[0][0][0]
        self.assertEqual(received_pdu_1.id, pdu_types.CommandId.deliver_sm)

        # Unbind and disconnect
        yield self.smppc_factory.smpp.unbindAndDisconnect()
        yield self.stopSmppClientConnectors()
