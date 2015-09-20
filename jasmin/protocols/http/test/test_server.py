import json
from datetime import datetime
from twisted.trial.unittest import TestCase
from twisted.internet import defer
from twisted_web_test_utils import DummySite
from jasmin.protocols.http.configs import HTTPApiConfig
from jasmin.protocols.http.server import HTTPApi
from jasmin.protocols.http.stats import HttpAPIStatsCollector
from jasmin.routing.router import RouterPB
from jasmin.routing.configs import RouterPBConfig
from jasmin.managers.clients import SMPPClientManagerPB
from jasmin.managers.configs import SMPPClientPBConfig
from jasmin.routing.jasminApi import User, Group, SmppClientConnector
from jasmin.routing.Filters import UserFilter, GroupFilter
from jasmin.routing.Routes import DefaultRoute, StaticMTRoute

class HTTPApiTestCases(TestCase):
    def setUp(self):
        # Instanciate a RouterPB (a requirement for HTTPApi)
        RouterPBConfigInstance = RouterPBConfig()
        self.RouterPB_f = RouterPB()
        self.RouterPB_f.setConfig(RouterPBConfigInstance)
        
        # Provision Router with User and Route
        self.u1 = User(1, Group(1), 'fourat', 'correct')
        self.RouterPB_f.users.append(self.u1)
        self.RouterPB_f.mt_routing_table.add(DefaultRoute(SmppClientConnector('abc')), 0)

        # Instanciate a SMPPClientManagerPB (a requirement for HTTPApi)
        SMPPClientPBConfigInstance = SMPPClientPBConfig()
        SMPPClientPBConfigInstance.authentication = False
        clientManager_f = SMPPClientManagerPB()
        clientManager_f.setConfig(SMPPClientPBConfigInstance)
        
        httpApiConfigInstance = HTTPApiConfig()
        self.web = DummySite(HTTPApi(self.RouterPB_f, clientManager_f, httpApiConfigInstance))
    
    def tearDown(self):
        self.RouterPB_f.cancelPersistenceTimer()

class PingTestCases(HTTPApiTestCases):
    @defer.inlineCallbacks
    def test_basic_ping(self):
        response = yield self.web.get("ping")
        self.assertEqual(response.responseCode, 200)
        self.assertEqual(response.value(), "Jasmin/PONG")

class SendTestCases(HTTPApiTestCases):
    username = 'fourat'

    @defer.inlineCallbacks
    def test_send_with_correct_args(self):
        response = yield self.web.get("send", {'username': self.username, 
                                               'password': 'incorrec',
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
        invalid_priorities = {-1, 'a', 44, 4}
        
        for params['priority'] in invalid_priorities:
            response = yield self.web.get("send", params)
            self.assertEqual(response.responseCode, 400)
            # This is a normal error since SMPPClientManagerPB is not really running
            self.assertEqual(response.value(), 'Error "Argument [priority] has an invalid value: [%s]."' % params['priority'])

    @defer.inlineCallbacks
    def test_send_with_validity_period(self):
        params = {'username': self.username, 
                  'password': 'correct',
                  'to': '98700177',
                  'content': 'anycontent'}

        # Validity period definitions
        valid_vps = {0, 1, 2, 3, 4000}
        
        for params['validity-period'] in valid_vps:
            response = yield self.web.get("send", params)
            self.assertEqual(response.responseCode, 500)
            # This is a normal error since SMPPClientManagerPB is not really running
            self.assertEqual(response.value(), "Error \"Cannot send submit_sm, check SMPPClientManagerPB log file for details\"")

        # Validity period definitions
        invalid_vps = {-1, 'a', 1.0}
        
        for params['validity-period'] in invalid_vps:
            response = yield self.web.get("send", params)
            self.assertEqual(response.responseCode, 400)
            # This is a normal error since SMPPClientManagerPB is not really running
            self.assertEqual(response.value(), 'Error "Argument [validity-period] has an invalid value: [%s]."' % params['validity-period'])

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

class RateTestCases(HTTPApiTestCases):
    def setUp(self):
        HTTPApiTestCases.setUp(self)

        # Provision Router with additional Users and Routes
        u2 = User(2, Group(2), 'user2', 'correct')
        u3 = User(3, Group(2), 'user3', 'correct')
        u3.mt_credential.setQuota('balance', 10)
        self.RouterPB_f.users.append(u2)
        self.RouterPB_f.users.append(u3)
        filters = [GroupFilter(Group(2))]
        route = StaticMTRoute(filters, SmppClientConnector('abc'), 1.5)
        self.RouterPB_f.mt_routing_table.add(route, 2)

    @defer.inlineCallbacks
    def test_rate_with_correct_args(self):
        response = yield self.web.get("rate", {'username': 'fourat', 
                                               'password': 'incorrec',
                                               'to': '98700177'})
        self.assertEqual(response.responseCode, 403)
        self.assertEqual(json.loads(response.value()), u'Authentication failure for username:%s' % 'fourat')

    @defer.inlineCallbacks
    def test_rate_with_incorrect_args(self):
        response = yield self.web.get("rate", {'username': 'fourat', 
                                               'passwd': 'correct',
                                               'content': 'hello',
                                               'to': '98700177'})
        self.assertEqual(response.responseCode, 400)
        self.assertEqual(json.loads(response.value()), u'Mandatory argument [password] is not found.')

    @defer.inlineCallbacks
    def test_rate_with_auth_success(self):
        response = yield self.web.get("rate", {'username': 'fourat', 
                                               'password': 'correct',
                                               'to': '98700177'})
        self.assertEqual(response.responseCode, 200)
        self.assertEqual(json.loads(response.value()), {u'submit_sm_count': 1, u'unit_rate': 0.0})

    @defer.inlineCallbacks
    def test_rate_rated_route_unlimited_balance(self):
        response = yield self.web.get("rate", {'username': 'user2', 
                                               'password': 'correct',
                                               'to': '98700177'})
        self.assertEqual(response.responseCode, 200)
        self.assertEqual(json.loads(response.value()), {u'submit_sm_count': 1, u'unit_rate': 0.0})

    @defer.inlineCallbacks
    def test_rate_rated_route_unlimited_balance_long_content(self):
        response = yield self.web.get("rate", {'username': 'user2', 
                                               'password': 'correct',
                                               'to': '98700177',
                                               'content': 'AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA'})
        self.assertEqual(response.responseCode, 200)
        self.assertEqual(json.loads(response.value()), {u'submit_sm_count': 2, u'unit_rate': 0.0})

    @defer.inlineCallbacks
    def test_rate_rated_route_defined_balance(self):
        response = yield self.web.get("rate", {'username': 'user3', 
                                               'password': 'correct',
                                               'to': '98700177'})
        self.assertEqual(response.responseCode, 200)
        self.assertEqual(json.loads(response.value()), {u'submit_sm_count': 1, u'unit_rate': 1.5})

    @defer.inlineCallbacks
    def test_rate_rated_route_defined_balance_long_content(self):
        response = yield self.web.get("rate", {'username': 'user3', 
                                               'password': 'correct',
                                               'to': '98700177',
                                               'content': 'AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA'})
        self.assertEqual(response.responseCode, 200)
        self.assertEqual(json.loads(response.value()), {u'submit_sm_count': 2, u'unit_rate': 1.5})

class BalanceTestCases(HTTPApiTestCases):
    def setUp(self):
        HTTPApiTestCases.setUp(self)

        # Provision Router with additional Users and Routes
        u2 = User(2, Group(2), 'user2', 'correct')
        u2.mt_credential.setQuota('balance', 100.2)
        u2.mt_credential.setQuota('submit_sm_count', 30)
        u3 = User(3, Group(2), 'user3', 'correct')
        u3.mt_credential.setQuota('balance', 10)
        self.RouterPB_f.users.append(u2)
        self.RouterPB_f.users.append(u3)

    @defer.inlineCallbacks
    def test_balance_with_correct_args(self):
        response = yield self.web.get("balance", {'username': 'fourat', 
                                               'password': 'incorrec'})
        self.assertEqual(response.responseCode, 403)
        self.assertEqual(json.loads(response.value()), u'Authentication failure for username:%s' % 'fourat')

    @defer.inlineCallbacks
    def test_balance_with_incorrect_args(self):
        response = yield self.web.get("balance", {'username': 'fourat', 
                                               'passwd': 'correct'})
        self.assertEqual(response.responseCode, 400)
        self.assertEqual(json.loads(response.value()), u'Mandatory argument [password] is not found.')

    @defer.inlineCallbacks
    def test_balance_with_auth_success_unlimited_quotas(self):
        response = yield self.web.get("balance", {'username': 'fourat', 
                                               'password': 'correct'})
        self.assertEqual(response.responseCode, 200)
        self.assertEqual(json.loads(response.value()), {u'balance': u'ND', u'sms_count': u'ND'})

    @defer.inlineCallbacks
    def test_balance_with_auth_success_defined_quotas_u2(self):
        response = yield self.web.get("balance", {'username': 'user2', 
                                               'password': 'correct'})
        self.assertEqual(response.responseCode, 200)
        self.assertEqual(json.loads(response.value()), {u'balance': 100.2, u'sms_count': 30})

    @defer.inlineCallbacks
    def test_balance_with_auth_success_defined_quotas_u3(self):
        response = yield self.web.get("balance", {'username': 'user3', 
                                               'password': 'correct'})
        self.assertEqual(response.responseCode, 200)
        self.assertEqual(json.loads(response.value()), {u'balance': 10, u'sms_count': u'ND'})

class UserStatsTestCases(HTTPApiTestCases):
    username = 'fourat'

    @defer.inlineCallbacks
    def test_send_failure(self):
        # Save before
        _submit_sm_request_count = self.RouterPB_f.getUser(1).getCnxStatus().httpapi['submit_sm_request_count']

        response = yield self.web.get("send", {'username': 'fourat', 
                                               'password': 'incorrec',
                                               'to': '98700177',
                                               'content': 'anycontent'})
        self.assertNotEqual(response.responseCode, 200)
        self.assertEqual(_submit_sm_request_count+0, self.RouterPB_f.getUser(1).getCnxStatus().httpapi['submit_sm_request_count'])

    @defer.inlineCallbacks
    def test_send_success(self):
        # Save before
        _submit_sm_request_count = self.RouterPB_f.getUser(1).getCnxStatus().httpapi['submit_sm_request_count']

        response = yield self.web.get("send", {'username': 'fourat', 
                                               'password': 'correct',
                                               'to': '98700177',
                                               'content': 'anycontent'})
        self.assertEqual(response.responseCode, 500)
        self.assertEqual(_submit_sm_request_count+1, self.RouterPB_f.getUser(1).getCnxStatus().httpapi['submit_sm_request_count'])

    @defer.inlineCallbacks
    def test_balance_failure(self):
        # Save before
        _balance_request_count = self.RouterPB_f.getUser(1).getCnxStatus().httpapi['balance_request_count']

        response = yield self.web.get("balance", {'username': 'fourat', 
                                               'password': 'incorrec'})
        self.assertNotEqual(response.responseCode, 200)
        self.assertEqual(_balance_request_count+0, self.RouterPB_f.getUser(1).getCnxStatus().httpapi['balance_request_count'])

    @defer.inlineCallbacks
    def test_balance_success(self):
        # Save before
        _balance_request_count = self.RouterPB_f.getUser(1).getCnxStatus().httpapi['balance_request_count']

        response = yield self.web.get("balance", {'username': 'fourat', 
                                               'password': 'correct'})
        self.assertEqual(response.responseCode, 200)
        self.assertEqual(_balance_request_count+1, self.RouterPB_f.getUser(1).getCnxStatus().httpapi['balance_request_count'])

    @defer.inlineCallbacks
    def test_rate_failure(self):
        # Save before
        _rate_request_count = self.RouterPB_f.getUser(1).getCnxStatus().httpapi['rate_request_count']

        response = yield self.web.get("rate", {'username': 'fourat', 
                                               'password': 'incorrec',
                                               'to': '98700177'})
        self.assertNotEqual(response.responseCode, 200)
        self.assertEqual(_rate_request_count+0, self.RouterPB_f.getUser(1).getCnxStatus().httpapi['rate_request_count'])

    @defer.inlineCallbacks
    def test_rate_success(self):
        # Save before
        _rate_request_count = self.RouterPB_f.getUser(1).getCnxStatus().httpapi['rate_request_count']

        response = yield self.web.get("rate", {'username': 'fourat', 
                                               'password': 'correct',
                                               'to': '98700177'})
        self.assertEqual(response.responseCode, 200)
        self.assertEqual(_rate_request_count+1, self.RouterPB_f.getUser(1).getCnxStatus().httpapi['rate_request_count'])

class StatsTestCases(HTTPApiTestCases):
    username = 'fourat'

    def setUp(self):
      HTTPApiTestCases.setUp(self)

      # Re-init stats singleton collector
      created_at = HttpAPIStatsCollector().get().get('created_at')
      HttpAPIStatsCollector().get().init()
      HttpAPIStatsCollector().get().set('created_at', created_at)

    @defer.inlineCallbacks
    def test_send_with_auth_failure(self):
        stats = HttpAPIStatsCollector().get()

        self.assertTrue(type(stats.get('created_at')) == datetime)
        self.assertEqual(stats.get('request_count'), 0)
        self.assertEqual(stats.get('last_request_at'), 0)
        self.assertEqual(stats.get('auth_error_count'), 0)
        self.assertEqual(stats.get('route_error_count'), 0)
        self.assertEqual(stats.get('throughput_error_count'), 0)
        self.assertEqual(stats.get('charging_error_count'), 0)
        self.assertEqual(stats.get('server_error_count'), 0)
        self.assertEqual(stats.get('success_count'), 0)
        self.assertEqual(stats.get('last_success_at'), 0)

        response = yield self.web.get("send", {'username': self.username, 
                                               'password': 'incorrec',
                                               'to': '98700177',
                                               'content': 'anycontent'})
        self.assertEqual(response.responseCode, 403)
        self.assertEqual(response.value(), "Error \"Authentication failure for username:%s\"" % self.username)

        self.assertTrue(type(stats.get('created_at')) == datetime)
        self.assertEqual(stats.get('request_count'), 1)
        self.assertTrue(type(stats.get('last_request_at')) == datetime)
        self.assertEqual(stats.get('auth_error_count'), 1)
        self.assertEqual(stats.get('route_error_count'), 0)
        self.assertEqual(stats.get('throughput_error_count'), 0)
        self.assertEqual(stats.get('charging_error_count'), 0)
        self.assertEqual(stats.get('server_error_count'), 0)
        self.assertEqual(stats.get('success_count'), 0)
        self.assertEqual(stats.get('last_success_at'), 0)

    @defer.inlineCallbacks
    def test_send_with_auth_success(self):
        stats = HttpAPIStatsCollector().get()

        response = yield self.web.get("send", {'username': self.username, 
                                               'password': 'correct',
                                               'to': '98700177',
                                               'content': 'anycontent'})
        self.assertEqual(response.responseCode, 500)
        # This is a normal error since SMPPClientManagerPB is not really running
        self.assertEqual(response.value(), "Error \"Cannot send submit_sm, check SMPPClientManagerPB log file for details\"")

        self.assertTrue(type(stats.get('created_at')) == datetime)
        self.assertEqual(stats.get('request_count'), 1)
        self.assertTrue(type(stats.get('last_request_at')) == datetime)
        self.assertEqual(stats.get('auth_error_count'), 0)
        self.assertEqual(stats.get('route_error_count'), 0)
        self.assertEqual(stats.get('throughput_error_count'), 0)
        self.assertEqual(stats.get('charging_error_count'), 0)
        self.assertEqual(stats.get('server_error_count'), 1)
        self.assertEqual(stats.get('success_count'), 0)
        self.assertEqual(stats.get('last_success_at'), 0)