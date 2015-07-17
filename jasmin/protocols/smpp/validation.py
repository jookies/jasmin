"""
SMPP validators
"""

from jasmin.protocols.validation import AbstractCredentialValidator
from jasmin.protocols.smpp.error import *
from jasmin.vendor.smpp.pdu.constants import priority_flag_value_map
from jasmin.vendor.smpp.pdu.pdu_types import RegisteredDeliveryReceipt, RegisteredDelivery

class SmppsCredentialValidator(AbstractCredentialValidator):
    "Will check for user MtMessagingCredential"

    def __init__(self, action, user, submit_sm):
        AbstractCredentialValidator.__init__(self, action, user)

        self.submit_sm = submit_sm
        
    def _checkSendAuthorizations(self):
        "MT Authorizations check"
        
        if not self.user.mt_credential.getAuthorization('smpps_send'):
            raise AuthorizationError('Authorization failed for username [%s] (Can not send MT messages).' % self.user)
        if (not self.user.mt_credential.getAuthorization('set_dlr_level') and 
            self.submit_sm.params['registered_delivery'] != RegisteredDelivery(RegisteredDeliveryReceipt.NO_SMSC_DELIVERY_RECEIPT_REQUESTED)):
            raise AuthorizationError('Authorization failed for username [%s] (Setting dlr level is not authorized).' % self.user)
        if (not self.user.mt_credential.getAuthorization('set_source_address') and 
            len(self.submit_sm.params['source_addr']) > 0):
            raise AuthorizationError('Authorization failed for username [%s] (Setting source address is not authorized).' % self.user)
        if (not self.user.mt_credential.getAuthorization('set_priority') and 
            str(self.submit_sm.params['priority_flag']) != priority_flag_value_map[0]):
            raise AuthorizationError('Authorization failed for username [%s] (Setting priority is not authorized).' % self.user)

    def _checkSendFilters(self):
        "MT Filters check"

        if (self.user.mt_credential.getValueFilter('destination_address') is None or 
            not self.user.mt_credential.getValueFilter('destination_address').match(self.submit_sm.params['destination_addr'])):
            raise FilterError('Value filter failed for username [%s] (destination_address filter mismatch).' % self.user,
                'destination_address')
        if (self.user.mt_credential.getValueFilter('source_address') is None or 
            not self.user.mt_credential.getValueFilter('source_address').match(self.submit_sm.params['source_addr'])):
            raise FilterError('Value filter failed for username [%s] (source_address filter mismatch).' % self.user,
                'source_address')
        if (self.user.mt_credential.getValueFilter('priority') is None or 
            not self.user.mt_credential.getValueFilter('priority').match(str(
                self.submit_sm.params['priority_flag'].index
                ))):
            raise FilterError('Value filter failed for username [%s] (priority filter mismatch).' % self.user,
                'priority')
        if (self.user.mt_credential.getValueFilter('content') is None or 
            not self.user.mt_credential.getValueFilter('content').match(self.submit_sm.params['short_message'])):
            raise FilterError('Value filter failed for username [%s] (content filter mismatch).' % self.user,
                'content')

    def updatePDUWithUserDefaults(self, PDU):
        """Will update SubmitSmPDU.params from User credential defaults whenever a 
        SubmitSmPDU.params item is None"""

        if (self.user.mt_credential.getDefaultValue('source_address') is not None and 
            (PDU.params['source_addr'] is None or len(PDU.params['source_addr']) == 0)):
            PDU.params['source_addr'] = self.user.mt_credential.getDefaultValue('source_address')

        return PDU
    
    def validate(self):
        "Will validate requests through Authorizations and ValueFilters credential check"
        
        if self.action == 'Send':
            self._checkSendAuthorizations()
            self._checkSendFilters()
        else:
            raise CredentialValidationError('Unknown action [%s].' % self.action)