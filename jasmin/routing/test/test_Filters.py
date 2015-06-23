import pickle
from twisted.trial.unittest import TestCase
from jasmin.routing.Routables import SimpleRoutablePDU
from jasmin.routing.jasminApi import *
from jasmin.vendor.smpp.pdu.operations import SubmitSM
from jasmin.routing.Filters import *

class FilterTestCase(TestCase):
    
    def setUp(self):
        self.connector = Connector('abc')
        self.PDU = SubmitSM(
            source_addr='20203060',
            destination_addr='20203060',
            short_message='hello world',
        )
        self.group = Group(100)
        self.user = User(1, self.group, 'username', 'password')
        self.routable = SimpleRoutablePDU(self.connector, self.PDU, self.user)
    
class TransparentFilterTestCase(FilterTestCase):
    _filter = TransparentFilter
    
    def setUp(self):
        FilterTestCase.setUp(self)
        self.f = self._filter()
    
    def test_standard(self):
        self.assertTrue(self.f.match(self.routable))
        
    def test_invalid_parameter(self):
        self.assertRaises(InvalidFilterParameterError, self.f.match, object)

class ConnectorFilterTestCase(FilterTestCase):
    _filter = ConnectorFilter
    
    def setUp(self):
        FilterTestCase.setUp(self)
        self.f = self._filter(self.connector)
    
    def test_standard(self):
        self.assertTrue(self.f.match(self.routable))
        
    def test_invalid_parameter(self):
        self.assertRaises(InvalidFilterParameterError, self.f.match, object)
        self.assertRaises(InvalidFilterParameterError, self._filter, object)
        
class UserFilterTestCase(FilterTestCase):
    _filter = UserFilter
    
    def setUp(self):
        FilterTestCase.setUp(self)
        self.f = self._filter(self.user)
    
    def test_standard(self):
        self.assertTrue(self.f.match(self.routable))
        
    def test_invalid_parameter(self):
        self.assertRaises(InvalidFilterParameterError, self.f.match, object)
        self.assertRaises(InvalidFilterParameterError, self._filter, object)
        
class GroupFilterTestCase(FilterTestCase):
    _filter = GroupFilter
    
    def setUp(self):
        FilterTestCase.setUp(self)
        self.f = self._filter(self.group)
    
    def test_standard(self):
        self.assertTrue(self.f.match(self.routable))
        
    def test_invalid_parameter(self):
        self.assertRaises(InvalidFilterParameterError, self.f.match, object)
        self.assertRaises(InvalidFilterParameterError, self._filter, object)
        
class SourceAddrFilterTestCase(FilterTestCase):
    _filter = SourceAddrFilter
    
    def setUp(self):
        FilterTestCase.setUp(self)
        self.f = self._filter('^\d+0$')
    
    def test_standard(self):
        self.assertTrue(self.f.match(self.routable))
        
    def test_invalid_parameter(self):
        self.assertRaises(InvalidFilterParameterError, self.f.match, object)
        self.assertRaises(TypeError, self._filter, object)
        
class DestinationAddrFilterTestCase(FilterTestCase):
    _filter = DestinationAddrFilter
    
    def setUp(self):
        FilterTestCase.setUp(self)
        self.f = self._filter('^20\d+')
    
    def test_standard(self):
        self.assertTrue(self.f.match(self.routable))
        
    def test_invalid_parameter(self):
        self.assertRaises(InvalidFilterParameterError, self.f.match, object)
        self.assertRaises(TypeError, self._filter, object)
        
class ShortMessageFilterTestCase(FilterTestCase):
    _filter = ShortMessageFilter
    
    def setUp(self):
        FilterTestCase.setUp(self)
        self.f = self._filter('^hello.*$')
    
    def test_standard(self):
        self.assertTrue(self.f.match(self.routable))
        
    def test_invalid_parameter(self):
        self.assertRaises(InvalidFilterParameterError, self.f.match, object)
        self.assertRaises(TypeError, self._filter, object)
        
class DateIntervalFilterTestCase(FilterTestCase):
    _filter = DateIntervalFilter
    
    def setUp(self):
        FilterTestCase.setUp(self)
        threeDaysEarlier = datetime.date.today() - datetime.timedelta(days = 3)
        threeDaysLater = datetime.date.today() + datetime.timedelta(days = 3)
        self.f = self._filter([threeDaysEarlier, threeDaysLater])
    
    def test_standard(self):
        self.assertTrue(self.f.match(self.routable))
        
    def test_invalid_parameter(self):
        self.assertRaises(InvalidFilterParameterError, self.f.match, object)
        self.assertRaises(InvalidFilterParameterError, self._filter, object)
        self.assertRaises(InvalidFilterParameterError, self._filter, {})
        self.assertRaises(InvalidFilterParameterError, self._filter, [])
        self.assertRaises(InvalidFilterParameterError, self._filter, [1, 2])

        yesterday = datetime.date.today() - datetime.timedelta(days = 1)
        tomorrow = datetime.date.today() + datetime.timedelta(days = 1)
        self.assertRaises(InvalidFilterParameterError, self._filter, [yesterday])
        self.assertRaises(InvalidFilterParameterError, self._filter, [tomorrow])

class TimeIntervalFilterTestCase(FilterTestCase):
    _filter = TimeIntervalFilter
    
    def setUp(self):
        FilterTestCase.setUp(self)
        # Redefine routable with a fixed datetime
        routableDatetime = datetime.datetime(year = 2012, month = 1, day = 1, hour = 6, minute = 0, second = 0)
        self.routable = SimpleRoutablePDU(self.connector, self.PDU, self.user, routableDatetime)

        threeHoursEarlier = datetime.time(hour=3, minute=0)
        threeHoursLater = datetime.time(hour=9, minute=0)
        self.f = self._filter([threeHoursEarlier, threeHoursLater])
    
    def test_standard(self):
        self.assertTrue(self.f.match(self.routable))
        
    def test_invalid_parameter(self):
        self.assertRaises(InvalidFilterParameterError, self.f.match, object)
        self.assertRaises(InvalidFilterParameterError, self._filter, object)
        self.assertRaises(InvalidFilterParameterError, self._filter, {})
        self.assertRaises(InvalidFilterParameterError, self._filter, [])
        self.assertRaises(InvalidFilterParameterError, self._filter, [1, 2])

        threeHoursEarlier = datetime.time(hour=3, minute=0)
        threeHoursLater = datetime.time(hour=9, minute=0)
        self.assertRaises(InvalidFilterParameterError, self._filter, [threeHoursEarlier])
        self.assertRaises(InvalidFilterParameterError, self._filter, [threeHoursLater])
        
        # We dont accept complete datetime, we need just a time with no date
        yesterday_time = datetime.datetime.now() - datetime.timedelta(days = 1)
        tomorrow_time = datetime.datetime.now() + datetime.timedelta(days = 1)
        self.assertRaises(InvalidFilterParameterError, self._filter, [yesterday_time, tomorrow_time])
        
class EvalPyFilterTestCase(FilterTestCase):
    """
    This filter can pass the routable object to a dynamic evaluated python code.
    It will be passed as the 'routable' global variable and should return True or False
    like any other filter
    """
    _filter = EvalPyFilter
    
    def setUp(self):
        FilterTestCase.setUp(self)
        
        pyCode = """
if routable.connector.cid == 'abc':
    result = True
else:
    result = False
"""
        self.f = self._filter(pyCode)
    
    def test_standard(self):
        self.assertTrue(self.f.match(self.routable))
        
    def test_invalid_parameter(self):
        self.assertRaises(InvalidFilterParameterError, self.f.match, object)
        self.assertRaises(TypeError, self._filter, object)
    
    def test_syntax_error(self):
        f = EvalPyFilter("def class anything ...")
        self.assertRaises(SyntaxError, f.match, self.routable)

    def test_is_picklable(self):
        """Related to #196:
        It appears that an EvalPyFilter raises an error when dumped through pickle
        after getting his .node defined (when .match is called)
        """

        # Before match
        unpickledFilter = pickle.loads(pickle.dumps(self.f))
        self.assertTrue(unpickledFilter.pyCode == self.f.pyCode)

        # Call the match() method to get the .node defined
        self.f.match(self.routable)

        # After match
        unpickledFilter = pickle.loads(pickle.dumps(self.f))
        self.assertTrue(unpickledFilter.pyCode == self.f.pyCode)