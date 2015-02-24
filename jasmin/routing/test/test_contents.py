"""
Test cases for amqp contents
"""

import pickle
from twisted.trial.unittest import TestCase
from jasmin.routing.content import RoutedDeliverSmContent
from jasmin.routing.jasminApi import HttpConnector, SmppServerSystemIdConnector

class ContentTestCase(TestCase):
    body = 'TESTBODY'
    msgid = 'ANY MESSAGE ID'
    scid = 'abc'

class RoutedDeliverSmContentTestCase(ContentTestCase):
    def test_using_HttpConnector_dc(self):
        dc = HttpConnector('def', 'http://127.0.0.1')
        c = RoutedDeliverSmContent(self.body, self.msgid, self.scid, dc)
        
        self.assertEquals(pickle.loads(c.body), self.body)
        self.assertEquals(c['message-id'], self.msgid)
        self.assertEquals(c['headers']['src-connector-id'], self.scid)
        self.assertEquals(c['headers']['dst-connector-id'], dc.cid)
        
        dc = pickle.loads(c['headers']['dst-connector'])
        self.assertEquals(dc.cid, dc.cid)

    def test_using_SmppServerSystemIdConnector_dc(self):
        dc = SmppServerSystemIdConnector('ghi')
        c = RoutedDeliverSmContent(self.body, self.msgid, self.scid, dc)
        
        self.assertEquals(pickle.loads(c.body), self.body)
        self.assertEquals(c['message-id'], self.msgid)
        self.assertEquals(c['headers']['src-connector-id'], self.scid)
        self.assertEquals(c['headers']['dst-connector-id'], dc.cid)
        
        dc = pickle.loads(c['headers']['dst-connector'])
        self.assertEquals(dc.cid, dc.cid)
