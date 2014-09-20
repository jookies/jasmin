from twisted.trial.unittest import TestCase
from twisted.internet import defer
from twisted_web_test_utils import DummySite
from jasmin.protocols.http.configs import HTTPApiConfig
from jasmin.protocols.http.server import HTTPApi
from jasmin.routing.router import RouterPB
from jasmin.routing.configs import RouterPBConfig
from jasmin.managers.clients import SMPPClientManagerPB
from jasmin.managers.configs import SMPPClientPBConfig
from jasmin.routing.jasminApi import User, Group, SmppClientConnector
from jasmin.routing.Routes import DefaultRoute

class HTTPApiTestCases(TestCase):
    def setUp(self):
        # Instanciate a RouterPB (a requirement for HTTPApi)
        RouterPBConfigInstance = RouterPBConfig()
        RouterPB_f = RouterPB()
        RouterPB_f.setConfig(RouterPBConfigInstance)
        
        # Provision Router with User and Route
        RouterPB_f.users.append(User(1, Group(1), 'fourat', 'correct'))
        RouterPB_f.mt_routing_table.add(DefaultRoute(SmppClientConnector('abc')), 0)

        # Instanciate a SMPPClientManagerPB (a requirement for HTTPApi)
        SMPPClientPBConfigInstance = SMPPClientPBConfig()
        SMPPClientPBConfigInstance.authentication = False
        clientManager_f = SMPPClientManagerPB()
        clientManager_f.setConfig(SMPPClientPBConfigInstance)
        
        httpApiConfigInstance = HTTPApiConfig()
        self.web = DummySite(HTTPApi(RouterPB_f, clientManager_f, httpApiConfigInstance))

class SendTestCases(HTTPApiTestCases):
    username = 'fourat'

    @defer.inlineCallbacks
    def test_send_with_correct_args(self):
        response = yield self.web.get("send", {'username': self.username, 
                                               'password': 'incorrect',
                                               'to': '98700177',
                                               'content': 'anycontent'})
        self.assertEqual(response.responseCode, 403)
        self.assertEqual(response.value(), "Error \"Authentication failure for username:%s\"" % self.username)

    @defer.inlineCallbacks
    def test_send_with_incorrect_args(self):
        response = yield self.web.get("send", {'username': self.username, 
                                               'passwd': 'correct',
                                               'to': '98700177',
                                               'content': 'anycontent'})
        self.assertEqual(response.responseCode, 400)
        self.assertEqual(response.value(), "Error \"Mandatory argument [password] is not found.\"")

    @defer.inlineCallbacks
    def test_send_with_auth_success(self):
        response = yield self.web.get("send", {'username': self.username, 
                                               'password': 'correct',
                                               'to': '98700177',
                                               'content': 'anycontent'})
        self.assertEqual(response.responseCode, 500)
        # This is a normal error since SMPPClientManagerPB is not really running
        self.assertEqual(response.value(), "Error \"Cannot send submit_sm, check SMPPClientManagerPB log file for details\"")

    @defer.inlineCallbacks
    def test_send_with_priority(self):
        params = {'username': self.username, 
                  'password': 'correct',
                  'to': '98700177',
                  'content': 'anycontent'}

        # Priority definitions
        valid_priorities = {0, 1, 2, 3}
        
        for params['priority'] in valid_priorities:
            response = yield self.web.get("send", params)
            self.assertEqual(response.responseCode, 500)
            # This is a normal error since SMPPClientManagerPB is not really running
            self.assertEqual(response.value(), "Error \"Cannot send submit_sm, check SMPPClientManagerPB log file for details\"")

        # Priority definitions
        valid_priorities = {-1, 'a', 44, 4}
        
        for params['priority'] in valid_priorities:
            response = yield self.web.get("send", params)
            self.assertEqual(response.responseCode, 400)
            # This is a normal error since SMPPClientManagerPB is not really running
            self.assertEqual(response.value(), 'Error "Argument [priority] has an invalid value: [%s]."' % params['priority'])

    @defer.inlineCallbacks
    def test_send_with_inurl_dlr(self):
        params = {'username': self.username, 
                  'password': 'correct',
                  'to': '98700177',
                  'content': 'anycontent'}
                  
        # URL definitions
        valid_urls = {'http://127.0.0.1/receipt',
                      'http://127.0.0.1:99/receipt',
                      'https://127.0.0.1/receipt',
                      'https://127.0.0.1:99/receipt',
                      'https://127.0.0.1/receipt.html',
                      'https://127.0.0.1:99/receipt.html',
                      'http://www.google.com/receipt',
                      'http://www.google.com:99/receipt',
                      'http://www.google.com/receipt.html',
                      'http://www.google.com:99/receipt.html',
                      'http://www.google.com/',
                      'http://www.google.com:99/',
                      'http://www.google.com',
                      'http://www.google.com:99'}
        
        for params['dlr-url'] in valid_urls:
            response = yield self.web.get("send", params)
            self.assertEqual(response.responseCode, 500)
            self.assertEqual(response.value(), "Error \"Cannot send submit_sm, check SMPPClientManagerPB log file for details\"")

        # URL definitions
        invalid_urls = {'ftp://127.0.0.1/receipt',
                      'smtp://127.0.0.1:99/receipt',
                      'smpp://127.0.0.1/receipt',
                      '127.0.0.1:99',
                      'www.google.com',
                      'www.google.com:99/'}
        
        for params['dlr-url'] in invalid_urls:
            response = yield self.web.get("send", params)
            self.assertEqual(response.responseCode, 400)
            self.assertEqual(response.value(), "Error \"Argument [dlr-url] has an invalid value: [%s].\"" % params['dlr-url'])

    @defer.inlineCallbacks
    def test_send_without_args(self):
        response = yield self.web.get("send")
        self.assertEqual(response.responseCode, 400)
        self.assertEqual(response.value(), "Error \"Mandatory argument [username] is not found.\"")
        
    @defer.inlineCallbacks
    def test_send_with_some_args(self):
        response = yield self.web.get("send", {'username': self.username})
        self.assertEqual(response.responseCode, 400)
        self.assertEqual(response.value()[:25], "Error \"Mandatory argument")