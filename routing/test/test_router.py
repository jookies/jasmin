# Copyright 2012 Fourat Zouari <fourat@gmail.com>
# See LICENSE for details.

import pickle
from twisted.internet import reactor, defer
from twisted.trial import unittest
from twisted.spread import pb
from jasmin.routing.router import RouterPB
from jasmin.routing.proxies import RouterPBProxy
from jasmin.routing.configs import RouterPBConfig
from jasmin.routing.Routes import DefaultRoute, StaticMTRoute
from jasmin.routing.Filters import GroupFilter
from jasmin.routing.jasminApi import Connector, Group, User

class RouterPBTestCase(unittest.TestCase):
    def setUp(self):
        # Initiating config objects without any filename
        # will lead to setting defaults and that's what we
        # need to run the tests
        RouterPBConfigInstance = RouterPBConfig()
        
        # Launch the router server
        pbRoot = RouterPB()
        pbRoot.setConfig(RouterPBConfigInstance)
        self.PBServer = reactor.listenTCP(0, pb.PBServerFactory(pbRoot))
        self.pbPort = self.PBServer.getHost().port
        
    def tearDown(self):
        self.disconnect()
        self.PBServer.stopListening()
        
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