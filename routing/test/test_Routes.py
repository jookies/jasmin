# Copyright 2012 Fourat Zouari <fourat@gmail.com>
# See LICENSE for details.

import copy
from twisted.trial.unittest import TestCase
from jasmin.routing.Routes import *
from jasmin.routing.Filters import *
from jasmin.routing.Routables import RoutableSubmitSm, RoutableDeliverSm
from smpp.pdu.operations import SubmitSM, DeliverSM

class RouteTestCase(TestCase):
    
    def setUp(self):
        self.connector1 = Connector('abc')
        self.connector2 = Connector('def')
        self.group100 = Group(100)
        self.user1 = User(1, self.group100)
        self.user2 = User(2, self.group100)
        
        self.invalid_filter = [ConnectorFilter(self.connector1), UserFilter(self.user1)]
        self.simple_filter_mo = [ConnectorFilter(self.connector1)]
        self.simple_filter_mt = [UserFilter(self.user1)]
    
class AnyStaticRouteTestCase(RouteTestCase):
    def test_standard(self):
        StaticMTRoute(self.simple_filter_mt, self.connector2)
        StaticMORoute(self.simple_filter_mo, self.connector2)
        
    def test_parameters(self):
        self.assertRaises(InvalidRouteParameterError, StaticMTRoute, 'anything', self.connector2)
        self.assertRaises(InvalidRouteParameterError, StaticMTRoute, ['anything in a list'], self.connector2)
        self.assertRaises(InvalidRouteParameterError, StaticMTRoute, self.simple_filter_mt, 'anything')
        
    def test_filter_type_compatibility(self):
        self.assertRaises(InvalidRouteFilterError, StaticMTRoute, self.invalid_filter, self.connector2)
        self.assertRaises(InvalidRouteFilterError, StaticMORoute, self.simple_filter_mt, self.connector2)
        self.assertRaises(InvalidRouteFilterError, StaticMTRoute, self.simple_filter_mo, self.connector2)
        
class DefaultRouteTestCase(RouteTestCase):
    def setUp(self):
        RouteTestCase.setUp(self)

        self.PDU_dst_1 = SubmitSM(
            source_addr='20203060',
            destination_addr='1',
            short_message='hello world',
        )
        
        self.routable_user1 = RoutableSubmitSm(self.PDU_dst_1, self.user1)        

    def test_standard(self):
        o = DefaultRoute(self.connector2)

        t = o.matchFilters(self.routable_user1)
        self.assertNotEqual(t, None)
        self.assertEqual(t, self.connector2)

class StaticMTRouteTestCase(RouteTestCase):
    
    def setUp(self):
        RouteTestCase.setUp(self)

        self.PDU_dst_1 = SubmitSM(
            source_addr='20203060',
            destination_addr='1',
            short_message='hello world',
        )
        
        self.routable_user1 = RoutableSubmitSm(self.PDU_dst_1, self.user1)        
        self.routable_user2 = RoutableSubmitSm(self.PDU_dst_1, self.user2)        
    
    def test_standard(self):
        o = StaticMTRoute(self.simple_filter_mt, self.connector2)

        t = o.matchFilters(self.routable_user1)
        self.assertNotEqual(t, None)
        self.assertEqual(t, self.connector2)
        
        t = o.matchFilters(self.routable_user2)
        self.assertEqual(t, None)
        
class StaticMORouteTestCase(RouteTestCase):
    
    def setUp(self):
        RouteTestCase.setUp(self)

        self.PDU_dst_1 = DeliverSM(
            source_addr='20203060',
            destination_addr='1',
            short_message='hello world',
        )
        
        self.routable_connector1 = RoutableDeliverSm(self.PDU_dst_1, self.connector1)
        self.routable_connector2 = RoutableDeliverSm(self.PDU_dst_1, self.connector2)
    
    def test_standard(self):
        o = StaticMORoute(self.simple_filter_mo, self.connector2)
        
        t = o.matchFilters(self.routable_connector1)
        self.assertNotEqual(t, None)
        self.assertEqual(t, self.connector2)
        
        t = o.matchFilters(self.routable_connector2)
        self.assertEqual(t, None)
        
class RandomRoundrobinMTRouteTestCase(RouteTestCase):
    
    def setUp(self):
        RouteTestCase.setUp(self)

        self.PDU_dst_1 = SubmitSM(
            source_addr='20203060',
            destination_addr='1',
            short_message='hello world',
        )
        
        self.routable_user1 = RoutableSubmitSm(self.PDU_dst_1, self.user1)        
        self.routable_user2 = RoutableSubmitSm(self.PDU_dst_1, self.user2)
        self.connectors = [self.connector1, self.connector2]        
    
    def test_standard(self):
        o = RandomRoundrobinMTRoute(self.simple_filter_mt, self.connectors)

        t = o.matchFilters(self.routable_user1)
        self.assertNotEqual(t, None)
        self.assertIn(t, self.connectors)
        
        t = o.matchFilters(self.routable_user2)
        self.assertEqual(t, None)
        
    def test_accepts_connectors_list(self):
        RandomRoundrobinMTRoute(self.simple_filter_mt, self.connectors)
        self.assertRaises(InvalidRouteParameterError, RandomRoundrobinMTRoute, self.simple_filter_mt, self.connector1)
        self.assertRaises(InvalidRouteParameterError, RandomRoundrobinMTRoute, self.simple_filter_mt, [0, 1])
        
class RandomRoundrobinMORouteTestCase(RouteTestCase):
    
    def setUp(self):
        RouteTestCase.setUp(self)

        self.PDU_dst_1 = SubmitSM(
            source_addr='20203060',
            destination_addr='1',
            short_message='hello world',
        )
        
        self.routable_connector1 = RoutableDeliverSm(self.PDU_dst_1, self.connector1)
        self.routable_connector2 = RoutableDeliverSm(self.PDU_dst_1, self.connector2)
        self.connectors = [self.connector1, self.connector2]        
    
    def test_standard(self):
        o = RandomRoundrobinMORoute(self.simple_filter_mo, self.connectors)

        t = o.matchFilters(self.routable_connector1)
        self.assertNotEqual(t, None)
        self.assertIn(t, self.connectors)
        
        t = o.matchFilters(self.routable_connector2)
        self.assertEqual(t, None)
        
    def test_accepts_connectors_list(self):
        RandomRoundrobinMORoute(self.simple_filter_mo, self.connectors)
        self.assertRaises(InvalidRouteParameterError, RandomRoundrobinMORoute, self.simple_filter_mo, self.connector1)
        self.assertRaises(InvalidRouteParameterError, RandomRoundrobinMORoute, self.simple_filter_mo, [0, 1])
        
class FailoverMORouteTestCase(RouteTestCase):
    def test_standard(self):
        pass
    test_standard.skip = 'This route type is not implemented yet'
    
class FailoverMTRouteTestCase(RouteTestCase):
    def test_standard(self):
        pass
    test_standard.skip = 'This route type is not implemented yet'
    
class LeastCostMTRouteTestCase(RouteTestCase):
    def test_standard(self):
        pass
    test_standard.skip = 'This route type is not implemented yet'

class BestQualityMTRouteTestCase(RouteTestCase):
    def test_standard(self):
        pass
    test_standard.skip = 'This route type is not implemented yet'