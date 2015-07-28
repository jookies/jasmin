import pickle
from twisted.trial.unittest import TestCase
from jasmin.vendor.smpp.pdu import smpp_time
from datetime import datetime

class SMPPTimeTest(TestCase):
    
    def test_pickle_unpickle_datetime_with_tz(self):
        """Related to #267

        Pickling tzinfo results in an error:
        Error in submit_sm_errback: __init__() takes exactly 3 arguments (1 given)

        This test will pickle then unpickle tzinfo object to ensure no exception is raised
        """

        tz = smpp_time.FixedOffset(72, 'Paris')

        # Pickle then unpickle
        pickled_tz = pickle.dumps(tz)
        unpickled_tz = pickle.loads(pickled_tz)

        # Asserts
        self.assertEqual(unpickled_tz.dst(datetime.now()), tz.dst(datetime.now()))
        self.assertEqual(unpickled_tz.utcoffset(datetime.now()), tz.utcoffset(datetime.now()))
        self.assertEqual(unpickled_tz.tzname(datetime.now()), tz.tzname(datetime.now()))