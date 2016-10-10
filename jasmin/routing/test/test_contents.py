"""
Test cases for amqp contents
"""

import cPickle as pickle
from twisted.trial.unittest import TestCase
from jasmin.routing.content import RoutedDeliverSmContent
from jasmin.routing.jasminApi import HttpConnector, SmppServerSystemIdConnector

class ContentTestCase(TestCase):
    body = 'TESTBODY'
    msgid = 'ANY MESSAGE ID'
    scid = 'abc'

class RoutedDeliverSmContentTestCase(ContentTestCase):
    def test_using_HttpConnector_dc(self):
        dcs = [HttpConnector('def', 'http://127.0.0.1')]
        c = RoutedDeliverSmContent(self.body, self.msgid, self.scid, dcs)

        self.assertEquals(pickle.loads(c.body), self.body)
        self.assertEquals(c['message-id'], self.msgid)
        self.assertEquals(c['headers']['src-connector-id'], self.scid)
        self.assertEquals(c['headers']['route-type'], 'simple')

        _dcs = pickle.loads(c['headers']['dst-connectors'])
        self.assertEquals(_dcs[0].cid, dcs[0].cid)

    def test_using_SmppServerSystemIdConnector_dc(self):
        dcs = [SmppServerSystemIdConnector('ghi')]
        c = RoutedDeliverSmContent(self.body, self.msgid, self.scid, dcs)

        self.assertEquals(pickle.loads(c.body), self.body)
        self.assertEquals(c['message-id'], self.msgid)
        self.assertEquals(c['headers']['src-connector-id'], self.scid)
        self.assertEquals(c['headers']['route-type'], 'simple')

        _dcs = pickle.loads(c['headers']['dst-connectors'])
        self.assertEquals(_dcs[0].cid, dcs[0].cid)

    def test_using_failover_route_type(self):
        dcs = [SmppServerSystemIdConnector('ghi'),SmppServerSystemIdConnector('ijk')]
        c = RoutedDeliverSmContent(self.body, self.msgid, self.scid, dcs, 'failover')

        self.assertEquals(pickle.loads(c.body), self.body)
        self.assertEquals(c['message-id'], self.msgid)
        self.assertEquals(c['headers']['src-connector-id'], self.scid)
        self.assertEquals(c['headers']['route-type'], 'failover')

        _dcs = pickle.loads(c['headers']['dst-connectors'])
        self.assertEquals(_dcs[0].cid, dcs[0].cid)
        self.assertEquals(len(_dcs), len(dcs))