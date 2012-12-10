# Copyright 2012 Fourat Zouari <fourat@gmail.com>
# See LICENSE for details.

import pickle
from twisted.internet import reactor, defer
from twisted.trial import unittest
from twisted.spread import pb
from twisted.web import server
from twisted.web.client import getPage
from jasmin.routing.router import RouterPB
from jasmin.routing.proxies import RouterPBProxy
from jasmin.routing.configs import RouterPBConfig
from jasmin.protocols.http.server import HTTPApi
from jasmin.protocols.http.configs import HTTPApiConfig
from jasmin.protocols.smpp.configs import SMPPClientConfig
from jasmin.managers.proxies import SMPPClientManagerPBProxy
from jasmin.managers.clients import SMPPClientManagerPB
from jasmin.managers.configs import SMPPClientPBConfig
from jasmin.routing.Routes import DefaultRoute, StaticMTRoute
from jasmin.routing.Filters import GroupFilter
from jasmin.routing.jasminApi import Connector, Group, User
from jasmin.queues.factory import AmqpFactory
from jasmin.queues.configs import AmqpConfig

class RouterPBTestCase(unittest.TestCase):
    def setUp(self):
        # Initiating config objects without any filename
        # will lead to setting defaults and that's what we
        # need to run the tests
        RouterPBConfigInstance = RouterPBConfig()
        
        # Launch the router server
        self.pbRoot_f = RouterPB()
        self.pbRoot_f.setConfig(RouterPBConfigInstance)
        self.PBServer = reactor.listenTCP(0, pb.PBServerFactory(self.pbRoot_f))
        self.pbPort = self.PBServer.getHost().port
        
    def tearDown(self):
        self.disconnect()
        self.PBServer.stopListening()
        
class HttpServerTestCase(RouterPBTestCase):
    def setUp(self):
        RouterPBTestCase.setUp(self)
        
        # Initiating config objects without any filename
        # will lead to setting defaults and that's what we
        # need to run the tests
        httpApiConfigInstance = HTTPApiConfig()
        SMPPClientPBConfigInstance = SMPPClientPBConfig()
        
        # Smpp client manager is required for HTTPApi instanciation
        self.clientManager_f = SMPPClientManagerPB()
        self.clientManager_f.setConfig(SMPPClientPBConfigInstance)

        # Launch the http server
        httpApi = HTTPApi(self.pbRoot_f, self.clientManager_f, httpApiConfigInstance)
        self.httpServer = reactor.listenTCP(httpApiConfigInstance.port, server.Site(httpApi))
        self.httpPort  = httpApiConfigInstance.port
        
    def tearDown(self):
        RouterPBTestCase.tearDown(self)
        
        self.httpServer.stopListening()

class SMPPClientManagerPBTestCase(HttpServerTestCase):
    @defer.inlineCallbacks
    def setUp(self):
        HttpServerTestCase.setUp(self)
        
        # Initiating config objects without any filename
        # will lead to setting defaults and that's what we
        # need to run the tests
        AMQPServiceConfigInstance = AmqpConfig()
        AMQPServiceConfigInstance.reconnectOnConnectionLoss = False

        # Launch AMQP Broker
        self.amqpBroker = AmqpFactory(AMQPServiceConfigInstance)
        self.amqpBroker.preConnect()
        self.amqpClient = reactor.connectTCP(AMQPServiceConfigInstance.host, AMQPServiceConfigInstance.port, self.amqpBroker)
        
        # Wait for AMQP Broker connection to get ready
        yield self.amqpBroker.getChannelReadyDeferred()
        
        # Setup smpp client manager pb
        self.clientManager_f.addAmqpBroker(self.amqpBroker)
        self.CManagerServer = reactor.listenTCP(0, pb.PBServerFactory(self.clientManager_f))
        self.CManagerPort = self.CManagerServer.getHost().port
        
        # Set a smpp client manager proxy instance
        self.SMPPClientManagerPBProxy = SMPPClientManagerPBProxy()

    def tearDown(self):
        HttpServerTestCase.tearDown(self)
        
        self.SMPPClientManagerPBProxy.disconnect()
        self.CManagerServer.stopListening()
        self.amqpClient.disconnect()
    
class RoutingTestCases(RouterPBProxy, RouterPBTestCase):
    @defer.inlineCallbacks
    def test_add_list_and_flush_mt_route(self):
        yield self.connect('127.0.0.1', self.pbPort)
        
        yield self.mtroute_add(StaticMTRoute([GroupFilter(Group(1))], Connector('abc')), 2)
        yield self.mtroute_add(DefaultRoute(Connector('def')), 0)
        listRet1 = yield self.mtroute_get_all()
        listRet1 = pickle.loads(listRet1)
        
        yield self.mtroute_flush()
        listRet2 = yield self.mtroute_get_all()
        listRet2 = pickle.loads(listRet2)

        self.assertEqual(2, len(listRet1))
        self.assertEqual(0, len(listRet2))
        
    @defer.inlineCallbacks
    def test_add_list_and_flush_mo_route(self):
        yield self.connect('127.0.0.1', self.pbPort)
        
        yield self.moroute_add(DefaultRoute(Connector('def')), 0)
        listRet1 = yield self.moroute_get_all()
        listRet1 = pickle.loads(listRet1)
        
        yield self.moroute_flush()
        listRet2 = yield self.moroute_get_all()
        listRet2 = pickle.loads(listRet2)

        self.assertEqual(1, len(listRet1))
        self.assertEqual(0, len(listRet2))
        
class AuthenticationTestCases(RouterPBProxy, RouterPBTestCase):
    @defer.inlineCallbacks
    def test_add_list_and_remove_user(self):
        yield self.connect('127.0.0.1', self.pbPort)
        
        u1 = User(1, Group(1), 'username', 'password')
        u2 = User(2, Group(1), 'username2', 'password')

        yield self.user_add(u1)
        c = yield self.user_get_all()
        c = pickle.loads(c)
        self.assertEqual(1, len(c))

        yield self.user_add(u2)
        c = yield self.user_get_all()
        c = pickle.loads(c)
        self.assertEqual(2, len(c))
        
        yield self.user_remove(u1)
        c = yield self.user_get_all()
        c = pickle.loads(c)
        self.assertEqual(1, len(c))

        yield self.user_add(u2)
        yield self.user_remove_all()
        c = yield self.user_get_all()
        c = pickle.loads(c)
        self.assertEqual(0, len(c))
        
    @defer.inlineCallbacks
    def test_user_unicity(self):
        yield self.connect('127.0.0.1', self.pbPort)
        
        # Users are unique by uid or username
        # The below 3 samples must be saved as two users
        # the Router will replace a User if it finds the same
        # uid or username
        u1 = User(1, Group(1), 'username', 'password')
        u2 = User(2, Group(1), 'username', 'password')
        u3 = User(2, Group(1), 'other', 'password')

        yield self.user_add(u1)
        yield self.user_add(u2)
        yield self.user_add(u3)
        c = yield self.user_get_all()
        c = pickle.loads(c)
        self.assertEqual(1, len(c))
        
        u = yield self.user_authenticate('other', 'password')
        u = pickle.loads(u)
        self.assertEqual(u3.username, u.username)
        
class MessageDeliveryTestCases(RouterPBProxy, SMPPClientManagerPBTestCase):
    @defer.inlineCallbacks
    def test_mt_delivery(self):
        yield self.connect('127.0.0.1', self.pbPort)
        
        c1 = Connector('def')
        u1 = User(1, Group(1), 'username', 'password')
        u2 = User(1, Group(1), 'username2', 'password2')
        yield self.user_add(u1)

        yield self.mtroute_add(DefaultRoute(c1), 0)
        
        # Send a SMS MT through http interface
        url_ko = 'http://127.0.0.1:1401/send?to=98700177&content=test&username=%s&password=%s' % (u2.username, u2.password)
        url_ok = 'http://127.0.0.1:1401/send?to=98700177&content=test&username=%s&password=%s' % (u1.username, u1.password)
        
        # Incorrect username/password will lead to '403 Forbidden' error
        lastErrorStatus = 200
        try:
            yield getPage(url_ko)
        except Exception, e:
            lastErrorStatus = e.status
        self.assertEqual(lastErrorStatus, '403')
        
        # Since Connector('def') doesnt really exist, the message will not be routed
        # to a queue, a 500 error will be returned, and more details will be written
        # in smpp client manager log:
        # 'Trying to enqueue a SUBMIT_SM to a connector with an unknown cid: def'
        try:
            yield getPage(url_ok)
        except Exception, e:
            lastErrorStatus = e.status
        self.assertEqual(lastErrorStatus, '500')
        
        # Now we'll create the connecter 'def' and send an MT to it
        yield self.SMPPClientManagerPBProxy.connect('127.0.0.1', self.CManagerPort)
        c1Config = SMPPClientConfig(id=c1.cid)        
        yield self.SMPPClientManagerPBProxy.add(c1Config)
        
        # We should receive a msg id
        c = yield getPage(url_ok)
        self.assertEqual(c[:7], 'Success')
        # @todo: Should be a real uuid pattern testing 
        self.assertApproximates(len(c), 40, 10)