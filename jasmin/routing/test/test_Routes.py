#pylint: disable-msg=W0401,W0611

import copy
from twisted.trial.unittest import TestCase
from jasmin.routing.Routes import *
from jasmin.routing.Filters import *
from jasmin.routing.Routables import RoutableSubmitSm, RoutableDeliverSm
from jasmin.vendor.smpp.pdu.operations import SubmitSM, DeliverSM

class RouteTestCase(TestCase):
    
    def setUp(self):
        self.connector1 = Connector('abc')
        self.connector2 = Connector('def')
        self.group100 = Group(100)
        self.user1 = User(1, self.group100, 'username', 'password')
        self.user2 = User(2, self.group100, 'username', 'password')
        
        self.invalid_filter = [ConnectorFilter(self.connector1), UserFilter(self.user1)]
        self.simple_filter_mo = [ConnectorFilter(self.connector1)]
        self.simple_filter_mt = [UserFilter(self.user1)]
        self.simple_filter_all = [DestinationAddrFilter(r'.*')]
        
class RouteStrTestCase(RouteTestCase):
    def test_StaticMTRoute(self):
        s = StaticMTRoute(self.simple_filter_mt, self.connector2, 0.0)
        self.assertEqual(str(s), 'StaticMTRoute to generic(def) NOT RATED')
    
    def test_StaticMORoute(self):
        s = StaticMORoute(self.simple_filter_mo, self.connector2)
        self.assertEqual(str(s), 'StaticMORoute to generic(def) NOT RATED')
    
    def test_DefaultRoute(self):
        s = DefaultRoute(self.connector2)
        self.assertEqual(str(s), 'DefaultRoute to generic(def) NOT RATED')
    
    def test_RandomRoundrobinMTRouteTestCase(self):
        s = RandomRoundrobinMTRoute(self.simple_filter_mt, [self.connector1, self.connector2], 0.0)
        self.assertEqual(str(s), 'RandomRoundrobinMTRoute to 2 connectors:\n\t- generic(abc)\n\t- generic(def) \nNOT RATED')

    def test_RandomRoundrobinMORouteTestCase(self):
        s = RandomRoundrobinMORoute(self.simple_filter_mo, [self.connector1, self.connector2])
        self.assertEqual(str(s), 'RandomRoundrobinMORoute to 2 connectors:\n\t- generic(abc)\n\t- generic(def)')
    
    def test_FailoverMTRouteTestCase(self):
        pass
    test_FailoverMTRouteTestCase.skip = 'This route type is not implemented yet'
    
    def test_FailoverMORouteTestCase(self):
        pass
    test_FailoverMORouteTestCase.skip = 'This route type is not implemented yet'
    
    def test_BestQualityMTRouteTestCase(self):
        pass
    test_BestQualityMTRouteTestCase.skip = 'This route type is not implemented yet'
    
class AnyStaticRouteTestCase(RouteTestCase):
    def test_standard(self):
        StaticMTRoute(self.simple_filter_mt, self.connector2, 0.0)
        StaticMORoute(self.simple_filter_mo, self.connector2)
        
    def test_parameters(self):
        self.assertRaises(InvalidRouteParameterError, StaticMTRoute, 'anything', self.connector2, 0.0)
        self.assertRaises(InvalidRouteParameterError, StaticMTRoute, ['anything in a list'], self.connector2, 0.0)
        self.assertRaises(InvalidRouteParameterError, StaticMTRoute, self.simple_filter_mt, 'anything', 0.0)
        
    def test_filter_type_compatibility(self):
        self.assertRaises(InvalidRouteFilterError, StaticMTRoute, self.invalid_filter, self.connector2, 0.0)
        self.assertRaises(InvalidRouteFilterError, StaticMORoute, self.simple_filter_mt, self.connector2, 0.0)
        self.assertRaises(InvalidRouteFilterError, StaticMTRoute, self.simple_filter_mo, self.connector2, 0.0)
        
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
        o = StaticMTRoute(self.simple_filter_mt, self.connector2, 0.0)

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
        o = RandomRoundrobinMTRoute(self.simple_filter_mt, self.connectors, 0.0)

        t = o.matchFilters(self.routable_user1)
        self.assertNotEqual(t, None)
        self.assertIn(t, self.connectors)
        
        t = o.matchFilters(self.routable_user2)
        self.assertEqual(t, None)
        
    def test_accepts_connectors_list(self):
        RandomRoundrobinMTRoute(self.simple_filter_mt, self.connectors, 0.0)
        self.assertRaises(InvalidRouteParameterError, RandomRoundrobinMTRoute, self.simple_filter_mt, self.connector1, 0.0)
        self.assertRaises(InvalidRouteParameterError, RandomRoundrobinMTRoute, self.simple_filter_mt, [0, 1], 0.0)
        
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

class RatedMTRoutesTestCase(RouteTestCase):
    def test_DefaultRoute(self):
        # DefaultRoute's rate parameter is optional
        dr = DefaultRoute(self.connector1)
        self.assertEqual(dr.getRate(), 0)
        self.assertEqual(str(dr), 'DefaultRoute to generic(abc) NOT RATED')
        
        dr = DefaultRoute(self.connector1, 2.3)
        self.assertEqual(dr.getRate(), 2.3)
        self.assertEqual(str(dr), 'DefaultRoute to generic(abc) rated 2.30')

        # Rate must be float, not integer !
        self.assertRaises(InvalidRouteParameterError, DefaultRoute, self.connector1, 0)
        # Rate must not be negative
        self.assertRaises(InvalidRouteParameterError, DefaultRoute, self.connector1, -2)
    
    def test_StaticMORoute(self):
        # StaticMORoute's rate must be ignored
        sr = StaticMORoute(self.simple_filter_mo, self.connector1)
        self.assertEqual(sr.getRate(), 0)
        self.assertEqual(str(sr), 'StaticMORoute to generic(abc) NOT RATED')
        sr = StaticMORoute(self.simple_filter_mo, self.connector1, 2.3)
        self.assertEqual(str(sr), 'StaticMORoute to generic(abc) NOT RATED')
        self.assertEqual(sr.getRate(), 0)

    def test_StaticMTRoute(self):
        sr = StaticMTRoute(self.simple_filter_mt, self.connector1, 0.0)
        self.assertEqual(sr.getRate(), 0)
        self.assertEqual(str(sr), 'StaticMTRoute to generic(abc) NOT RATED')
        sr = StaticMTRoute(self.simple_filter_mt, self.connector1, 2.3)
        self.assertEqual(sr.getRate(), 2.3)
        self.assertEqual(str(sr), 'StaticMTRoute to generic(abc) rated 2.30')

        # Adding rate parameter will lead to:
        # exceptions.TypeError: __init__() takes exactly 4 arguments (3 given)
        self.assertRaises(TypeError, StaticMTRoute, self.simple_filter_mt, self.connector1)
        # Rate must be float, not integer !
        self.assertRaises(InvalidRouteParameterError, StaticMTRoute, self.simple_filter_mt, self.connector1, 0)
        # Rate must not be negative
        self.assertRaises(InvalidRouteParameterError, StaticMTRoute, self.simple_filter_mt, self.connector1, -2)

    def test_RandomRoundrobinMORoute(self):
        # RandomRoundrobinMORoute is not rated
        rrr = RandomRoundrobinMORoute(self.simple_filter_mo, [self.connector1, self.connector2])
        self.assertEqual(rrr.getRate(), 0)
        self.assertEqual(str(rrr), 'RandomRoundrobinMORoute to 2 connectors:\n\t- generic(abc)\n\t- generic(def)')
        # Adding rate parameter will lead to:
        # exceptions.TypeError: __init__() takes exactly 3 arguments (4 given)
        self.assertRaises(TypeError, RandomRoundrobinMORoute, self.simple_filter_mo, [self.connector1, self.connector2], 2.3)

    def test_RandomRoundrobinMTRoute(self):
        rrr = RandomRoundrobinMTRoute(self.simple_filter_mt, [self.connector1, self.connector2], 0.0)
        self.assertEqual(rrr.getRate(), 0)
        self.assertEqual(str(rrr), 'RandomRoundrobinMTRoute to 2 connectors:\n\t- generic(abc)\n\t- generic(def) \nNOT RATED')
        rrr = RandomRoundrobinMTRoute(self.simple_filter_mt, [self.connector1, self.connector2], 5.6)
        self.assertEqual(rrr.getRate(), 5.6)
        self.assertEqual(str(rrr), 'RandomRoundrobinMTRoute to 2 connectors:\n\t- generic(abc)\n\t- generic(def) \nrated 5.60')

        # Adding rate parameter will lead to:
        # exceptions.TypeError: __init__() takes exactly 4 arguments (3 given)
        self.assertRaises(TypeError, RandomRoundrobinMTRoute, self.simple_filter_mt, [self.connector1, self.connector2])
        # Rate must be float, not integer !
        self.assertRaises(InvalidRouteParameterError, RandomRoundrobinMTRoute, self.simple_filter_mt, [self.connector1, self.connector2], 0)
        # Rate must not be negative
        self.assertRaises(InvalidRouteParameterError, RandomRoundrobinMTRoute, self.simple_filter_mt, [self.connector1, self.connector2], -2)

class getBillForTestCase(RouteTestCase):
    test_routes = ['DefaultRoute', 'StaticMORoute', 'StaticMTRoute', 'RandomRoundrobinMORoute',
                   'RandomRoundrobinMTRoute']
    
    def test_all_routes(self):
        for route_class in self.test_routes:
            if globals()[route_class].type not in ['default', 'mt']:
                continue
            
            # Init user
            user1 = copy.copy(self.user1)
            
            # unrated route intialization
            if globals()[route_class].type == 'default':
                r = globals()[route_class](self.connector1, 0.0)
            elif globals()[route_class].type == 'mt' and route_class[:6] != 'Random':
                r = globals()[route_class](self.simple_filter_all, self.connector1, 0.0)
            else:
                r = globals()[route_class](self.simple_filter_all, [self.connector1, self.connector2], 0.0)
            
            # User instance assertion
            self.assertRaises(InvalidRouteParameterError, r.getBillFor, 'user')
            # Default amounts for default user (having unlimited
            self.assertEqual(r.getBillFor(user1).getTotalAmounts(), 0)
            
            # Set user balance
            user1.mt_credential.setQuota('balance', 10)
            user1.mt_credential.setQuota('early_decrement_balance_percent', None)
            user1.mt_credential.setQuota('submit_sm_count', 10)
            # make asserts
            self.assertEqual(r.getBillFor(user1).getTotalAmounts(), 0)

            # rated route intialization
            if globals()[route_class].type == 'default':
                r = globals()[route_class](self.connector1, 2.0)
            elif globals()[route_class].type == 'mt' and route_class[:6] != 'Random':
                r = globals()[route_class](self.simple_filter_all, self.connector1, 2.0)
            else:
                r = globals()[route_class](self.simple_filter_all, [self.connector1, self.connector2], 2.0)
            # make asserts
            bill = r.getBillFor(user1)
            self.assertEqual(bill.getTotalAmounts(), 2.0)
            self.assertEqual(bill.getAmount('submit_sm'), 2.0)
            self.assertEqual(bill.getAction('decrement_submit_sm_count'), 1)
            
            # Set early decrementing balance
            user1.mt_credential.setQuota('early_decrement_balance_percent', 50)
            bill = r.getBillFor(user1)
            self.assertEqual(bill.getTotalAmounts(), 2.0)
            self.assertEqual(bill.getAmount('submit_sm'), 1.0)
            self.assertEqual(bill.getAmount('submit_sm_resp'), 1.0)
            self.assertEqual(bill.getAction('decrement_submit_sm_count'), 1)
            
class FailoverMORouteTestCase(RouteTestCase):
    def test_standard(self):
        pass
    test_standard.skip = 'This route type is not implemented yet'
    
class FailoverMTRouteTestCase(RouteTestCase):
    def test_standard(self):
        pass
    test_standard.skip = 'This route type is not implemented yet'
    
class BestQualityMTRouteTestCase(RouteTestCase):
    def test_standard(self):
        pass
    test_standard.skip = 'This route type is not implemented yet'
