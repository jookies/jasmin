# Copyright 2012 Fourat Zouari <fourat@gmail.com>
# See LICENSE for details.

from twisted.trial.unittest import TestCase
from twisted.internet import defer
from twisted_web_test_utils import DummySite
from jasmin.protocols.http.configs import HTTPApiConfig
from jasmin.protocols.http.server import HTTPApi
from jasmin.routing.router import RouterPB
from jasmin.routing.configs import RouterPBConfig
from jasmin.managers.clients import SMPPClientManagerPB
from jasmin.managers.configs import SMPPClientPBConfig
from jasmin.routing.jasminApi import User, Group, Connector
from jasmin.routing.Routes import DefaultRoute

class HTTPApiTestCases(TestCase):
    def setUp(self):
        # Instanciate a RouterPB (a requirement for HTTPApi)
        RouterPBConfigInstance = RouterPBConfig()
        RouterPB_f = RouterPB()
        RouterPB_f.setConfig(RouterPBConfigInstance)
        
        # Provision Router with User and Route
        RouterPB_f.users.append(User(1, Group(1), 'fourat', 'correct'))
        RouterPB_f.mt_routing_table.add(DefaultRoute(Connector('abc')), 0)

        # Instanciate a SMPPClientManagerPB (a requirement for HTTPApi)
        SMPPClientPBConfigInstance = SMPPClientPBConfig()
        clientManager_f = SMPPClientManagerPB()
        clientManager_f.setConfig(SMPPClientPBConfigInstance)
        
        httpApiConfigInstance = HTTPApiConfig()
        self.web = DummySite(HTTPApi(RouterPB_f, clientManager_f, httpApiConfigInstance))

class SendTestCases(HTTPApiTestCases):
    @defer.inlineCallbacks
    def test_send_with_correct_args(self):
        response = yield self.web.get("send", {'username': 'fourat', 
                                               'password': 'incorrect',
                                               'to': '98700177',
                                               'content': 'anycontent'})
        self.assertEqual(response.responseCode, 403)
        self.assertEqual(response.value(), "Error \"Authentication failure\"")

    @defer.inlineCallbacks
    def test_send_with_auth_success(self):
        response = yield self.web.get("send", {'username': 'fourat', 
                                               'password': 'correct',
                                               'to': '98700177',
                                               'content': 'anycontent'})
        self.assertEqual(response.responseCode, 403)
        # This is a normal error since SMPPClientManagerPB is not really running
        self.assertEqual(response.value(), "Error \"Cannot send submit_sm, check log file for details\"")

    @defer.inlineCallbacks
    def test_send_without_args(self):
        response = yield self.web.get("send")
        self.assertEqual(response.responseCode, 403)
        self.assertEqual(response.value(), "Error \"'username'\"")