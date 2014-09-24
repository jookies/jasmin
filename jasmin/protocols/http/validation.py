"""
HTTP request validators
"""

import re
from jasmin.protocols.http.errors import UrlArgsValidationError, CredentialValidationError

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
                    if isinstance(args[field][0], int):
                        value = str(args[field][0])
                    else:
                        value = args[field][0]
                    
                    # Validate known args
                    if 'pattern' in self.fields[field] and self.fields[field]['pattern'].match(value) is None:
                        raise UrlArgsValidationError("Argument [%s] has an invalid value: [%s]." % (field, value))
                elif not fieldData['optional']:
                    raise UrlArgsValidationError("Mandatory argument [%s] is not found." % field)

        return True

class CredentialValidator:
    "Will check for user MtMessagingCredential"

    def __init__(self, action, user, submit_sm, request, fields):
        self.action = action
        self.submit_sm = submit_sm
        self.user = user
        self.fields = fields
        self.request = request
        
    def _checkSendAuthorizations(self):
        "MT Authorizations check"
        
        if not self.user.mt_credential.getAuthorization('http_send'):
            raise CredentialValidationError('Authorization failed for username [%s] (Can not send MT messages).' % self.user)
        if hasattr(self.submit_sm, 'nextPdu') and not self.user.mt_credential.getAuthorization('long_content'):
            raise CredentialValidationError('Authorization failed for username [%s] (Long content is not authorized).' % self.user)
        if 'dlr-level' in self.request.args and not self.user.mt_credential.getAuthorization('set_dlr_level'):
            raise CredentialValidationError('Authorization failed for username [%s] (Setting dlr level is not authorized).' % self.user)
        if 'dlr-method' in self.request.args and not self.user.mt_credential.getAuthorization('set_dlr_method'):
            raise CredentialValidationError('Authorization failed for username [%s] (Setting dlr method is not authorized).' % self.user)
        if 'from' in self.request.args and not self.user.mt_credential.getAuthorization('set_source_address'):
            raise CredentialValidationError('Authorization failed for username [%s] (Setting source address is not authorized).' % self.user)
        if 'priority' in self.request.args and not self.user.mt_credential.getAuthorization('set_priority'):
            raise CredentialValidationError('Authorization failed for username [%s] (Setting priority is not authorized).' % self.user)
        
    def _checkSendFilters(self):
        "MT Filters check"
        
        if (self.user.mt_credential.getValueFilter('destination_address') is None or 
            not re.match(self.user.mt_credential.getValueFilter('destination_address'), str(self.request.args['to'][0]))):
            raise CredentialValidationError('Value filter failed for username [%s] (destination_address filter mismatch).' % self.user)
        if 'from' in self.request.args and (self.user.mt_credential.getValueFilter('source_address') is None or 
                                            not re.match(self.user.mt_credential.getValueFilter('source_address'), str(self.request.args['from'][0]))):
            raise CredentialValidationError('Value filter failed for username [%s] (source_address filter mismatch).' % self.user)
        if 'priority' in self.request.args and (self.user.mt_credential.getValueFilter('priority') is None or 
                                                not re.match(self.user.mt_credential.getValueFilter('priority'), str(self.request.args['priority'][0]))):
            raise CredentialValidationError('Value filter failed for username [%s] (priority filter mismatch).' % self.user)
        if (self.user.mt_credential.getValueFilter('content') is None or 
            not re.match(self.user.mt_credential.getValueFilter('content'), str(self.request.args['content'][0]))):
            raise CredentialValidationError('Value filter failed for username [%s] (content filter mismatch).' % self.user)

    def updatePDUWithSendDefaults(self, SubmitSmPDU):
        """Will update SubmitSmPDU.params from User credential defaults whenever a 
        SubmitSmPDU.params item is None"""
        
        if self.user.mt_credential.getDefaultValue('source_address') is not None and SubmitSmPDU.params['source_addr'] is None:
            SubmitSmPDU.params['source_addr'] = self.user.mt_credential.getDefaultValue('source_address')
        
        return SubmitSmPDU
    
    def validate(self, ):
        "Will validate requests through Authorizations and ValueFilters credential check"
        
        if self.action == 'Send':
            self._checkSendAuthorizations()
            self._checkSendFilters()
        else:
            raise CredentialValidationError('Unknown action [%s].' % self.action)