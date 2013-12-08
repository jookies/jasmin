# Copyright 2012 Fourat Zouari <fourat@gmail.com>
# See LICENSE for details.

"""
Test cases for jasmin.protocols.smpp.configs module.
"""

from twisted.trial.unittest import TestCase
from jasmin.protocols.smpp.configs import SMPPClientConfig
from jasmin.protocols.smpp.configs import ConfigUndefinedIdError, ConfigInvalidIdError, InvalidValue

class SMPPClientConfigTest(TestCase):
    def test_id_is_mandatory(self):
        self.assertRaises(ConfigUndefinedIdError, SMPPClientConfig)
        
    def test_id_syntax_validation(self):
        invalidIds = [ 'zzz s', '', 'a,', 'r#r', '9a', '&"()=+~#{[|\`\^@]}', 'a123456789012345678901234-', 'aa']
        
        for invalidId in invalidIds:
            self.assertRaises(ConfigInvalidIdError, SMPPClientConfig, id=invalidId)

    def test_invalid_longContentSplit(self):
        self.assertRaises(InvalidValue, SMPPClientConfig, id='validId', longContentSplit='anything')