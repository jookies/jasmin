from twisted.internet import defer
from twisted.web.client import getPage
from test_router import SMPPClientManagerPBTestCase, id_generator
from jasmin.routing.jasminApi import *
from jasmin.routing.proxies import RouterPBProxy
from jasmin.protocols.smpp.configs import SMPPClientConfig
from jasmin.routing.Routes import DefaultRoute
from jasmin.routing.Interceptors import DefaultInterceptor

class SubmitSmInterceptionTestCases(RouterPBProxy, SMPPClientManagerPBTestCase):

    @defer.inlineCallbacks
    def setUp(self):
        yield SMPPClientManagerPBTestCase.setUp(self)

        yield self.connect('127.0.0.1', self.pbPort)

        # Provision user, group, default mt route and 
        # default mt interceptor 
        self.g1 = Group(1)
        yield self.group_add(self.g1)
        
        self.c1 = SmppClientConnector(id_generator())
        self.mt_interceptor = MTInterceptorScript('print yes')
        self.u1_password = 'password'
        self.u1 = User(1, self.g1, 'username', self.u1_password)
        self.u2_password = 'password'
        self.u2 = User(1, self.g1, 'username2', self.u2_password)
        yield self.user_add(self.u1)

        yield self.mtroute_add(DefaultRoute(self.c1), 0)
        yield self.mtinterceptor_add(DefaultInterceptor(self.mt_interceptor), 0)

        # Now we'll create the connector
        yield self.SMPPClientManagerPBProxy.connect('127.0.0.1', self.CManagerPort)
        c1Config = SMPPClientConfig(id=self.c1.cid)
        yield self.SMPPClientManagerPBProxy.add(c1Config)

    @defer.inlineCallbacks
    def test_httpapi_interceptorpb_not_set(self):
        # Send a SMS MT through http interface
        url = 'http://127.0.0.1:1401/send?to=98700177&content=test&username=%s&password=%s' % (
            self.u1.username, self.u1_password)
        
        # We should receive an error since no interceptorpb is set
        lastErrorStatus = None
        try:
            yield getPage(url)
        except Exception, e:
            lastErrorStatus = e.status
        self.assertEqual(lastErrorStatus, '500')