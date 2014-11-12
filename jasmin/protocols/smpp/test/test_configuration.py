"""
Test cases for jasmin.protocols.smpp.configs module.
"""

import logging
from twisted.trial.unittest import TestCase
from jasmin.protocols.smpp.configs import SMPPClientConfig
from jasmin.protocols.smpp.configs import ConfigUndefinedIdError, ConfigInvalidIdError, ConfigInvalidLogLevelError

class SMPPClientConfigTest(TestCase):
    def test_id_is_mandatory(self):
        self.assertRaises(ConfigUndefinedIdError, SMPPClientConfig)
        
    def test_id_syntax_validation(self):
        invalidValues = [ 'zzz s', '', 'a,', 'r#r', '9a', '&"()=+~#{[|\`\^@]}', 'a123456789012345678901234-', 'aa']
        for invalidValue in invalidValues:
            self.assertRaises(ConfigInvalidIdError, SMPPClientConfig, id=invalidValue)

    def test_log_level_syntax_validation(self):
        invalidValues = [ 1, 32, 100, 0, 'debug', 'info']
        for invalidValue in invalidValues:
            self.assertRaises(ConfigInvalidLogLevelError, SMPPClientConfig, id='anyID', log_level=invalidValue)

        validValues = [ 10, 20, 30, 40, 50, logging.INFO, logging.DEBUG]
        for validValue in validValues:
        	SMPPClientConfig(id='anyID', log_level=validValue)