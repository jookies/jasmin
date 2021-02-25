"""
Test cases for jasmin.protocols.smpp.validation module.
"""

import copy
from twisted.trial.unittest import TestCase
from jasmin.protocols.smpp.validation import SmppsCredentialValidator
from jasmin.protocols.smpp.configs import SMPPClientConfig
from jasmin.protocols.smpp.operations import SMPPOperationFactory
from jasmin.routing.jasminApi import User, Group


class ValidatorTest(TestCase):
    def setUp(self):
        self.opFactory = SMPPOperationFactory(SMPPClientConfig(id='test-id'))
        self.user = User(1, Group(100), 'username', 'password')
        self.pdu = self.opFactory.SubmitSM(
            source_addr=b'2',
            destination_addr=b'3',
            short_message=b'sunny day !',
        )


class SmppsCredentialValidatorTest(ValidatorTest):

    def test_all_filters(self):

        pdu = copy.copy(self.pdu)

        # Make Credential validation
        v = SmppsCredentialValidator('Send', self.user, pdu)

        try:
            v.validate()
        except UnicodeDecodeError as e:
            self.fail("(UnicodeDecodeError): %s" % e)

    def test_check_send_filters_utf8(self):
        """
        Make regex matching pass the utf8 content
        Related to #954
        """

        pdu = copy.copy(self.pdu)
        # This is the '§' character:
        pdu.params['short_message'] = b'\x00\xa7'

        # Make Credential validation
        v = SmppsCredentialValidator('Send', self.user, pdu)

        try:
            v.validate()
        except UnicodeDecodeError as e:
            self.fail("(UnicodeDecodeError): %s" % e)
