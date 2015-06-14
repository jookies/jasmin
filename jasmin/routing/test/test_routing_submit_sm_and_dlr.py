import urllib
import mock
import copy
import random
from datetime import datetime
from twisted.web import server
from twisted.internet import reactor, defer
from twisted.web.client import getPage
from jasmin.vendor.smpp.pdu import pdu_types
from jasmin.routing.test.http_server import AckServer
from jasmin.routing.test.test_router import (HappySMSCTestCase, SubmitSmTestCaseTools,
											composeMessage)
from jasmin.routing.test.test_router_smpps import SMPPClientTestCases
from jasmin.routing.proxies import RouterPBProxy
from jasmin.protocols.smpp.test.smsc_simulator import *
from jasmin.vendor.smpp.pdu.pdu_types import MessageState

class HttpParameterTestCases(RouterPBProxy, HappySMSCTestCase, SubmitSmTestCaseTools):

    @defer.inlineCallbacks
    def test_validity_period(self):
        yield self.connect('127.0.0.1', self.pbPort)
        yield self.prepareRoutingsAndStartConnector()
        
        self.params['validity-period'] = 1440 # 1 Day = 24 x 60 minutes = 1440 minutes
        baseurl = 'http://127.0.0.1:1401/send?%s' % urllib.urlencode(self.params)
        
        # Send a MT
        # We should receive a msg id
        c = yield getPage(baseurl, method = self.method, postdata = self.postdata)
        msgStatus = c[:7]
        msgId = c[9:45]
        
        yield self.stopSmppClientConnectors()

        # Run tests
        self.assertEqual(msgStatus, 'Success')
        self.assertEqual(1, len(self.SMSCPort.factory.lastClient.submitRecords))
        self.assertNotEqual(None, self.SMSCPort.factory.lastClient.submitRecords[0].params['validity_period'])
        timediff = self.SMSCPort.factory.lastClient.submitRecords[0].params['validity_period'] - datetime.now()
        self.assertGreaterEqual(timediff.seconds / 60, (self.params['validity-period'] - 1)) # Tolerate one minute of test latency
        self.assertLess(timediff.seconds / 60, (self.params['validity-period'] + 1))

    @defer.inlineCallbacks
    def test_dlr_level_default(self):
        yield self.connect('127.0.0.1', self.pbPort)
        yield self.prepareRoutingsAndStartConnector()
        
        baseurl = 'http://127.0.0.1:1401/send?%s' % urllib.urlencode(self.params)
        
        # Send a MT
        # We should receive a msg id
        c = yield getPage(baseurl, method = self.method, postdata = self.postdata)
        msgStatus = c[:7]
        msgId = c[9:45]
        
        yield self.stopSmppClientConnectors()

        # Run tests
        self.assertEqual(msgStatus, 'Success')
        self.assertEqual(1, len(self.SMSCPort.factory.lastClient.submitRecords))
        self.assertEqual(str(self.SMSCPort.factory.lastClient.submitRecords[0].params['registered_delivery'].receipt), 
            'NO_SMSC_DELIVERY_RECEIPT_REQUESTED')

    @defer.inlineCallbacks
    def test_dlr_level_1(self):
        yield self.connect('127.0.0.1', self.pbPort)
        yield self.prepareRoutingsAndStartConnector()
        
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
        self.assertEqual(1, len(self.SMSCPort.factory.lastClient.submitRecords))
        self.assertEqual(str(self.SMSCPort.factory.lastClient.submitRecords[0].params['registered_delivery'].receipt), 
            'SMSC_DELIVERY_RECEIPT_REQUESTED')

    @defer.inlineCallbacks
    def test_dlr_level_2(self):
        yield self.connect('127.0.0.1', self.pbPort)
        yield self.prepareRoutingsAndStartConnector()
        
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
        self.assertEqual(1, len(self.SMSCPort.factory.lastClient.submitRecords))
        self.assertEqual(str(self.SMSCPort.factory.lastClient.submitRecords[0].params['registered_delivery'].receipt), 
            'SMSC_DELIVERY_RECEIPT_REQUESTED')

    @defer.inlineCallbacks
    def test_dlr_level_3(self):
        yield self.connect('127.0.0.1', self.pbPort)
        yield self.prepareRoutingsAndStartConnector()
        
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
        self.assertEqual(1, len(self.SMSCPort.factory.lastClient.submitRecords))
        self.assertEqual(str(self.SMSCPort.factory.lastClient.submitRecords[0].params['registered_delivery'].receipt), 
            'SMSC_DELIVERY_RECEIPT_REQUESTED')

class HttpDlrCallbackingTestCases(RouterPBProxy, HappySMSCTestCase, SubmitSmTestCaseTools):
    @defer.inlineCallbacks
    def setUp(self):
        yield HappySMSCTestCase.setUp(self)
        
        # Start http servers
        self.AckServerResource = AckServer()
        self.AckServer = reactor.listenTCP(0, server.Site(self.AckServerResource))

    @defer.inlineCallbacks
    def tearDown(self):
        yield HappySMSCTestCase.tearDown(self)
        
        yield self.AckServer.stopListening()

    @defer.inlineCallbacks
    def test_receipt_with_inurl_dlr_level1(self):
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
        
        # Wait 2 seconds for submit_sm_resp
        exitDeferred = defer.Deferred()
        reactor.callLater(2, exitDeferred.callback, None)
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
    def test_receipt_with_inurl_dlr_level2(self):
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
        
        # Wait 2 seconds for submit_sm_resp
        exitDeferred = defer.Deferred()
        reactor.callLater(2, exitDeferred.callback, None)
        yield exitDeferred

        # Trigger a deliver_sm containing a DLR
        yield self.SMSCPort.factory.lastClient.trigger_DLR()

        # Wait 1 seconds for deliver_sm
        exitDeferred = defer.Deferred()
        reactor.callLater(1, exitDeferred.callback, None)
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
    def test_receipt_with_inurl_dlr_level3(self):
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
        reactor.callLater(2, exitDeferred.callback, None)
        yield exitDeferred

        # Trigger a deliver_sm
        yield self.SMSCPort.factory.lastClient.trigger_DLR()

        # Wait 1 seconds for deliver_sm
        exitDeferred = defer.Deferred()
        reactor.callLater(1, exitDeferred.callback, None)
        yield exitDeferred

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
    def test_receipt_with_inurl_dlr_level1_GET(self):
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
        
        # Wait 1 seconds for submit_sm_resp
        exitDeferred = defer.Deferred()
        reactor.callLater(1, exitDeferred.callback, None)
        yield exitDeferred

        yield self.stopSmppClientConnectors()
        
        # Run tests
        self.assertEqual(msgStatus, 'Success')        
        # A DLR must be sent to dlr_url
        self.assertEqual(self.AckServerResource.render_GET.call_count, 1)
        # Message ID must be transmitted in the DLR
        callArgs = self.AckServerResource.render_GET.call_args_list[0][0][0].args
        self.assertEqual(callArgs['id'][0], msgId)

    @defer.inlineCallbacks
    def test_correct_args_dlr_level1(self):
        """Related to #71
        Will check for correct args when throwing a level1 ack
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
        
        # Wait 1 seconds for submit_sm_resp
        exitDeferred = defer.Deferred()
        reactor.callLater(1, exitDeferred.callback, None)
        yield exitDeferred

        yield self.stopSmppClientConnectors()
        
        # Run tests
        self.assertEqual(msgStatus, 'Success')
        # A DLR must be sent to dlr_url
        self.assertEqual(self.AckServerResource.render_POST.call_count, 1)
        # Args assertions
        callArgs = self.AckServerResource.render_POST.call_args_list[0][0][0].args
        self.assertEqual(len(callArgs), 3)
        self.assertTrue('id' in callArgs)
        self.assertTrue('message_status' in callArgs)
        self.assertTrue('level' in callArgs)
        self.assertEqual(callArgs['level'][0], '1')
        for k, v in callArgs.iteritems():
            self.assertNotEqual(v[0], '')

    @defer.inlineCallbacks
    def test_correct_args_dlr_level2(self):
        """Related to #71
        Will check for correct args when throwing a level2 ack
        """
        yield self.connect('127.0.0.1', self.pbPort)
        yield self.prepareRoutingsAndStartConnector()
        
        self.params['dlr-url'] = self.dlr_url
        self.params['dlr-level'] = 2
        baseurl = 'http://127.0.0.1:1401/send?%s' % urllib.urlencode(self.params)
        
        # Send a MT
        # We should receive a msg id
        c = yield getPage(baseurl, method = self.method, postdata = self.postdata)
        msgStatus = c[:7]
        msgId = c[9:45]
        
        # Wait 1 seconds for submit_sm_resp
        exitDeferred = defer.Deferred()
        reactor.callLater(1, exitDeferred.callback, None)
        yield exitDeferred

        # Trigger a deliver_sm containing a DLR
        yield self.SMSCPort.factory.lastClient.trigger_DLR()

        # Wait 1 seconds for deliver_sm
        exitDeferred = defer.Deferred()
        reactor.callLater(1, exitDeferred.callback, None)
        yield exitDeferred

        yield self.stopSmppClientConnectors()
        
        # Run tests
        self.assertEqual(msgStatus, 'Success')
        # A DLR must be sent to dlr_url
        self.assertEqual(self.AckServerResource.render_POST.call_count, 1)
        # Args assertions
        callArgs = self.AckServerResource.render_POST.call_args_list[0][0][0].args
        self.assertEqual(len(callArgs), 10)
        self.assertTrue('id' in callArgs)
        self.assertTrue('message_status' in callArgs)
        self.assertTrue('level' in callArgs)
        self.assertTrue('donedate' in callArgs)
        self.assertTrue('sub' in callArgs)
        self.assertTrue('err' in callArgs)
        self.assertTrue('text' in callArgs)
        self.assertTrue('id_smsc' in callArgs)
        self.assertTrue('dlvrd' in callArgs)
        self.assertTrue('subdate' in callArgs)
        self.assertEqual(callArgs['level'][0], '2')
        for k, v in callArgs.iteritems():
            self.assertNotEqual(v[0], '')

    @defer.inlineCallbacks
    def test_correct_args_dlr_level3(self):
        """Related to #71
        Will check for correct args when throwing a level3 ack
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
        
        # Wait 1 seconds for submit_sm_resp
        exitDeferred = defer.Deferred()
        reactor.callLater(1, exitDeferred.callback, None)
        yield exitDeferred

        # Trigger a deliver_sm containing a DLR
        yield self.SMSCPort.factory.lastClient.trigger_DLR()

        # Wait 1 seconds for deliver_sm
        exitDeferred = defer.Deferred()
        reactor.callLater(1, exitDeferred.callback, None)
        yield exitDeferred

        yield self.stopSmppClientConnectors()
        
        # Run tests
        self.assertEqual(msgStatus, 'Success')
        # A DLR must be sent to dlr_url
        self.assertEqual(self.AckServerResource.render_POST.call_count, 2)
        # Args assertions for first call (level1)
        callArgs = self.AckServerResource.render_POST.call_args_list[0][0][0].args
        self.assertEqual(len(callArgs), 3)
        self.assertTrue('id' in callArgs)
        self.assertTrue('message_status' in callArgs)
        self.assertTrue('level' in callArgs)
        self.assertEqual(callArgs['level'][0], '1')
        for k, v in callArgs.iteritems():
            self.assertNotEqual(v[0], '')
        # Args assertions for second call (level2)
        callArgs = self.AckServerResource.render_POST.call_args_list[1][0][0].args
        self.assertEqual(len(callArgs), 10)
        self.assertTrue('id' in callArgs)
        self.assertTrue('message_status' in callArgs)
        self.assertTrue('level' in callArgs)
        self.assertTrue('donedate' in callArgs)
        self.assertTrue('sub' in callArgs)
        self.assertTrue('err' in callArgs)
        self.assertTrue('text' in callArgs)
        self.assertTrue('id_smsc' in callArgs)
        self.assertTrue('dlvrd' in callArgs)
        self.assertTrue('subdate' in callArgs)
        self.assertEqual(callArgs['level'][0], '2')
        for k, v in callArgs.iteritems():
            self.assertNotEqual(v[0], '')

    @defer.inlineCallbacks
    def test_receipt_with_inurl_dlr_level2_GET(self):
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
        
        # Wait 1 seconds for submit_sm_resp
        exitDeferred = defer.Deferred()
        reactor.callLater(1, exitDeferred.callback, None)
        yield exitDeferred

        # Trigger a deliver_sm containing a DLR
        yield self.SMSCPort.factory.lastClient.trigger_DLR()

        # Wait 1 seconds for deliver_sm
        exitDeferred = defer.Deferred()
        reactor.callLater(1, exitDeferred.callback, None)
        yield exitDeferred

        yield self.stopSmppClientConnectors()

        # Run tests
        self.assertEqual(msgStatus, 'Success')        
        # A DLR must be sent to dlr_url
        self.assertEqual(self.AckServerResource.render_GET.call_count, 1)
        # Message ID must be transmitted in the DLR
        callArgs = self.AckServerResource.render_GET.call_args_list[0][0][0].args
        self.assertEqual(callArgs['id'][0], msgId)

    @defer.inlineCallbacks
    def test_receipt_with_inurl_dlr_level3_GET(self):
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
        
        # Wait 1 seconds for submit_sm_resp
        exitDeferred = defer.Deferred()
        reactor.callLater(1, exitDeferred.callback, None)
        yield exitDeferred

        # Trigger a deliver_sm
        yield self.SMSCPort.factory.lastClient.trigger_DLR()

        # Wait 1 seconds for deliver_sm
        exitDeferred = defer.Deferred()
        reactor.callLater(1, exitDeferred.callback, None)
        yield exitDeferred

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
    def test_receipt_empty_content(self):
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

class LongSmHttpDlrCallbackingTestCases(RouterPBProxy, HappySMSCTestCase, SubmitSmTestCaseTools):
    @defer.inlineCallbacks
    def setUp(self):
        yield HappySMSCTestCase.setUp(self)
        
        # Start http servers
        self.AckServerResource = AckServer()
        self.AckServer = reactor.listenTCP(0, server.Site(self.AckServerResource))

    @defer.inlineCallbacks
    def tearDown(self):
        yield HappySMSCTestCase.tearDown(self)
        
        yield self.AckServer.stopListening()

    @defer.inlineCallbacks
    def test_receipt_with_inurl_dlr_level1(self):
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
        
        # Wait 1 seconds for submit_sm_resp
        exitDeferred = defer.Deferred()
        reactor.callLater(1, exitDeferred.callback, None)
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
    def test_receipt_with_inurl_dlr_level2(self):
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
        
        # Wait 1 seconds for submit_sm_resp
        exitDeferred = defer.Deferred()
        reactor.callLater(1, exitDeferred.callback, None)
        yield exitDeferred

        # Trigger a deliver_sm containing a DLR
        yield self.SMSCPort.factory.lastClient.trigger_DLR()

        # Wait 1 seconds for deliver_sm
        exitDeferred = defer.Deferred()
        reactor.callLater(1, exitDeferred.callback, None)
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
    def test_receipt_with_inurl_dlr_level3(self):
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
        
        # Wait 1 seconds for submit_sm_resp
        exitDeferred = defer.Deferred()
        reactor.callLater(1, exitDeferred.callback, None)
        yield exitDeferred

        # Trigger a deliver_sm
        yield self.SMSCPort.factory.lastClient.trigger_DLR()

        # Wait 1 seconds for deliver_sm
        exitDeferred = defer.Deferred()
        reactor.callLater(1, exitDeferred.callback, None)
        yield exitDeferred

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
    def test_receipt_with_inurl_dlr_level1_GET(self):
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
        
        # Wait 1 seconds for submit_sm_resp
        exitDeferred = defer.Deferred()
        reactor.callLater(1, exitDeferred.callback, None)
        yield exitDeferred

        yield self.stopSmppClientConnectors()
        
        # Run tests
        self.assertEqual(msgStatus, 'Success')        
        # A DLR must be sent to dlr_url
        self.assertEqual(self.AckServerResource.render_GET.call_count, 1)
        # Message ID must be transmitted in the DLR
        callArgs = self.AckServerResource.render_GET.call_args_list[0][0][0].args
        self.assertEqual(callArgs['id'][0], msgId)

    @defer.inlineCallbacks
    def test_receipt_with_inurl_dlr_level2_GET(self):
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
        
        # Wait 1 seconds for submit_sm_resp
        exitDeferred = defer.Deferred()
        reactor.callLater(1, exitDeferred.callback, None)
        yield exitDeferred

        # Trigger a deliver_sm containing a DLR
        yield self.SMSCPort.factory.lastClient.trigger_DLR()

        # Wait 1 seconds for deliver_sm
        exitDeferred = defer.Deferred()
        reactor.callLater(1, exitDeferred.callback, None)
        yield exitDeferred

        yield self.stopSmppClientConnectors()

        # Run tests
        self.assertEqual(msgStatus, 'Success')        
        # A DLR must be sent to dlr_url
        self.assertEqual(self.AckServerResource.render_GET.call_count, 1)
        # Message ID must be transmitted in the DLR
        callArgs = self.AckServerResource.render_GET.call_args_list[0][0][0].args
        self.assertEqual(callArgs['id'][0], msgId)

    @defer.inlineCallbacks
    def test_receipt_with_inurl_dlr_level3_GET(self):
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
        
        # Wait 1 seconds for submit_sm_resp
        exitDeferred = defer.Deferred()
        reactor.callLater(1, exitDeferred.callback, None)
        yield exitDeferred

        # Trigger a deliver_sm
        yield self.SMSCPort.factory.lastClient.trigger_DLR()

        # Wait 1 seconds for deliver_sm
        exitDeferred = defer.Deferred()
        reactor.callLater(1, exitDeferred.callback, None)
        yield exitDeferred

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

class SmppsDlrCallbacking(RouterPBProxy, SMPPClientTestCases, SubmitSmTestCaseTools):
    msg_stats_final = ['UNDELIV',
                       'REJECTD',
                       'DELIVRD',
                       'EXPIRED',
                       'DELETED',
                       ]
    msg_stats_non_final = ['ACCEPTD',
                           'UNKNOWN'
                           ]
    formatted_stats = {'UNDELIV': 'UNDELIVERABLE',
    				   'REJECTD': 'REJECTED',
    				   'DELIVRD': 'DELIVERED',
    				   'EXPIRED': 'EXPIRED',
    				   'DELETED': 'DELETED',
    				   'ACCEPTD': 'ACCEPTED',
    				   'UNKNOWN': 'UNKNOWN'}

class SmppsDlrCallbackingTestCases(SmppsDlrCallbacking):
    @defer.inlineCallbacks
    def test_receipt_as_deliver_sm(self):
        """Will:
        1. Set a SMS-MT route to connector A
        2. Send a SMS-MT to that route from a SMPPc and request DLR
        3. Wait for the DLR (deliver_sm) to be routed back to SMPPc through SMPPs as a deliver_sm
        """
        yield self.connect('127.0.0.1', self.pbPort)
        yield self.prepareRoutingsAndStartConnector()
        
        # Bind
        yield self.smppc_factory.connectAndBind()

        # Install mocks
        self.smpps_factory.lastProto.sendPDU = mock.Mock(wraps=self.smpps_factory.lastProto.sendPDU)

        # Send a SMS MT through smpps interface
        SubmitSmPDU = copy.deepcopy(self.SubmitSmPDU)
        SubmitSmPDU.params['registered_delivery'] = RegisteredDelivery(RegisteredDeliveryReceipt.SMSC_DELIVERY_RECEIPT_REQUESTED)
        yield self.smppc_factory.lastProto.sendDataRequest(SubmitSmPDU)
        
        # Wait 3 seconds for submit_sm_resp
        exitDeferred = defer.Deferred()
        reactor.callLater(3, exitDeferred.callback, None)
        yield exitDeferred

        # Run tests
        self.assertEqual(self.smpps_factory.lastProto.sendPDU.call_count, 1)
        # smpps response #1 was a submit_sm_resp with ESME_ROK
        response_pdu_1 = self.smpps_factory.lastProto.sendPDU.call_args_list[0][0][0]
        self.assertEqual(response_pdu_1.id, pdu_types.CommandId.submit_sm_resp)

        # Trigger receipts with non final states
        x = self.smpps_factory.lastProto.sendPDU.call_count
        for msg_stat in self.msg_stats_non_final:
            # Trigger a receipt
            stat = str(msg_stat)
            yield self.SMSCPort.factory.lastClient.trigger_DLR(stat = stat)
            x+= 1

            # Wait some time before testing
            exitDeferred = defer.Deferred()
            reactor.callLater(0.5, exitDeferred.callback, None)
            yield exitDeferred

            # Run tests
            # smpps response #x was a deliver_sm with stat = msg_stat
            self.assertEqual(self.smpps_factory.lastProto.sendPDU.call_count, x, 'No receipt received !')
            response_pdu_x = self.smpps_factory.lastProto.sendPDU.call_args_list[x - 1][0][0]
            self.assertEqual(response_pdu_x.id, pdu_types.CommandId.deliver_sm)
            self.assertEqual(response_pdu_x.seqNum, x - 1)
            self.assertEqual(response_pdu_x.status, pdu_types.CommandStatus.ESME_ROK)
            self.assertEqual(response_pdu_x.params['source_addr'], SubmitSmPDU.params['destination_addr'])
            self.assertEqual(response_pdu_x.params['destination_addr'], SubmitSmPDU.params['source_addr'])
            self.assertEqual(response_pdu_x.params['receipted_message_id'], response_pdu_1.params['message_id'])
            self.assertEqual(str(response_pdu_x.params['message_state']), self.formatted_stats[stat])

        # Trigger receipts with final states
        # pick up a random final state, there must be only one receipt (the first one) because
        # SMPPs map is deleted when message is receipted (delivered or not)
        x = self.smpps_factory.lastProto.sendPDU.call_count
        random.shuffle(self.msg_stats_final)
        final_state_triggered = False
        for msg_stat in self.msg_stats_final:
            # Trigger a receipt
            stat = str(msg_stat)
            yield self.SMSCPort.factory.lastClient.trigger_DLR(stat = stat)
            x+= 1

            # Wait some time before testing
            exitDeferred = defer.Deferred()
            reactor.callLater(0.5, exitDeferred.callback, None)
            yield exitDeferred

            # Run tests
            if not final_state_triggered:
                # smpps response #x was a deliver_sm with stat = msg_stat
                self.assertEqual(self.smpps_factory.lastProto.sendPDU.call_count, x, 'No receipt received !')
                response_pdu_x = self.smpps_factory.lastProto.sendPDU.call_args_list[x - 1][0][0]
                self.assertEqual(response_pdu_x.id, pdu_types.CommandId.deliver_sm)
                self.assertEqual(response_pdu_x.seqNum, x - 1)
                self.assertEqual(response_pdu_x.status, pdu_types.CommandStatus.ESME_ROK)
                self.assertEqual(response_pdu_x.params['source_addr'], SubmitSmPDU.params['destination_addr'])
                self.assertEqual(response_pdu_x.params['destination_addr'], SubmitSmPDU.params['source_addr'])
                self.assertEqual(response_pdu_x.params['receipted_message_id'], response_pdu_1.params['message_id'])
                self.assertEqual(str(response_pdu_x.params['message_state']), self.formatted_stats[stat])
                final_state_triggered = True
                x_value_when_fstate_triggered = x
            else:
                # SMPPs map must be deleted when a final state were triggered
                # We get no more deliver_sm receipts for any further triggered DLR
                self.assertEqual(self.smpps_factory.lastProto.sendPDU.call_count, x_value_when_fstate_triggered)

        # Unbind & Disconnect
        yield self.smppc_factory.smpp.unbindAndDisconnect()
        yield self.stopSmppClientConnectors()

        # Run tests
        # smpps last response was a unbind_resp
        last_pdu = self.smpps_factory.lastProto.sendPDU.call_args_list[x_value_when_fstate_triggered][0][0]
        self.assertEqual(last_pdu.id, pdu_types.CommandId.unbind_resp)

    @defer.inlineCallbacks
    def test_receipt_as_data_sm(self):
        """Will:
        1. Set a SMS-MT route to connector A
        2. Send a SMS-MT to that route from a SMPPc and request DLR
        3. Wait for the DLR (data_sm) to be routed back to SMPPc through SMPPs as a data_sm
        """
        #
        # TODO: include this test in the previous one (test_receipt_as_deliver_sm) as 
        # an iteration (trigger_DLR type = data_sm)
        #
        yield self.connect('127.0.0.1', self.pbPort)
    test_receipt_as_data_sm.skip = 'TODO #92'

    @defer.inlineCallbacks
    def test_receipt_for_unknown_message(self):
        """Will:
        #. Receive a DLR for an unknown message (not mapped)
        """
        yield self.connect('127.0.0.1', self.pbPort)
        yield self.prepareRoutingsAndStartConnector()
        
        # Bind
        yield self.smppc_factory.connectAndBind()

        # Install mocks
        self.smpps_factory.lastProto.sendPDU = mock.Mock(wraps=self.smpps_factory.lastProto.sendPDU)

        # Send a SMS MT through smpps interface
        SubmitSmPDU = copy.deepcopy(self.SubmitSmPDU)
        SubmitSmPDU.params['registered_delivery'] = RegisteredDelivery(RegisteredDeliveryReceipt.SMSC_DELIVERY_RECEIPT_REQUESTED_FOR_FAILURE)
        yield self.smppc_factory.lastProto.sendDataRequest(SubmitSmPDU)
        
        # Wait 3 seconds for submit_sm_resp
        exitDeferred = defer.Deferred()
        reactor.callLater(3, exitDeferred.callback, None)
        yield exitDeferred

        # Run tests
        self.assertEqual(self.smpps_factory.lastProto.sendPDU.call_count, 1)
        # smpps response #1 was a submit_sm_resp with ESME_ROK
        response_pdu_1 = self.smpps_factory.lastProto.sendPDU.call_args_list[0][0][0]
        self.assertEqual(response_pdu_1.id, pdu_types.CommandId.submit_sm_resp)

        # Trigger receipt with an unknown id
        yield self.SMSCPort.factory.lastClient.trigger_DLR(stat = 'DELIVRD', _id = '77unknown_id77')

        # Wait some time before testing
        exitDeferred = defer.Deferred()
        reactor.callLater(0.5, exitDeferred.callback, None)
        yield exitDeferred

        # Unbind & Disconnect
        yield self.smppc_factory.smpp.unbindAndDisconnect()
        yield self.stopSmppClientConnectors()

        # Run tests
        # smpps last response was a unbind_resp, and there were no further deliver_sm
        self.assertEqual(self.smpps_factory.lastProto.sendPDU.call_count, 2)
        last_pdu = self.smpps_factory.lastProto.sendPDU.call_args_list[1][0][0]
        self.assertEqual(last_pdu.id, pdu_types.CommandId.unbind_resp)