# -*- coding: utf-8 -*- 
# Copyright 2012 Fourat Zouari <fourat@gmail.com>
# See LICENSE for details.

import mock
import pickle
import time
import urllib
import string
import random
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
from jasmin.routing.configs import deliverSmThrowerConfig, DLRThrowerConfig
from jasmin.routing.throwers import deliverSmThrower, DLRThrower
from jasmin.protocols.http.server import HTTPApi
from jasmin.protocols.http.configs import HTTPApiConfig
from jasmin.protocols.smpp.configs import SMPPClientConfig
from jasmin.managers.proxies import SMPPClientManagerPBProxy
from jasmin.managers.clients import SMPPClientManagerPB
from jasmin.managers.configs import SMPPClientPBConfig
from jasmin.routing.Routes import DefaultRoute, StaticMTRoute
from jasmin.routing.Filters import GroupFilter
from jasmin.routing.jasminApi import Connector, HttpConnector, Group, User
from jasmin.queues.factory import AmqpFactory
from jasmin.queues.configs import AmqpConfig

def id_generator(size=6, chars=string.ascii_uppercase + string.digits):
    return ''.join(random.choice(chars) for x in range(size))

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
        
        yield self.mtroute_add(StaticMTRoute([GroupFilter(Group(1))], Connector(id_generator())), 2)
        yield self.mtroute_add(DefaultRoute(Connector(id_generator())), 0)
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
        
        yield self.moroute_add(DefaultRoute(HttpConnector(id_generator(), 'http://127.0.0.1')), 0)
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
        
class SimpleNonConnectedSubmitSmDeliveryTestCases(RouterPBProxy, SMPPClientManagerPBTestCase):
    @defer.inlineCallbacks
    def test_delivery(self):
        yield self.connect('127.0.0.1', self.pbPort)
        
        c1 = Connector(id_generator())
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
        self.c1 = Connector(id_generator())
        self.u1 = User(1, Group(1), 'username', 'password')
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
    def stopSmppConnectors(self):
        # Disconnect the connector
        yield self.SMPPClientManagerPBProxy.stop(self.c1.cid)
        # Wait for 'BOUND_TRX' state
        while True:
            ssRet = yield self.SMPPClientManagerPBProxy.session_state(self.c1.cid)
            if ssRet == 'NONE':
                break;
            else:
                time.sleep(0.2)

    
class AdvancedSubmitSmTestCases(RouterPBProxy, HappySMSCTestCase, SubmitSmTestCaseTools):
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
        
        yield self.stopSmppConnectors()
        
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

        yield self.stopSmppConnectors()

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

        yield self.stopSmppConnectors()

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
        
        yield self.stopSmppConnectors()
        
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

        yield self.stopSmppConnectors()

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

        yield self.stopSmppConnectors()

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
        
        yield self.stopSmppConnectors()

        # Run tests
        self.assertEqual(msgStatus, 'Success')        

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
        
        yield self.stopSmppConnectors()

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
        
class DeliverSmDeliveryTestCases(RouterPBProxy, DeliverSmSMSCTestCase):
    
    @defer.inlineCallbacks
    def setUp(self):
        yield DeliverSmSMSCTestCase.setUp(self)
        
        # Start http servers
        self.AckServerResource = AckServer()
        self.AckServer = reactor.listenTCP(0, server.Site(self.AckServerResource))
        
        # Initiating config objects without any filename
        # will lead to setting defaults and that's what we
        # need to run the tests
        deliverSmThrowerConfigInstance = deliverSmThrowerConfig()
        # Lower the timeout config to pass the timeout tests quickly
        deliverSmThrowerConfigInstance.timeout = 2
        deliverSmThrowerConfigInstance.retryDelay = 1
        deliverSmThrowerConfigInstance.maxRetries = 2
        
        # Launch the deliverSmThrower
        self.deliverSmThrower = deliverSmThrower()
        self.deliverSmThrower.setConfig(deliverSmThrowerConfigInstance)
        
        # Add the broker to the deliverSmThrower
        yield self.deliverSmThrower.addAmqpBroker(self.amqpBroker)

    @defer.inlineCallbacks
    def tearDown(self):
        self.AckServer.stopListening()
        yield self.deliverSmThrower.stopService()
        yield DeliverSmSMSCTestCase.tearDown(self)

    @defer.inlineCallbacks
    def test_delivery_HttpConnector(self):
        yield self.connect('127.0.0.1', self.pbPort)
        
        self.AckServerResource.render_GET = mock.Mock(wraps=self.AckServerResource.render_GET)

        # Prepare for routing
        c1_source = Connector(id_generator())
        c1_source.port = self.SMSCPort.getHost().port
        c2_destination = HttpConnector(id_generator(), 'http://127.0.0.1:%s/send' % self.AckServer.getHost().port)
        # Set the route
        yield self.moroute_add(DefaultRoute(c2_destination), 0)
        
        # Now we'll create the connector 1
        yield self.SMPPClientManagerPBProxy.connect('127.0.0.1', self.CManagerPort)
        c1Config = SMPPClientConfig(id=c1_source.cid, port=c1_source.port)        
        yield self.SMPPClientManagerPBProxy.add(c1Config)
        
        # Start the connector
        yield self.SMPPClientManagerPBProxy.start(c1_source.cid)
        # Wait for 'BOUND_TRX' state
        while True:
            ssRet = yield self.SMPPClientManagerPBProxy.session_state(c1_source.cid)
            if ssRet == 'BOUND_TRX':
                break;
            else:
                time.sleep(0.2)
                
        # Send a deliver_sm from the SMSC
        pdu = DeliverSM(
            source_addr='1234',
            destination_addr='4567',
            short_message='any content',
        )
        yield self.SMSCPort.factory.lastClient.trigger_deliver_sm(pdu)

        # Wait 3 seconds
        exitDeferred = defer.Deferred()
        reactor.callLater(3, exitDeferred.callback, None)
        yield exitDeferred

        # Destination connector must receive the message one time (no retries)
        self.assertEqual(self.AckServerResource.render_GET.call_count, 1)

        # Disconnect the connector
        yield self.SMPPClientManagerPBProxy.stop(c1_source.cid)
        # Wait for 'BOUND_TRX' state
        while True:
            ssRet = yield self.SMPPClientManagerPBProxy.session_state(c1_source.cid)
            if ssRet == 'NONE':
                break;
            else:
                time.sleep(0.2)

    @defer.inlineCallbacks
    def test_delivery_SmppConnector(self):
        pass
    test_delivery_SmppConnector.skip = 'TODO'