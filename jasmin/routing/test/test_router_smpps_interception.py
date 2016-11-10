import mock
from twisted.cred import portal
from twisted.cred.checkers import AllowAnonymousAccess, InMemoryUsernamePasswordDatabaseDontUse
from twisted.internet import reactor, defer
from twisted.spread import pb

from jasmin.interceptor.configs import InterceptorPBConfig, InterceptorPBClientConfig
from jasmin.interceptor.interceptor import InterceptorPB
from jasmin.interceptor.proxies import InterceptorPBProxy
from jasmin.protocols.smpp.stats import SMPPServerStatsCollector
from jasmin.routing.Filters import TagFilter
from jasmin.routing.Interceptors import DefaultInterceptor
from jasmin.routing.Routes import StaticMTRoute
from jasmin.routing.jasminApi import *
from jasmin.routing.proxies import RouterPBProxy
from jasmin.routing.test.test_router import SubmitSmTestCaseTools
from jasmin.routing.test.test_router_smpps import SMPPClientTestCases
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

        # Connect to RouterPB
        yield self.connect('127.0.0.1', self.pbPort)
        # Provision mt interceptor
        self.mt_interceptor = MTInterceptorScript(self.script)
        yield self.mtinterceptor_add(DefaultInterceptor(self.mt_interceptor), 0)
        # Disconnect from RouterPB
        self.disconnect()

        # Get stats singletons
        self.stats_smpps = SMPPServerStatsCollector().get(cid=self.smpps_config.id)

    @defer.inlineCallbacks
    def tearDown(self):
        yield SMPPClientTestCases.tearDown(self)


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


class SmppsSubmitSmNoInterceptorPBTestCases(ProvisionWithoutInterceptorPB, RouterPBProxy, SMPPClientTestCases,
                                            SubmitSmTestCaseTools):
    @defer.inlineCallbacks
    def test_interceptorpb_not_set(self):
        _ic = self.stats_smpps.get('interceptor_count')
        _iec = self.stats_smpps.get('interceptor_error_count')

        yield self.connect('127.0.0.1', self.pbPort)
        yield self.prepareRoutingsAndStartConnector()

        # Bind
        yield self.smppc_factory.connectAndBind()

        # Install mocks
        self.smpps_factory.lastProto.sendPDU = mock.Mock(wraps=self.smpps_factory.lastProto.sendPDU)

        # Send a SMS MT through smpps interface
        yield self.smppc_factory.lastProto.sendDataRequest(self.SubmitSmPDU)

        # Wait 3 seconds for submit_sm_resp
        yield waitFor(3)

        # Unbind & Disconnect
        yield self.smppc_factory.smpp.unbindAndDisconnect()
        yield self.stopSmppClientConnectors()

        # Run tests on final destination smpp server (third party mocker)
        self.assertEqual(0, len(self.SMSCPort.factory.lastClient.submitRecords))
        # Run tests on Jasmin's SMPPs
        self.assertEqual(self.smpps_factory.lastProto.sendPDU.call_count, 2)
        # smpps response was a submit_sm_resp with ESME_ROK
        response_pdu = self.smpps_factory.lastProto.sendPDU.call_args_list[0][0][0]
        self.assertEqual(response_pdu.id, pdu_types.CommandId.submit_sm_resp)
        self.assertEqual(response_pdu.seqNum, 2)
        self.assertEqual(response_pdu.status, pdu_types.CommandStatus.ESME_RSYSERR)
        self.assertTrue('message_id' not in response_pdu.params)
        self.assertEqual(_ic, self.stats_smpps.get('interceptor_count'))
        self.assertEqual(_iec + 1, self.stats_smpps.get('interceptor_error_count'))


class SmppsSubmitSmInterceptionTestCases(ProvisionInterceptorPB, RouterPBProxy, SMPPClientTestCases,
                                         SubmitSmTestCaseTools):
    update_message_sript = "routable.pdu.params['short_message'] = 'Intercepted message'"
    raise_any_exception = "raise Exception('Exception from interceptor script')"
    return_ESME_RINVESMCLASS = "smpp_status = 67"
    return_HTTP_300 = "http_status = 300"

    @defer.inlineCallbacks
    def test_interceptorpb_not_connected(self):
        _ic = self.stats_smpps.get('interceptor_count')
        _iec = self.stats_smpps.get('interceptor_error_count')

        yield self.connect('127.0.0.1', self.pbPort)
        yield self.prepareRoutingsAndStartConnector()

        # Bind
        yield self.smppc_factory.connectAndBind()

        # Install mocks
        self.smpps_factory.lastProto.sendPDU = mock.Mock(wraps=self.smpps_factory.lastProto.sendPDU)

        # Send a SMS MT through smpps interface
        yield self.smppc_factory.lastProto.sendDataRequest(self.SubmitSmPDU)

        # Wait 3 seconds for submit_sm_resp
        yield waitFor(3)

        # Unbind & Disconnect
        yield self.smppc_factory.smpp.unbindAndDisconnect()
        yield self.stopSmppClientConnectors()

        # Run tests on final destination smpp server (third party mocker)
        self.assertEqual(0, len(self.SMSCPort.factory.lastClient.submitRecords))
        # Run tests on Jasmin's SMPPs
        self.assertEqual(self.smpps_factory.lastProto.sendPDU.call_count, 2)
        # smpps response was a submit_sm_resp with ESME_ROK
        response_pdu = self.smpps_factory.lastProto.sendPDU.call_args_list[0][0][0]
        self.assertEqual(response_pdu.id, pdu_types.CommandId.submit_sm_resp)
        self.assertEqual(response_pdu.seqNum, 2)
        self.assertEqual(response_pdu.status, pdu_types.CommandStatus.ESME_RSYSERR)
        self.assertTrue('message_id' not in response_pdu.params)
        self.assertEqual(_ic, self.stats_smpps.get('interceptor_count'))
        self.assertEqual(_iec + 1, self.stats_smpps.get('interceptor_error_count'))

    @defer.inlineCallbacks
    def test_syntax_error(self):
        _ic = self.stats_smpps.get('interceptor_count')
        _iec = self.stats_smpps.get('interceptor_error_count')

        # Connect to InterceptorPB
        yield self.ipb_connect()

        yield self.connect('127.0.0.1', self.pbPort)
        yield self.prepareRoutingsAndStartConnector()

        # Bind
        yield self.smppc_factory.connectAndBind()

        # Install mocks
        self.smpps_factory.lastProto.sendPDU = mock.Mock(wraps=self.smpps_factory.lastProto.sendPDU)

        # Send a SMS MT through smpps interface
        yield self.smppc_factory.lastProto.sendDataRequest(self.SubmitSmPDU)

        # Wait 3 seconds for submit_sm_resp
        yield waitFor(3)

        # Unbind & Disconnect
        yield self.smppc_factory.smpp.unbindAndDisconnect()
        yield self.stopSmppClientConnectors()

        # Run tests on final destination smpp server (third party mocker)
        self.assertEqual(0, len(self.SMSCPort.factory.lastClient.submitRecords))
        # Run tests on Jasmin's SMPPs
        self.assertEqual(self.smpps_factory.lastProto.sendPDU.call_count, 2)
        # smpps response was a submit_sm_resp with ESME_ROK
        response_pdu = self.smpps_factory.lastProto.sendPDU.call_args_list[0][0][0]
        self.assertEqual(response_pdu.id, pdu_types.CommandId.submit_sm_resp)
        self.assertEqual(response_pdu.seqNum, 2)
        self.assertEqual(response_pdu.status, pdu_types.CommandStatus.ESME_RSYSERR)
        self.assertTrue('message_id' not in response_pdu.params)
        self.assertEqual(_ic, self.stats_smpps.get('interceptor_count'))
        self.assertEqual(_iec + 1, self.stats_smpps.get('interceptor_error_count'))

    @defer.inlineCallbacks
    def test_success(self):
        _ic = self.stats_smpps.get('interceptor_count')
        _iec = self.stats_smpps.get('interceptor_error_count')

        # Re-provision interceptor with correct script
        # Connect to RouterPB
        yield self.connect('127.0.0.1', self.pbPort)
        mt_interceptor = MTInterceptorScript(self.update_message_sript)
        yield self.mtinterceptor_add(DefaultInterceptor(mt_interceptor), 0)
        # Disconnect from RouterPB
        self.disconnect()

        # Connect to InterceptorPB
        yield self.ipb_connect()

        yield self.connect('127.0.0.1', self.pbPort)
        yield self.prepareRoutingsAndStartConnector()

        # Bind
        yield self.smppc_factory.connectAndBind()

        # Install mocks
        self.smpps_factory.lastProto.sendPDU = mock.Mock(wraps=self.smpps_factory.lastProto.sendPDU)

        # Send a SMS MT through smpps interface
        yield self.smppc_factory.lastProto.sendDataRequest(self.SubmitSmPDU)

        # Wait 3 seconds for submit_sm_resp
        yield waitFor(3)

        # Unbind & Disconnect
        yield self.smppc_factory.smpp.unbindAndDisconnect()
        yield self.stopSmppClientConnectors()

        # Run tests on final destination smpp server (third party mocker)
        self.assertEqual(1, len(self.SMSCPort.factory.lastClient.submitRecords))
        # Run tests on Jasmin's SMPPs
        self.assertEqual(self.smpps_factory.lastProto.sendPDU.call_count, 2)
        # smpps response was a submit_sm_resp with ESME_ROK
        response_pdu = self.smpps_factory.lastProto.sendPDU.call_args_list[0][0][0]
        self.assertEqual(response_pdu.id, pdu_types.CommandId.submit_sm_resp)
        self.assertEqual(response_pdu.seqNum, 2)
        self.assertEqual(response_pdu.status, pdu_types.CommandStatus.ESME_ROK)
        self.assertNotEqual(None, response_pdu.params['message_id'])
        # Message content has been updated
        self.assertEqual('Intercepted message',
                         self.SMSCPort.factory.lastClient.submitRecords[0].params['short_message'])
        self.assertEqual(_ic + 1, self.stats_smpps.get('interceptor_count'))
        self.assertEqual(_iec, self.stats_smpps.get('interceptor_error_count'))

    @defer.inlineCallbacks
    def test_any_exception_from_script(self):
        _ic = self.stats_smpps.get('interceptor_count')
        _iec = self.stats_smpps.get('interceptor_error_count')

        # Re-provision interceptor with script raising an exception
        # Connect to RouterPB
        yield self.connect('127.0.0.1', self.pbPort)
        mt_interceptor = MTInterceptorScript(self.raise_any_exception)
        yield self.mtinterceptor_add(DefaultInterceptor(mt_interceptor), 0)
        # Disconnect from RouterPB
        self.disconnect()

        # Connect to InterceptorPB
        yield self.ipb_connect()

        yield self.connect('127.0.0.1', self.pbPort)
        yield self.prepareRoutingsAndStartConnector()

        # Bind
        yield self.smppc_factory.connectAndBind()

        # Install mocks
        self.smpps_factory.lastProto.sendPDU = mock.Mock(wraps=self.smpps_factory.lastProto.sendPDU)

        # Send a SMS MT through smpps interface
        yield self.smppc_factory.lastProto.sendDataRequest(self.SubmitSmPDU)

        # Wait 3 seconds for submit_sm_resp
        yield waitFor(3)

        # Unbind & Disconnect
        yield self.smppc_factory.smpp.unbindAndDisconnect()
        yield self.stopSmppClientConnectors()

        # Run tests on final destination smpp server (third party mocker)
        self.assertEqual(0, len(self.SMSCPort.factory.lastClient.submitRecords))
        # Run tests on Jasmin's SMPPs
        self.assertEqual(self.smpps_factory.lastProto.sendPDU.call_count, 2)
        # smpps response was a submit_sm_resp with ESME_ROK
        response_pdu = self.smpps_factory.lastProto.sendPDU.call_args_list[0][0][0]
        self.assertEqual(response_pdu.id, pdu_types.CommandId.submit_sm_resp)
        self.assertEqual(response_pdu.seqNum, 2)
        self.assertEqual(response_pdu.status, pdu_types.CommandStatus.ESME_RSYSERR)
        self.assertTrue('message_id' not in response_pdu.params)
        self.assertEqual(_ic, self.stats_smpps.get('interceptor_count'))
        self.assertEqual(_iec + 1, self.stats_smpps.get('interceptor_error_count'))

    @defer.inlineCallbacks
    def test_ESME_RINVESMCLASS_from_script(self):
        _ic = self.stats_smpps.get('interceptor_count')
        _iec = self.stats_smpps.get('interceptor_error_count')

        # Re-provision interceptor with script returning a ESME_RINVESMCLASS
        # Connect to RouterPB
        yield self.connect('127.0.0.1', self.pbPort)
        mt_interceptor = MTInterceptorScript(self.return_ESME_RINVESMCLASS)
        yield self.mtinterceptor_add(DefaultInterceptor(mt_interceptor), 0)
        # Disconnect from RouterPB
        self.disconnect()

        # Connect to InterceptorPB
        yield self.ipb_connect()

        yield self.connect('127.0.0.1', self.pbPort)
        yield self.prepareRoutingsAndStartConnector()

        # Bind
        yield self.smppc_factory.connectAndBind()

        # Install mocks
        self.smpps_factory.lastProto.sendPDU = mock.Mock(wraps=self.smpps_factory.lastProto.sendPDU)

        # Send a SMS MT through smpps interface
        yield self.smppc_factory.lastProto.sendDataRequest(self.SubmitSmPDU)

        # Wait 3 seconds for submit_sm_resp
        yield waitFor(3)

        # Unbind & Disconnect
        yield self.smppc_factory.smpp.unbindAndDisconnect()
        yield self.stopSmppClientConnectors()

        # Run tests on final destination smpp server (third party mocker)
        self.assertEqual(0, len(self.SMSCPort.factory.lastClient.submitRecords))
        # Run tests on Jasmin's SMPPs
        self.assertEqual(self.smpps_factory.lastProto.sendPDU.call_count, 2)
        # smpps response was a submit_sm_resp with ESME_ROK
        response_pdu = self.smpps_factory.lastProto.sendPDU.call_args_list[0][0][0]
        self.assertEqual(response_pdu.id, pdu_types.CommandId.submit_sm_resp)
        self.assertEqual(response_pdu.seqNum, 2)
        self.assertEqual(response_pdu.status, pdu_types.CommandStatus.ESME_RINVESMCLASS)
        self.assertTrue('message_id' not in response_pdu.params)
        self.assertEqual(_ic, self.stats_smpps.get('interceptor_count'))
        self.assertEqual(_iec + 1, self.stats_smpps.get('interceptor_error_count'))

    @defer.inlineCallbacks
    def test_HTTP_300_from_script(self):
        "Will ensure if script defines only http error it will implicitly cause a smpp ESME_RUNKNOWNERR error"

        _ic = self.stats_smpps.get('interceptor_count')
        _iec = self.stats_smpps.get('interceptor_error_count')

        # Re-provision interceptor with script returning a HTTP 300
        # Connect to RouterPB
        yield self.connect('127.0.0.1', self.pbPort)
        mt_interceptor = MTInterceptorScript(self.return_HTTP_300)
        yield self.mtinterceptor_add(DefaultInterceptor(mt_interceptor), 0)
        # Disconnect from RouterPB
        self.disconnect()

        # Connect to InterceptorPB
        yield self.ipb_connect()

        yield self.connect('127.0.0.1', self.pbPort)
        yield self.prepareRoutingsAndStartConnector()

        # Bind
        yield self.smppc_factory.connectAndBind()

        # Install mocks
        self.smpps_factory.lastProto.sendPDU = mock.Mock(wraps=self.smpps_factory.lastProto.sendPDU)

        # Send a SMS MT through smpps interface
        yield self.smppc_factory.lastProto.sendDataRequest(self.SubmitSmPDU)

        # Wait 3 seconds for submit_sm_resp
        yield waitFor(3)

        # Unbind & Disconnect
        yield self.smppc_factory.smpp.unbindAndDisconnect()
        yield self.stopSmppClientConnectors()

        # Run tests on final destination smpp server (third party mocker)
        self.assertEqual(0, len(self.SMSCPort.factory.lastClient.submitRecords))
        # Run tests on Jasmin's SMPPs
        self.assertEqual(self.smpps_factory.lastProto.sendPDU.call_count, 2)
        # smpps response was a submit_sm_resp with ESME_ROK
        response_pdu = self.smpps_factory.lastProto.sendPDU.call_args_list[0][0][0]
        self.assertEqual(response_pdu.id, pdu_types.CommandId.submit_sm_resp)
        self.assertEqual(response_pdu.seqNum, 2)
        self.assertEqual(response_pdu.status, pdu_types.CommandStatus.ESME_RUNKNOWNERR)
        self.assertTrue('message_id' not in response_pdu.params)
        self.assertEqual(_ic, self.stats_smpps.get('interceptor_count'))
        self.assertEqual(_iec + 1, self.stats_smpps.get('interceptor_error_count'))

    @defer.inlineCallbacks
    def test_tagging(self):
        """Refs #495
        Will tag message inside interceptor script and assert
        routing based tagfilter were correctly done
        """
        # Re-provision interceptor with correct script
        # Connect to RouterPB
        yield self.connect('127.0.0.1', self.pbPort)
        mt_interceptor = MTInterceptorScript("routable.addTag(10)")
        yield self.mtinterceptor_add(DefaultInterceptor(mt_interceptor), 0)
        # Disconnect from RouterPB
        self.disconnect()

        # Connect to InterceptorPB
        yield self.ipb_connect()

        yield self.connect('127.0.0.1', self.pbPort)
        yield self.prepareRoutingsAndStartConnector()

        # Change routing rules by shadowing (high order value) default route with a
        # static route having a tagfilter
        yield self.mtroute_flush()
        yield self.mtroute_add(StaticMTRoute([TagFilter(10)], self.c1, 0.0), 1000)

        # Bind
        yield self.smppc_factory.connectAndBind()

        # Send a SMS MT through smpps interface
        yield self.smppc_factory.lastProto.sendDataRequest(self.SubmitSmPDU)

        # Wait 3 seconds for submit_sm_resp
        yield waitFor(3)

        # Unbind & Disconnect
        yield self.smppc_factory.smpp.unbindAndDisconnect()
        yield self.stopSmppClientConnectors()

        # Run tests on final destination smpp server (third party mocker)
        self.assertEqual(1, len(self.SMSCPort.factory.lastClient.submitRecords))
