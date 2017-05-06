import base64
import jasmin
import json
import os
import sys

from .api import PingResource, BalanceResource, RateResource, SendResource, SendBatchResource
from .config import *

sys.path.append("%s/vendor" % os.path.dirname(os.path.abspath(jasmin.__file__)))
import falcon

# @TODO: make configuration loadable from /etc/jasmin/restapi.conf
logger = logging.getLogger('jasmin-restapi')
if len(logger.handlers) == 0:
    logger.setLevel(log_level)
    handler = logging.handlers.TimedRotatingFileHandler(filename=log_file, when=log_rotate)
    handler.setFormatter(logging.Formatter(log_format, log_date_format))
    logger.addHandler(handler)


class TokenNotFound(Exception):
    """Raised when authentication token is not found"""


class JsonResponserMiddleware(object):
    """Encodes response to json and set content_type accordingly"""

    def process_response(self, request, response, resource):
        """Force response to be json only and add Jasmin signature"""

        if response.content_type != 'application/json':
            response.content_type = 'application/json'

        if response.status[:3] == '200':
            response.body = json.dumps(response.body)

        # Add Jasmin signature
        if show_jasmin_version:
            response.set_header('Powered-By', 'Jasmin %s' % jasmin.get_release())


class LoggingMiddleware(object):
    """Logging api calls"""

    def process_response(self, request, response, resource):
        if response.status[:3] == '200':
            logger.info('[%s] %s@%s %s %s' % (
                response.status[:3], request.context.get('username', '*'), request.remote_addr,
                request.method, request.relative_uri))
        else:
            logger.error('[%s] %s@%s %s %s' % (
                response.status[:3], request.context.get('username', '*'), request.remote_addr,
                request.method, request.relative_uri))


class ContentTypeFilter(object):
    """Enforces client uses json media type"""

    def process_request(self, request, response):
        if not request.client_accepts_json:
            raise falcon.HTTPUnsupportedMediaType(
                'Unsupported media type',
                'This API supports JSON media type only.',
                'http://docs.jasminsms.com/en/latest/apis/rest/index.html')


class AuthenticationFilter(object):
    """Extract username/password from Auth token and make it accessible from context"""

    def _token_decode(self, request, token):
        """Decodes token and set username/password in context"""

        try:
            token_keys = token.split(' ')
            if len(token_keys) != 2:
                raise TokenNotFound('Invalid token: %s' % token)

            # Get the auth token and extract username/password
            auth_token = base64.b64decode(token_keys[1])
        except TokenNotFound as e:
            raise falcon.HTTPUnauthorized('%s' % e,
                                          'Please provide a valid Basic auth token',
                                          href='http://docs.jasminsms.com/en/latest/apis/rest/index.html')
        except Exception as e:
            raise falcon.HTTPUnauthorized('Invalid token: %s' % e,
                                          'Please provide a valid Basic auth token',
                                          href='http://docs.jasminsms.com/en/latest/apis/rest/index.html')
        else:
            request.context['username'], request.context['password'] = auth_token.split(':')

    def process_request(self, request, response):
        """Ensuring request has the Basic auth token,
        Authentication is mandatory for /secure/* routes only"""

        if request.path.split('/')[1] == 'secure':
            token = request.get_header('Authorization')

            if token is None:
                raise falcon.HTTPUnauthorized('Authentication required',
                                              'Please provide a valid Basic auth token',
                                              href='http://docs.jasminsms.com/en/latest/apis/rest/index.html')

            self._token_decode(request, token)


# Start the falcon API with some fancy logging
logger.info('Starting Jasmin Rest API ...')
api = falcon.API(
    middleware=[
        ContentTypeFilter(),
        AuthenticationFilter(),
        JsonResponserMiddleware(),
        LoggingMiddleware()
    ]
)
api.add_route('/ping', PingResource())
logger.info('\t[OK] /ping')
api.add_route('/secure/balance', BalanceResource())
logger.info('\t[OK] /secure/balance')
api.add_route('/secure/rate', RateResource())
logger.info('\t[OK] /secure/rate')
api.add_route('/secure/send', SendResource())
logger.info('\t[OK] /secure/send')
api.add_route('/secure/sendbatch', SendBatchResource())
logger.info('\t[OK] /secure/sendbatch')
logger.info('API Started.')
