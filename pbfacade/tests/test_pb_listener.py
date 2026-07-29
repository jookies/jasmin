from hashlib import md5

from twisted.cred import portal
from twisted.cred.checkers import InMemoryUsernamePasswordDatabaseDontUse
from twisted.cred.credentials import UsernamePassword
from twisted.internet import defer, reactor
from twisted.spread import pb
from twisted.trial import unittest

import jasmin
from pbfacade.avatars import RouterAvatar
from jasmin.tools.cred.portal import JasminPBRealm
from jasmin.tools.spread.pb import JasminPBPortalRoot


class NoopClient:
    def call(self, method, params=None):
        return True


class TrustedPBListenerTest(unittest.TestCase):
    @defer.inlineCallbacks
    def setUp(self):
        avatar = RouterAvatar(NoopClient())
        auth_portal = portal.Portal(JasminPBRealm(avatar))
        checker = InMemoryUsernamePasswordDatabaseDontUse()
        checker.addUser("test-user", md5(b"test-password").digest())
        auth_portal.registerChecker(checker)
        self.listener = reactor.listenTCP(
            0,
            pb.PBServerFactory(JasminPBPortalRoot(auth_portal)),
            interface="127.0.0.1",
        )
        self.factory = pb.PBClientFactory()
        reactor.connectTCP("127.0.0.1", self.listener.getHost().port, self.factory)
        yield self.factory.getRootObject()

    @defer.inlineCallbacks
    def tearDown(self):
        self.factory.disconnect()
        yield self.listener.stopListening()

    @defer.inlineCallbacks
    def test_digest_login_and_frozen_perspective_call(self):
        perspective = yield self.factory.login(UsernamePassword("test-user", b"test-password"))
        version = yield perspective.callRemote("version")
        self.assertEqual(jasmin.get_version(), version)

    @defer.inlineCallbacks
    def test_wrong_digest_is_rejected(self):
        result = yield self.factory.login(UsernamePassword("test-user", b"wrong"))
        self.assertEqual((False, "Authentication error test-user"), result)
