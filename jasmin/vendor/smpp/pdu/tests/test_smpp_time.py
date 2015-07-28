"""
Copyright 2009-2010 Mozes, Inc.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either expressed or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
"""
import unittest
from jasmin.vendor.smpp.pdu import smpp_time
from datetime import datetime, timedelta

class SMPPTimeTest(unittest.TestCase):
    
    def test_parse_t(self):
        self.assertEquals(0, smpp_time.parse_t('0'))
        self.assertEquals('0', smpp_time.unparse_t(0))
        self.assertEquals(9, smpp_time.parse_t('9'))
        self.assertEquals('9', smpp_time.unparse_t(9))
        self.assertRaises(ValueError, smpp_time.parse_t, 'a')
        self.assertRaises(ValueError, smpp_time.parse_t, '03')
        
    def test_parse_nn(self):
        self.assertEquals(0, smpp_time.parse_nn('00'))
        self.assertEquals('00', smpp_time.unparse_nn(0))
        self.assertEquals(48, smpp_time.parse_nn('48'))
        self.assertEquals('48', smpp_time.unparse_nn(48))
        self.assertRaises(ValueError, smpp_time.parse_nn, '49')
        self.assertRaises(ValueError, smpp_time.parse_nn, '0')
        
    def test_parse_relative(self):
        str = '020610233429000R'
        rel = smpp_time.parse(str)
        self.assertEquals(smpp_time.SMPPRelativeTime, rel.__class__)
        self.assertEquals(2, rel.years)
        self.assertEquals(6, rel.months)
        self.assertEquals(10, rel.days)
        self.assertEquals(23, rel.hours)
        self.assertEquals(34, rel.minutes)
        self.assertEquals(29, rel.seconds)
        self.assertEquals(str, smpp_time.unparse(rel))
        
    def test_parse_relative_mins_only(self):
        str = '000000001000000R'
        rel = smpp_time.parse(str)
        self.assertEquals(smpp_time.SMPPRelativeTime, rel.__class__)
        self.assertEquals(0, rel.years)
        self.assertEquals(0, rel.months)
        self.assertEquals(0, rel.days)
        self.assertEquals(0, rel.hours)
        self.assertEquals(10, rel.minutes)
        self.assertEquals(0, rel.seconds)
        self.assertEquals(str, smpp_time.unparse(rel))

    def test_parse_absolute_no_offset(self):
        str = '070927233429800+'
        dt = smpp_time.parse(str)
        self.assertEquals(2007, dt.year)
        self.assertEquals(9, dt.month)
        self.assertEquals(27, dt.day)
        self.assertEquals(23, dt.hour)
        self.assertEquals(34, dt.minute)
        self.assertEquals(29, dt.second)
        self.assertEquals(800000, dt.microsecond)
        self.assertEquals(None, dt.tzinfo)
        self.assertEquals(str, smpp_time.unparse(dt))
        
    def test_parse_absolute_positive_offset(self):
        str = '070927233429848+'
        dt = smpp_time.parse(str)
        self.assertEquals(2007, dt.year)
        self.assertEquals(9, dt.month)
        self.assertEquals(27, dt.day)
        self.assertEquals(23, dt.hour)
        self.assertEquals(34, dt.minute)
        self.assertEquals(29, dt.second)
        self.assertEquals(800000, dt.microsecond)
        self.assertEquals(timedelta(hours=12), dt.tzinfo.utcoffset(None))
        self.assertEquals(str, smpp_time.unparse(dt))
        
    def test_parse_absolute_negative_offset(self):
        str = '070927233429848-'
        dt = smpp_time.parse(str)
        self.assertEquals(2007, dt.year)
        self.assertEquals(9, dt.month)
        self.assertEquals(27, dt.day)
        self.assertEquals(23, dt.hour)
        self.assertEquals(34, dt.minute)
        self.assertEquals(29, dt.second)
        self.assertEquals(800000, dt.microsecond)
        self.assertEquals(timedelta(hours=-12), dt.tzinfo.utcoffset(None))
        self.assertEquals(str, smpp_time.unparse(dt))
    
    
        
if __name__ == '__main__':
    unittest.main()
