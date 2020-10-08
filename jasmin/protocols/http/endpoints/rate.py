from datetime import datetime
import re
import json
import pickle

from twisted.internet import reactor, defer
from twisted.web.resource import Resource
from twisted.web.server import NOT_DONE_YET
import messaging.sms.gsm0338

from jasmin.routing.Routables import RoutableSubmitSm
from jasmin.protocols.smpp.operations import SMPPOperationFactory
from jasmin.protocols.http.errors import UrlArgsValidationError
from jasmin.protocols.http.validation import UrlArgsValidator, HttpAPICredentialValidator
from jasmin.protocols.http.errors import HttpApiError, AuthenticationError, InterceptorNotSetError, InterceptorNotConnectedError, InterceptorRunError, RouteNotFoundError
from jasmin.protocols.http.endpoints import hex2bin, authenticate_user


class Rate(Resource):
    isleaf = True

    def __init__(self, HTTPApiConfig, RouterPB, stats, log, interceptorpb_client):
        Resource.__init__(self)

        self.RouterPB = RouterPB
        self.stats = stats
        self.log = log
        self.interceptorpb_client = interceptorpb_client

        # opFactory is initiated with a dummy SMPPClientConfig used for building SubmitSm only
        self.opFactory = SMPPOperationFactory(long_content_max_parts=HTTPApiConfig.long_content_max_parts,
                                              long_content_split=HTTPApiConfig.long_content_split)

    @defer.inlineCallbacks
    def route_routable(self, request):
        try:
            # Do we have a hex-content ?
            if b'hex-content' not in request.args:
                # Convert utf8 to GSM 03.38
                if request.args[b'coding'][0] == '0':
                    if isinstance(request.args[b'content'][0], bytes):
                        short_message = request.args[b'content'][0].decode().encode('gsm0338', 'replace')
                    else:
                        short_message = request.args[b'content'][0].encode('gsm0338', 'replace')
                    request.args[b'content'][0] = short_message
                else:
                    # Otherwise forward it as is
                    short_message = request.args[b'content'][0]
            else:
                # Otherwise convert hex to bin
                short_message = hex2bin(request.args[b'hex-content'][0])

            # Authentication
            user = authenticate_user(
                request.args[b'username'][0],
                request.args[b'password'][0],
                self.RouterPB,
                self.stats,
                self.log
            )

            # Update CnxStatus
            user.getCnxStatus().httpapi['connects_count'] += 1
            user.getCnxStatus().httpapi['rate_request_count'] += 1
            user.getCnxStatus().httpapi['last_activity_at'] = datetime.now()

            # Build SubmitSmPDU
            SubmitSmPDU = self.opFactory.SubmitSM(
                source_add=None if b'from' not in request.args else request.args[b'from'][0],
                destination_addr=request.args[b'to'][0],
                short_message=short_message,
                data_coding=int(request.args[b'coding'][0]),
            )
            self.log.debug("Built base SubmitSmPDU: %s", SubmitSmPDU)

            # Make Credential validation
            v = HttpAPICredentialValidator('Rate', user, request, submit_sm=SubmitSmPDU)
            v.validate()

            # Update SubmitSmPDU by default values from user MtMessagingCredential
            SubmitSmPDU = v.updatePDUWithUserDefaults(SubmitSmPDU)

            # Prepare for interception than routing
            routable = RoutableSubmitSm(SubmitSmPDU, user)
            self.log.debug("Built Routable %s for SubmitSmPDU: %s", routable, SubmitSmPDU)

            # Should we tag the routable ?
            tags = []
            if b'tags' in request.args:
                tags = request.args[b'tags'][0].split(b',')
                for tag in tags:
                    if isinstance(tag, bytes):
                        routable.addTag(tag.decode())
                    else:
                        routable.addTag(tag)
                    self.log.debug('Tagged routable %s: +%s', routable, tag)

            # Intercept
            interceptor = self.RouterPB.getMTInterceptionTable().getInterceptorFor(routable)
            if interceptor is not None:
                self.log.debug("RouterPB selected %s interceptor for this SubmitSmPDU", interceptor)
                if self.interceptorpb_client is None:
                    self.stats.inc('interceptor_error_count')
                    self.log.error("InterceptorPB not set !")
                    raise InterceptorNotSetError('InterceptorPB not set !')
                if not self.interceptorpb_client.isConnected:
                    self.stats.inc('interceptor_error_count')
                    self.log.error("InterceptorPB not connected !")
                    raise InterceptorNotConnectedError('InterceptorPB not connected !')

                script = interceptor.getScript()
                self.log.debug("Interceptor script loaded: %s", script)

                # Run !
                r = yield self.interceptorpb_client.run_script(script, routable)
                if isinstance(r, dict) and r['http_status'] != 200:
                    self.stats.inc('interceptor_error_count')
                    self.log.error('Interceptor script returned %s http_status error.', r['http_status'])
                    raise InterceptorRunError(
                        code=r['http_status'],
                        message='Interception specific error code %s' % r['http_status']
                    )
                elif isinstance(r, (str, bytes)):
                    self.stats.inc('interceptor_count')
                    routable = pickle.loads(r)
                else:
                    self.stats.inc('interceptor_error_count')
                    self.log.error('Failed running interception script, got the following return: %s', r)
                    raise InterceptorRunError(message='Failed running interception script, check log for details')

            # Routing
            route = self.RouterPB.getMTRoutingTable().getRouteFor(routable)
            if route is None:
                self.log.error("No route matched from user %s for SubmitSmPDU: %s", user, SubmitSmPDU)
                raise RouteNotFoundError("No route found")

            # Get connector from selected route
            self.log.debug("RouterPB selected %s for this SubmitSmPDU", route)

            # Get number of PDUs to be sent (for billing purpose)
            _pdu = SubmitSmPDU
            submit_sm_count = 1
            while hasattr(_pdu, 'nextPdu'):
                _pdu = _pdu.nextPdu
                submit_sm_count += 1

            # Get the bill
            bill = route.getBillFor(user)

            response = {
                'return': {
                    'unit_rate': bill.getTotalAmounts(),
                    'submit_sm_count': submit_sm_count},
                'status': 200}
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

            if isinstance(response['return'], bytes):
                request.write(json.dumps(response['return'].decode()).encode())
            else:
                request.write(json.dumps(response['return']).encode())
            request.finish()

    def render_GET(self, request):
        """
        /rate request processing

        Note: This method will indicate the rate of the message once sent
        """

        self.log.debug("Rendering /rate response with args: %s from %s",
                       request.args, request.getClientIP())
        request.responseHeaders.addRawHeader(b"content-type", b"application/json")
        response = {'return': None, 'status': 200}

        self.stats.inc('request_count')
        self.stats.set('last_request_at', datetime.now())

        try:
            # Validation (must be almost the same params as /send service)
            fields = {b'to': {'optional': False, 'pattern': re.compile(rb'^\+{0,1}\d+$')},
                      b'from': {'optional': True},
                      b'coding': {'optional': True, 'pattern': re.compile(rb'^(0|1|2|3|4|5|6|7|8|9|10|13|14){1}$')},
                      b'username': {'optional': False, 'pattern': re.compile(rb'^.{1,16}$')},
                      b'password': {'optional': False, 'pattern': re.compile(rb'^.{1,16}$')},
                      # Priority validation pattern can be validated/filtered further more
                      # through HttpAPICredentialValidator
                      b'priority': {'optional': True, 'pattern': re.compile(rb'^[0-3]$')},
                      # Validity period validation pattern can be validated/filtered further more
                      # through HttpAPICredentialValidator
                      b'validity-period': {'optional': True, 'pattern': re.compile(rb'^\d+$')},
                      b'tags': {'optional': True, 'pattern': re.compile(rb'^([-a-zA-Z0-9,])*$')},
                      b'content': {'optional': True},
                      b'hex-content': {'optional': True},
                      }

            # Default coding is 0 when not provided
            if b'coding' not in request.args:
                request.args[b'coding'] = [b'0']

            # Content is optional, defaults to empty content string
            if b'hex-content' not in request.args and b'content' not in request.args:
                request.args[b'content'] = [b'']

            # Make validation
            v = UrlArgsValidator(request, fields)
            v.validate()

            # Check if have content --OR-- hex-content
            # @TODO: make this inside UrlArgsValidator !
            if b'content' in request.args and b'hex-content' in request.args:
                raise UrlArgsValidationError("content and hex-content cannot be used both in same request.")

            # Continue routing in a separate thread
            reactor.callFromThread(self.route_routable, request=request)
        except HttpApiError as e:
            self.log.error("Error: %s", e)
            response = {'return': e.message, 'status': e.code}

            self.log.debug("Returning %s to %s.", response, request.getClientIP())

            # Return message
            if response['return'] is None:
                response['return'] = 'System error'
                request.setResponseCode(500)
            else:
                request.setResponseCode(response['status'])
            if isinstance(response['return'], bytes):
                return json.dumps(response['return'].decode()).encode()
            return json.dumps(response['return']).encode()
            
        except Exception as e:
            self.log.error("Error: %s", e)
            response = {'return': "Unknown error: %s" % e, 'status': 500}

            self.log.debug("Returning %s to %s.", response, request.getClientIP())

            # Return message
            if response['return'] is None:
                response['return'] = 'System error'
                request.setResponseCode(500)
            else:
                request.setResponseCode(response['status'])
            return json.dumps(response['return']).encode()
        else:
            return NOT_DONE_YET
