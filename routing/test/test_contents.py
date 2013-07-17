# Copyright 2012 Fourat Zouari <fourat@gmail.com>
# See LICENSE for details.

"""
Test cases for amqp contents
"""

import pickle
from twisted.trial.unittest import TestCase
from jasmin.routing.content import RoutedDeliverSmContent
from jasmin.routing.jasminApi import HttpConnector

class ContentTestCase(TestCase):
    body = 'TESTBODY'
    msgid = 'ANY MESSAGE ID'
    scid = 'abc'
    dc = HttpConnector('def', 'http://127.0.0.1')

class RoutedDeliverSmContentTestCase(ContentTestCase):
    def test_normal(self):
        c = RoutedDeliverSmContent(self.body, self.msgid, self.scid, self.dc)
        
        self.assertEquals(pickle.loads(c.body), self.body)
        self.assertEquals(c['message-id'], self.msgid)
        self.assertEquals(c['headers']['src-connector-id'], self.scid)
        self.assertEquals(c['headers']['dst-connector-id'], self.dc.cid)
        
        dc = pickle.loads(c['headers']['dst-connector'])
        self.assertEquals(dc.cid, self.dc.cid)