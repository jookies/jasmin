"""
SMPP validators
"""
import re
from enum import Enum
from jasmin.protocols.validation import AbstractCredentialValidator
from jasmin.protocols.smpp.error import *
from smpp.pdu.constants import priority_flag_value_map, priority_flag_name_map
from smpp.pdu.pdu_types import RegisteredDeliveryReceipt, RegisteredDelivery


class SmppsCredentialValidator(AbstractCredentialValidator):
    """Will check for user MtMessagingCredential"""

    def __init__(self, action, user, submit_sm):
        AbstractCredentialValidator.__init__(self, action, user)

        self.submit_sm = submit_sm

    def _checkSendAuthorizations(self):
        """MT Authorizations check"""

        if not self.user.mt_credential.getAuthorization('smpps_send'):
            raise AuthorizationError(
                'Authorization failed for username [%s] (Can not send MT messages).' % self.user)
        if (not self.user.mt_credential.getAuthorization('set_dlr_level') and
                    self.submit_sm.params['registered_delivery'] != RegisteredDelivery(
                    RegisteredDeliveryReceipt.NO_SMSC_DELIVERY_RECEIPT_REQUESTED)):
            raise AuthorizationError(
                'Authorization failed for username [%s] (Setting dlr level is not authorized).' % self.user)
        if (not self.user.mt_credential.getAuthorization('set_source_address') and
                    len(self.submit_sm.params['source_addr']) > 0):
            raise AuthorizationError(
                'Authorization failed for username [%s] (Setting source address is not authorized).' % self.user)
        if (not self.user.mt_credential.getAuthorization('set_priority') and
                    self.submit_sm.params['priority_flag'] != priority_flag_value_map[0]):
            raise AuthorizationError(
                'Authorization failed for username [%s] (Setting priority is not authorized).' % self.user)

    def _get_binary_r(self, key, credential=None):
        "Return a compile re object with a binary pattern"
        if credential is None:
            credential = self.user.mt_credential

        r = credential.getValueFilter(key)
        if isinstance(r.pattern, str):
            r = re.compile(r.pattern.encode())

        return r

    def _checkSendFilters(self):
        """MT Filters check"""

        # Filtering destination_address
        _value = self.submit_sm.params['destination_addr']
        _r = self._get_binary_r('destination_address')
        if _r is None or (_r.pattern != b'.*' and not _r.match(_value)):
            raise FilterError(
                'Value filter failed for username [%s] (destination_address filter mismatch).' % self.user,
                'destination_address')

        # Filtering source_address
        _value = self.submit_sm.params['source_addr']
        _r = self._get_binary_r('source_address')
        if _r is None or (_r.pattern != b'.*' and not _r.match(_value)):
            raise FilterError(
                'Value filter failed for username [%s] (source_address filter mismatch).' % self.user,
                'source_address')

        # Filtering priority_flag
        _value = ('%s' % priority_flag_name_map[self.submit_sm.params['priority_flag'].name]).encode()
        _r = self._get_binary_r('priority')
        if _r is None or (_r.pattern != b'^[0-3]$' and not _r.match(_value)):
            raise FilterError(
                'Value filter failed for username [%s] (priority filter mismatch).' % self.user,
                'priority')

        # Filtering content
        _value = self.submit_sm.params['short_message']
        _r = self._get_binary_r('content')
        if _r is None or (_r.pattern != b'.*' and not _r.match(_value)):
            raise FilterError(
                'Value filter failed for username [%s] (content filter mismatch).' % self.user,
                'content')

    def updatePDUWithUserDefaults(self, PDU):
        """Will update SubmitSmPDU.params from User credential defaults whenever a
        SubmitSmPDU.params item is None"""

        if (self.user.mt_credential.getDefaultValue('source_address') is not None and
                (PDU.params['source_addr'] is None or len(PDU.params['source_addr']) == 0)):
            PDU.params['source_addr'] = self.user.mt_credential.getDefaultValue('source_address')

        return PDU

    def validate(self):
        """Will validate requests through Authorizations and ValueFilters credential check"""

        if self.action == 'Send':
            self._checkSendAuthorizations()
            self._checkSendFilters()
        else:
            raise CredentialValidationError('Unknown action [%s].' % self.action)
