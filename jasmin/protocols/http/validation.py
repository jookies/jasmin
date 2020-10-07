"""
HTTP request validators
"""

from jasmin.protocols.http.errors import UrlArgsValidationError, CredentialValidationError
from jasmin.protocols.validation import AbstractCredentialValidator


class UrlArgsValidator:
    """Will check for arguments syntax errors"""

    def __init__(self, request, fields):  # TODO add if JSON dont do [0]
        self.fields = fields
        self.request = request

    def validate(self):
        """Validate arguments, raises UrlArgsValidationError if something is wrong"""

        args = self.request.args

        if len(args) == 0:
            raise UrlArgsValidationError(
                'Mandatory arguments not found, please refer to the HTTPAPI specifications.')

        for arg in args:
            # Check for unknown args
            if arg not in self.fields:
                # we probably just should drop extraneous args rather than throwing an error...
                raise UrlArgsValidationError(b"Argument [%s] is unknown." % arg)

            # Validate known args and check for mandatory fields
            for field in self.fields:
                fieldData = self.fields[field]

                if field in args:
                    if isinstance(args[field][0], dict) or isinstance(args[field][0], list):
                        continue  # Todo check structure of dict/list
                    elif isinstance(args[field][0], int) or isinstance(args[field][0], float):
                        value = str(args[field][0]).encode()
                    elif isinstance(args[field][0], str):
                        value = args[field][0].encode()
                    else:
                        value = args[field][0]

                    # Validate known args
                    # print(f'Validating field {field} of value {value}')
                    if ('pattern' in self.fields[field]
                        and self.fields[field]['pattern'].match(value) is None):
                        raise UrlArgsValidationError(b"Argument [%s] has an invalid value: [%s]." % (
                            field, value))
                elif not fieldData['optional']:
                    raise UrlArgsValidationError(b"Mandatory argument [%s] is not found." % field)

        return True


class HttpAPICredentialValidator(AbstractCredentialValidator):
    """Will check for user MtMessagingCredential"""

    def __init__(self, action, user, request, submit_sm=None):
        AbstractCredentialValidator.__init__(self, action, user)

        self.submit_sm = submit_sm
        self.request = request

    def _checkSendAuthorizations(self):
        """MT Authorizations check"""

        if not self.user.mt_credential.getAuthorization('http_send'):
            raise CredentialValidationError(
                'Authorization failed for user [%s] (Cannot send MT messages).' % self.user)
        if (hasattr(self.submit_sm, 'nextPdu')
            and not self.user.mt_credential.getAuthorization('http_long_content')):
            raise CredentialValidationError(
                'Authorization failed for user [%s] (Long content not authorized).' % self.user)
        if (b'dlr-level' in self.request.args
            and not self.user.mt_credential.getAuthorization('set_dlr_level')):
            raise CredentialValidationError(
                'Authorization failed for user [%s] (Setting dlr level not authorized).' % self.user)
        if (b'dlr-method' in self.request.args
            and not self.user.mt_credential.getAuthorization('http_set_dlr_method')):
            raise CredentialValidationError(
                'Authorization failed for user [%s] (Setting dlr method not authorized).' % self.user)
        if (b'from' in self.request.args
            and not self.user.mt_credential.getAuthorization('set_source_address')):
            raise CredentialValidationError(
                'Authorization failed for user [%s] (Setting source address not authorized).' % self.user)
        if (b'priority' in self.request.args
            and not self.user.mt_credential.getAuthorization('set_priority')):
            raise CredentialValidationError(
                'Authorization failed for user [%s] (Setting priority not authorized).' % self.user)
        if (b'validity-period' in self.request.args
            and not self.user.mt_credential.getAuthorization('set_validity_period')):
            raise CredentialValidationError(
                'Authorization failed for user [%s] (Setting validity period not authorized).' % self.user)
        if (b'hex-content' in self.request.args
            and not self.user.mt_credential.getAuthorization('set_hex_content')):
            raise CredentialValidationError(
                'Authorization failed for user [%s] (Setting hex content not authorized).' % self.user)
        if (b'sdt' in self.request.args
            and not self.user.mt_credential.getAuthorization('set_schedule_delivery_time')):
            raise CredentialValidationError(
                'Authorization failed for user [%s] (Setting schedule delivery time not authorized).' % self.user)

    def _checkBalanceAuthorizations(self):
        """Balance Authorizations check"""

        if not self.user.mt_credential.getAuthorization('http_balance'):
            raise CredentialValidationError(
                'Authorization failed for user [%s] (Cannot check balance).' % self.user)

    def _checkRateAuthorizations(self):
        """Rate Authorizations check"""

        if not self.user.mt_credential.getAuthorization('http_rate'):
            raise CredentialValidationError(
                'Authorization failed for user [%s] (Cannot check rate).' % self.user)

    def _checkSendFilters(self):
        """MT Filters check"""

        if (self.user.mt_credential.getValueFilter('destination_address') is None or
                not self.user.mt_credential.getValueFilter('destination_address').match(
                    self._convert_to_string(b'to'))):
            raise CredentialValidationError(
                'Value filter failed for user [%s] (destination_address filter mismatch).' % self.user)
        if b'from' in self.request.args and (self.user.mt_credential.getValueFilter('source_address') is None or
                                                not self.user.mt_credential.getValueFilter('source_address').match(
                                                    self._convert_to_string(b'from'))):
            raise CredentialValidationError(
                'Value filter failed for user [%s] (source_address filter mismatch).' % self.user)
        if b'priority' in self.request.args and (self.user.mt_credential.getValueFilter('priority') is None or
                                                    not self.user.mt_credential.getValueFilter('priority').match(
                                                        self._convert_to_string(b'priority'))):
            raise CredentialValidationError(
                'Value filter failed for user [%s] (priority filter mismatch).' % self.user)
        if b'validity-period' in self.request.args and (
                        self.user.mt_credential.getValueFilter('validity_period') is None or
                    not self.user.mt_credential.getValueFilter('validity_period').match(
                        self._convert_to_string(b'validity-period'))):
            raise CredentialValidationError(
                'Value filter failed for user [%s] (validity_period filter mismatch).' % self.user)
        if (b'content' in self.request.args and 
                (self.user.mt_credential.getValueFilter('content') is None or
                not self.user.mt_credential.getValueFilter('content').match(self._convert_to_string(b'content', self.request.args.get(b'coding', [None])[0])))):
            raise CredentialValidationError(
                'Value filter failed for user [%s] (content filter mismatch).' % self.user)

    def updatePDUWithUserDefaults(self, PDU):
        """Will update SubmitSmPDU.params from User credential defaults whenever a
        SubmitSmPDU.params item is None"""

        if (self.user.mt_credential.getDefaultValue('source_address') is not None
            and PDU.params['source_addr'] is None):
            PDU.params['source_addr'] = self.user.mt_credential.getDefaultValue('source_address')

        return PDU

    def validate(self):
        """Will validate requests through Authorizations and ValueFilters credential check"""

        if self.action == 'Send':
            self._checkSendAuthorizations()
            self._checkSendFilters()
        elif self.action == 'Rate':
            self._checkRateAuthorizations()
        elif self.action == 'Balance':
            self._checkBalanceAuthorizations()
        else:
            raise CredentialValidationError('Unknown action [%s].' % self.action)

    def _convert_to_string(self, arg_name, encoding_type=None):
        value = self.request.args[arg_name][0]
        if isinstance(value, bytes):
            if encoding_type == b'13':
                # JISX0212 can be decoded this way given the escape sequences
                return (b'\x1b$(D' + value + b'\x1b(B').decode('iso2022jp-1')
            if encoding_type in (b'2', b'4', b'14'):
                # These types dont decode properly
                return ''
            return value.decode(self.encoding_map.get(encoding_type, 'ascii'))
        if isinstance(value, str):
            return value
        return str(value)
        