import random
import jasmin
from .test_jcli import jCliWithAuthTestCases
from hashlib import md5
from twisted.internet import defer


class AuthenticationTestCases(jCliWithAuthTestCases):
    @defer.inlineCallbacks
    def test_password_prompt(self):
        commands = [{'command': 'AnyUsername'}]
        yield self._test(r'Password: ', commands)

    @defer.inlineCallbacks
    def test_auth_failure(self):
        commands = [{'command': 'AnyUsername'},
                    {'command': 'AnyPassword%s' % random.randrange(100, 200), 'expect': 'Incorrect Username/Password.',
                     'noecho': True}]
        yield self._test(r'Username: ', commands)

    @defer.inlineCallbacks
    def test_auth_success(self):
        testPassword = 'AnyPassword%s' % random.randrange(100, 200)
        self.JCliConfigInstance.admin_password = md5(testPassword.encode('ascii')).digest()

        commands = [{'command': self.JCliConfigInstance.admin_username},
                    {'command': testPassword, 'expect': 'Welcome to Jasmin %s console' % jasmin.get_release(),
                     'noecho': True}]
        yield self._test(r'jcli : ', commands)
