from datetime import datetime

from twisted.trial.unittest import TestCase

from jasmin.routing.Routables import (SimpleRoutablePDU, RoutableSubmitSm,
                                      RoutableDeliverSm, InvalidRoutableParameterError,
                                      InvalidTagError, TagNotFoundError,
                                      InvalidLockError)
from jasmin.routing.jasminApi import *
from jasmin.vendor.smpp.pdu.operations import SubmitSM


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

    def test_tagging(self):
        o = SimpleRoutablePDU(self.connector, self.PDU, self.user, datetime.now())

        _any_object = object()
        self.assertRaises(InvalidTagError, o.addTag, _any_object)
        self.assertRaises(InvalidTagError, o.hasTag, _any_object)
        self.assertRaises(InvalidTagError, o.removeTag, _any_object)

        # Integer tags
        o.addTag(23)
        self.assertTrue(o.hasTag(23))
        self.assertFalse(o.hasTag(30))
        self.assertRaises(TagNotFoundError, o.removeTag, 30)
        self.assertEqual([23], o.getTags())
        o.flushTags()
        self.assertEqual([], o.getTags())

        # String tags
        o.addTag('23')
        self.assertTrue(o.hasTag('23'))
        self.assertFalse(o.hasTag('30'))
        self.assertRaises(TagNotFoundError, o.removeTag, '30')
        self.assertEqual(['23'], o.getTags())
        o.flushTags()
        self.assertEqual([], o.getTags())

        # Mixed tags
        o.addTag('23')
        o.addTag(23)
        self.assertEqual(['23', 23], o.getTags())
        o.flushTags()

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

    def test_tagging(self):
        o = RoutableSubmitSm(self.PDU, self.user, datetime.now())

        _any_object = object()
        self.assertRaises(InvalidTagError, o.addTag, _any_object)
        self.assertRaises(InvalidTagError, o.hasTag, _any_object)
        self.assertRaises(InvalidTagError, o.removeTag, _any_object)

        # Integer tags
        o.addTag(23)
        self.assertTrue(o.hasTag(23))
        self.assertFalse(o.hasTag(30))
        self.assertRaises(TagNotFoundError, o.removeTag, 30)
        self.assertEqual([23], o.getTags())
        o.flushTags()
        self.assertEqual([], o.getTags())

        # String tags
        o.addTag('23')
        self.assertTrue(o.hasTag('23'))
        self.assertFalse(o.hasTag('30'))
        self.assertRaises(TagNotFoundError, o.removeTag, '30')
        self.assertEqual(['23'], o.getTags())
        o.flushTags()
        self.assertEqual([], o.getTags())

        # Mixed tags
        o.addTag('23')
        o.addTag(23)
        self.assertEqual(['23', 23], o.getTags())
        o.flushTags()

    def test_locking(self):
        o = RoutableSubmitSm(self.PDU, self.user, datetime.now())

        self.assertRaises(InvalidLockError, o.lockPduParam, 'anything')
        self.assertRaises(InvalidLockError, o.pduParamIsLocked, 'anything')

        o.lockPduParam('service_type')
        self.assertTrue(o.pduParamIsLocked('service_type'))
        self.assertFalse(o.pduParamIsLocked('source_addr_ton'))

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

    def test_tagging(self):
        o = RoutableDeliverSm(self.PDU, self.connector, datetime.now())

        _any_object = object()
        self.assertRaises(InvalidTagError, o.addTag, _any_object)
        self.assertRaises(InvalidTagError, o.hasTag, _any_object)
        self.assertRaises(InvalidTagError, o.removeTag, _any_object)

        # Integer tags
        o.addTag(23)
        self.assertTrue(o.hasTag(23))
        self.assertFalse(o.hasTag(30))
        self.assertRaises(TagNotFoundError, o.removeTag, 30)
        self.assertEqual([23], o.getTags())
        o.flushTags()
        self.assertEqual([], o.getTags())

        # String tags
        o.addTag('23')
        self.assertTrue(o.hasTag('23'))
        self.assertFalse(o.hasTag('30'))
        self.assertRaises(TagNotFoundError, o.removeTag, '30')
        self.assertEqual(['23'], o.getTags())
        o.flushTags()
        self.assertEqual([], o.getTags())

        # Mixed tags
        o.addTag('23')
        o.addTag(23)
        self.assertEqual(['23', 23], o.getTags())
        o.flushTags()