# -*- coding: utf-8 -*- 
# Copyright 2012 Fourat Zouari <fourat@gmail.com>
# See LICENSE for details.

import glob
import os
import mock
import pickle
import time
import urllib
import string
import random
import copy
import struct
from twisted.internet import reactor, defer
from twisted.trial import unittest
from twisted.spread import pb
from twisted.web import server
from twisted.web.client import getPage
from jasmin.protocols.smpp.test.smsc_simulator import *
from jasmin.redis.configs import RedisForJasminConfig
from jasmin.redis.client import ConnectionWithConfiguration
from jasmin.routing.router import RouterPB
from jasmin.routing.proxies import RouterPBProxy
from jasmin.routing.configs import RouterPBConfig
from jasmin.routing.test.http_server import AckServer
from jasmin.routing.configs import deliverSmHttpThrowerConfig, DLRThrowerConfig
from jasmin.routing.throwers import deliverSmHttpThrower, DLRThrower
from jasmin.protocols.http.server import HTTPApi
from jasmin.protocols.http.configs import HTTPApiConfig
from jasmin.protocols.smpp.configs import SMPPClientConfig
from jasmin.managers.proxies import SMPPClientManagerPBProxy
from jasmin.managers.clients import SMPPClientManagerPB
from jasmin.managers.configs import SMPPClientPBConfig
from jasmin.routing.Routes import DefaultRoute, StaticMTRoute
from jasmin.routing.Filters import GroupFilter
from jasmin.routing.jasminApi import *
from jasmin.queues.factory import AmqpFactory
from jasmin.queues.configs import AmqpConfig
from jasmin.vendor.smpp.pdu.pdu_types import EsmClass, EsmClassMode, MoreMessagesToSend

def composeMessage(characters, length):
    if length <= len(characters):
        return ''.join(random.sample(characters, length))
    else:
        s = ''
        while len(s) < length:
            s += ''.join(random.sample(characters, len(characters)))
        return s[:length]
    
def id_generator(size=12, chars=string.ascii_uppercase + string.digits):
    return ''.join(random.choice(chars) for x in range(size))

class RouterPBTestCase(unittest.TestCase):
    def setUp(self):
        # Initiating config objects without any filename
        # will lead to setting defaults and that's what we
        # need to run the tests
        self.RouterPBConfigInstance = RouterPBConfig()
        
        # Launch the router server
        self.pbRoot_f = RouterPB()
        self.pbRoot_f.setConfig(self.RouterPBConfigInstance)
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
        
        # Add the broker to the RouterPB
        self.pbRoot_f.addAmqpBroker(self.amqpBroker)
        
        # Setup smpp client manager pb
        self.clientManager_f.addAmqpBroker(self.amqpBroker)
        self.CManagerServer = reactor.listenTCP(0, pb.PBServerFactory(self.clientManager_f))
        self.CManagerPort = self.CManagerServer.getHost().port
        
        # Start DLRThrower
        DLRThrowerConfigInstance = DLRThrowerConfig()
        self.DLRThrower = DLRThrower()
        self.DLRThrower.setConfig(DLRThrowerConfigInstance)
        self.DLRThrower.addAmqpBroker(self.amqpBroker)
        
        # Connect to redis server
        RedisForJasminConfigInstance = RedisForJasminConfig()
        self.rc = yield ConnectionWithConfiguration(RedisForJasminConfigInstance)
        # Authenticate and select db
        if RedisForJasminConfigInstance.password is not None:
            yield self.rc.auth(RedisForJasminConfigInstance.password)
            yield self.rc.select(RedisForJasminConfigInstance.dbid)
        # Connect CM with RC:
        self.clientManager_f.addRedisClient(self.rc)
        
        # Set a smpp client manager proxy instance
        self.SMPPClientManagerPBProxy = SMPPClientManagerPBProxy()
    
    @defer.inlineCallbacks
    def tearDown(self):
        HttpServerTestCase.tearDown(self)
        
        self.SMPPClientManagerPBProxy.disconnect()
        self.CManagerServer.stopListening()
        self.amqpClient.disconnect()
        yield self.rc.disconnect()
        
class RoutingTestCases(RouterPBProxy, RouterPBTestCase):
    @defer.inlineCallbacks
    def test_add_list_and_flush_mt_route(self):
        yield self.connect('127.0.0.1', self.pbPort)
        
        yield self.mtroute_add(StaticMTRoute([GroupFilter(Group(1))], SmppClientConnector(id_generator())), 2)
        yield self.mtroute_add(DefaultRoute(SmppClientConnector(id_generator())), 0)
        listRet1 = yield self.mtroute_get_all()
        listRet1 = pickle.loads(listRet1)
        
        yield self.mtroute_flush()
        listRet2 = yield self.mtroute_get_all()
        listRet2 = pickle.loads(listRet2)

        self.assertEqual(2, len(listRet1))
        self.assertEqual(0, len(listRet2))
        
    @defer.inlineCallbacks
    def test_add_list_and_remove_mt_route(self):
        yield self.connect('127.0.0.1', self.pbPort)
        
        yield self.mtroute_add(StaticMTRoute([GroupFilter(Group(1))], SmppClientConnector(id_generator())), 2)
        yield self.mtroute_add(DefaultRoute(SmppClientConnector(id_generator())), 0)
        listRet1 = yield self.mtroute_get_all()
        listRet1 = pickle.loads(listRet1)
        
        yield self.mtroute_remove(2)
        listRet2 = yield self.mtroute_get_all()
        listRet2 = pickle.loads(listRet2)

        self.assertEqual(2, len(listRet1))
        self.assertEqual(1, len(listRet2))

    @defer.inlineCallbacks
    def test_add_list_and_flush_mo_route(self):
        yield self.connect('127.0.0.1', self.pbPort)
        
        yield self.moroute_add(DefaultRoute(HttpConnector(id_generator(), 'http://127.0.0.1')), 0)
        listRet1 = yield self.moroute_get_all()
        listRet1 = pickle.loads(listRet1)
        
        yield self.moroute_flush()
        listRet2 = yield self.moroute_get_all()
        listRet2 = pickle.loads(listRet2)

        self.assertEqual(1, len(listRet1))
        self.assertEqual(0, len(listRet2))
        
    @defer.inlineCallbacks
    def test_add_list_and_remove_mo_route(self):
        yield self.connect('127.0.0.1', self.pbPort)
        
        yield self.moroute_add(DefaultRoute(HttpConnector(id_generator(), 'http://127.0.0.1')), 0)
        listRet1 = yield self.moroute_get_all()
        listRet1 = pickle.loads(listRet1)
        
        yield self.mtroute_remove(0)
        listRet2 = yield self.mtroute_get_all()
        listRet2 = pickle.loads(listRet2)

        self.assertEqual(1, len(listRet1))
        self.assertEqual(0, len(listRet2))

class RoutingConnectorTypingCases(RouterPBProxy, RouterPBTestCase):
    @defer.inlineCallbacks
    def test_add_mt_route(self):
        yield self.connect('127.0.0.1', self.pbPort)
        
        r = yield self.mtroute_add(DefaultRoute(HttpConnector(id_generator(), 'http://127.0.0.1')), 0)
        self.assertFalse(r)
        r = yield self.mtroute_add(DefaultRoute(Connector(id_generator())), 0)
        self.assertFalse(r)
        r = yield self.mtroute_add(DefaultRoute(SmppClientConnector(id_generator())), 0)
        self.assertTrue(r)
        
    @defer.inlineCallbacks
    def test_add_mo_route(self):
        yield self.connect('127.0.0.1', self.pbPort)
        
        r = yield self.moroute_add(DefaultRoute(HttpConnector(id_generator(), 'http://127.0.0.1')), 0)
        self.assertTrue(r)
        r = yield self.moroute_add(DefaultRoute(Connector(id_generator())), 0)
        self.assertFalse(r)
        r = yield self.moroute_add(DefaultRoute(SmppClientConnector(id_generator())), 0)
        self.assertFalse(r)

class UserAndGroupTestCases(RouterPBProxy, RouterPBTestCase):
    @defer.inlineCallbacks
    def test_add_user_without_group(self):
        yield self.connect('127.0.0.1', self.pbPort)
        
        # This group will not be added to router
        g1 = Group(1)
        
        u1 = User(1, g1, 'username', 'password')
        r = yield self.user_add(u1)
        self.assertEqual(r, False)

    @defer.inlineCallbacks
    def test_authenticate(self):
        yield self.connect('127.0.0.1', self.pbPort)
        
        g1 = Group(1)
        yield self.group_add(g1)
        
        u1 = User(1, g1, 'username', 'password')
        yield self.user_add(u1)

        r = yield self.user_authenticate('username', 'password')
        self.assertNotEqual(r, None)
        r = pickle.loads(r)
        self.assertEqual(u1.uid, r.uid)
        self.assertEqual(u1.username, r.username)
        self.assertEqual(u1.password, r.password)
        self.assertEqual(u1.group, g1)

        r = yield self.user_authenticate('username', 'incorrect')
        self.assertEqual(r, None)

        r = yield self.user_authenticate('incorrect', 'password')
        self.assertEqual(r, None)

        r = yield self.user_authenticate('incorrect', 'incorrect')
        self.assertEqual(r, None)
        
    @defer.inlineCallbacks
    def test_add_list_and_remove_group(self):
        yield self.connect('127.0.0.1', self.pbPort)
        
        g1 = Group(1)
        yield self.group_add(g1)
        g2 = Group(2)
        yield self.group_add(g2)
        g3 = Group(3)
        yield self.group_add(g3)
        
        c = yield self.group_get_all()
        c = pickle.loads(c)
        self.assertEqual(3, len(c))
        
        yield self.group_remove(1)
        c = yield self.group_get_all()
        c = pickle.loads(c)
        self.assertEqual(2, len(c))
        
        yield self.group_remove_all()
        c = yield self.group_get_all()
        c = pickle.loads(c)
        self.assertEqual(0, len(c))

    @defer.inlineCallbacks
    def test_remove_not_empty_group(self):
        yield self.connect('127.0.0.1', self.pbPort)
        
        g1 = Group(1)
        yield self.group_add(g1)
        
        u1 = User(1, g1, 'username1', 'password')
        yield self.user_add(u1)
        u2 = User(2, g1, 'username2', 'password')
        yield self.user_add(u2)

        c = yield self.user_get_all()
        c = pickle.loads(c)
        self.assertEqual(2, len(c))

        yield self.group_remove_all()
        c = yield self.group_get_all()
        c = pickle.loads(c)
        self.assertEqual(0, len(c))

        c = yield self.user_get_all()
        c = pickle.loads(c)
        self.assertEqual(0, len(c))

    @defer.inlineCallbacks
    def test_add_list_and_remove_user(self):
        yield self.connect('127.0.0.1', self.pbPort)
        
        g1 = Group(1)
        yield self.group_add(g1)
        
        u1 = User(1, g1, 'username', 'password')
        u2 = User(2, g1, 'username2', 'password')

        yield self.user_add(u1)
        c = yield self.user_get_all()
        c = pickle.loads(c)
        self.assertEqual(1, len(c))

        yield self.user_add(u2)
        c = yield self.user_get_all()
        c = pickle.loads(c)
        self.assertEqual(2, len(c))
        
        yield self.user_remove(u1.uid)
        c = yield self.user_get_all()
        c = pickle.loads(c)
        self.assertEqual(1, len(c))

        yield self.user_add(u2)
        yield self.user_remove_all()
        c = yield self.user_get_all()
        c = pickle.loads(c)
        self.assertEqual(0, len(c))
        
    @defer.inlineCallbacks
    def test_add_list_user_with_groups(self):
        yield self.connect('127.0.0.1', self.pbPort)
        
        g1 = Group(1)
        yield self.group_add(g1)
        
        g2 = Group(2)
        yield self.group_add(g2)

        u1 = User(1, g1, 'username', 'password')
        yield self.user_add(u1)
        u2 = User(2, g2, 'username2', 'password')
        yield self.user_add(u2)

        # Get all users
        c = yield self.user_get_all()
        c = pickle.loads(c)
        self.assertEqual(2, len(c))
        
        # Get users from gid=1
        c = yield self.user_get_all(1)
        c = pickle.loads(c)
        self.assertEqual(1, len(c))

    @defer.inlineCallbacks
    def test_user_unicity(self):
        yield self.connect('127.0.0.1', self.pbPort)
        
        g1 = Group(1)
        yield self.group_add(g1)
        
        # Users are unique by uid or username
        # The below 3 samples must be saved as two users
        # the Router will replace a User if it finds the same
        # uid or username
        u1 = User(1, g1, 'username', 'password')
        u2 = User(2, g1, 'username', 'password')
        u3 = User(2, g1, 'other', 'password')

        yield self.user_add(u1)
        yield self.user_add(u2)
        yield self.user_add(u3)
        c = yield self.user_get_all()
        c = pickle.loads(c)
        self.assertEqual(1, len(c))
        
        u = yield self.user_authenticate('other', 'password')
        u = pickle.loads(u)
        self.assertEqual(u3.username, u.username)

class ConfigurationPersistenceTestCases(RouterPBProxy, RouterPBTestCase):
    def tearDown(self):
        # Remove persisted configurations
        filelist = glob.glob("%s/*" % self.RouterPBConfigInstance.store_path)
        for f in filelist:
            os.remove(f)
            
        return RouterPBTestCase.tearDown(self)
    
    @defer.inlineCallbacks
    def test_persist_default(self):
        yield self.connect('127.0.0.1', self.pbPort)
        
        persistRet = yield self.persist()
        
        self.assertTrue(persistRet)

    @defer.inlineCallbacks
    def test_load_undefined_profile(self):
        yield self.connect('127.0.0.1', self.pbPort)
        
        loadRet = yield self.load()
        
        self.assertFalse(loadRet)

    @defer.inlineCallbacks
    def test_add_persist_and_load_default(self):
        yield self.connect('127.0.0.1', self.pbPort)
        
        # Add users and groups
        g1 = Group(1)
        yield self.group_add(g1)
        
        u1 = User(1, g1, 'username', 'password')
        yield self.user_add(u1)
        u2 = User(2, g1, 'username2', 'password')
        yield self.user_add(u2)
        
        # List users
        c = yield self.user_get_all()
        c = pickle.loads(c)
        self.assertEqual(2, len(c))
        # List groups
        c = yield self.group_get_all()
        c = pickle.loads(c)
        self.assertEqual(1, len(c))
        
        # Persist
        yield self.persist()

        # Remove all users
        yield self.user_remove_all()

        # List and assert
        c = yield self.user_get_all()
        c = pickle.loads(c)
        self.assertEqual(0, len(c))

        # Load
        yield self.load()

        # List users
        c = yield self.user_get_all()
        c = pickle.loads(c)
        self.assertEqual(2, len(c))
        # List groups
        c = yield self.group_get_all()
        c = pickle.loads(c)
        self.assertEqual(1, len(c))

    @defer.inlineCallbacks
    def test_add_persist_and_load_profile(self):
        yield self.connect('127.0.0.1', self.pbPort)
        
        # Add users and groups
        g1 = Group(1)
        yield self.group_add(g1)
        
        u1 = User(1, g1, 'username', 'password')
        yield self.user_add(u1)
        u2 = User(2, g1, 'username2', 'password')
        yield self.user_add(u2)
        
        # List users
        c = yield self.user_get_all()
        c = pickle.loads(c)
        self.assertEqual(2, len(c))
        # List groups
        c = yield self.group_get_all()
        c = pickle.loads(c)
        self.assertEqual(1, len(c))
        
        # Persist
        yield self.persist('profile')

        # Remove all users
        yield self.user_remove_all()

        # List and assert
        c = yield self.user_get_all()
        c = pickle.loads(c)
        self.assertEqual(0, len(c))

        # Load
        yield self.load('profile')

        # List users
        c = yield self.user_get_all()
        c = pickle.loads(c)
        self.assertEqual(2, len(c))
        # List groups
        c = yield self.group_get_all()
        c = pickle.loads(c)
        self.assertEqual(1, len(c))

    @defer.inlineCallbacks
    def test_persist_scope_groups(self):
        yield self.connect('127.0.0.1', self.pbPort)
        
        # Add users and groups
        g1 = Group(1)
        yield self.group_add(g1)
        
        u1 = User(1, g1, 'username', 'password')
        yield self.user_add(u1)
        u2 = User(2, g1, 'username2', 'password')
        yield self.user_add(u2)
        
        # List users
        c = yield self.user_get_all()
        c = pickle.loads(c)
        self.assertEqual(2, len(c))
        # List groups
        c = yield self.group_get_all()
        c = pickle.loads(c)
        self.assertEqual(1, len(c))
        
        # Persist groups only
        yield self.persist(scope='groups')

        # Remove all users
        yield self.user_remove_all()
        # Remove all groups
        yield self.group_remove_all()

        # List users
        c = yield self.user_get_all()
        c = pickle.loads(c)
        self.assertEqual(0, len(c))
        # List groups
        c = yield self.group_get_all()
        c = pickle.loads(c)
        self.assertEqual(0, len(c))

        # Load
        yield self.load(scope='groups') # Load with scope=all may also work

        # List users
        c = yield self.user_get_all()
        c = pickle.loads(c)
        self.assertEqual(0, len(c))
        # List groups
        c = yield self.group_get_all()
        c = pickle.loads(c)
        self.assertEqual(1, len(c))
        
class SimpleNonConnectedSubmitSmDeliveryTestCases(RouterPBProxy, SMPPClientManagerPBTestCase):
    @defer.inlineCallbacks
    def test_delivery(self):
        yield self.connect('127.0.0.1', self.pbPort)
        
        g1 = Group(1)
        yield self.group_add(g1)
        
        c1 = SmppClientConnector(id_generator())
        u1 = User(1, g1, 'username', 'password')
        u2 = User(1, g1, 'username2', 'password2')
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
        
        # Since Connector doesnt really exist, the message will not be routed
        # to a queue, a 500 error will be returned, and more details will be written
        # in smpp client manager log:
        # 'Trying to enqueue a SUBMIT_SM to a connector with an unknown cid: '
        try:
            yield getPage(url_ok)
        except Exception, e:
            lastErrorStatus = e.status
        self.assertEqual(lastErrorStatus, '500')
        
        # Now we'll create the connecter and send an MT to it
        yield self.SMPPClientManagerPBProxy.connect('127.0.0.1', self.CManagerPort)
        c1Config = SMPPClientConfig(id=c1.cid)        
        yield self.SMPPClientManagerPBProxy.add(c1Config)

        # We should receive a msg id
        c = yield getPage(url_ok)
        self.assertEqual(c[:7], 'Success')
        # @todo: Should be a real uuid pattern testing 
        self.assertApproximates(len(c), 40, 10)
        
class LastClientFactory(Factory):
    lastClient = None
    def buildProtocol(self, addr):
        self.lastClient = Factory.buildProtocol(self, addr)
        return self.lastClient

class HappySMSCTestCase(SMPPClientManagerPBTestCase):
    protocol = ManualDeliveryReceiptHappySMSC
    
    @defer.inlineCallbacks
    def setUp(self):
        yield SMPPClientManagerPBTestCase.setUp(self)
        
        self.smsc_f = LastClientFactory()
        self.smsc_f.protocol = self.protocol      
        self.SMSCPort = reactor.listenTCP(0, self.smsc_f)
                
    @defer.inlineCallbacks
    def tearDown(self):
        yield SMPPClientManagerPBTestCase.tearDown(self)
        
        self.SMSCPort.stopListening()

class SubmitSmTestCaseTools():
    """
    Factorized methods for child classes
    """
    
    @defer.inlineCallbacks
    def prepareRoutingsAndStartConnector(self, bindOperation = 'transceiver'):
        # Routing stuff
        g1 = Group(1)
        yield self.group_add(g1)
        
        self.c1 = SmppClientConnector(id_generator())
        self.u1 = User(1, g1, 'username', 'password')
        yield self.user_add(self.u1)
        yield self.mtroute_add(DefaultRoute(self.c1), 0)

        # Now we'll create the connecter
        yield self.SMPPClientManagerPBProxy.connect('127.0.0.1', self.CManagerPort)
        c1Config = SMPPClientConfig(id=self.c1.cid, port = self.SMSCPort.getHost().port, 
                                    bindOperation = bindOperation)
        yield self.SMPPClientManagerPBProxy.add(c1Config)

        # Start the connector
        yield self.SMPPClientManagerPBProxy.start(self.c1.cid)
        # Wait for 'BOUND_TRX' state
        while True:
            ssRet = yield self.SMPPClientManagerPBProxy.session_state(self.c1.cid)
            if ssRet[:6] == 'BOUND_':
                break;
            else:
                time.sleep(0.2)

        # Configuration
        self.method = 'GET'
        self.postdata = None
        self.params = {'to': '98700177', 
                        'username': self.u1.username, 
                        'password': self.u1.password, 
                        'content': 'test'}

        if hasattr(self, 'AckServer'):
            # Send a SMS MT through http interface and set delivery receipt callback in url
            self.dlr_url = 'http://127.0.0.1:%d/receipt' % (self.AckServer.getHost().port)
            
            self.AckServerResource.render_POST = mock.Mock(wraps=self.AckServerResource.render_POST)
            self.AckServerResource.render_GET = mock.Mock(wraps=self.AckServerResource.render_GET)

    @defer.inlineCallbacks
    def stopSmppClientConnectors(self):
        # Disconnect the connector
        yield self.SMPPClientManagerPBProxy.stop(self.c1.cid)
        # Wait for 'BOUND_TRX' state
        while True:
            ssRet = yield self.SMPPClientManagerPBProxy.session_state(self.c1.cid)
            if ssRet == 'NONE':
                break;
            else:
                time.sleep(0.2)

    
class DlrCallbackingTestCases(RouterPBProxy, HappySMSCTestCase, SubmitSmTestCaseTools):
    @defer.inlineCallbacks
    def setUp(self):
        yield HappySMSCTestCase.setUp(self)
        
        # Start http servers
        self.AckServerResource = AckServer()
        self.AckServer = reactor.listenTCP(0, server.Site(self.AckServerResource))

    @defer.inlineCallbacks
    def tearDown(self):
        yield HappySMSCTestCase.tearDown(self)
        
        self.AckServer.stopListening()

    @defer.inlineCallbacks
    def test_delivery_with_inurl_dlr_level1(self):
        """Will:
        1. Set a SMS-MT route to connector A
        2. Send a SMS-MT to that route and set a DLR callback for level 1
        3. Wait for the level1 DLR (submit_sm_resp) and run tests
        """
        yield self.connect('127.0.0.1', self.pbPort)
        yield self.prepareRoutingsAndStartConnector()
        
        self.params['dlr-url'] = self.dlr_url
        self.params['dlr-level'] = 1
        baseurl = 'http://127.0.0.1:1401/send?%s' % urllib.urlencode(self.params)
        
        # Send a MT
        # We should receive a msg id
        c = yield getPage(baseurl, method = self.method, postdata = self.postdata)
        msgStatus = c[:7]
        msgId = c[9:45]
        
        yield self.stopSmppClientConnectors()
        
        # Run tests
        self.assertEqual(msgStatus, 'Success')        
        # A DLR must be sent to dlr_url
        self.assertEqual(self.AckServerResource.render_POST.call_count, 1)
        # Message ID must be transmitted in the DLR
        callArgs = self.AckServerResource.render_POST.call_args_list[0][0][0].args
        self.assertEqual(callArgs['id'][0], msgId)

    @defer.inlineCallbacks
    def test_delivery_with_inurl_dlr_level2(self):
        """Will:
        1. Set a SMS-MT route to connector A
        2. Send a SMS-MT to that route and set a DLR callback for level 2
        3. Wait for the level2 DLR (deliver_sm) and run tests
        """
        yield self.connect('127.0.0.1', self.pbPort)
        yield self.prepareRoutingsAndStartConnector()
        
        self.params['dlr-url'] = self.dlr_url
        self.params['dlr-level'] = 1
        baseurl = 'http://127.0.0.1:1401/send?%s' % urllib.urlencode(self.params)
        
        # Send a MT
        # We should receive a msg id
        c = yield getPage(baseurl, method = self.method, postdata = self.postdata)
        msgStatus = c[:7]
        msgId = c[9:45]
        
        # Wait 3 seconds for submit_sm_resp
        exitDeferred = defer.Deferred()
        reactor.callLater(3, exitDeferred.callback, None)
        yield exitDeferred

        # Trigger a deliver_sm containing a DLR
        yield self.SMSCPort.factory.lastClient.trigger_DLR()

        yield self.stopSmppClientConnectors()

        # Run tests
        self.assertEqual(msgStatus, 'Success')        
        # A DLR must be sent to dlr_url
        self.assertEqual(self.AckServerResource.render_POST.call_count, 1)
        # Message ID must be transmitted in the DLR
        callArgs = self.AckServerResource.render_POST.call_args_list[0][0][0].args
        self.assertEqual(callArgs['id'][0], msgId)

    @defer.inlineCallbacks
    def test_delivery_with_inurl_dlr_level3(self):
        """Will:
        1. Set a SMS-MT route to connector A
        2. Send a SMS-MT to that route and set a DLR callback for level 3
        3. Wait for the level1 & level2 DLRs and run tests
        """
        yield self.connect('127.0.0.1', self.pbPort)
        yield self.prepareRoutingsAndStartConnector()
        
        self.params['dlr-url'] = self.dlr_url
        self.params['dlr-level'] = 3
        baseurl = 'http://127.0.0.1:1401/send?%s' % urllib.urlencode(self.params)
        
        # Send a MT
        # We should receive a msg id
        c = yield getPage(baseurl, method = self.method, postdata = self.postdata)
        msgStatus = c[:7]
        msgId = c[9:45]
        
        # Wait 2 seconds for submit_sm_resp
        exitDeferred = defer.Deferred()
        reactor.callLater(3, exitDeferred.callback, None)
        yield exitDeferred

        # Trigger a deliver_sm
        yield self.SMSCPort.factory.lastClient.trigger_DLR()

        yield self.stopSmppClientConnectors()

        # Run tests
        self.assertEqual(msgStatus, 'Success')        
        # A DLR must be sent to dlr_url
        self.assertEqual(self.AckServerResource.render_POST.call_count, 2)
        # Message ID must be transmitted in the DLR
        callArgs_level1 = self.AckServerResource.render_POST.call_args_list[0][0][0].args
        callArgs_level2 = self.AckServerResource.render_POST.call_args_list[1][0][0].args
        self.assertEqual(callArgs_level1['id'][0], msgId)
        self.assertEqual(callArgs_level2['id'][0], msgId)

    @defer.inlineCallbacks
    def test_delivery_with_inurl_dlr_level1_GET(self):
        """Will:
        1. Set a SMS-MT route to connector A
        2. Send a SMS-MT to that route and set a DLR callback for level 1 using GET method
        3. Wait for the level1 DLR (submit_sm_resp) and run tests
        """
        yield self.connect('127.0.0.1', self.pbPort)
        yield self.prepareRoutingsAndStartConnector()
        
        self.params['dlr-url'] = self.dlr_url
        self.params['dlr-level'] = 1
        self.params['dlr-method'] = 'GET'
        baseurl = 'http://127.0.0.1:1401/send?%s' % urllib.urlencode(self.params)
        
        # Send a MT
        # We should receive a msg id
        c = yield getPage(baseurl, method = self.method, postdata = self.postdata)
        msgStatus = c[:7]
        msgId = c[9:45]
        
        yield self.stopSmppClientConnectors()
        
        # Run tests
        self.assertEqual(msgStatus, 'Success')        
        # A DLR must be sent to dlr_url
        self.assertEqual(self.AckServerResource.render_GET.call_count, 1)
        # Message ID must be transmitted in the DLR
        callArgs = self.AckServerResource.render_GET.call_args_list[0][0][0].args
        self.assertEqual(callArgs['id'][0], msgId)

    @defer.inlineCallbacks
    def test_delivery_with_inurl_dlr_level2_GET(self):
        """Will:
        1. Set a SMS-MT route to connector A
        2. Send a SMS-MT to that route and set a DLR callback for level 2 using GET method
        3. Wait for the level2 DLR (deliver_sm) and run tests
        """
        yield self.connect('127.0.0.1', self.pbPort)
        yield self.prepareRoutingsAndStartConnector()
        
        self.params['dlr-url'] = self.dlr_url
        self.params['dlr-level'] = 2
        self.params['dlr-method'] = 'GET'
        baseurl = 'http://127.0.0.1:1401/send?%s' % urllib.urlencode(self.params)
        
        # Send a MT
        # We should receive a msg id
        c = yield getPage(baseurl, method = self.method, postdata = self.postdata)
        msgStatus = c[:7]
        msgId = c[9:45]
        
        # Wait 3 seconds for submit_sm_resp
        exitDeferred = defer.Deferred()
        reactor.callLater(3, exitDeferred.callback, None)
        yield exitDeferred

        # Trigger a deliver_sm containing a DLR
        yield self.SMSCPort.factory.lastClient.trigger_DLR()

        yield self.stopSmppClientConnectors()

        # Run tests
        self.assertEqual(msgStatus, 'Success')        
        # A DLR must be sent to dlr_url
        self.assertEqual(self.AckServerResource.render_GET.call_count, 1)
        # Message ID must be transmitted in the DLR
        callArgs = self.AckServerResource.render_GET.call_args_list[0][0][0].args
        self.assertEqual(callArgs['id'][0], msgId)

    @defer.inlineCallbacks
    def test_delivery_with_inurl_dlr_level3_GET(self):
        """Will:
        1. Set a SMS-MT route to connector A
        2. Send a SMS-MT to that route and set a DLR callback for level 3 using GET method
        3. Wait for the level1 & level2 DLRs and run tests
        """
        yield self.connect('127.0.0.1', self.pbPort)
        yield self.prepareRoutingsAndStartConnector()
        
        self.params['dlr-url'] = self.dlr_url
        self.params['dlr-level'] = 3
        self.params['dlr-method'] = 'GET'
        baseurl = 'http://127.0.0.1:1401/send?%s' % urllib.urlencode(self.params)
        
        # Send a MT
        # We should receive a msg id
        c = yield getPage(baseurl, method = self.method, postdata = self.postdata)
        msgStatus = c[:7]
        msgId = c[9:45]
        
        # Wait 2 seconds for submit_sm_resp
        exitDeferred = defer.Deferred()
        reactor.callLater(3, exitDeferred.callback, None)
        yield exitDeferred

        # Trigger a deliver_sm
        yield self.SMSCPort.factory.lastClient.trigger_DLR()

        yield self.stopSmppClientConnectors()

        # Run tests
        self.assertEqual(msgStatus, 'Success')        
        # A DLR must be sent to dlr_url
        self.assertEqual(self.AckServerResource.render_GET.call_count, 2)
        # Message ID must be transmitted in the DLR
        callArgs_level1 = self.AckServerResource.render_GET.call_args_list[0][0][0].args
        callArgs_level2 = self.AckServerResource.render_GET.call_args_list[1][0][0].args
        self.assertEqual(callArgs_level1['id'][0], msgId)
        self.assertEqual(callArgs_level2['id'][0], msgId)

    @defer.inlineCallbacks
    def test_delivery_empty_content(self):
        yield self.connect('127.0.0.1', self.pbPort)
        yield self.prepareRoutingsAndStartConnector()
        
        self.params['content'] = ''
        baseurl = 'http://127.0.0.1:1401/send?%s' % urllib.urlencode(self.params)
        # Send a MT
        c = yield getPage(baseurl, method = self.method, postdata = self.postdata)
        msgStatus = c[:7]
        
        yield self.stopSmppClientConnectors()

        # Run tests
        self.assertEqual(msgStatus, 'Success')        

class LongSmDlrCallbackingTestCases(RouterPBProxy, HappySMSCTestCase, SubmitSmTestCaseTools):
    @defer.inlineCallbacks
    def setUp(self):
        yield HappySMSCTestCase.setUp(self)
        
        # Start http servers
        self.AckServerResource = AckServer()
        self.AckServer = reactor.listenTCP(0, server.Site(self.AckServerResource))

    @defer.inlineCallbacks
    def tearDown(self):
        yield HappySMSCTestCase.tearDown(self)
        
        self.AckServer.stopListening()

    @defer.inlineCallbacks
    def test_delivery_with_inurl_dlr_level1(self):
        """Will:
        1. Set a SMS-MT route to connector A
        2. Send a SMS-MT to that route and set a DLR callback for level 1
        3. Wait for the level1 DLR (submit_sm_resp) and run tests
        """
        yield self.connect('127.0.0.1', self.pbPort)
        yield self.prepareRoutingsAndStartConnector()
        
        self.params['dlr-url'] = self.dlr_url
        self.params['dlr-level'] = 1
        self.params['content'] = composeMessage({'_'}, 200)
        baseurl = 'http://127.0.0.1:1401/send?%s' % urllib.urlencode(self.params)
        
        # Send a MT
        # We should receive a msg id
        c = yield getPage(baseurl, method = self.method, postdata = self.postdata)
        msgStatus = c[:7]
        msgId = c[9:45]
        
        # Wait 3 seconds for submit_sm_resp
        exitDeferred = defer.Deferred()
        reactor.callLater(3, exitDeferred.callback, None)
        yield exitDeferred

        yield self.stopSmppClientConnectors()
        
        # Run tests
        self.assertEqual(msgStatus, 'Success')        
        # A DLR must be sent to dlr_url
        self.assertEqual(self.AckServerResource.render_POST.call_count, 1)
        # Message ID must be transmitted in the DLR
        callArgs = self.AckServerResource.render_POST.call_args_list[0][0][0].args
        self.assertEqual(callArgs['id'][0], msgId)

    @defer.inlineCallbacks
    def test_delivery_with_inurl_dlr_level2(self):
        """Will:
        1. Set a SMS-MT route to connector A
        2. Send a SMS-MT to that route and set a DLR callback for level 2
        3. Wait for the level2 DLR (deliver_sm) and run tests
        """
        yield self.connect('127.0.0.1', self.pbPort)
        yield self.prepareRoutingsAndStartConnector()
        
        self.params['dlr-url'] = self.dlr_url
        self.params['dlr-level'] = 1
        baseurl = 'http://127.0.0.1:1401/send?%s' % urllib.urlencode(self.params)
        
        # Send a MT
        # We should receive a msg id
        c = yield getPage(baseurl, method = self.method, postdata = self.postdata)
        msgStatus = c[:7]
        msgId = c[9:45]
        
        # Wait 3 seconds for submit_sm_resp
        exitDeferred = defer.Deferred()
        reactor.callLater(3, exitDeferred.callback, None)
        yield exitDeferred

        # Trigger a deliver_sm containing a DLR
        yield self.SMSCPort.factory.lastClient.trigger_DLR()

        yield self.stopSmppClientConnectors()

        # Run tests
        self.assertEqual(msgStatus, 'Success')        
        # A DLR must be sent to dlr_url
        self.assertEqual(self.AckServerResource.render_POST.call_count, 1)
        # Message ID must be transmitted in the DLR
        callArgs = self.AckServerResource.render_POST.call_args_list[0][0][0].args
        self.assertEqual(callArgs['id'][0], msgId)

    @defer.inlineCallbacks
    def test_delivery_with_inurl_dlr_level3(self):
        """Will:
        1. Set a SMS-MT route to connector A
        2. Send a SMS-MT to that route and set a DLR callback for level 3
        3. Wait for the level1 & level2 DLRs and run tests
        """
        yield self.connect('127.0.0.1', self.pbPort)
        yield self.prepareRoutingsAndStartConnector()
        
        self.params['dlr-url'] = self.dlr_url
        self.params['dlr-level'] = 3
        baseurl = 'http://127.0.0.1:1401/send?%s' % urllib.urlencode(self.params)
        
        # Send a MT
        # We should receive a msg id
        c = yield getPage(baseurl, method = self.method, postdata = self.postdata)
        msgStatus = c[:7]
        msgId = c[9:45]
        
        # Wait 2 seconds for submit_sm_resp
        exitDeferred = defer.Deferred()
        reactor.callLater(3, exitDeferred.callback, None)
        yield exitDeferred

        # Trigger a deliver_sm
        yield self.SMSCPort.factory.lastClient.trigger_DLR()

        yield self.stopSmppClientConnectors()

        # Run tests
        self.assertEqual(msgStatus, 'Success')        
        # A DLR must be sent to dlr_url
        self.assertEqual(self.AckServerResource.render_POST.call_count, 2)
        # Message ID must be transmitted in the DLR
        callArgs_level1 = self.AckServerResource.render_POST.call_args_list[0][0][0].args
        callArgs_level2 = self.AckServerResource.render_POST.call_args_list[1][0][0].args
        self.assertEqual(callArgs_level1['id'][0], msgId)
        self.assertEqual(callArgs_level2['id'][0], msgId)

    @defer.inlineCallbacks
    def test_delivery_with_inurl_dlr_level1_GET(self):
        """Will:
        1. Set a SMS-MT route to connector A
        2. Send a SMS-MT to that route and set a DLR callback for level 1 using GET method
        3. Wait for the level1 DLR (submit_sm_resp) and run tests
        """
        yield self.connect('127.0.0.1', self.pbPort)
        yield self.prepareRoutingsAndStartConnector()
        
        self.params['dlr-url'] = self.dlr_url
        self.params['dlr-level'] = 1
        self.params['dlr-method'] = 'GET'
        baseurl = 'http://127.0.0.1:1401/send?%s' % urllib.urlencode(self.params)
        
        # Send a MT
        # We should receive a msg id
        c = yield getPage(baseurl, method = self.method, postdata = self.postdata)
        msgStatus = c[:7]
        msgId = c[9:45]
        
        yield self.stopSmppClientConnectors()
        
        # Run tests
        self.assertEqual(msgStatus, 'Success')        
        # A DLR must be sent to dlr_url
        self.assertEqual(self.AckServerResource.render_GET.call_count, 1)
        # Message ID must be transmitted in the DLR
        callArgs = self.AckServerResource.render_GET.call_args_list[0][0][0].args
        self.assertEqual(callArgs['id'][0], msgId)

    @defer.inlineCallbacks
    def test_delivery_with_inurl_dlr_level2_GET(self):
        """Will:
        1. Set a SMS-MT route to connector A
        2. Send a SMS-MT to that route and set a DLR callback for level 2 using GET method
        3. Wait for the level2 DLR (deliver_sm) and run tests
        """
        yield self.connect('127.0.0.1', self.pbPort)
        yield self.prepareRoutingsAndStartConnector()
        
        self.params['dlr-url'] = self.dlr_url
        self.params['dlr-level'] = 2
        self.params['dlr-method'] = 'GET'
        baseurl = 'http://127.0.0.1:1401/send?%s' % urllib.urlencode(self.params)
        
        # Send a MT
        # We should receive a msg id
        c = yield getPage(baseurl, method = self.method, postdata = self.postdata)
        msgStatus = c[:7]
        msgId = c[9:45]
        
        # Wait 3 seconds for submit_sm_resp
        exitDeferred = defer.Deferred()
        reactor.callLater(3, exitDeferred.callback, None)
        yield exitDeferred

        # Trigger a deliver_sm containing a DLR
        yield self.SMSCPort.factory.lastClient.trigger_DLR()

        yield self.stopSmppClientConnectors()

        # Run tests
        self.assertEqual(msgStatus, 'Success')        
        # A DLR must be sent to dlr_url
        self.assertEqual(self.AckServerResource.render_GET.call_count, 1)
        # Message ID must be transmitted in the DLR
        callArgs = self.AckServerResource.render_GET.call_args_list[0][0][0].args
        self.assertEqual(callArgs['id'][0], msgId)

    @defer.inlineCallbacks
    def test_delivery_with_inurl_dlr_level3_GET(self):
        """Will:
        1. Set a SMS-MT route to connector A
        2. Send a SMS-MT to that route and set a DLR callback for level 3 using GET method
        3. Wait for the level1 & level2 DLRs and run tests
        """
        yield self.connect('127.0.0.1', self.pbPort)
        yield self.prepareRoutingsAndStartConnector()
        
        self.params['dlr-url'] = self.dlr_url
        self.params['dlr-level'] = 3
        self.params['dlr-method'] = 'GET'
        baseurl = 'http://127.0.0.1:1401/send?%s' % urllib.urlencode(self.params)
        
        # Send a MT
        # We should receive a msg id
        c = yield getPage(baseurl, method = self.method, postdata = self.postdata)
        msgStatus = c[:7]
        msgId = c[9:45]
        
        # Wait 2 seconds for submit_sm_resp
        exitDeferred = defer.Deferred()
        reactor.callLater(3, exitDeferred.callback, None)
        yield exitDeferred

        # Trigger a deliver_sm
        yield self.SMSCPort.factory.lastClient.trigger_DLR()

        yield self.stopSmppClientConnectors()

        # Run tests
        self.assertEqual(msgStatus, 'Success')        
        # A DLR must be sent to dlr_url
        self.assertEqual(self.AckServerResource.render_GET.call_count, 2)
        # Message ID must be transmitted in the DLR
        callArgs_level1 = self.AckServerResource.render_GET.call_args_list[0][0][0].args
        callArgs_level2 = self.AckServerResource.render_GET.call_args_list[1][0][0].args
        self.assertEqual(callArgs_level1['id'][0], msgId)
        self.assertEqual(callArgs_level2['id'][0], msgId)

class NoSubmitSmWhenReceiverIsBoundSMSC(SMPPClientManagerPBTestCase):
    protocol = NoSubmitSmWhenReceiverIsBoundSMSC
    
    @defer.inlineCallbacks
    def setUp(self):
        yield SMPPClientManagerPBTestCase.setUp(self)
        
        self.smsc_f = LastClientFactory()
        self.smsc_f.protocol = self.protocol      
        self.SMSCPort = reactor.listenTCP(0, self.smsc_f)
                
    @defer.inlineCallbacks
    def tearDown(self):
        yield SMPPClientManagerPBTestCase.tearDown(self)
        
        self.SMSCPort.stopListening()

class BOUND_RX_SubmitSmTestCases(RouterPBProxy, NoSubmitSmWhenReceiverIsBoundSMSC, SubmitSmTestCaseTools):
    @defer.inlineCallbacks
    def setUp(self):
        yield NoSubmitSmWhenReceiverIsBoundSMSC.setUp(self)
        
        # Start http servers
        self.AckServerResource = AckServer()
        self.AckServer = reactor.listenTCP(0, server.Site(self.AckServerResource))

    @defer.inlineCallbacks
    def tearDown(self):
        yield NoSubmitSmWhenReceiverIsBoundSMSC.tearDown(self)
        
        self.AckServer.stopListening()

    @defer.inlineCallbacks
    def test_test_delivery_using_incorrectly_bound_connector(self):
        yield self.connect('127.0.0.1', self.pbPort)
        yield self.prepareRoutingsAndStartConnector(bindOperation = 'receiver')
        
        self.params['dlr-url'] = self.dlr_url
        self.params['dlr-level'] = 1
        baseurl = 'http://127.0.0.1:1401/send?%s' % urllib.urlencode(self.params)
        # Send a MT
        c = yield getPage(baseurl, method = self.method, postdata = self.postdata)
        msgStatus = c[:7]
        msgId = c[9:45]
        
        yield self.stopSmppClientConnectors()

        # Run tests
        self.assertEqual(msgStatus, 'Success')        
        # A DLR must be sent to dlr_url
        self.assertEqual(self.AckServerResource.render_POST.call_count, 1)
        # Message ID must be transmitted in the DLR
        callArgs = self.AckServerResource.render_POST.call_args_list[0][0][0].args
        self.assertEqual(callArgs['id'][0], msgId)
        self.assertEqual(callArgs['message_status'][0], 'ESME_RINVBNDSTS')

class DeliverSmSMSCTestCase(SMPPClientManagerPBTestCase):
    protocol = DeliverSmSMSC
    
    @defer.inlineCallbacks
    def setUp(self):
        yield SMPPClientManagerPBTestCase.setUp(self)
        
        self.smsc_f = LastClientFactory()
        self.smsc_f.protocol = self.protocol      
        self.SMSCPort = reactor.listenTCP(0, self.smsc_f)
                
    def tearDown(self):        
        self.SMSCPort.stopListening()
        return SMPPClientManagerPBTestCase.tearDown(self)
        
class DeliverSmThrowingTestCases(RouterPBProxy, DeliverSmSMSCTestCase):
    
    @defer.inlineCallbacks
    def setUp(self):
        yield DeliverSmSMSCTestCase.setUp(self)
        
        # Start http servers
        self.AckServerResource = AckServer()
        self.AckServer = reactor.listenTCP(0, server.Site(self.AckServerResource))
        
        # Initiating config objects without any filename
        # will lead to setting defaults and that's what we
        # need to run the tests
        deliverSmHttpThrowerConfigInstance = deliverSmHttpThrowerConfig()
        # Lower the timeout config to pass the timeout tests quickly
        deliverSmHttpThrowerConfigInstance.timeout = 2
        deliverSmHttpThrowerConfigInstance.retryDelay = 1
        deliverSmHttpThrowerConfigInstance.maxRetries = 2
        
        # Launch the deliverSmHttpThrower
        self.deliverSmHttpThrower = deliverSmHttpThrower()
        self.deliverSmHttpThrower.setConfig(deliverSmHttpThrowerConfigInstance)
        
        # Add the broker to the deliverSmHttpThrower
        yield self.deliverSmHttpThrower.addAmqpBroker(self.amqpBroker)

    @defer.inlineCallbacks
    def tearDown(self):
        self.AckServer.stopListening()
        yield self.deliverSmHttpThrower.stopService()
        yield DeliverSmSMSCTestCase.tearDown(self)
        
    @defer.inlineCallbacks
    def prepareRoutingsAndStartConnector(self, connector):
        self.AckServerResource.render_GET = mock.Mock(wraps=self.AckServerResource.render_GET)

        # Prepare for routing
        connector.port = self.SMSCPort.getHost().port
        c2_destination = HttpConnector(id_generator(), 'http://127.0.0.1:%s/send' % self.AckServer.getHost().port)
        # Set the route
        yield self.moroute_add(DefaultRoute(c2_destination), 0)
        
        # Now we'll create the connector 1
        yield self.SMPPClientManagerPBProxy.connect('127.0.0.1', self.CManagerPort)
        c1Config = SMPPClientConfig(id=connector.cid, port=connector.port)        
        yield self.SMPPClientManagerPBProxy.add(c1Config)
        
        # Start the connector
        yield self.SMPPClientManagerPBProxy.start(connector.cid)
        # Wait for 'BOUND_TRX' state
        while True:
            ssRet = yield self.SMPPClientManagerPBProxy.session_state(connector.cid)
            if ssRet == 'BOUND_TRX':
                break;
            else:
                time.sleep(0.2)        
        
    @defer.inlineCallbacks
    def stopConnector(self, connector):
        # Disconnect the connector
        yield self.SMPPClientManagerPBProxy.stop(connector.cid)
        # Wait for 'BOUND_TRX' state
        while True:
            ssRet = yield self.SMPPClientManagerPBProxy.session_state(connector.cid)
            if ssRet == 'NONE':
                break;
            else:
                time.sleep(0.2)
    
    @defer.inlineCallbacks
    def triggerDeliverSmFromSMSC(self, pdus):
        for pdu in pdus:
            yield self.SMSCPort.factory.lastClient.trigger_deliver_sm(pdu)

        # Wait 3 seconds
        exitDeferred = defer.Deferred()
        reactor.callLater(3, exitDeferred.callback, None)
        yield exitDeferred

    @defer.inlineCallbacks
    def test_delivery_HttpConnector(self):
        yield self.connect('127.0.0.1', self.pbPort)
        # Connect to SMSC
        source_connector = Connector(id_generator())
        yield self.prepareRoutingsAndStartConnector(source_connector)
        
        # Send a deliver_sm from the SMSC
        pdu = DeliverSM(
            source_addr='1234',
            destination_addr='4567',
            short_message='any content',
        )
        yield self.triggerDeliverSmFromSMSC([pdu])

        # Run tests
        # Destination connector must receive the message one time (no retries)
        self.assertEqual(self.AckServerResource.render_GET.call_count, 1)
        # Assert received args
        receivedHttpReq = self.AckServerResource.last_request.args
        self.assertEqual(len(receivedHttpReq), 7)
        self.assertEqual(receivedHttpReq['from'], [pdu.params['source_addr']])
        self.assertEqual(receivedHttpReq['to'], [pdu.params['destination_addr']])
        self.assertEqual(receivedHttpReq['content'], [pdu.params['short_message']])
        self.assertEqual(receivedHttpReq['origin-connector'], [source_connector.cid])

        # Disconnector from SMSC
        yield self.stopConnector(source_connector)

    @defer.inlineCallbacks
    def test_long_content_delivery_SAR_HttpConnector(self):
        yield self.connect('127.0.0.1', self.pbPort)
        # Connect to SMSC
        #source_connector = Connector(id_generator())
        source_connector = Connector(id_generator())
        yield self.prepareRoutingsAndStartConnector(source_connector)
        
        # Send a deliver_sm from the SMSC
        basePdu = DeliverSM(
            source_addr = '1234',
            destination_addr = '4567',
            short_message = '',
            sar_total_segments = 3,
            sar_msg_ref_num = int(id_generator(size = 2, chars=string.digits)),
        )
        pdu_part1 = copy.deepcopy(basePdu)
        pdu_part2 = copy.deepcopy(basePdu)
        pdu_part3 = copy.deepcopy(basePdu)
        pdu_part1.params['short_message'] = '__1st_part_with_153_char________________________________________________________________________________________________________________________________.'
        pdu_part1.params['sar_segment_seqnum'] = 1
        pdu_part2.params['short_message'] = '__2nd_part_with_153_char________________________________________________________________________________________________________________________________.'
        pdu_part2.params['sar_segment_seqnum'] = 2
        pdu_part3.params['short_message'] = '__3rd_part_end.'
        pdu_part3.params['sar_segment_seqnum'] = 3
        yield self.triggerDeliverSmFromSMSC([pdu_part1, pdu_part2, pdu_part3])

        # Run tests
        # Destination connector must receive the message one time (no retries)
        self.assertEqual(self.AckServerResource.render_GET.call_count, 1)
        # Assert received args
        receivedHttpReq = self.AckServerResource.last_request.args
        self.assertEqual(len(receivedHttpReq), 7)
        self.assertEqual(receivedHttpReq['from'], [basePdu.params['source_addr']])
        self.assertEqual(receivedHttpReq['to'], [basePdu.params['destination_addr']])
        self.assertEqual(receivedHttpReq['content'], [pdu_part1.params['short_message'] + pdu_part2.params['short_message'] + pdu_part3.params['short_message']])
        self.assertEqual(receivedHttpReq['origin-connector'], [source_connector.cid])

        # Disconnector from SMSC
        yield self.stopConnector(source_connector)

    @defer.inlineCallbacks
    def test_long_content_delivery_UDH_HttpConnector(self):
        yield self.connect('127.0.0.1', self.pbPort)
        # Connect to SMSC
        #source_connector = Connector(id_generator())
        source_connector = Connector(id_generator())
        yield self.prepareRoutingsAndStartConnector(source_connector)
        
        # Build a UDH
        baseUdh = []
        baseUdh.append(struct.pack('!B', 5)) # Length of User Data Header
        baseUdh.append(struct.pack('!B', 0)) # Information Element Identifier, equal to 00 (Concatenated short messages, 8-bit reference number)
        baseUdh.append(struct.pack('!B', 3)) # Length of the header, excluding the first two fields; equal to 03
        baseUdh.append(struct.pack('!B', int(id_generator(size = 2, chars=string.digits)))) # msg_ref_num
        baseUdh.append(struct.pack('!B', 3)) # total_segments

        # Send a deliver_sm from the SMSC
        basePdu = DeliverSM(
            source_addr = '1234',
            destination_addr = '4567',
            short_message = '',
            esm_class = EsmClass(EsmClassMode.DEFAULT, EsmClassType.DEFAULT, [EsmClassGsmFeatures.UDHI_INDICATOR_SET]),
        )
        pdu_part1 = copy.deepcopy(basePdu)
        udh_part1 = copy.deepcopy(baseUdh)
        pdu_part2 = copy.deepcopy(basePdu)
        udh_part2 = copy.deepcopy(baseUdh)
        pdu_part3 = copy.deepcopy(basePdu)
        udh_part3 = copy.deepcopy(baseUdh)
        udh_part1.append(struct.pack('!B', 1)) # segment_seqnum
        pdu_part1.params['more_messages_to_send'] = MoreMessagesToSend.MORE_MESSAGES
        pdu_part1.params['short_message'] = ''.join(udh_part1)+'__1st_part_with_153_char________________________________________________________________________________________________________________________________.'
        udh_part2.append(struct.pack('!B', 2)) # segment_seqnum
        pdu_part2.params['more_messages_to_send'] = MoreMessagesToSend.MORE_MESSAGES
        pdu_part2.params['short_message'] = ''.join(udh_part2)+'__2nd_part_with_153_char________________________________________________________________________________________________________________________________.'
        udh_part3.append(struct.pack('!B', 3)) # segment_seqnum
        pdu_part3.params['more_messages_to_send'] = MoreMessagesToSend.NO_MORE_MESSAGES
        pdu_part3.params['short_message'] = ''.join(udh_part3)+'__3rd_part_end.'
        yield self.triggerDeliverSmFromSMSC([pdu_part1, pdu_part2, pdu_part3])

        # Run tests
        # Destination connector must receive the message one time (no retries)
        self.assertEqual(self.AckServerResource.render_GET.call_count, 1)
        # Assert received args
        receivedHttpReq = self.AckServerResource.last_request.args
        self.assertEqual(len(receivedHttpReq), 7)
        self.assertEqual(receivedHttpReq['from'], [basePdu.params['source_addr']])
        self.assertEqual(receivedHttpReq['to'], [basePdu.params['destination_addr']])
        self.assertEqual(receivedHttpReq['content'], [pdu_part1.params['short_message'][6:] + pdu_part2.params['short_message'][6:] + pdu_part3.params['short_message'][6:]])
        self.assertEqual(receivedHttpReq['origin-connector'], [source_connector.cid])

        # Disconnector from SMSC
        yield self.stopConnector(source_connector)

    @defer.inlineCallbacks
    def test_unordered_long_content_delivery_HttpConnector(self):
        yield self.connect('127.0.0.1', self.pbPort)
        # Connect to SMSC
        #source_connector = Connector(id_generator())
        source_connector = Connector(id_generator())
        yield self.prepareRoutingsAndStartConnector(source_connector)
        
        # Send a deliver_sm from the SMSC
        basePdu = DeliverSM(
            source_addr = '1234',
            destination_addr = '4567',
            short_message = '',
            sar_total_segments = 3,
            sar_msg_ref_num = int(id_generator(size = 2, chars=string.digits)),
        )
        pdu_part1 = copy.deepcopy(basePdu)
        pdu_part2 = copy.deepcopy(basePdu)
        pdu_part3 = copy.deepcopy(basePdu)
        pdu_part1.params['short_message'] = '__1st_part_with_153_char________________________________________________________________________________________________________________________________.'
        pdu_part1.params['sar_segment_seqnum'] = 1
        pdu_part2.params['short_message'] = '__2nd_part_with_153_char________________________________________________________________________________________________________________________________.'
        pdu_part2.params['sar_segment_seqnum'] = 2
        pdu_part3.params['short_message'] = '__3rd_part_end.'
        pdu_part3.params['sar_segment_seqnum'] = 3
        yield self.triggerDeliverSmFromSMSC([pdu_part1, pdu_part3, pdu_part2])

        # Run tests
        # Destination connector must receive the message one time (no retries)
        self.assertEqual(self.AckServerResource.render_GET.call_count, 1)
        # Assert received args
        receivedHttpReq = self.AckServerResource.last_request.args
        self.assertEqual(len(receivedHttpReq), 7)
        self.assertEqual(receivedHttpReq['from'], [basePdu.params['source_addr']])
        self.assertEqual(receivedHttpReq['to'], [basePdu.params['destination_addr']])
        self.assertEqual(receivedHttpReq['content'], [pdu_part1.params['short_message'] + pdu_part2.params['short_message'] + pdu_part3.params['short_message']])
        self.assertEqual(receivedHttpReq['origin-connector'], [source_connector.cid])

        # Disconnector from SMSC
        yield self.stopConnector(source_connector)

    def test_delivery_SmppClientConnector(self):
        pass
    test_delivery_SmppClientConnector.skip = 'TODO: When SMPP Server will be implemented ?'