"""
HTTP request validators
"""

import re
from jasmin.protocols.http.errors import UrlArgsValidationError, CredentialValidationError
from jasmin.protocols.validation import AbstractCredentialValidator

class UrlArgsValidator:
    "Will check for arguments syntax errors"
    
    def __init__(self, request, fields):
        self.fields = fields
        self.request = request
        
    def validate(self):
        "Validate arguments, raises UrlArgsValidationError if something is wrong"
        
        args = self.request.args
        
        if len(args) == 0:
            raise UrlArgsValidationError('Mandatory arguments not found, please refer to the HTTPAPI specifications.')
        
        for arg in args:
            # Check for unknown args
            if arg not in self.fields:
                raise UrlArgsValidationError("Argument [%s] is unknown." % arg)
                            
            # Validate known args and check for mandatory fields
            for field in self.fields:
                fieldData = self.fields[field]
                    
                if field in args:
                    if isinstance(args[field][0], int) or isinstance(args[field][0], float):
                        value = str(args[field][0])
                    else:
                        value = args[field][0]
                    
                    # Validate known args
                    if 'pattern' in self.fields[field] and self.fields[field]['pattern'].match(value) is None:
                        raise UrlArgsValidationError("Argument [%s] has an invalid value: [%s]." % (field, value))
                elif not fieldData['optional']:
                    raise UrlArgsValidationError("Mandatory argument [%s] is not found." % field)

        return True

class HttpAPICredentialValidator(AbstractCredentialValidator):
    "Will check for user MtMessagingCredential"

    def __init__(self, action, user, request, submit_sm = None):
        AbstractCredentialValidator.__init__(self, action, user)

        self.submit_sm = submit_sm
        self.request = request
        
    def _checkSendAuthorizations(self):
        "MT Authorizations check"
        
        if not self.user.mt_credential.getAuthorization('http_send'):
            raise CredentialValidationError('Authorization failed for username [%s] (Can not send MT messages).' % self.user)
        if hasattr(self.submit_sm, 'nextPdu') and not self.user.mt_credential.getAuthorization('http_long_content'):
            raise CredentialValidationError('Authorization failed for username [%s] (Long content is not authorized).' % self.user)
        if 'dlr-level' in self.request.args and not self.user.mt_credential.getAuthorization('set_dlr_level'):
            raise CredentialValidationError('Authorization failed for username [%s] (Setting dlr level is not authorized).' % self.user)
        if 'dlr-method' in self.request.args and not self.user.mt_credential.getAuthorization('http_set_dlr_method'):
            raise CredentialValidationError('Authorization failed for username [%s] (Setting dlr method is not authorized).' % self.user)
        if 'from' in self.request.args and not self.user.mt_credential.getAuthorization('set_source_address'):
            raise CredentialValidationError('Authorization failed for username [%s] (Setting source address is not authorized).' % self.user)
        if 'priority' in self.request.args and not self.user.mt_credential.getAuthorization('set_priority'):
            raise CredentialValidationError('Authorization failed for username [%s] (Setting priority is not authorized).' % self.user)
        if 'validity-period' in self.request.args and not self.user.mt_credential.getAuthorization('set_validity_period'):
            raise CredentialValidationError('Authorization failed for username [%s] (Setting validity period is not authorized).' % self.user)
        
    def _checkBalanceAuthorizations(self):
        "Balance Authorizations check"
        
        if not self.user.mt_credential.getAuthorization('http_balance'):
            raise CredentialValidationError('Authorization failed for username [%s] (Can not check balance).' % self.user)
        
    def _checkRateAuthorizations(self):
        "Rate Authorizations check"
        
        if not self.user.mt_credential.getAuthorization('http_rate'):
            raise CredentialValidationError('Authorization failed for username [%s] (Can not check rate).' % self.user)
        
    def _checkSendFilters(self):
        "MT Filters check"

        if (self.user.mt_credential.getValueFilter('destination_address') is None or 
            not self.user.mt_credential.getValueFilter('destination_address').match(str(self.request.args['to'][0]))):
            raise CredentialValidationError('Value filter failed for username [%s] (destination_address filter mismatch).' % self.user)
        if 'from' in self.request.args and (self.user.mt_credential.getValueFilter('source_address') is None or 
            not self.user.mt_credential.getValueFilter('source_address').match(str(self.request.args['from'][0]))):
            raise CredentialValidationError('Value filter failed for username [%s] (source_address filter mismatch).' % self.user)
        if 'priority' in self.request.args and (self.user.mt_credential.getValueFilter('priority') is None or 
            not self.user.mt_credential.getValueFilter('priority').match(str(self.request.args['priority'][0]))):
            raise CredentialValidationError('Value filter failed for username [%s] (priority filter mismatch).' % self.user)
        if 'validity-period' in self.request.args and (self.user.mt_credential.getValueFilter('validity_period') is None or 
            not self.user.mt_credential.getValueFilter('validity_period').match(str(self.request.args['validity-period'][0]))):
            raise CredentialValidationError('Value filter failed for username [%s] (validity_period filter mismatch).' % self.user)
        if (self.user.mt_credential.getValueFilter('content') is None or 
            not self.user.mt_credential.getValueFilter('content').match(str(self.request.args['content'][0]))):
            raise CredentialValidationError('Value filter failed for username [%s] (content filter mismatch).' % self.user)

    def updatePDUWithUserDefaults(self, PDU):
        """Will update SubmitSmPDU.params from User credential defaults whenever a 
        SubmitSmPDU.params item is None"""
        
        if self.user.mt_credential.getDefaultValue('source_address') is not None and PDU.params['source_addr'] is None:
            PDU.params['source_addr'] = self.user.mt_credential.getDefaultValue('source_address')
        
        return PDU
    
    def validate(self):
        "Will validate requests through Authorizations and ValueFilters credential check"
        
        if self.action == 'Send':
            self._checkSendAuthorizations()
            self._checkSendFilters()
        elif self.action == 'Rate':
            self._checkRateAuthorizations()
        elif self.action == 'Balance':
            self._checkBalanceAuthorizations()
        else:
            raise CredentialValidationError('Unknown action [%s].' % self.action)