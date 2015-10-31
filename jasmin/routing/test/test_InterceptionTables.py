#pylint: disable=W0401,W0611

from twisted.trial.unittest import TestCase
from jasmin.routing.InterceptionTables import *
from jasmin.routing.Interceptors import *
from jasmin.routing.Filters import *
from jasmin.vendor.smpp.pdu.operations import SubmitSM, DeliverSM
from jasmin.routing.Routables import RoutableSubmitSm, RoutableDeliverSm

class InterceptionTableTests(object):

    def test_standard(self):
        interception_t = self._interceptionTable()
        interception_t.add(self.interceptor2, 2)
        interception_t.add(self.interceptor1, 1)
        interception_t.add(self.interceptor4, 0)

        i = interception_t.getInterceptorFor(self.routable_matching_interceptor1)
        self.assertEqual(i.getScript(), self.script1)
        i = interception_t.getInterceptorFor(self.routable_matching_interceptor2)
        self.assertEqual(i.getScript(), self.script2)
        i = interception_t.getInterceptorFor(self.routable_notmatching_any)
        self.assertEqual(i.getScript(), self.script4)

    def test_interception_table_order(self):
        interception_t = self._interceptionTable()
        interception_t.add(self.interceptor4, 0)
        interception_t.add(self.interceptor2, 2)
        interception_t.add(self.interceptor1, 1)
        interception_t.add(self.interceptor3, 3)

        self.assertEqual(interception_t.getAll()[0].keys()[0], 3)
        self.assertEqual(interception_t.getAll()[1].keys()[0], 2)
        self.assertEqual(interception_t.getAll()[2].keys()[0], 1)
        self.assertEqual(interception_t.getAll()[3].keys()[0], 0)

    def test_interception_table_replace_interceptor(self):
        interception_t = self._interceptionTable()
        interception_t.add(self.interceptor4, 0)
        interception_t.add(self.interceptor2, 2)
        interception_t.add(self.interceptor3, 2)

        self.assertEqual(interception_t.getAll()[0].values()[0], self.interceptor3)

    def test_remove_interceptor(self):
        interception_t = self._interceptionTable()
        interception_t.add(self.interceptor2, 2)
        interception_t.add(self.interceptor1, 1)
        interception_t.add(self.interceptor4, 0)
        self.assertEqual(len(interception_t.getAll()), 3)

        # Remove non existent interceptor
        r = interception_t.remove(3)
        self.assertFalse(r)

        # List after removing one interceptor
        interception_t.remove(1)
        self.assertEqual(len(interception_t.getAll()), 2)

    def test_default_interceptor(self):
        interception_t = self._interceptionTable()
        self.assertRaises(InvalidInterceptionTableParameterError, interception_t.add, self.interceptor3, 0)

    def test_flush(self):
        interception_t = self._interceptionTable()
        interception_t.add(self.interceptor4, 0)
        interception_t.add(self.interceptor3, 2)

        allInterceptors = interception_t.getAll()
        self.assertEqual(len(allInterceptors), 2)

        interception_t.flush()
        allInterceptors = interception_t.getAll()
        self.assertEqual(len(allInterceptors), 0)

        interception_t.add(self.interceptor4, 0)
        allInterceptors = interception_t.getAll()
        self.assertEqual(len(allInterceptors), 1)

class MTInterceptionTableTestCase(InterceptionTableTests, TestCase):
    _interceptionTable = MTInterceptionTable

    def setUp(self):
        self.script1 = MTInterceptorScript('abc')
        self.script2 = MTInterceptorScript('def')
        self.script3 = MTInterceptorScript('ghi')
        self.script4 = MTInterceptorScript('jkl')
        self.group100 = Group(100)
        self.user1 = User(1, self.group100, 'username', 'password')
        self.user2 = User(2, self.group100, 'username', 'password')

        self.mt_filter1 = [UserFilter(self.user1)]
        self.mt_filter2 = [DestinationAddrFilter('^10\d+')]
        self.transparent_filter = [TransparentFilter()]
        self.interceptor1 = StaticMTInterceptor(self.mt_filter1, self.script1)
        self.interceptor2 = StaticMTInterceptor(self.mt_filter2, self.script2)
        self.interceptor3 = StaticMTInterceptor(self.transparent_filter, self.script3)
        self.interceptor4 = DefaultInterceptor(self.script4)

        self.PDU_dst_1 = SubmitSM(
            source_addr='x',
            destination_addr='200',
            short_message='hello world',
        )
        self.PDU_dst_2 = SubmitSM(
            source_addr='x',
            destination_addr='100',
            short_message='hello world',
        )

        self.routable_matching_interceptor1 = RoutableSubmitSm(self.PDU_dst_1, self.user1)
        self.routable_matching_interceptor2 = RoutableSubmitSm(self.PDU_dst_2, self.user2)
        self.routable_notmatching_any = RoutableSubmitSm(self.PDU_dst_1, self.user2)

class MOInterceptionTableTestCase(InterceptionTableTests, TestCase):
    _interceptionTable = MOInterceptionTable

    def setUp(self):
        self.connector = SmppServerSystemIdConnector('jkl')
        self.script1 = MOInterceptorScript('abc')
        self.script2 = MOInterceptorScript('def')
        self.script3 = MOInterceptorScript('ghi')
        self.script4 = MOInterceptorScript('jkl')

        self.mt_filter1 = [SourceAddrFilter('^10\d+')]
        self.mt_filter2 = [DestinationAddrFilter('^90\d+')]
        self.transparent_filter = [TransparentFilter()]
        self.interceptor1 = StaticMOInterceptor(self.mt_filter1, self.script1)
        self.interceptor2 = StaticMOInterceptor(self.mt_filter2, self.script2)
        self.interceptor3 = StaticMOInterceptor(self.transparent_filter, self.script3)
        self.interceptor4 = DefaultInterceptor(self.script4)

        self.PDU_dst_1 = DeliverSM(
            source_addr='100',
            destination_addr='200',
            short_message='hello world',
        )
        self.PDU_dst_2 = DeliverSM(
            source_addr='x',
            destination_addr='900',
            short_message='hello world',
        )
        self.PDU_dst_3 = DeliverSM(
            source_addr='x',
            destination_addr='y',
            short_message='hello world',
        )

        self.routable_matching_interceptor1 = RoutableDeliverSm(self.PDU_dst_1, self.connector)
        self.routable_matching_interceptor2 = RoutableDeliverSm(self.PDU_dst_2, self.connector)
        self.routable_notmatching_any = RoutableDeliverSm(self.PDU_dst_3, self.connector)
