from twisted.cred import portal
from twisted.cred.checkers import AllowAnonymousAccess, InMemoryUsernamePasswordDatabaseDontUse
from twisted.internet import reactor, defer
from twisted.spread import pb
from twisted.web.client import getPage

from jasmin.interceptor.configs import InterceptorPBConfig, InterceptorPBClientConfig
from jasmin.interceptor.interceptor import InterceptorPB
from jasmin.interceptor.proxies import InterceptorPBProxy
from jasmin.protocols.http.stats import HttpAPIStatsCollector
from jasmin.protocols.smpp.configs import SMPPClientConfig
from jasmin.routing.Filters import TagFilter
from jasmin.routing.Interceptors import DefaultInterceptor
from jasmin.routing.Routes import DefaultRoute, StaticMTRoute
from jasmin.routing.jasminApi import *
from jasmin.routing.proxies import RouterPBProxy
from jasmin.tools.cred.portal import JasminPBRealm
from jasmin.tools.spread.pb import JasminPBPortalRoot
from test_router import HappySMSCTestCase, id_generator


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
            yield HappySMSCTestCase.setUp(self, interceptorpb_client = self.ipb_client)
        else:
            yield HappySMSCTestCase.setUp(self)

        # Connect to RouterPB
        yield self.connect('127.0.0.1', self.pbPort)

        # Provision user, group, default mt route and
        # default mt interceptor
        self.g1 = Group(1)
        yield self.group_add(self.g1)

        self.c1 = SmppClientConnector(id_generator())
        self.mt_interceptor = MTInterceptorScript(self.script)
        self.u1_password = 'password'
        self.u1 = User(1, self.g1, 'username', self.u1_password)
        self.u2_password = 'password'
        self.u2 = User(1, self.g1, 'username2', self.u2_password)
        yield self.user_add(self.u1)

        yield self.mtroute_add(DefaultRoute(self.c1), 0)
        yield self.mtinterceptor_add(DefaultInterceptor(self.mt_interceptor), 0)

        # Now we'll create the connector
        yield self.SMPPClientManagerPBProxy.connect('127.0.0.1', self.CManagerPort)
        c1Config = SMPPClientConfig(id=self.c1.cid, port = self.SMSCPort.getHost().port)
        yield self.SMPPClientManagerPBProxy.add(c1Config)

        # And start it !
        yield self.SMPPClientManagerPBProxy.start(self.c1.cid)

        # Get stats singletons
        self.stats_http = HttpAPIStatsCollector().get()

    @defer.inlineCallbacks
    def tearDown(self):
        # Stop smppc
        yield self.SMPPClientManagerPBProxy.stop(self.c1.cid)
        # Wait for 'BOUND_TRX' state
        while True:
            ssRet = yield self.SMPPClientManagerPBProxy.session_state(self.c1.cid)
            if ssRet == 'NONE' or ssRet == 'UNBOUND':
                break;
            else:
                yield waitFor(0.2)

        yield HappySMSCTestCase.tearDown(self)

class ProvisionInterceptorPB(ProvisionWithoutInterceptorPB):
    @defer.inlineCallbacks
    def setUp(self, authentication = False):
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
    def ipb_connect(self, config = None):
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

class HttpAPISubmitSmNoInterceptorPBTestCases(ProvisionWithoutInterceptorPB, RouterPBProxy, HappySMSCTestCase):

    @defer.inlineCallbacks
    def test_send_interceptorpb_not_set(self):
        _ic = self.stats_http.get('interceptor_count')
        _iec = self.stats_http.get('interceptor_error_count')

        # Send a SMS MT through http interface
        url = 'http://127.0.0.1:1401/send?to=06155423&content=test&username=%s&password=%s' % (
            self.u1.username, self.u1_password)

        # We should receive an error since no interceptorpb is set
        lastErrorStatus = None
        lastResponse = None
        try:
            yield getPage(url)
        except Exception, e:
            lastErrorStatus = e.status
            lastResponse = e.response

        # Asserts
        self.assertEqual(lastErrorStatus, '503')
        self.assertEqual(lastResponse, 'Error "InterceptorPB not set !"')
        self.assertEqual(_ic, self.stats_http.get('interceptor_count'))
        self.assertEqual(_iec+1, self.stats_http.get('interceptor_error_count'))

    @defer.inlineCallbacks
    def test_rate_interceptorpb_not_set(self):
        _ic = self.stats_http.get('interceptor_count')
        _iec = self.stats_http.get('interceptor_error_count')

        # Send a SMS MT through http interface
        url = 'http://127.0.0.1:1401/rate?to=06155423&username=%s&password=%s' % (
            self.u1.username, self.u1_password)

        # We should receive an error since no interceptorpb is set
        lastErrorStatus = None
        lastResponse = None
        try:
            yield getPage(url)
        except Exception, e:
            lastErrorStatus = e.status
            lastResponse = e.response

        # Asserts
        self.assertEqual(lastErrorStatus, '503')
        self.assertEqual(lastResponse, '"InterceptorPB not set !"')
        self.assertEqual(_ic, self.stats_http.get('interceptor_count'))
        self.assertEqual(_iec+1, self.stats_http.get('interceptor_error_count'))

class HttpAPISubmitSmInterceptionTestCases(ProvisionInterceptorPB, RouterPBProxy, HappySMSCTestCase):
    update_message_sript = "routable.pdu.params['short_message'] = 'Intercepted message'"
    update_message_from_tags_sript = "routable.pdu.params['short_message'] = '%s' % routable.getTags()"
    lock_param_script = "routable.pdu.params['sm_default_msg_id'] = 10;routable.lockPduParam('sm_default_msg_id')"
    raise_any_exception = "raise Exception('Exception from interceptor script')"
    return_ESME_RINVESMCLASS = "smpp_status = 67"
    return_HTTP_300 = "http_status = 300"

    @defer.inlineCallbacks
    def test_send_interceptorpb_not_connected(self):
        _ic = self.stats_http.get('interceptor_count')
        _iec = self.stats_http.get('interceptor_error_count')

        # Send a SMS MT through http interface
        url = 'http://127.0.0.1:1401/send?to=06155423&content=test&username=%s&password=%s' % (
            self.u1.username, self.u1_password)

        # We should receive an error since interceptorpb is not connected
        lastErrorStatus = None
        lastResponse = None
        try:
            yield getPage(url)
        except Exception, e:
            lastErrorStatus = e.status
            lastResponse = e.response

        # Asserts
        self.assertEqual(lastErrorStatus, '503')
        self.assertEqual(lastResponse, 'Error "InterceptorPB not connected !"')
        self.assertEqual(_ic, self.stats_http.get('interceptor_count'))
        self.assertEqual(_iec+1, self.stats_http.get('interceptor_error_count'))

    @defer.inlineCallbacks
    def test_send_syntax_error(self):
        _ic = self.stats_http.get('interceptor_count')
        _iec = self.stats_http.get('interceptor_error_count')

        # Connect to InterceptorPB
        yield self.ipb_connect()

        # Send a SMS MT through http interface
        url = 'http://127.0.0.1:1401/send?to=06155423&content=test&username=%s&password=%s' % (
            self.u1.username, self.u1_password)

        # We should receive an error since interceptorpb is not connected
        lastErrorStatus = None
        lastResponse = None
        try:
            yield getPage(url)
        except Exception, e:
            lastErrorStatus = e.status
            lastResponse = e.response

        # Asserts
        self.assertEqual(lastErrorStatus, '400')
        self.assertEqual(lastResponse, 'Error "Failed running interception script, check log for details"')
        self.assertEqual(_ic, self.stats_http.get('interceptor_count'))
        self.assertEqual(_iec+1, self.stats_http.get('interceptor_error_count'))

    @defer.inlineCallbacks
    def test_send_success(self):
        _ic = self.stats_http.get('interceptor_count')
        _iec = self.stats_http.get('interceptor_error_count')

        # Re-provision interceptor with correct script
        mt_interceptor = MTInterceptorScript(self.update_message_sript)
        yield self.mtinterceptor_add(DefaultInterceptor(mt_interceptor), 0)

        # Connect to InterceptorPB
        yield self.ipb_connect()

        # Send a SMS MT through http interface
        url = 'http://127.0.0.1:1401/send?to=06155423&content=test&username=%s&password=%s' % (
            self.u1.username, self.u1_password)

        # We should receive an error since interceptorpb is not connected
        lastErrorStatus = None
        lastResponse = None
        try:
            yield getPage(url)
        except Exception, e:
            lastErrorStatus = e.status
            lastResponse = e.response

        # Wait some time for message delivery through smppc
        yield waitFor(2)

        # Asserts
        self.assertEqual(lastErrorStatus, None)
        self.assertEqual(1, len(self.SMSCPort.factory.lastClient.submitRecords))
        self.assertEqual('Intercepted message', self.SMSCPort.factory.lastClient.submitRecords[0].params['short_message'])
        self.assertEqual(_ic+1, self.stats_http.get('interceptor_count'))
        self.assertEqual(_iec, self.stats_http.get('interceptor_error_count'))

    @defer.inlineCallbacks
    def test_send_with_tags(self):
        """Related to #455
        Will send message through http api using tags and then assert for getting the tags into the short_message
        """
        # Re-provision interceptor with correct script
        mt_interceptor = MTInterceptorScript(self.update_message_from_tags_sript)
        yield self.mtinterceptor_add(DefaultInterceptor(mt_interceptor), 0)

        # Connect to InterceptorPB
        yield self.ipb_connect()

        # Send a SMS MT through http interface
        url = 'http://127.0.0.1:1401/send?to=06155423&content=temporary&username=%s&password=%s&tags=%s' % (
            self.u1.username, self.u1_password, '123,456')

        # We should receive an error since interceptorpb is not connected
        lastErrorStatus = None
        lastResponse = None
        try:
            yield getPage(url)
        except Exception, e:
            lastErrorStatus = e.status
            lastResponse = e.response

        # Wait some time for message delivery through smppc
        yield waitFor(2)

        # Asserts
        self.assertEqual(lastErrorStatus, None)
        self.assertEqual(1, len(self.SMSCPort.factory.lastClient.submitRecords))
        self.assertEqual('[\'123\', \'456\']', self.SMSCPort.factory.lastClient.submitRecords[0].params['short_message'])

    @defer.inlineCallbacks
    def test_send_and_lock_param(self):
        """Related to #458
        Will set and lock sm_default_msg_id inside interceptor and assert it were kept as set.
        """
        # Re-provision interceptor with correct script
        mt_interceptor = MTInterceptorScript(self.lock_param_script)
        yield self.mtinterceptor_add(DefaultInterceptor(mt_interceptor), 0)

        # Connect to InterceptorPB
        yield self.ipb_connect()

        # Send a SMS MT through http interface
        url = 'http://127.0.0.1:1401/send?to=06155423&content=temporary&username=%s&password=%s' % (
            self.u1.username, self.u1_password)

        # We should receive an error since interceptorpb is not connected
        lastErrorStatus = None
        lastResponse = None
        try:
            yield getPage(url)
        except Exception, e:
            lastErrorStatus = e.status
            lastResponse = e.response

        # Wait some time for message delivery through smppc
        yield waitFor(2)

        # Asserts
        self.assertEqual(lastErrorStatus, None)
        self.assertEqual(1, len(self.SMSCPort.factory.lastClient.submitRecords))
        self.assertEqual(10, self.SMSCPort.factory.lastClient.submitRecords[0].params['sm_default_msg_id'])

    @defer.inlineCallbacks
    def test_send_any_exception_from_script(self):
        _ic = self.stats_http.get('interceptor_count')
        _iec = self.stats_http.get('interceptor_error_count')

        # Re-provision interceptor with correct script
        mt_interceptor = MTInterceptorScript(self.raise_any_exception)
        yield self.mtinterceptor_add(DefaultInterceptor(mt_interceptor), 0)

        # Connect to InterceptorPB
        yield self.ipb_connect()

        # Send a SMS MT through http interface
        url = 'http://127.0.0.1:1401/send?to=06155423&content=test&username=%s&password=%s' % (
            self.u1.username, self.u1_password)

        # We should receive an error since interceptorpb is not connected
        lastErrorStatus = None
        lastResponse = None
        try:
            yield getPage(url)
        except Exception, e:
            lastErrorStatus = e.status
            lastResponse = e.response

        # Wait some time for message delivery through smppc
        yield waitFor(2)

        # Asserts
        self.assertEqual(lastErrorStatus, '400')
        self.assertEqual(lastResponse, 'Error "Failed running interception script, check log for details"')
        self.assertEqual(_ic, self.stats_http.get('interceptor_count'))
        self.assertEqual(_iec+1, self.stats_http.get('interceptor_error_count'))

    @defer.inlineCallbacks
    def test_send_HTTP_300_from_script(self):
        _ic = self.stats_http.get('interceptor_count')
        _iec = self.stats_http.get('interceptor_error_count')

        # Re-provision interceptor with correct script
        mt_interceptor = MTInterceptorScript(self.return_HTTP_300)
        yield self.mtinterceptor_add(DefaultInterceptor(mt_interceptor), 0)

        # Connect to InterceptorPB
        yield self.ipb_connect()

        # Send a SMS MT through http interface
        url = 'http://127.0.0.1:1401/send?to=06155423&content=test&username=%s&password=%s' % (
            self.u1.username, self.u1_password)

        # We should receive an error since interceptorpb is not connected
        lastErrorStatus = None
        lastResponse = None
        try:
            yield getPage(url)
        except Exception, e:
            lastErrorStatus = e.status
            lastResponse = e.response

        # Wait some time for message delivery through smppc
        yield waitFor(2)

        # Asserts
        self.assertEqual(lastErrorStatus, '300')
        self.assertEqual(lastResponse, 'Error "Interception specific error code 300"')
        self.assertEqual(_ic, self.stats_http.get('interceptor_count'))
        self.assertEqual(_iec+1, self.stats_http.get('interceptor_error_count'))

    @defer.inlineCallbacks
    def test_send_ESME_RINVESMCLASS_from_script(self):
        "Will ensure if script defines only smpp error it will implicitly cause a http 520 error"

        _ic = self.stats_http.get('interceptor_count')
        _iec = self.stats_http.get('interceptor_error_count')

        # Re-provision interceptor with correct script
        mt_interceptor = MTInterceptorScript(self.return_ESME_RINVESMCLASS)
        yield self.mtinterceptor_add(DefaultInterceptor(mt_interceptor), 0)

        # Connect to InterceptorPB
        yield self.ipb_connect()

        # Send a SMS MT through http interface
        url = 'http://127.0.0.1:1401/send?to=06155423&content=test&username=%s&password=%s' % (
            self.u1.username, self.u1_password)

        # We should receive an error since interceptorpb is not connected
        lastErrorStatus = None
        lastResponse = None
        try:
            yield getPage(url)
        except Exception, e:
            lastErrorStatus = e.status
            lastResponse = e.response

        # Wait some time for message delivery through smppc
        yield waitFor(2)

        # Asserts
        self.assertEqual(lastErrorStatus, '520')
        self.assertEqual(lastResponse, 'Error "Interception specific error code 520"')
        self.assertEqual(_ic, self.stats_http.get('interceptor_count'))
        self.assertEqual(_iec+1, self.stats_http.get('interceptor_error_count'))

    @defer.inlineCallbacks
    def test_rate_interceptorpb_not_connected(self):
        _ic = self.stats_http.get('interceptor_count')
        _iec = self.stats_http.get('interceptor_error_count')

        # Send a SMS MT through http interface
        url = 'http://127.0.0.1:1401/rate?to=06155423&username=%s&password=%s' % (
            self.u1.username, self.u1_password)

        # We should receive an error since interceptorpb is not connected
        lastErrorStatus = None
        lastResponse = None
        try:
            yield getPage(url)
        except Exception, e:
            lastErrorStatus = e.status
            lastResponse = e.response

        # Asserts
        self.assertEqual(lastErrorStatus, '503')
        self.assertEqual(lastResponse, '"InterceptorPB not connected !"')
        self.assertEqual(_ic, self.stats_http.get('interceptor_count'))
        self.assertEqual(_iec+1, self.stats_http.get('interceptor_error_count'))

    @defer.inlineCallbacks
    def test_rate_syntax_error(self):
        _ic = self.stats_http.get('interceptor_count')
        _iec = self.stats_http.get('interceptor_error_count')

        # Connect to InterceptorPB
        yield self.ipb_connect()

        # Send a SMS MT through http interface
        url = 'http://127.0.0.1:1401/rate?to=06155423&username=%s&password=%s' % (
            self.u1.username, self.u1_password)

        # We should receive an error since interceptorpb is not connected
        lastErrorStatus = None
        lastResponse = None
        try:
            yield getPage(url)
        except Exception, e:
            lastErrorStatus = e.status
            lastResponse = e.response

        # Asserts
        self.assertEqual(lastErrorStatus, '400')
        self.assertEqual(lastResponse, '"Failed running interception script, check log for details"')
        self.assertEqual(_ic, self.stats_http.get('interceptor_count'))
        self.assertEqual(_iec+1, self.stats_http.get('interceptor_error_count'))

    @defer.inlineCallbacks
    def test_rate_success(self):
        _ic = self.stats_http.get('interceptor_count')
        _iec = self.stats_http.get('interceptor_error_count')

        # Re-provision interceptor with correct script
        mt_interceptor = MTInterceptorScript(self.update_message_sript)
        yield self.mtinterceptor_add(DefaultInterceptor(mt_interceptor), 0)

        # Connect to InterceptorPB
        yield self.ipb_connect()

        # Send a SMS MT through http interface
        url = 'http://127.0.0.1:1401/rate?to=06155423&username=%s&password=%s' % (
            self.u1.username, self.u1_password)

        # We should receive an error since interceptorpb is not connected
        lastErrorStatus = None
        lastResponse = None
        try:
            yield getPage(url)
        except Exception, e:
            lastErrorStatus = e.status
            lastResponse = e.response

        # Asserts
        self.assertEqual(lastErrorStatus, None)
        self.assertEqual(_ic+1, self.stats_http.get('interceptor_count'))
        self.assertEqual(_iec, self.stats_http.get('interceptor_error_count'))

    @defer.inlineCallbacks
    def test_rate_any_exception_from_script(self):
        _ic = self.stats_http.get('interceptor_count')
        _iec = self.stats_http.get('interceptor_error_count')

        # Re-provision interceptor with correct script
        mt_interceptor = MTInterceptorScript(self.raise_any_exception)
        yield self.mtinterceptor_add(DefaultInterceptor(mt_interceptor), 0)

        # Connect to InterceptorPB
        yield self.ipb_connect()

        # Send a SMS MT through http interface
        url = 'http://127.0.0.1:1401/rate?to=06155423&username=%s&password=%s' % (
            self.u1.username, self.u1_password)

        # We should receive an error since interceptorpb is not connected
        lastErrorStatus = None
        lastResponse = None
        try:
            yield getPage(url)
        except Exception, e:
            lastErrorStatus = e.status
            lastResponse = e.response

        # Wait some time for message delivery through smppc
        yield waitFor(2)

        # Asserts
        self.assertEqual(lastErrorStatus, '400')
        self.assertEqual(lastResponse, '"Failed running interception script, check log for details"')
        self.assertEqual(_ic, self.stats_http.get('interceptor_count'))
        self.assertEqual(_iec+1, self.stats_http.get('interceptor_error_count'))

    @defer.inlineCallbacks
    def test_rate_HTTP_300_from_script(self):
        _ic = self.stats_http.get('interceptor_count')
        _iec = self.stats_http.get('interceptor_error_count')

        # Re-provision interceptor with correct script
        mt_interceptor = MTInterceptorScript(self.return_HTTP_300)
        yield self.mtinterceptor_add(DefaultInterceptor(mt_interceptor), 0)

        # Connect to InterceptorPB
        yield self.ipb_connect()

        # Send a SMS MT through http interface
        url = 'http://127.0.0.1:1401/rate?to=06155423&username=%s&password=%s' % (
            self.u1.username, self.u1_password)

        # We should receive an error since interceptorpb is not connected
        lastErrorStatus = None
        lastResponse = None
        try:
            yield getPage(url)
        except Exception, e:
            lastErrorStatus = e.status
            lastResponse = e.response

        # Wait some time for message delivery through smppc
        yield waitFor(2)

        # Asserts
        self.assertEqual(lastErrorStatus, '300')
        self.assertEqual(lastResponse, '"Interception specific error code 300"')
        self.assertEqual(_ic, self.stats_http.get('interceptor_count'))
        self.assertEqual(_iec+1, self.stats_http.get('interceptor_error_count'))

    @defer.inlineCallbacks
    def test_rate_ESME_RINVESMCLASS_from_script(self):
        "Will ensure if script defines only smpp error it will implicitly cause a http 520 error"

        _ic = self.stats_http.get('interceptor_count')
        _iec = self.stats_http.get('interceptor_error_count')

        # Re-provision interceptor with correct script
        mt_interceptor = MTInterceptorScript(self.return_ESME_RINVESMCLASS)
        yield self.mtinterceptor_add(DefaultInterceptor(mt_interceptor), 0)

        # Connect to InterceptorPB
        yield self.ipb_connect()

        # Send a SMS MT through http interface
        url = 'http://127.0.0.1:1401/rate?to=06155423&username=%s&password=%s' % (
            self.u1.username, self.u1_password)

        # We should receive an error since interceptorpb is not connected
        lastErrorStatus = None
        lastResponse = None
        try:
            yield getPage(url)
        except Exception, e:
            lastErrorStatus = e.status
            lastResponse = e.response

        # Wait some time for message delivery through smppc
        yield waitFor(2)

        # Asserts
        self.assertEqual(lastErrorStatus, '520')
        self.assertEqual(lastResponse, '"Interception specific error code 520"')
        self.assertEqual(_ic, self.stats_http.get('interceptor_count'))
        self.assertEqual(_iec+1, self.stats_http.get('interceptor_error_count'))

    @defer.inlineCallbacks
    def test_tagging(self):
        """Refs #495
        Will tag message inside interceptor script and assert
        routing based tagfilter were correctly done
        """
        # Re-provision interceptor with correct script
        mt_interceptor = MTInterceptorScript("routable.addTag(10)")
        yield self.mtinterceptor_add(DefaultInterceptor(mt_interceptor), 0)

        # Change routing rules by shadowing (high order value) default route with a
        # static route having a tagfilter
        yield self.mtroute_flush()
        yield self.mtroute_add(StaticMTRoute([TagFilter(10)], self.c1, 0.0), 1000)

        # Connect to InterceptorPB
        yield self.ipb_connect()

        # Send a SMS MT through http interface
        url = 'http://127.0.0.1:1401/send?to=06155423&content=test&username=%s&password=%s' % (
            self.u1.username, self.u1_password)

        # We should receive an error since interceptorpb is not connected
        lastErrorStatus = None
        lastResponse = None
        try:
            yield getPage(url)
        except Exception, e:
            lastErrorStatus = e.status
            lastResponse = e.response

        # Wait some time for message delivery through smppc
        yield waitFor(2)

        # Asserts
        self.assertEqual(lastErrorStatus, None)
        self.assertEqual(1, len(self.SMSCPort.factory.lastClient.submitRecords))
