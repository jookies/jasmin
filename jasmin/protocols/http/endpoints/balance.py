from datetime import datetime
import re
import json

from twisted.web.resource import Resource

from jasmin.protocols.http.validation import UrlArgsValidator, HttpAPICredentialValidator
from jasmin.protocols.http.errors import HttpApiError, AuthenticationError

class Balance(Resource):
    isleaf = True

    def __init__(self, RouterPB, stats, log):
        Resource.__init__(self)

        self.RouterPB = RouterPB
        self.stats = stats
        self.log = log

    def render_GET(self, request):
        """
        /balance request processing

        Note: Balance is used by user to check his balance
        """

        self.log.debug("Rendering /balance response with args: %s from %s",
                       request.args, request.getClientIP())
        request.responseHeaders.addRawHeader(b"content-type", b"application/json")
        response = {'return': None, 'status': 200}

        self.stats.inc('request_count')
        self.stats.set('last_request_at', datetime.now())

        try:
            # Validation
            fields = {'username': {'optional': False, 'pattern': re.compile(r'^.{1,15}$')},
                      'password': {'optional': False, 'pattern': re.compile(r'^.{1,8}$')}}

            # Make validation
            v = UrlArgsValidator(request, fields)
            v.validate()

            # Authentication
            user = self.RouterPB.authenticateUser(
                username=request.args['username'][0],
                password=request.args['password'][0]
            )
            if user is None:
                self.stats.inc('auth_error_count')

                self.log.debug(
                    "Authentication failure for username:%s and password:%s",
                    request.args['username'][0], request.args['password'][0])
                self.log.error(
                    "Authentication failure for username:%s",
                    request.args['username'][0])
                raise AuthenticationError(
                    'Authentication failure for username:%s' % request.args['username'][0])

            # Update CnxStatus
            user.getCnxStatus().httpapi['connects_count'] += 1
            user.getCnxStatus().httpapi['balance_request_count'] += 1
            user.getCnxStatus().httpapi['last_activity_at'] = datetime.now()

            # Make Credential validation
            v = HttpAPICredentialValidator('Balance', user, request)
            v.validate()

            balance = user.mt_credential.getQuota('balance')
            if balance is None:
                balance = 'ND'
            sms_count = user.mt_credential.getQuota('submit_sm_count')
            if sms_count is None:
                sms_count = 'ND'
            response = {'return': {'balance': balance, 'sms_count': sms_count}, 'status': 200}
        except HttpApiError as e:
            self.log.error("Error: %s", e)
            response = {'return': e.message, 'status': e.code}
        except Exception as e:
            self.log.error("Error: %s", e)
            response = {'return': "Unknown error: %s" % e, 'status': 500}
        finally:
            self.log.debug("Returning %s to %s.", response, request.getClientIP())

            # Return message
            if response['return'] is None:
                response['return'] = 'System error'
                request.setResponseCode(500)
            else:
                request.setResponseCode(response['status'])
            return json.dumps(response['return']).encode()