#pylint: disable=W0401,W0611

from twisted.trial.unittest import TestCase
from jasmin.routing.Bills import SubmitSmBill, SubmitSmRespBill, InvalidBillKeyError, InvalidBillValueError
from jasmin.routing.jasminApi import User, Group

class BillsTestCase(TestCase):

    def setUp(self):
        self.group = Group(1)
        self.user = User(1, self.group, 'foo', 'bar')

class SubmitSmBillTestCase(BillsTestCase):
    def test_default(self):
        b = SubmitSmBill(self.user)

        self.assertEqual(b.getAmount('submit_sm'), 0.0)
        self.assertEqual(b.getAmount('submit_sm_resp'), 0.0)
        self.assertEqual(b.getAction('decrement_submit_sm_count'), 0)
        self.assertNotEquals(b.bid, None)
        self.assertEqual(b.user, self.user)

    def test_typing(self):
        b = SubmitSmBill(self.user)

        self.assertRaises(InvalidBillKeyError, b.getAmount, 'anyKey')
        self.assertRaises(InvalidBillKeyError, b.getAction, 'anyKey')
        self.assertRaises(InvalidBillKeyError, b.setAmount, 'anyKey', 0)
        self.assertRaises(InvalidBillKeyError, b.setAction, 'anyKey', 0)
        self.assertRaises(InvalidBillValueError, b.setAction, 'decrement_submit_sm_count', 1.1)
        self.assertRaises(InvalidBillValueError, b.setAmount, 'submit_sm', '1.1')
        self.assertRaises(InvalidBillValueError, b.setAmount, 'submit_sm_resp', 'A')

    def test_amounts(self):
        b = SubmitSmBill(self.user)

        b.setAmount('submit_sm', 1.1)
        b.setAmount('submit_sm_resp', 2)
        self.assertEqual(b.getAmount('submit_sm'), 1.1)
        self.assertEqual(b.getAmount('submit_sm_resp'), 2)
        self.assertEqual(b.getTotalAmounts(), 3.1)

    def test_getSubmitSmRespBill(self):
        b = SubmitSmBill(self.user)

        b.setAmount('submit_sm', 1.1)
        b.setAmount('submit_sm_resp', 2)
        c = b.getSubmitSmRespBill()

        self.assertRaises(InvalidBillKeyError, c.getAmount, 'submit_sm')
        self.assertEqual(c.getAmount('submit_sm_resp'), 2.0)
        self.assertRaises(InvalidBillKeyError, c.getAction, 'decrement_submit_sm_count')
        self.assertNotEquals(c.bid, None)
        self.assertNotEquals(b.bid, c.bid)
        self.assertEqual(b.user, c.user)

class SubmitSmRespBillTestCase(BillsTestCase):
    def test_default(self):
        b = SubmitSmRespBill(self.user)

        self.assertEqual(b.getAmount('submit_sm_resp'), 0.0)
        self.assertNotEquals(b.bid, None)
        self.assertEqual(b.user, self.user)

    def test_typing(self):
        b = SubmitSmRespBill(self.user)

        self.assertRaises(InvalidBillKeyError, b.getAmount, 'anyKey')
        self.assertRaises(InvalidBillKeyError, b.getAction, 'anyKey')
        self.assertRaises(InvalidBillKeyError, b.setAmount, 'anyKey', 0)
        self.assertRaises(InvalidBillKeyError, b.setAction, 'anyKey', 0)
        self.assertRaises(InvalidBillValueError, b.setAmount, 'submit_sm_resp', 'A')

    def test_amounts(self):
        b = SubmitSmRespBill(self.user)

        b.setAmount('submit_sm_resp', 2)
        self.assertEqual(b.getAmount('submit_sm_resp'), 2)
        self.assertEqual(b.getTotalAmounts(), 2)
