from hashlib import md5

from twisted.cred import portal
from twisted.cred.checkers import AllowAnonymousAccess, InMemoryUsernamePasswordDatabaseDontUse
from twisted.internet import defer
from twisted.spread import pb

import jasmin
from jasmin.protocols.smpp.configs import SMPPServerPBConfig
from jasmin.protocols.smpp.pb import SMPPServerPB
from jasmin.protocols.smpp.proxies import SMPPServerPBProxy
from jasmin.protocols.smpp.test.smsc_simulator import *
from jasmin.protocols.smpp.test.test_smpp_server import SMPPServerTestCases
from jasmin.tools.cred.portal import JasminPBRealm
from jasmin.tools.proxies import ConnectError
from jasmin.tools.spread.pb import JasminPBPortalRoot


class SMPPServerPBTestCase(SMPPServerTestCases):
    def setUp(self, authentication=False):
        SMPPServerTestCases.setUp(self)

        # Initiating config objects without any filename
        self.SMPPServerPBConfigInstance = SMPPServerPBConfig()
        self.SMPPServerPBConfigInstance.authentication = authentication

        # Launch the SMPPServerPB
        pbRoot = SMPPServerPB(self.SMPPServerPBConfigInstance)
        pbRoot.addSmpps(self.smpps_factory)

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

    @defer.inlineCallbacks
    def tearDown(self):
        yield SMPPServerTestCases.tearDown(self)
        yield self.PBServer.stopListening()


class SMPPServerPBProxyTestCase(SMPPServerPBProxy, SMPPServerPBTestCase):
    @defer.inlineCallbacks
    def tearDown(self):
        yield SMPPServerPBTestCase.tearDown(self)
        yield self.disconnect()


class AuthenticatedTestCases(SMPPServerPBProxyTestCase):
    def setUp(self, authentication=False):
        SMPPServerPBProxyTestCase.setUp(self, authentication=True)

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


class BasicTestCases(SMPPServerPBProxyTestCase):
    @defer.inlineCallbacks
    def test_version_release(self):
        yield self.connect('127.0.0.1', self.pbPort)

        version_release = yield self.version_release()

        self.assertEqual(version_release, jasmin.get_release())

    @defer.inlineCallbacks
    def test_version(self):
        yield self.connect('127.0.0.1', self.pbPort)

        version = yield self.version()

        self.assertEqual(version, jasmin.get_version())

    @defer.inlineCallbacks
    def test_list_bound_systemids(self):
        yield self.connect('127.0.0.1', self.pbPort)

        r = yield self.list_bound_systemids()

        self.assertEqual([], r)

    @defer.inlineCallbacks
    def test_deliverer_send_request(self):
        yield self.connect('127.0.0.1', self.pbPort)

        pdu = DeliverSM(
            source_addr='1111',
            destination_addr='22222',
            short_message='Some content',
        )

        r = yield self.deliverer_send_request('any_system_id', pdu)

        # Returns False because there's no deliverers
        self.assertEqual(False, r)
