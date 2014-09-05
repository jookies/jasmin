from datetime import datetime
from twisted.trial.unittest import TestCase
from jasmin.routing.Routables import SimpleRoutablePDU, RoutableSubmitSm, RoutableDeliverSm, InvalidRoutableParameterError
from jasmin.vendor.smpp.pdu.operations import SubmitSM
from jasmin.routing.jasminApi import *

class RoutablePDUTestCase(TestCase):
    
    def setUp(self):
        self.PDU = SubmitSM(
            source_addr='20203060',
            destination_addr='20203060',
            short_message='hello world',
        )
        self.connector = Connector('abc')
        self.user = User(1, Group(100), 'username', 'password')
    
class SimpleRoutablePDUTestCase(RoutablePDUTestCase):
    
    def test_standard(self):
        o = SimpleRoutablePDU(self.connector, self.PDU, self.user, datetime.now())
        
        self.assertEqual(o.pdu, self.PDU)
        self.assertEqual(o.connector.cid, self.connector.cid)
        self.assertEqual(o.user.uid, self.user.uid)
        self.assertEqual(o.user.group.gid, self.user.group.gid)
        self.assertNotEqual(o.datetime, None)
        
    def test_without_datetime(self):
        o = SimpleRoutablePDU(self.connector, self.PDU, self.user)
        self.assertNotEqual(o.datetime, None)
        
    def test_invalid_parameter(self):
        self.assertRaises(InvalidRoutableParameterError, SimpleRoutablePDU, self.connector, object, self.user)
        self.assertRaises(InvalidRoutableParameterError, SimpleRoutablePDU, object, self.PDU, self.user)
        self.assertRaises(InvalidRoutableParameterError, SimpleRoutablePDU, self.connector, self.PDU, object)
        
class RoutableSubmitSmTestCase(RoutablePDUTestCase):
    
    def test_standard(self):
        o = RoutableSubmitSm(self.PDU, self.user, datetime.now())
        
        self.assertEqual(o.pdu, self.PDU)
        self.assertEqual(o.user.uid, self.user.uid)
        self.assertEqual(o.user.group.gid, self.user.group.gid)
        self.assertNotEqual(o.datetime, None)
        
    def test_without_datetime(self):
        o = RoutableSubmitSm(self.PDU, self.user)
        self.assertNotEqual(o.datetime, None)
        
    def test_invalid_parameter(self):
        self.assertRaises(InvalidRoutableParameterError, RoutableSubmitSm, object, self.user)
        self.assertRaises(InvalidRoutableParameterError, RoutableSubmitSm, self.PDU, object)
        
class RoutableDeliverSmTestCase(RoutablePDUTestCase):
    
    def test_standard(self):
        o = RoutableDeliverSm(self.PDU, self.connector, datetime.now())
        
        self.assertEqual(o.pdu, self.PDU)
        self.assertEqual(o.connector.cid, self.connector.cid)
        self.assertNotEqual(o.datetime, None)
        
    def test_without_datetime(self):
        o = RoutableSubmitSm(self.PDU, self.user)
        self.assertNotEqual(o.datetime, None)
        
    def test_invalid_parameter(self):
        self.assertRaises(InvalidRoutableParameterError, RoutableDeliverSm, object, self.connector)
        self.assertRaises(InvalidRoutableParameterError, RoutableDeliverSm, self.PDU, object)
