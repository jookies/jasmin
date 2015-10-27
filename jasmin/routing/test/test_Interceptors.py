#pylint: disable=W0401,W0611

from twisted.trial.unittest import TestCase
from jasmin.routing.Interceptors import *
from jasmin.routing.Filters import *
from jasmin.routing.Routables import RoutableSubmitSm, RoutableDeliverSm
from jasmin.vendor.smpp.pdu.operations import SubmitSM, DeliverSM

class InterceptorTestCase(TestCase):

    def setUp(self):
        self.connector1 = Connector('abc')
        self.connector2 = Connector('def')
        self.script1 = InterceptorScript("print 'some code here'")
        self.group100 = Group(100)
        self.user1 = User(1, self.group100, 'username', 'password')
        self.user2 = User(2, self.group100, 'username', 'password')

        self.invalid_filter = [ConnectorFilter(self.connector1), UserFilter(self.user1)]
        self.simple_filter_mo = [ConnectorFilter(self.connector1)]
        self.simple_filter_mt = [UserFilter(self.user1)]
        self.simple_filter_all = [DestinationAddrFilter(r'.*')]

class InterceptorStrTestCase(InterceptorTestCase):
    def test_StaticMTInterceptor(self):
        s = StaticMTInterceptor(self.simple_filter_mt, self.script1)
        self.assertEqual(str(s), 'StaticMTInterceptor/<IS (pyCode=print \'some code here\' ..)>')

    def test_StaticMOInterceptor(self):
        s = StaticMOInterceptor(self.simple_filter_mo, self.script1)
        self.assertEqual(str(s), 'StaticMOInterceptor/<IS (pyCode=print \'some code here\' ..)>')

    def test_DefaultInterceptor(self):
        s = DefaultInterceptor(self.script1)
        self.assertEqual(str(s), 'DefaultInterceptor/<IS (pyCode=print \'some code here\' ..)>')

class AnyStaticInterceptorTestCase(InterceptorTestCase):
    def test_standard(self):
        StaticMTInterceptor(self.simple_filter_mt, self.script1)
        StaticMOInterceptor(self.simple_filter_mo, self.script1)

    def test_parameters(self):
        self.assertRaises(InvalidInterceptorParameterError,
            StaticMTInterceptor, 'anything', self.script1)
        self.assertRaises(InvalidInterceptorParameterError,
            StaticMTInterceptor, ['anything in a list'], self.script1)
        self.assertRaises(InvalidInterceptorParameterError,
            StaticMTInterceptor, self.simple_filter_mt, 'anything')

    def test_filter_type_compatibility(self):
        self.assertRaises(InvalidInterceptorFilterError,
            StaticMTInterceptor, self.invalid_filter, self.script1)
        self.assertRaises(InvalidInterceptorFilterError,
            StaticMOInterceptor, self.simple_filter_mt, self.script1)
        self.assertRaises(InvalidInterceptorFilterError,
            StaticMTInterceptor, self.simple_filter_mo, self.script1)

class DefaultInterceptorTestCase(InterceptorTestCase):
    def setUp(self):
        InterceptorTestCase.setUp(self)

        self.PDU_dst_1 = SubmitSM(
            source_addr='20203060',
            destination_addr='1',
            short_message='hello world',
        )

        self.routable_user1 = RoutableSubmitSm(self.PDU_dst_1, self.user1)

    def test_standard(self):
        o = DefaultInterceptor(self.script1)

        t = o.matchFilters(self.routable_user1)
        self.assertNotEqual(t, None)
        self.assertEqual(t, self.script1)

class StaticMTInterceptorTestCase(InterceptorTestCase):

    def setUp(self):
        InterceptorTestCase.setUp(self)

        self.PDU_dst_1 = SubmitSM(
            source_addr='20203060',
            destination_addr='1',
            short_message='hello world',
        )

        self.routable_user1 = RoutableSubmitSm(self.PDU_dst_1, self.user1)
        self.routable_user2 = RoutableSubmitSm(self.PDU_dst_1, self.user2)

    def test_standard(self):
        o = StaticMTInterceptor(self.simple_filter_mt, self.script1)

        t = o.matchFilters(self.routable_user1)
        self.assertNotEqual(t, None)
        self.assertEqual(t, self.script1)

        t = o.matchFilters(self.routable_user2)
        self.assertEqual(t, None)

class StaticMOInterceptorTestCase(InterceptorTestCase):

    def setUp(self):
        InterceptorTestCase.setUp(self)

        self.PDU_dst_1 = DeliverSM(
            source_addr='20203060',
            destination_addr='1',
            short_message='hello world',
        )

        self.routable_connector1 = RoutableDeliverSm(self.PDU_dst_1, self.connector1)
        self.routable_connector2 = RoutableDeliverSm(self.PDU_dst_1, self.connector2)

    def test_standard(self):
        o = StaticMOInterceptor(self.simple_filter_mo, self.script1)

        t = o.matchFilters(self.routable_connector1)
        self.assertNotEqual(t, None)
        self.assertEqual(t, self.script1)

        t = o.matchFilters(self.routable_connector2)
        self.assertEqual(t, None)
