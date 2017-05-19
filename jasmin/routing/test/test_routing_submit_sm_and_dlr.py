# -*- coding: utf-8 -*-

import copy
import string
import urllib
import binascii

import mock
from twisted.internet import defer
from twisted.web import server
from twisted.web.client import getPage

from jasmin.protocols.smpp.test.smsc_simulator import *
from jasmin.redis.client import ConnectionWithConfiguration
from jasmin.redis.configs import RedisForJasminConfig
from jasmin.routing.Filters import TransparentFilter
from jasmin.routing.Routes import FailoverMTRoute
from jasmin.routing.jasminApi import SmppClientConnector
from jasmin.routing.proxies import RouterPBProxy
from jasmin.routing.test.http_server import AckServer
from jasmin.routing.test.test_router import (HappySMSCTestCase, SubmitSmTestCaseTools,
                                             composeMessage, SMPPClientManagerPBTestCase,
                                             LastClientFactory, id_generator)
from jasmin.routing.test.test_router_smpps import SMPPClientTestCases
from jasmin.vendor.smpp.pdu import pdu_types
from jasmin.vendor.smpp.pdu.pdu_types import AddrTon, AddrNpi
from jasmin.vendor.smpp.pdu.smpp_time import FixedOffset


@defer.inlineCallbacks
def waitFor(seconds):
    # Wait seconds
    waitDeferred = defer.Deferred()
    reactor.callLater(seconds, waitDeferred.callback, None)
    yield waitDeferred


class HttpParameterTestCases(RouterPBProxy, HappySMSCTestCase, SubmitSmTestCaseTools):
    @defer.inlineCallbacks
    def test_validity_period(self):
        yield self.connect('127.0.0.1', self.pbPort)
        yield self.prepareRoutingsAndStartConnector()

        self.params['validity-period'] = 1440  # 1 Day = 24 x 60 minutes = 1440 minutes
        baseurl = 'http://127.0.0.1:1401/send?%s' % urllib.urlencode(self.params)

        # Send a MT
        # We should receive a msg id
        c = yield getPage(baseurl, method=self.method, postdata=self.postdata)
        msgStatus = c[:7]

        yield self.stopSmppClientConnectors()

        # Run tests
        self.assertEqual(msgStatus, 'Success')
        self.assertEqual(1, len(self.SMSCPort.factory.lastClient.submitRecords))
        self.assertNotEqual(None, self.SMSCPort.factory.lastClient.submitRecords[0].params['validity_period'])
        timediff = self.SMSCPort.factory.lastClient.submitRecords[0].params['validity_period'] - datetime.now()
        self.assertGreaterEqual(timediff.seconds / 60,
                                (self.params['validity-period'] - 1))  # Tolerate one minute of test latency
        self.assertLess(timediff.seconds / 60, (self.params['validity-period'] + 1))

    @defer.inlineCallbacks
    def test_dlr_level_default(self):
        yield self.connect('127.0.0.1', self.pbPort)
        yield self.prepareRoutingsAndStartConnector()

        baseurl = 'http://127.0.0.1:1401/send?%s' % urllib.urlencode(self.params)

        # Send a MT
        # We should receive a msg id
        c = yield getPage(baseurl, method=self.method, postdata=self.postdata)
        msgStatus = c[:7]

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
        c = yield getPage(baseurl, method=self.method, postdata=self.postdata)
        msgStatus = c[:7]

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
        c = yield getPage(baseurl, method=self.method, postdata=self.postdata)
        msgStatus = c[:7]

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
        c = yield getPage(baseurl, method=self.method, postdata=self.postdata)
        msgStatus = c[:7]

        yield self.stopSmppClientConnectors()

        # Run tests
        self.assertEqual(msgStatus, 'Success')
        self.assertEqual(1, len(self.SMSCPort.factory.lastClient.submitRecords))
        self.assertEqual(str(self.SMSCPort.factory.lastClient.submitRecords[0].params['registered_delivery'].receipt),
                         'SMSC_DELIVERY_RECEIPT_REQUESTED')

    @defer.inlineCallbacks
    def test_connector_source_addr_ton(self):
        "Related to #104"
        yield self.connect('127.0.0.1', self.pbPort)
        yield self.prepareRoutingsAndStartConnector()

        baseurl = 'http://127.0.0.1:1401/send?%s' % urllib.urlencode(self.params)

        # Send a MT
        # We should receive a msg id
        c = yield getPage(baseurl, method=self.method, postdata=self.postdata)
        msgStatus = c[:7]

        yield self.stopSmppClientConnectors()

        # Run tests
        self.assertEqual(msgStatus, 'Success')
        self.assertEqual(1, len(self.SMSCPort.factory.lastClient.submitRecords))
        self.assertEqual(str(self.SMSCPort.factory.lastClient.submitRecords[0].params['source_addr_ton']),
                         'NATIONAL')

    @defer.inlineCallbacks
    def test_connector_source_addr_ton_long_message(self):
        "Related to #104, will check if all parts of long message will get the same source_addr_ton"
        yield self.connect('127.0.0.1', self.pbPort)
        yield self.prepareRoutingsAndStartConnector()

        self.params['content'] = composeMessage({'_'}, 200)
        baseurl = 'http://127.0.0.1:1401/send?%s' % urllib.urlencode(self.params)

        # Send a MT
        # We should receive a msg id
        c = yield getPage(baseurl, method=self.method, postdata=self.postdata)
        msgStatus = c[:7]

        yield self.stopSmppClientConnectors()

        # Run tests
        self.assertEqual(msgStatus, 'Success')
        self.assertEqual(2, len(self.SMSCPort.factory.lastClient.submitRecords))
        self.assertEqual(str(self.SMSCPort.factory.lastClient.submitRecords[0].params['source_addr_ton']),
                         'NATIONAL')
        self.assertEqual(str(self.SMSCPort.factory.lastClient.submitRecords[1].params['source_addr_ton']),
                         'NATIONAL')

    @defer.inlineCallbacks
    def test_gsm338_chars_in_smsc_default_data_coding(self):
        """Related to #566"""
        yield self.connect('127.0.0.1', self.pbPort)
        yield self.prepareRoutingsAndStartConnector()

        # Some gsm338 chars
        self.params['content'] = '@$ΔßÉ'
        self.params['coding'] = '0'
        baseurl = 'http://127.0.0.1:1401/send?%s' % urllib.urlencode(self.params)

        # Send a MT
        # We should receive a msg id
        c = yield getPage(baseurl, method=self.method, postdata=self.postdata)
        msgStatus = c[:7]

        yield self.stopSmppClientConnectors()

        # Run tests
        self.assertEqual(msgStatus, 'Success')
        self.assertEqual(1, len(self.SMSCPort.factory.lastClient.submitRecords))
        # '@$ΔßÉ' encoded in gsm338 = '\x00\x02\x10\x1e\x1f'
        self.assertEqual(str(self.SMSCPort.factory.lastClient.submitRecords[0].params['short_message']),
                         '\x00\x02\x10\x1e\x1f')

    @defer.inlineCallbacks
    def test_hex_content(self):
        yield self.connect('127.0.0.1', self.pbPort)
        yield self.prepareRoutingsAndStartConnector()

        # Some hebrew data
        del(self.params['content'])
        self.params['hex-content'] = '05d905d005dc05dc05dc05dc05dc05dc05dc05d4'
        self.params['coding'] = '8'
        baseurl = 'http://127.0.0.1:1401/send?%s' % urllib.urlencode(self.params)

        # Send a MT
        # We should receive a msg id
        c = yield getPage(baseurl, method=self.method, postdata=self.postdata)
        msgStatus = c[:7]

        yield self.stopSmppClientConnectors()

        # Run tests
        self.assertEqual(msgStatus, 'Success')
        self.assertEqual(1, len(self.SMSCPort.factory.lastClient.submitRecords))
        self.assertEqual(binascii.hexlify(self.SMSCPort.factory.lastClient.submitRecords[0].params['short_message']),
                         self.params['hex-content'])


class FailoverMTRouteHttpTestCases(RouterPBProxy, HappySMSCTestCase, SubmitSmTestCaseTools):
    @defer.inlineCallbacks
    def test_failovermtroute(self):
        """#467: Will ensure a failover route will deliver the message"""

        yield self.connect('127.0.0.1', self.pbPort)

        # Initiate a failover mt route with c1 as the falling back connector
        c1 = SmppClientConnector(id_generator())
        route = FailoverMTRoute(
            [TransparentFilter()],
            [SmppClientConnector(id_generator()), c1],
            0.0)
        yield self.prepareRoutingsAndStartConnector(route=route, c1=c1)

        baseurl = 'http://127.0.0.1:1401/send?%s' % urllib.urlencode(self.params)

        # Send a MT
        # We should receive a msg id
        c = yield getPage(baseurl, method=self.method, postdata=self.postdata)
        msgStatus = c[:7]

        yield self.stopSmppClientConnectors()

        # Run tests
        self.assertEqual(msgStatus, 'Success')
        self.assertEqual(1, len(self.SMSCPort.factory.lastClient.submitRecords))


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
        c = yield getPage(baseurl, method=self.method, postdata=self.postdata)
        msgStatus = c[:7]
        msgId = c[9:45]

        # Wait 2 seconds for submit_sm_resp
        yield waitFor(2)

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
        self.params['dlr-level'] = 2
        baseurl = 'http://127.0.0.1:1401/send?%s' % urllib.urlencode(self.params)

        # Send a MT
        # We should receive a msg id
        c = yield getPage(baseurl, method=self.method, postdata=self.postdata)
        msgStatus = c[:7]
        msgId = c[9:45]

        # Wait 2 seconds for submit_sm_resp
        yield waitFor(2)

        # Trigger a deliver_sm containing a DLR
        yield self.SMSCPort.factory.lastClient.trigger_DLR()

        # Wait 1 seconds for deliver_sm
        yield waitFor(1)

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
        c = yield getPage(baseurl, method=self.method, postdata=self.postdata)
        msgStatus = c[:7]
        msgId = c[9:45]

        # Wait 2 seconds for submit_sm_resp
        yield waitFor(2)

        # Trigger a deliver_sm
        yield self.SMSCPort.factory.lastClient.trigger_DLR()

        # Wait 1 seconds for deliver_sm
        yield waitFor(1)

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
        c = yield getPage(baseurl, method=self.method, postdata=self.postdata)
        msgStatus = c[:7]
        msgId = c[9:45]

        # Wait 1 seconds for submit_sm_resp
        yield waitFor(1)

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
        c = yield getPage(baseurl, method=self.method, postdata=self.postdata)
        msgStatus = c[:7]
        msgId = c[9:45]

        # Wait 1 seconds for submit_sm_resp
        yield waitFor(1)

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
        c = yield getPage(baseurl, method=self.method, postdata=self.postdata)
        msgStatus = c[:7]
        msgId = c[9:45]

        # Wait 1 seconds for submit_sm_resp
        yield waitFor(1)

        # Trigger a deliver_sm containing a DLR
        yield self.SMSCPort.factory.lastClient.trigger_DLR()

        # Wait 1 seconds for deliver_sm
        yield waitFor(1)

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
        c = yield getPage(baseurl, method=self.method, postdata=self.postdata)
        msgStatus = c[:7]
        msgId = c[9:45]

        # Wait 1 seconds for submit_sm_resp
        yield waitFor(1)

        # Trigger a deliver_sm containing a DLR
        yield self.SMSCPort.factory.lastClient.trigger_DLR()

        # Wait 1 seconds for deliver_sm
        yield waitFor(1)

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
        c = yield getPage(baseurl, method=self.method, postdata=self.postdata)
        msgStatus = c[:7]
        msgId = c[9:45]

        # Wait 1 seconds for submit_sm_resp
        yield waitFor(1)

        # Trigger a deliver_sm containing a DLR
        yield self.SMSCPort.factory.lastClient.trigger_DLR()

        # Wait 1 seconds for deliver_sm
        yield waitFor(1)

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
        c = yield getPage(baseurl, method=self.method, postdata=self.postdata)
        msgStatus = c[:7]
        msgId = c[9:45]

        # Wait 1 seconds for submit_sm_resp
        yield waitFor(1)

        # Trigger a deliver_sm
        yield self.SMSCPort.factory.lastClient.trigger_DLR()

        # Wait 1 seconds for deliver_sm
        yield waitFor(1)

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
        c = yield getPage(baseurl, method=self.method, postdata=self.postdata)
        msgStatus = c[:7]

        yield self.stopSmppClientConnectors()

        # Run tests
        self.assertEqual(msgStatus, 'Success')

    @defer.inlineCallbacks
    def test_long_message_with_inurl_dlr(self):
        """As discussed in #483: It appears that when sending a long message through http and requesting
        dlr-level 3 with a callback, only the first pdu is asking for dlr and when deliver_sm holding delivery
        receipt is received for that pdu jasmin is not finding the correct mapping for it.
        """
        yield self.connect('127.0.0.1', self.pbPort)
        yield self.prepareRoutingsAndStartConnector()

        self.params['dlr-url'] = self.dlr_url
        self.params['dlr-level'] = 3
        self.params['dlr-method'] = 'GET'
        self.params['content'] = ''.join(random.choice(string.ascii_uppercase + string.digits) for _ in range(200))
        baseurl = 'http://127.0.0.1:1401/send?%s' % urllib.urlencode(self.params)

        # Send a MT
        # We should receive a msg id
        c = yield getPage(baseurl, method=self.method, postdata=self.postdata)
        msgStatus = c[:7]
        msgId = c[9:45]

        # Wait 1 seconds for 2 submit_sm_resps
        yield waitFor(1)

        # Trigger a deliver_sm
        yield self.SMSCPort.factory.lastClient.trigger_DLR()

        # Wait 1 seconds for deliver_sm
        yield waitFor(1)

        yield self.stopSmppClientConnectors()

        # Run tests
        # Ensure the only the last pdu has requested a dlr
        self.assertEqual(2, len(self.SMSCPort.factory.lastClient.submitRecords))
        self.assertEqual(str(self.SMSCPort.factory.lastClient.submitRecords[0].params['registered_delivery'].receipt),
                         'NO_SMSC_DELIVERY_RECEIPT_REQUESTED')
        self.assertEqual(str(self.SMSCPort.factory.lastClient.submitRecords[1].params['registered_delivery'].receipt),
                         'SMSC_DELIVERY_RECEIPT_REQUESTED')

        # Ensure dlr-url were called
        self.assertEqual(msgStatus, 'Success')
        # A DLR must be sent to dlr_url
        self.assertEqual(self.AckServerResource.render_GET.call_count, 2)
        # Message ID must be transmitted in the DLR
        callArgs_level1 = self.AckServerResource.render_GET.call_args_list[0][0][0].args
        callArgs_level2 = self.AckServerResource.render_GET.call_args_list[1][0][0].args
        self.assertEqual(callArgs_level1['id'][0], msgId)
        self.assertEqual(callArgs_level2['id'][0], msgId)

    @defer.inlineCallbacks
    def test_quick_dlr(self):
        """Refs #472
        Will slow down the call to redis when saving the dlr map in submit_sm_resp callback and get the
        deliver_sm dlr before it, this will let DLRLookup to retry looking up the dlr map.
        """
        yield self.connect('127.0.0.1', self.pbPort)
        yield self.prepareRoutingsAndStartConnector()

        # Make a new connection to redis
        # It is used to wrap DLRLookup's redis client and slowdown calls to hmset
        RCInstance = RedisForJasminConfig()
        r = yield ConnectionWithConfiguration(RCInstance)
        # Authenticate and select db
        if RCInstance.password is not None:
            yield r.auth(RCInstance.password)
            yield r.select(RCInstance.dbid)

        # Mock hmset redis's call to slow it down
        @defer.inlineCallbacks
        def mocked_hmset(k, v):
            # Slow down hmset
            # We need to receive the deliver_sm dlr before submit_sm_resp
            if k[:11] == 'queue-msgid':
                yield waitFor(1)

            yield r.hmset(k, v)

        self.dlrlookup.redisClient.hmset = mock.MagicMock(wraps=mocked_hmset)

        # Ask for DLR
        self.params['dlr-url'] = self.dlr_url
        self.params['dlr-level'] = 2
        self.params['dlr-method'] = 'GET'
        self.params['content'] = 'somecontent'
        baseurl = 'http://127.0.0.1:1401/send?%s' % urllib.urlencode(self.params)

        # Send SubmitSmPDU
        yield getPage(baseurl, method=self.method, postdata=self.postdata)
        yield waitFor(1)
        # Push DLR
        yield self.SMSCPort.factory.lastClient.trigger_DLR()

        # Wait for DLRLookup retrial
        yield waitFor(11)

        yield self.stopSmppClientConnectors()
        yield r.disconnect()

        # Run tests
        # A DLR must be sent to dlr_url
        self.assertEqual(self.AckServerResource.render_GET.call_count, 1)


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
        c = yield getPage(baseurl, method=self.method, postdata=self.postdata)
        msgStatus = c[:7]
        msgId = c[9:45]

        # Wait 1 seconds for submit_sm_resp
        yield waitFor(1)

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
        self.params['dlr-level'] = 2
        baseurl = 'http://127.0.0.1:1401/send?%s' % urllib.urlencode(self.params)

        # Send a MT
        # We should receive a msg id
        c = yield getPage(baseurl, method=self.method, postdata=self.postdata)
        msgStatus = c[:7]
        msgId = c[9:45]

        # Wait 1 seconds for submit_sm_resp
        yield waitFor(1)

        # Trigger a deliver_sm containing a DLR
        yield self.SMSCPort.factory.lastClient.trigger_DLR()

        # Wait 1 seconds for deliver_sm
        yield waitFor(1)

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
        c = yield getPage(baseurl, method=self.method, postdata=self.postdata)
        msgStatus = c[:7]
        msgId = c[9:45]

        # Wait 1 seconds for submit_sm_resp
        yield waitFor(1)

        # Trigger a deliver_sm
        yield self.SMSCPort.factory.lastClient.trigger_DLR()

        # Wait 1 seconds for deliver_sm
        yield waitFor(1)

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
        c = yield getPage(baseurl, method=self.method, postdata=self.postdata)
        msgStatus = c[:7]
        msgId = c[9:45]

        # Wait 1 seconds for submit_sm_resp
        yield waitFor(1)

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
        c = yield getPage(baseurl, method=self.method, postdata=self.postdata)
        msgStatus = c[:7]
        msgId = c[9:45]

        # Wait 1 seconds for submit_sm_resp
        yield waitFor(1)

        # Trigger a deliver_sm containing a DLR
        yield self.SMSCPort.factory.lastClient.trigger_DLR()

        # Wait 1 seconds for deliver_sm
        yield waitFor(1)

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
        c = yield getPage(baseurl, method=self.method, postdata=self.postdata)
        msgStatus = c[:7]
        msgId = c[9:45]

        # Wait 1 seconds for submit_sm_resp
        yield waitFor(1)

        # Trigger a deliver_sm
        yield self.SMSCPort.factory.lastClient.trigger_DLR()

        # Wait 1 seconds for deliver_sm
        yield waitFor(1)

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


class NoResponseOnSubmitSMSCTestCase(SMPPClientManagerPBTestCase):
    protocol = NoResponseOnSubmitSMSCRecorder

    @defer.inlineCallbacks
    def setUp(self):
        yield SMPPClientManagerPBTestCase.setUp(self)

        self.smsc_f = LastClientFactory()
        self.smsc_f.protocol = self.protocol
        self.SMSCPort = reactor.listenTCP(0, self.smsc_f)

    @defer.inlineCallbacks
    def tearDown(self):
        yield SMPPClientManagerPBTestCase.tearDown(self)

        yield self.SMSCPort.stopListening()


class SendtoNoResponseOnSubmitSMSCTestCases(RouterPBProxy, NoResponseOnSubmitSMSCTestCase, SubmitSmTestCaseTools):
    @defer.inlineCallbacks
    def test_submit_sm_with_no_submit_sm_resp(self):
        """Related to #247

        Send sms to a NoResponseOnSubmitSMSC and check that sms was requeued on pdu request timeout
        error.
        """

        yield self.connect('127.0.0.1', self.pbPort)
        yield self.prepareRoutingsAndStartConnector(reconnectOnConnectionLoss=False)

        self.params['dlr-level'] = 1
        baseurl = 'http://127.0.0.1:1401/send?%s' % urllib.urlencode(self.params)

        # Send a MT
        # We should receive a msg id
        c = yield getPage(baseurl, method=self.method, postdata=self.postdata)
        msgStatus = c[:7]
        msgId = c[9:45]

        # Wait till the connector unbinds because of no response
        yield waitFor(4)
        yield self.stopSmppClientConnectors()

        yield self.SMPPClientManagerPBProxy.start(self.c1.cid)

        # Wait till the connector unbinds because of no response
        yield waitFor(4)
        yield self.stopSmppClientConnectors()

        # Run tests
        self.assertEqual(msgStatus, 'Success')
        self.assertEqual(2, len(self.SMSCPort.factory.lastClient.submitRecords))


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

        # Cancel DLRLookup retrial
        self.dlrlookup.config.dlr_lookup_max_retries = 1

        # Bind
        yield self.smppc_factory.connectAndBind()

        # Install mocks
        self.smpps_factory.lastProto.sendPDU = mock.Mock(wraps=self.smpps_factory.lastProto.sendPDU)

        # Send a SMS MT through smpps interface
        SubmitSmPDU = copy.deepcopy(self.SubmitSmPDU)
        SubmitSmPDU.params['registered_delivery'] = RegisteredDelivery(
            RegisteredDeliveryReceipt.SMSC_DELIVERY_RECEIPT_REQUESTED)
        yield self.smppc_factory.lastProto.sendDataRequest(SubmitSmPDU)

        # Wait 3 seconds for submit_sm_resp
        yield waitFor(3)

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
            yield self.SMSCPort.factory.lastClient.trigger_DLR(stat=stat)
            x += 1

            # Wait some time before testing
            yield waitFor(0.5)

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
            yield self.SMSCPort.factory.lastClient.trigger_DLR(stat=stat)
            x += 1

            # Wait some time before testing
            yield waitFor(0.5)

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
        yield self.connect('127.0.0.1', self.pbPort)
        yield self.prepareRoutingsAndStartConnector()

        # Cancel DLRLookup retrial
        self.dlrlookup.config.dlr_lookup_max_retries = 1

        # Bind
        yield self.smppc_factory.connectAndBind()

        # Install mocks
        self.smpps_factory.lastProto.sendPDU = mock.Mock(wraps=self.smpps_factory.lastProto.sendPDU)

        # Send a SMS MT through smpps interface
        SubmitSmPDU = copy.deepcopy(self.SubmitSmPDU)
        SubmitSmPDU.params['registered_delivery'] = RegisteredDelivery(
            RegisteredDeliveryReceipt.SMSC_DELIVERY_RECEIPT_REQUESTED)
        yield self.smppc_factory.lastProto.sendDataRequest(SubmitSmPDU)

        # Wait 3 seconds for submit_sm_resp
        yield waitFor(3)

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
            yield self.SMSCPort.factory.lastClient.trigger_DLR(stat=stat, pdu_type='data_sm')
            x += 1

            # Wait some time before testing
            yield waitFor(0.5)

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
            yield self.SMSCPort.factory.lastClient.trigger_DLR(stat=stat, pdu_type='data_sm')
            x += 1

            # Wait some time before testing
            yield waitFor(0.5)

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
    def test_receipt_for_unknown_message(self):
        """Will:
        #. Receive a DLR for an unknown message (not mapped)
        """
        yield self.connect('127.0.0.1', self.pbPort)
        yield self.prepareRoutingsAndStartConnector()

        # Cancel DLRLookup retrial
        self.dlrlookup.config.dlr_lookup_max_retries = 1

        # Bind
        yield self.smppc_factory.connectAndBind()

        # Install mocks
        self.smpps_factory.lastProto.sendPDU = mock.Mock(wraps=self.smpps_factory.lastProto.sendPDU)

        # Send a SMS MT through smpps interface
        SubmitSmPDU = copy.deepcopy(self.SubmitSmPDU)
        SubmitSmPDU.params['registered_delivery'] = RegisteredDelivery(
            RegisteredDeliveryReceipt.SMSC_DELIVERY_RECEIPT_REQUESTED_FOR_FAILURE)
        yield self.smppc_factory.lastProto.sendDataRequest(SubmitSmPDU)

        # Wait 3 seconds for submit_sm_resp
        yield waitFor(3)

        # Run tests
        self.assertEqual(self.smpps_factory.lastProto.sendPDU.call_count, 1)
        # smpps response #1 was a submit_sm_resp with ESME_ROK
        response_pdu_1 = self.smpps_factory.lastProto.sendPDU.call_args_list[0][0][0]
        self.assertEqual(response_pdu_1.id, pdu_types.CommandId.submit_sm_resp)

        # Trigger receipt with an unknown id
        yield self.SMSCPort.factory.lastClient.trigger_DLR(stat='DELIVRD', _id='77unknown_id77')

        # Wait some time before testing
        yield waitFor(0.5)

        # Unbind & Disconnect
        yield self.smppc_factory.smpp.unbindAndDisconnect()
        yield self.stopSmppClientConnectors()

        # Run tests
        # smpps last response was a unbind_resp, and there were no further deliver_sm
        self.assertEqual(self.smpps_factory.lastProto.sendPDU.call_count, 2)
        last_pdu = self.smpps_factory.lastProto.sendPDU.call_args_list[1][0][0]
        self.assertEqual(last_pdu.id, pdu_types.CommandId.unbind_resp)

    @defer.inlineCallbacks
    def test_receipt_for_long_destination_addr(self):
        """It's been reported that long destination_addr will lead to an error taking Jasmin down.
        Related to #405
        """
        yield self.connect('127.0.0.1', self.pbPort)
        yield self.prepareRoutingsAndStartConnector()

        # Bind
        yield self.smppc_factory.connectAndBind()

        # Install mocks
        self.smpps_factory.lastProto.sendPDU = mock.Mock(wraps=self.smpps_factory.lastProto.sendPDU)

        # Send a SMS MT through smpps interface
        SubmitSmPDU = copy.deepcopy(self.SubmitSmPDU)
        SubmitSmPDU.params['destination_addr'] = '905325932042'
        SubmitSmPDU.params['registered_delivery'] = RegisteredDelivery(
            RegisteredDeliveryReceipt.SMSC_DELIVERY_RECEIPT_REQUESTED)
        yield self.smppc_factory.lastProto.sendDataRequest(SubmitSmPDU)

        # Wait 3 seconds for submit_sm_resp
        yield waitFor(3)

        # Trigger DLR
        yield self.SMSCPort.factory.lastClient.trigger_DLR(stat='DELIVRD')

        # Wait some time before testing
        yield waitFor(0.5)

        # Asserts
        self.assertEqual(self.smpps_factory.lastProto.sendPDU.call_count, 2)

        # Unbind & Disconnect
        yield self.smppc_factory.smpp.unbindAndDisconnect()
        yield self.stopSmppClientConnectors()

        # Run tests
        # smpps last response was a unbind_resp
        last_pdu = self.smpps_factory.lastProto.sendPDU.call_args_list[2][0][0]
        self.assertEqual(last_pdu.id, pdu_types.CommandId.unbind_resp)

    @defer.inlineCallbacks
    def test_receipt_with_correct_ton_npi(self):
        """It's been reported that source and destination ton/npi are not taken from original submit_sm when
		delivering dlr
        Related to #448
        """
        yield self.connect('127.0.0.1', self.pbPort)
        yield self.prepareRoutingsAndStartConnector()

        # Bind
        yield self.smppc_factory.connectAndBind()

        # Install mocks
        self.smpps_factory.lastProto.sendPDU = mock.Mock(wraps=self.smpps_factory.lastProto.sendPDU)

        # Send a SMS MT through smpps interface
        SubmitSmPDU = copy.deepcopy(self.SubmitSmPDU)
        SubmitSmPDU.params['registered_delivery'] = RegisteredDelivery(
            RegisteredDeliveryReceipt.SMSC_DELIVERY_RECEIPT_REQUESTED)
        SubmitSmPDU.params['source_addr_ton'] = AddrTon.NATIONAL
        SubmitSmPDU.params['source_addr_npi'] = AddrNpi.ISDN
        SubmitSmPDU.params['dest_addr_ton'] = AddrTon.INTERNATIONAL
        SubmitSmPDU.params['dest_addr_npi'] = AddrNpi.INTERNET
        yield self.smppc_factory.lastProto.sendDataRequest(SubmitSmPDU)

        # Wait 3 seconds for submit_sm_resp
        yield waitFor(3)

        # Trigger DLR
        yield self.SMSCPort.factory.lastClient.trigger_DLR(stat='DELIVRD')

        # Wait some time before testing
        yield waitFor(0.5)

        # Unbind & Disconnect
        yield self.smppc_factory.smpp.unbindAndDisconnect()
        yield self.stopSmppClientConnectors()

        # Run tests
        dlr_pdu = self.smpps_factory.lastProto.sendPDU.call_args_list[1][0][0]
        # assert correct ton/npi are transmitted to downstream (reversed from submit_sm)
        self.assertEqual(AddrTon.NATIONAL, dlr_pdu.params['dest_addr_ton'])
        self.assertEqual(AddrNpi.ISDN, dlr_pdu.params['dest_addr_npi'])
        self.assertEqual(AddrTon.INTERNATIONAL, dlr_pdu.params['source_addr_ton'])
        self.assertEqual(AddrNpi.INTERNET, dlr_pdu.params['source_addr_npi'])
        # smpps last response was a unbind_resp
        last_pdu = self.smpps_factory.lastProto.sendPDU.call_args_list[2][0][0]
        self.assertEqual(last_pdu.id, pdu_types.CommandId.unbind_resp)

    @defer.inlineCallbacks
    def test_receipt_data_sm_with_correct_ton_npi(self):
        """It's been reported that source and destination ton/npi are not taken from original submit_sm when
		delivering dlr
        Related to #448
        """
        yield self.connect('127.0.0.1', self.pbPort)
        yield self.prepareRoutingsAndStartConnector()

        # Bind
        yield self.smppc_factory.connectAndBind()

        # Install mocks
        self.smpps_factory.lastProto.sendPDU = mock.Mock(wraps=self.smpps_factory.lastProto.sendPDU)

        # Send a SMS MT through smpps interface
        SubmitSmPDU = copy.deepcopy(self.SubmitSmPDU)
        SubmitSmPDU.params['registered_delivery'] = RegisteredDelivery(
            RegisteredDeliveryReceipt.SMSC_DELIVERY_RECEIPT_REQUESTED)
        SubmitSmPDU.params['source_addr_ton'] = AddrTon.NATIONAL
        SubmitSmPDU.params['source_addr_npi'] = AddrNpi.ISDN
        SubmitSmPDU.params['dest_addr_ton'] = AddrTon.INTERNATIONAL
        SubmitSmPDU.params['dest_addr_npi'] = AddrNpi.INTERNET
        yield self.smppc_factory.lastProto.sendDataRequest(SubmitSmPDU)

        # Wait 3 seconds for submit_sm_resp
        yield waitFor(3)

        # Trigger DLR
        yield self.SMSCPort.factory.lastClient.trigger_DLR(stat='DELIVRD', pdu_type='data_sm')

        # Wait some time before testing
        yield waitFor(0.5)

        # Unbind & Disconnect
        yield self.smppc_factory.smpp.unbindAndDisconnect()
        yield self.stopSmppClientConnectors()

        # Run tests
        dlr_pdu = self.smpps_factory.lastProto.sendPDU.call_args_list[1][0][0]
        # assert correct ton/npi are transmitted to downstream (reversed from submit_sm)
        self.assertEqual(AddrTon.NATIONAL, dlr_pdu.params['dest_addr_ton'])
        self.assertEqual(AddrNpi.ISDN, dlr_pdu.params['dest_addr_npi'])
        self.assertEqual(AddrTon.INTERNATIONAL, dlr_pdu.params['source_addr_ton'])
        self.assertEqual(AddrNpi.INTERNET, dlr_pdu.params['source_addr_npi'])
        # smpps last response was a unbind_resp
        last_pdu = self.smpps_factory.lastProto.sendPDU.call_args_list[2][0][0]
        self.assertEqual(last_pdu.id, pdu_types.CommandId.unbind_resp)

    @defer.inlineCallbacks
    def test_receipt_with_SAR_prefix(self):
        "Related to #418"

        yield self.connect('127.0.0.1', self.pbPort)
        yield self.prepareRoutingsAndStartConnector()

        # Bind
        yield self.smppc_factory.connectAndBind()

        # Install mocks
        self.smpps_factory.lastProto.sendPDU = mock.Mock(wraps=self.smpps_factory.lastProto.sendPDU)

        # Tell the SMSCPort to response with the defined MsgId
        self.SMSCPort.factory.lastClient.nextResponseMsgId = 'SAR-234802311309-2024752f-2-1'

        # Send a SMS MT through smpps interface
        SubmitSmPDU = copy.deepcopy(self.SubmitSmPDU)
        SubmitSmPDU.params['registered_delivery'] = RegisteredDelivery(
            RegisteredDeliveryReceipt.SMSC_DELIVERY_RECEIPT_REQUESTED)
        yield self.smppc_factory.lastProto.sendDataRequest(SubmitSmPDU)

        # Wait 3 seconds for submit_sm_resp
        yield waitFor(3)

        # Run tests
        self.assertEqual(self.smpps_factory.lastProto.sendPDU.call_count, 1)
        # smpps response #1 was a submit_sm_resp with ESME_ROK
        response_pdu_1 = self.smpps_factory.lastProto.sendPDU.call_args_list[0][0][0]
        self.assertEqual(response_pdu_1.id, pdu_types.CommandId.submit_sm_resp)

        # Trigger receipt with an unknown id
        yield self.SMSCPort.factory.lastClient.trigger_DLR(stat='UNDELIV')

        # Wait some time before testing
        yield waitFor(0.5)

        # Unbind & Disconnect
        yield self.smppc_factory.smpp.unbindAndDisconnect()
        yield self.stopSmppClientConnectors()

        # Run tests
        self.assertEqual(self.smpps_factory.lastProto.sendPDU.call_count, 3)
        # smpps last response was a unbind_resp
        last_pdu = \
            self.smpps_factory.lastProto.sendPDU.call_args_list[self.smpps_factory.lastProto.sendPDU.call_count - 1][0][
                0]
        self.assertEqual(last_pdu.id, pdu_types.CommandId.unbind_resp)

    @defer.inlineCallbacks
    def test_receipt_with_leading_zeros(self):
        "Related to #408, #423"

        yield self.connect('127.0.0.1', self.pbPort)
        yield self.prepareRoutingsAndStartConnector()

        # Bind
        yield self.smppc_factory.connectAndBind()

        # Install mocks
        self.smpps_factory.lastProto.sendPDU = mock.Mock(wraps=self.smpps_factory.lastProto.sendPDU)

        # Tell the SMSCPort to response with the defined MsgId
        self.SMSCPort.factory.lastClient.nextResponseMsgId = '04062581461565934000241'

        # Send a SMS MT through smpps interface
        SubmitSmPDU = copy.deepcopy(self.SubmitSmPDU)
        SubmitSmPDU.params['registered_delivery'] = RegisteredDelivery(
            RegisteredDeliveryReceipt.SMSC_DELIVERY_RECEIPT_REQUESTED)
        yield self.smppc_factory.lastProto.sendDataRequest(SubmitSmPDU)

        # Wait 3 seconds for submit_sm_resp
        yield waitFor(3)

        # Run tests
        self.assertEqual(self.smpps_factory.lastProto.sendPDU.call_count, 1)
        # smpps response #1 was a submit_sm_resp with ESME_ROK
        response_pdu_1 = self.smpps_factory.lastProto.sendPDU.call_args_list[0][0][0]
        self.assertEqual(response_pdu_1.id, pdu_types.CommandId.submit_sm_resp)

        # Trigger receipt with an unknown id
        yield self.SMSCPort.factory.lastClient.trigger_DLR(_id='4062581461565934000241')

        # Wait some time before testing
        yield waitFor(0.5)

        # Unbind & Disconnect
        yield self.smppc_factory.smpp.unbindAndDisconnect()
        yield self.stopSmppClientConnectors()

        # Run tests
        self.assertEqual(self.smpps_factory.lastProto.sendPDU.call_count, 3)
        # smpps last response was a unbind_resp
        last_pdu = \
            self.smpps_factory.lastProto.sendPDU.call_args_list[self.smpps_factory.lastProto.sendPDU.call_count - 1][0][
                0]
        self.assertEqual(last_pdu.id, pdu_types.CommandId.unbind_resp)

    @defer.inlineCallbacks
    def test_quick_dlr(self):
        """Refs #472
        Will slow down the call to redis when saving the dlr map in submit_sm_resp callback and get the
        deliver_sm dlr before it, this will let DLRLookup to retry looking up the dlr map.
        """
        yield self.connect('127.0.0.1', self.pbPort)
        yield self.prepareRoutingsAndStartConnector()

        # Bind
        yield self.smppc_factory.connectAndBind()

        # Install mocks
        self.smpps_factory.lastProto.sendPDU = mock.Mock(wraps=self.smpps_factory.lastProto.sendPDU)

        # Make a new connection to redis
        # It is used to wrap DLRLookup's redis client and slowdown calls to hmset
        RCInstance = RedisForJasminConfig()
        r = yield ConnectionWithConfiguration(RCInstance)
        # Authenticate and select db
        if RCInstance.password is not None:
            yield r.auth(RCInstance.password)
            yield r.select(RCInstance.dbid)

        # Mock hmset redis's call to slow it down
        @defer.inlineCallbacks
        def mocked_hmset(k, v):
            # Slow down hmset
            # We need to receive the deliver_sm dlr before submit_sm_resp
            if k[:11] == 'queue-msgid':
                yield waitFor(1)

            yield r.hmset(k, v)
        self.dlrlookup.redisClient.hmset = mock.MagicMock(wraps=mocked_hmset)

        # Ask for DLR
        SubmitSmPDU = copy.deepcopy(self.SubmitSmPDU)
        SubmitSmPDU.params['registered_delivery'] = RegisteredDelivery(
            RegisteredDeliveryReceipt.SMSC_DELIVERY_RECEIPT_REQUESTED)

        # Send SubmitSmPDU
        yield self.smppc_factory.lastProto.sendDataRequest(SubmitSmPDU)
        yield waitFor(1)
        # Push DLR
        yield self.SMSCPort.factory.lastClient.trigger_DLR(stat='DELIVRD')

        # Wait for DLRLookup retrial
        yield waitFor(11)

        # Count delivers
        # 2 = submit_sm_resp + deliver_sm
        self.assertEqual(2, self.smpps_factory.lastProto.sendPDU.call_count)

        # Unbind & Disconnect
        yield self.smppc_factory.smpp.unbindAndDisconnect()
        yield self.stopSmppClientConnectors()
        yield r.disconnect()


class SmppsMessagingTestCases(RouterPBProxy, SMPPClientTestCases, SubmitSmTestCaseTools):
    @defer.inlineCallbacks
    def test_validity_period_with_tzinfo(self):
        """Related to #267

        Having validity_period with timezone set would lead into the following error in messages.log
        Error in submit_sm_errback: __init__() takes exactly 3 arguments (1 given)
        """
        yield self.connect('127.0.0.1', self.pbPort)
        yield self.prepareRoutingsAndStartConnector()

        # Bind
        yield self.smppc_factory.connectAndBind()

        # Install mocks
        self.smpps_factory.lastProto.sendPDU = mock.Mock(wraps=self.smpps_factory.lastProto.sendPDU)

        # Send a SMS MT through smpps interface with validity period set
        validity_period = datetime(2015, 7, 29, 15, 21, 54, tzinfo=FixedOffset(24, 'Paris'))
        SubmitSmPDU = copy.deepcopy(self.SubmitSmPDU)
        SubmitSmPDU.params['validity_period'] = validity_period
        yield self.smppc_factory.lastProto.sendDataRequest(SubmitSmPDU)

        # Wait 3 seconds for submit_sm_resp
        yield waitFor(3)

        # Unbind & Disconnect
        yield self.smppc_factory.smpp.unbindAndDisconnect()
        yield self.stopSmppClientConnectors()

        # Run tests
        self.assertEqual(1, len(self.SMSCPort.factory.lastClient.submitRecords))
        self.assertTrue(type(self.SMSCPort.factory.lastClient.submitRecords[0].params['validity_period']) == datetime)


class FailoverMTRouteSmppTestCases(RouterPBProxy, SMPPClientTestCases, SubmitSmTestCaseTools):
    @defer.inlineCallbacks
    def test_failovermtroute(self):
        """#467: Will ensure a failover route will deliver the message"""

        yield self.connect('127.0.0.1', self.pbPort)

        # Initiate a failover mt route with c1 as the falling back connector
        c1 = SmppClientConnector(id_generator())
        route = FailoverMTRoute(
            [TransparentFilter()],
            [SmppClientConnector(id_generator()), c1],
            0.0)
        yield self.prepareRoutingsAndStartConnector(route=route, c1=c1)

        # Bind
        yield self.smppc_factory.connectAndBind()

        # Install mocks
        self.smpps_factory.lastProto.sendPDU = mock.Mock(wraps=self.smpps_factory.lastProto.sendPDU)

        # Send a SMS MT through smpps interface
        yield self.smppc_factory.lastProto.sendDataRequest(self.SubmitSmPDU)

        # Wait 3 seconds for submit_sm_resp
        yield waitFor(3)

        # Unbind & Disconnect
        yield self.smppc_factory.smpp.unbindAndDisconnect()
        yield self.stopSmppClientConnectors()

        # Run tests
        self.assertEqual(1, len(self.SMSCPort.factory.lastClient.submitRecords))


class DlrMsgIdBaseTestCases(RouterPBProxy, SMPPClientTestCases, SubmitSmTestCaseTools):
    @defer.inlineCallbacks
    def test_msg_id_dec_then_hex(self):
        """Will:
        1. Set a SMS-MT route to connector A
        2. Send a SMS-MT to that route from a SMPPc and get a message id as integer
        3. Wait for the DLR (deliver_sm) to be routed back to SMPPc through SMPPs as a deliver_sm with hex
           based message id
        """
        msgid_dec = random.randint(1, 9999999999)
        msgid_hex = '%x' % msgid_dec

        yield self.connect('127.0.0.1', self.pbPort)
        yield self.prepareRoutingsAndStartConnector(dlr_msg_id_bases=2)

        # Bind
        yield self.smppc_factory.connectAndBind()

        # Install mocks
        self.smpps_factory.lastProto.sendPDU = mock.Mock(wraps=self.smpps_factory.lastProto.sendPDU)

        # Tell the SMSCPort to response with the defined MsgId
        self.SMSCPort.factory.lastClient.nextResponseMsgId = msgid_dec

        # Send a SMS MT through smpps interface
        SubmitSmPDU = copy.deepcopy(self.SubmitSmPDU)
        SubmitSmPDU.params['registered_delivery'] = RegisteredDelivery(
            RegisteredDeliveryReceipt.SMSC_DELIVERY_RECEIPT_REQUESTED)
        yield self.smppc_factory.lastProto.sendDataRequest(SubmitSmPDU)

        # Wait 3 seconds for submit_sm_resp
        yield waitFor(3)

        # Trigger receipt with different base
        yield self.SMSCPort.factory.lastClient.trigger_DLR(_id=msgid_hex)

        # Wait some time before testing
        yield waitFor(1)

        # Run tests
        self.assertEqual(self.smpps_factory.lastProto.sendPDU.call_count, 2)
        response_pdu_1 = self.smpps_factory.lastProto.sendPDU.call_args_list[0][0][0]
        # smpps response #2 was a deliver_sm: dlr mapping were done correctly !
        response_pdu_2 = self.smpps_factory.lastProto.sendPDU.call_args_list[1][0][0]
        self.assertEqual(response_pdu_2.id, pdu_types.CommandId.deliver_sm)
        self.assertEqual(response_pdu_2.params['receipted_message_id'], response_pdu_1.params['message_id'])

        # Unbind & Disconnect
        yield self.smppc_factory.smpp.unbindAndDisconnect()
        yield self.stopSmppClientConnectors()

        # Run tests
        # smpps last response was a unbind_resp
        last_pdu = \
            self.smpps_factory.lastProto.sendPDU.call_args_list[self.smpps_factory.lastProto.sendPDU.call_count - 1][0][
                0]
        self.assertEqual(last_pdu.id, pdu_types.CommandId.unbind_resp)

    @defer.inlineCallbacks
    def test_msg_id_hex_then_dec(self):
        """Will:
        1. Set a SMS-MT route to connector A
        2. Send a SMS-MT to that route from a SMPPc and get a message id as hex
        3. Wait for the DLR (deliver_sm) to be routed back to SMPPc through SMPPs as a deliver_sm with int
           based message id
        """
        msgid_dec = random.randint(1, 9999999999)
        msgid_hex = '%x' % msgid_dec

        yield self.connect('127.0.0.1', self.pbPort)
        yield self.prepareRoutingsAndStartConnector(dlr_msg_id_bases=1)

        # Bind
        yield self.smppc_factory.connectAndBind()

        # Install mocks
        self.smpps_factory.lastProto.sendPDU = mock.Mock(wraps=self.smpps_factory.lastProto.sendPDU)

        # Tell the SMSCPort to response with the defined MsgId
        self.SMSCPort.factory.lastClient.nextResponseMsgId = msgid_hex

        # Send a SMS MT through smpps interface
        SubmitSmPDU = copy.deepcopy(self.SubmitSmPDU)
        SubmitSmPDU.params['registered_delivery'] = RegisteredDelivery(
            RegisteredDeliveryReceipt.SMSC_DELIVERY_RECEIPT_REQUESTED)
        yield self.smppc_factory.lastProto.sendDataRequest(SubmitSmPDU)

        # Wait 3 seconds for submit_sm_resp
        yield waitFor(3)

        # Trigger receipt with different base
        yield self.SMSCPort.factory.lastClient.trigger_DLR(_id=msgid_dec)

        # Wait some time before testing
        yield waitFor(1)

        # Run tests
        self.assertEqual(self.smpps_factory.lastProto.sendPDU.call_count, 2)
        response_pdu_1 = self.smpps_factory.lastProto.sendPDU.call_args_list[0][0][0]
        # smpps response #2 was a deliver_sm: dlr mapping were done correctly !
        response_pdu_2 = self.smpps_factory.lastProto.sendPDU.call_args_list[1][0][0]
        self.assertEqual(response_pdu_2.id, pdu_types.CommandId.deliver_sm)
        self.assertEqual(response_pdu_2.params['receipted_message_id'], response_pdu_1.params['message_id'])

        # Unbind & Disconnect
        yield self.smppc_factory.smpp.unbindAndDisconnect()
        yield self.stopSmppClientConnectors()

        # Run tests
        # smpps last response was a unbind_resp
        last_pdu = \
            self.smpps_factory.lastProto.sendPDU.call_args_list[self.smpps_factory.lastProto.sendPDU.call_count - 1][0][
                0]
        self.assertEqual(last_pdu.id, pdu_types.CommandId.unbind_resp)
