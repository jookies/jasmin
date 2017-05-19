import cPickle as pickle
import json
import logging
import re
import binascii
from datetime import datetime, timedelta
from logging.handlers import TimedRotatingFileHandler

from twisted.internet import reactor, defer
from twisted.web.resource import Resource
from twisted.web.server import NOT_DONE_YET

from jasmin.protocols.http.errors import UrlArgsValidationError
from jasmin.protocols.smpp.operations import SMPPOperationFactory, gsm_encode
from jasmin.routing.Routables import RoutableSubmitSm
from jasmin.vendor.smpp.pdu.constants import priority_flag_value_map
from jasmin.vendor.smpp.pdu.pdu_types import RegisteredDeliveryReceipt, RegisteredDelivery
from .errors import (AuthenticationError, ServerError, RouteNotFoundError, ConnectorNotFoundError,
                     ChargingError, ThroughputExceededError, InterceptorNotSetError,
                     InterceptorNotConnectedError, InterceptorRunError)
from .stats import HttpAPIStatsCollector
from .validation import UrlArgsValidator, HttpAPICredentialValidator

LOG_CATEGORY = "jasmin-http-api"

# @TODO make it configurable
reactor.suggestThreadPoolSize(30)


def update_submit_sm_pdu(routable, config):
    """Will set pdu parameters from smppclient configuration.
    Parameters that were locked through the routable.lockPduParam() method will not be updated."""

    update_params = [
        'protocol_id',
        'replace_if_present_flag',
        'dest_addr_ton',
        'source_addr_npi',
        'dest_addr_npi',
        'service_type',
        'source_addr_ton',
        'sm_default_msg_id',
    ]

    for param in update_params:
        _pdu = routable.pdu

        # Force setting param in main pdu
        if not routable.pduParamIsLocked(param):
            _pdu.params[param] = getattr(config, param)

        # Force setting param in sub-pdus (multipart use case)
        while hasattr(_pdu, 'nextPdu'):
            _pdu = _pdu.nextPdu
            if not routable.pduParamIsLocked(param):
                _pdu.params[param] = getattr(config, param)

    return routable


def hex2bin(hex_content):
    """Convert hex-content back to binary data, raise a UrlArgsValidationError on failure"""

    try:
        b = binascii.unhexlify(hex_content)
    except:
        raise UrlArgsValidationError("Invalid hex-content data: '%s'" % hex_content)
    else:
        return b


class Send(Resource):
    isleaf = True

    def __init__(self, HTTPApiConfig, RouterPB, SMPPClientManagerPB, stats, log, interceptorpb_client):
        Resource.__init__(self)

        self.SMPPClientManagerPB = SMPPClientManagerPB
        self.RouterPB = RouterPB
        self.stats = stats
        self.log = log
        self.interceptorpb_client = interceptorpb_client

        # opFactory is initiated with a dummy SMPPClientConfig used for building SubmitSm only
        self.opFactory = SMPPOperationFactory(long_content_max_parts=HTTPApiConfig.long_content_max_parts,
                                              long_content_split=HTTPApiConfig.long_content_split)

    @defer.inlineCallbacks
    def route_routable(self, updated_request):
        try:
            # Do we have a hex-content ?
            if 'hex-content' not in updated_request.args:
                # Convert utf8 to GSM 03.38
                if updated_request.args['coding'][0] == '0':
                    short_message = gsm_encode(updated_request.args['content'][0].decode('utf-8'))
                else:
                    # Otherwise forward it as is
                    short_message = updated_request.args['content'][0]
            else:
                # Otherwise convert hex to bin
                short_message = hex2bin(updated_request.args['hex-content'][0])

            # Authentication
            user = self.RouterPB.authenticateUser(
                username=updated_request.args['username'][0],
                password=updated_request.args['password'][0])
            if user is None:
                self.stats.inc('auth_error_count')

                self.log.debug(
                    "Authentication failure for username:%s and password:%s",
                    updated_request.args['username'][0], updated_request.args['password'][0])
                self.log.error(
                    "Authentication failure for username:%s",
                    updated_request.args['username'][0])
                raise AuthenticationError(
                    'Authentication failure for username:%s' % updated_request.args['username'][0])

            # Update CnxStatus
            user.getCnxStatus().httpapi['connects_count'] += 1
            user.getCnxStatus().httpapi['submit_sm_request_count'] += 1
            user.getCnxStatus().httpapi['last_activity_at'] = datetime.now()

            # Build SubmitSmPDU
            SubmitSmPDU = self.opFactory.SubmitSM(
                source_addr=None if 'from' not in updated_request.args else updated_request.args['from'][0],
                destination_addr=updated_request.args['to'][0],
                short_message=short_message,
                data_coding=int(updated_request.args['coding'][0]))
            self.log.debug("Built base SubmitSmPDU: %s", SubmitSmPDU)

            # Make Credential validation
            v = HttpAPICredentialValidator('Send', user, updated_request, submit_sm=SubmitSmPDU)
            v.validate()

            # Update SubmitSmPDU by default values from user MtMessagingCredential
            SubmitSmPDU = v.updatePDUWithUserDefaults(SubmitSmPDU)

            # Prepare for interception then routing
            routedConnector = None # init
            routable = RoutableSubmitSm(SubmitSmPDU, user)
            self.log.debug("Built Routable %s for SubmitSmPDU: %s", routable, SubmitSmPDU)

            # Should we tag the routable ?
            tags = []
            if 'tags' in updated_request.args:
                tags = updated_request.args['tags'][0].split(',')
                for tag in tags:
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
                elif isinstance(r, str):
                    self.stats.inc('interceptor_count')
                    routable = pickle.loads(r)
                else:
                    self.stats.inc('interceptor_error_count')
                    self.log.error('Failed running interception script, got the following return: %s', r)
                    raise InterceptorRunError(message='Failed running interception script, check log for details')

            # Get the route
            route = self.RouterPB.getMTRoutingTable().getRouteFor(routable)
            if route is None:
                self.stats.inc('route_error_count')
                self.log.error("No route matched from user %s for SubmitSmPDU: %s", user, routable.pdu)
                raise RouteNotFoundError("No route found")

            # Get connector from selected route
            self.log.debug("RouterPB selected %s route for this SubmitSmPDU", route)
            routedConnector = route.getConnector()
            # Is it a failover route ? then check for a bound connector, otherwise don't route
            # The failover route requires at least one connector to be up, no message enqueuing will
            # occur otherwise.
            if repr(route) == 'FailoverMTRoute':
                self.log.debug('Selected route is a failover, will ensure connector is bound:')
                while True:
                    c = self.SMPPClientManagerPB.perspective_connector_details(routedConnector.cid)
                    if c:
                        self.log.debug('Connector [%s] is: %s', routedConnector.cid, c['session_state'])
                    else:
                        self.log.debug('Connector [%s] is not found', routedConnector.cid)

                    if c and c['session_state'][:6] == 'BOUND_':
                        # Choose this connector
                        break
                    else:
                        # Check next connector, None if no more connectors are available
                        routedConnector = route.getConnector()
                        if routedConnector is None:
                            break

            if routedConnector is None:
                self.stats.inc('route_error_count')
                self.log.error("Failover route has no bound connector to handle SubmitSmPDU: %s", routable.pdu)
                raise ConnectorNotFoundError("Failover route has no bound connectors")

            # Re-update SubmitSmPDU with parameters from the route's connector
            connector_config = self.SMPPClientManagerPB.perspective_connector_config(routedConnector.cid)
            if connector_config:
                connector_config = pickle.loads(connector_config)
                routable = update_submit_sm_pdu(routable=routable, config=connector_config)

            # Set priority
            priority = 0
            if 'priority' in updated_request.args:
                priority = int(updated_request.args['priority'][0])
                routable.pdu.params['priority_flag'] = priority_flag_value_map[priority]
            self.log.debug("SubmitSmPDU priority is set to %s", priority)

            # Set validity_period
            if 'validity-period' in updated_request.args:
                delta = timedelta(minutes=int(updated_request.args['validity-period'][0]))
                routable.pdu.params['validity_period'] = datetime.today() + delta
                self.log.debug(
                    "SubmitSmPDU validity_period is set to %s (+%s minutes)",
                    routable.pdu.params['validity_period'],
                    updated_request.args['validity-period'][0])

            # Set DLR bit mask on the last pdu
            _last_pdu = routable.pdu
            while True:
                if hasattr(_last_pdu, 'nextPdu'):
                    _last_pdu = _last_pdu.nextPdu
                else:
                    break
            # DLR setting is clearly described in #107
            _last_pdu.params['registered_delivery'] = RegisteredDelivery(
                RegisteredDeliveryReceipt.NO_SMSC_DELIVERY_RECEIPT_REQUESTED)
            if updated_request.args['dlr'][0] == 'yes':
                _last_pdu.params['registered_delivery'] = RegisteredDelivery(
                    RegisteredDeliveryReceipt.SMSC_DELIVERY_RECEIPT_REQUESTED)
                self.log.debug(
                    "SubmitSmPDU registered_delivery is set to %s",
                    str(_last_pdu.params['registered_delivery']))

                dlr_level = int(updated_request.args['dlr-level'][0])
                if 'dlr-url' in updated_request.args:
                    dlr_url = updated_request.args['dlr-url'][0]
                else:
                    dlr_url = None
                if updated_request.args['dlr-level'][0] == '1':
                    dlr_level_text = 'SMS-C'
                elif updated_request.args['dlr-level'][0] == '2':
                    dlr_level_text = 'Terminal'
                else:
                    dlr_level_text = 'All'
                dlr_method = updated_request.args['dlr-method'][0]
            else:
                dlr_url = None
                dlr_level = 0
                dlr_level_text = 'No'
                dlr_method = None

            # QoS throttling
            if user.mt_credential.getQuota('http_throughput') >= 0 and user.getCnxStatus().httpapi['qos_last_submit_sm_at'] != 0:
                qos_throughput_second = 1 / float(user.mt_credential.getQuota('http_throughput'))
                qos_throughput_ysecond_td = timedelta(microseconds=qos_throughput_second * 1000000)
                qos_delay = datetime.now() - user.getCnxStatus().httpapi['qos_last_submit_sm_at']
                if qos_delay < qos_throughput_ysecond_td:
                    self.stats.inc('throughput_error_count')
                    self.log.error(
                        "QoS: submit_sm_event is faster (%s) than fixed throughput (%s), user:%s, rejecting message.",
                        qos_delay,
                        qos_throughput_ysecond_td,
                        user)

                    raise ThroughputExceededError("User throughput exceeded")
            user.getCnxStatus().httpapi['qos_last_submit_sm_at'] = datetime.now()

            # Get number of PDUs to be sent (for billing purpose)
            _pdu = routable.pdu
            submit_sm_count = 1
            while hasattr(_pdu, 'nextPdu'):
                _pdu = _pdu.nextPdu
                submit_sm_count += 1

            # Pre-sending submit_sm: Billing processing
            bill = route.getBillFor(user)
            self.log.debug("SubmitSmBill [bid:%s] [ttlamounts:%s] generated for this SubmitSmPDU (x%s)",
                           bill.bid, bill.getTotalAmounts(), submit_sm_count)
            charging_requirements = []
            u_balance = user.mt_credential.getQuota('balance')
            u_subsm_count = user.mt_credential.getQuota('submit_sm_count')
            if u_balance is not None and bill.getTotalAmounts() > 0:
                # Ensure user have enough balance to pay submit_sm and submit_sm_resp
                charging_requirements.append({
                    'condition': bill.getTotalAmounts() * submit_sm_count <= u_balance,
                    'error_message': 'Not enough balance (%s) for charging: %s' % (
                        u_balance, bill.getTotalAmounts())})
            if u_subsm_count is not None:
                # Ensure user have enough submit_sm_count to to cover
                # the bill action (decrement_submit_sm_count)
                charging_requirements.append({
                    'condition': bill.getAction('decrement_submit_sm_count') * submit_sm_count <= u_subsm_count,
                    'error_message': 'Not enough submit_sm_count (%s) for charging: %s' % (
                        u_subsm_count, bill.getAction('decrement_submit_sm_count'))})

            if self.RouterPB.chargeUserForSubmitSms(user, bill, submit_sm_count, charging_requirements) is None:
                self.stats.inc('charging_error_count')
                self.log.error('Charging user %s failed, [bid:%s] [ttlamounts:%s] SubmitSmPDU (x%s)',
                               user, bill.bid, bill.getTotalAmounts(), submit_sm_count)
                raise ChargingError('Cannot charge submit_sm, check RouterPB log file for details')

            ########################################################
            # Send SubmitSmPDU through smpp client manager PB server
            self.log.debug("Connector '%s' is set to be a route for this SubmitSmPDU", routedConnector.cid)
            c = self.SMPPClientManagerPB.perspective_submit_sm(
                cid=routedConnector.cid,
                SubmitSmPDU=routable.pdu,
                submit_sm_bill=bill,
                priority=priority,
                pickled=False,
                dlr_url=dlr_url,
                dlr_level=dlr_level,
                dlr_method=dlr_method)

            # Build final response
            if not c.result:
                self.stats.inc('server_error_count')
                self.log.error('Failed to send SubmitSmPDU to [cid:%s]', routedConnector.cid)
                raise ServerError('Cannot send submit_sm, check SMPPClientManagerPB log file for details')
            else:
                self.stats.inc('success_count')
                self.stats.set('last_success_at', datetime.now())
                self.log.debug('SubmitSmPDU sent to [cid:%s], result = %s', routedConnector.cid, c.result)
                response = {'return': c.result, 'status': 200}
        except Exception, e:
            self.log.error("Error: %s", e)

            if hasattr(e, 'code'):
                response = {'return': e.message, 'status': e.code}
            else:
                response = {'return': "Unknown error: %s" % e, 'status': 500}
        finally:
            self.log.debug("Returning %s to %s.", response, updated_request.getClientIP())
            updated_request.setResponseCode(response['status'])

            # Default return
            _return = 'Error "%s"' % response['return']

            # Success return
            if response['status'] == 200 and routedConnector is not None:
                self.log.info(
                    'SMS-MT [uid:%s] [cid:%s] [msgid:%s] [prio:%s] [dlr:%s] [from:%s] [to:%s] [content:%s]',
                    user.uid,
                    routedConnector.cid,
                    response['return'],
                    priority,
                    dlr_level_text,
                    routable.pdu.params['source_addr'],
                    updated_request.args['to'][0],
                    re.sub(r'[^\x20-\x7E]+', '.', short_message))
                _return = 'Success "%s"' % response['return']

            updated_request.write(_return)
            updated_request.finish()

    def render(self, request):
        """
        /send request processing

        Note: This method MUST behave exactly like jasmin.protocols.smpp.factory.SMPPServerFactory.submit_sm_event
        """

        self.log.debug("Rendering /send response with args: %s from %s", request.args, request.getClientIP())
        request.responseHeaders.addRawHeader(b"content-type", b"text/plain")
        response = {'return': None, 'status': 200}

        self.stats.inc('request_count')
        self.stats.set('last_request_at', datetime.now())

        # updated_request will be filled with default values where request will never get modified
        # updated_request is used for sending the SMS, request is just kept as an original request object
        updated_request = request

        try:
            # Validation (must have almost the same params as /rate service)
            fields = {'to'          : {'optional': False, 'pattern': re.compile(r'^\+{0,1}\d+$')},
                      'from'        : {'optional': True},
                      'coding'      : {'optional': True, 'pattern': re.compile(r'^(0|1|2|3|4|5|6|7|8|9|10|13|14){1}$')},
                      'username'    : {'optional': False, 'pattern': re.compile(r'^.{1,15}$')},
                      'password'    : {'optional': False, 'pattern': re.compile(r'^.{1,8}$')},
                      # Priority validation pattern can be validated/filtered further more
                      # through HttpAPICredentialValidator
                      'priority'    : {'optional': True, 'pattern': re.compile(r'^[0-3]$')},
                      # Validity period validation pattern can be validated/filtered further more
                      # through HttpAPICredentialValidator
                      'validity-period' : {'optional': True, 'pattern': re.compile(r'^\d+$')},
                      'dlr'         : {'optional': False, 'pattern': re.compile(r'^(yes|no)$')},
                      'dlr-url'     : {'optional': True, 'pattern': re.compile(r'^(http|https)\://.*$')},
                      # DLR Level validation pattern can be validated/filtered further more
                      # through HttpAPICredentialValidator
                      'dlr-level'   : {'optional': True, 'pattern': re.compile(r'^[1-3]$')},
                      'dlr-method'  : {'optional': True, 'pattern': re.compile(r'^(get|post)$', re.IGNORECASE)},
                      'tags'        : {'optional': True, 'pattern': re.compile(r'^([-a-zA-Z0-9,])*$')},
                      'content'     : {'optional': True},
                      'hex-content' : {'optional': True}}

            # Default coding is 0 when not provided
            if 'coding' not in updated_request.args:
                updated_request.args['coding'] = ['0']

            # Set default for undefined updated_request.arguments
            if 'dlr-url' in updated_request.args or 'dlr-level' in updated_request.args:
                updated_request.args['dlr'] = ['yes']
            if 'dlr' not in updated_request.args:
                # Setting DLR updated_request to 'no'
                updated_request.args['dlr'] = ['no']

            # Set default values
            if updated_request.args['dlr'][0] == 'yes':
                if 'dlr-level' not in updated_request.args:
                    # If DLR is requested and no dlr-level were provided, assume minimum level (1)
                    updated_request.args['dlr-level'] = [1]
                if 'dlr-method' not in updated_request.args:
                    # If DLR is requested and no dlr-method were provided, assume default (POST)
                    updated_request.args['dlr-method'] = ['POST']

            # DLR method must be uppercase
            if 'dlr-method' in updated_request.args:
                updated_request.args['dlr-method'][0] = updated_request.args['dlr-method'][0].upper()

            # Make validation
            v = UrlArgsValidator(updated_request, fields)
            v.validate()

            # Check if have content --OR-- hex-content
            # @TODO: make this inside UrlArgsValidator !
            if 'content' not in request.args and 'hex-content' not in request.args:
                raise UrlArgsValidationError("content or hex-content not present.")
            elif 'content' in request.args and 'hex-content' in request.args:
                raise UrlArgsValidationError("content and hex-content cannot be used both in same request.")

            # Continue routing in a separate thread
            reactor.callFromThread(self.route_routable, updated_request=updated_request)
        except Exception, e:
            self.log.error("Error: %s", e)

            if hasattr(e, 'code'):
                response = {'return': e.message, 'status': e.code}
            else:
                response = {'return': "Unknown error: %s" % e, 'status': 500}

            self.log.debug("Returning %s to %s.", response, updated_request.getClientIP())
            updated_request.setResponseCode(response['status'])

            return 'Error "%s"' % response['return']
        else:
            return NOT_DONE_YET


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
            if 'hex-content' not in request.args:
                # Convert utf8 to GSM 03.38
                if request.args['coding'][0] == '0':
                    short_message = gsm_encode(request.args['content'][0].decode('utf-8'))
                else:
                    # Otherwise forward it as is
                    short_message = request.args['content'][0]
            else:
                # Otherwise convert hex to bin
                short_message = hex2bin(request.args['hex-content'][0])

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
            user.getCnxStatus().httpapi['rate_request_count'] += 1
            user.getCnxStatus().httpapi['last_activity_at'] = datetime.now()

            # Build SubmitSmPDU
            SubmitSmPDU = self.opFactory.SubmitSM(
                source_add=None if 'from' not in request.args else request.args['from'][0],
                destination_addr=request.args['to'][0],
                short_message=short_message,
                data_coding=int(request.args['coding'][0]),
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
            if 'tags' in request.args:
                tags = request.args['tags'][0].split(',')
                for tag in tags:
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
                elif isinstance(r, str):
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
        except Exception, e:
            self.log.error("Error: %s", e)

            if hasattr(e, 'code'):
                response = {'return': e.message, 'status': e.code}
            else:
                response = {'return': "Unknown error: %s" % e, 'status': 500}
        finally:
            self.log.debug("Returning %s to %s.", response, request.getClientIP())

            # Return message
            if response['return'] is None:
                response['return'] = 'System error'
                request.setResponseCode(500)
            else:
                request.setResponseCode(response['status'])

            request.write(json.dumps(response['return']))
            request.finish()

    def render(self, request):
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
            fields = {'to'          : {'optional': False, 'pattern': re.compile(r'^\+{0,1}\d+$')},
                      'from'        : {'optional': True},
                      'coding'      : {'optional': True, 'pattern': re.compile(r'^(0|1|2|3|4|5|6|7|8|9|10|13|14){1}$')},
                      'username'    : {'optional': False, 'pattern': re.compile(r'^.{1,15}$')},
                      'password'    : {'optional': False, 'pattern': re.compile(r'^.{1,8}$')},
                      # Priority validation pattern can be validated/filtered further more
                      # through HttpAPICredentialValidator
                      'priority'    : {'optional': True, 'pattern': re.compile(r'^[0-3]$')},
                      # Validity period validation pattern can be validated/filtered further more
                      # through HttpAPICredentialValidator
                      'validity-period' :{'optional': True, 'pattern': re.compile(r'^\d+$')},
                      'tags'        : {'optional': True, 'pattern': re.compile(r'^([-a-zA-Z0-9,])*$')},
                      'content'     : {'optional': True},
                      'hex-content' : {'optional': True},
                      }

            # Default coding is 0 when not provided
            if 'coding' not in request.args:
                request.args['coding'] = ['0']

            # Content is optional, defaults to empty content string
            if 'hex-content' not in request.args and 'content' not in request.args:
                request.args['content'] = ['']

            # Make validation
            v = UrlArgsValidator(request, fields)
            v.validate()

            # Check if have content --OR-- hex-content
            # @TODO: make this inside UrlArgsValidator !
            if 'content' in request.args and 'hex-content' in request.args:
                raise UrlArgsValidationError("content and hex-content cannot be used both in same request.")

            # Continue routing in a separate thread
            reactor.callFromThread(self.route_routable, request=request)
        except Exception, e:
            self.log.error("Error: %s", e)

            if hasattr(e, 'code'):
                response = {'return': e.message, 'status': e.code}
            else:
                response = {'return': "Unknown error: %s" % e, 'status': 500}

            self.log.debug("Returning %s to %s.", response, request.getClientIP())

            # Return message
            if response['return'] is None:
                response['return'] = 'System error'
                request.setResponseCode(500)
            else:
                request.setResponseCode(response['status'])
            return json.dumps(response['return'])
        else:
            return NOT_DONE_YET


class Balance(Resource):
    isleaf = True

    def __init__(self, RouterPB, stats, log):
        Resource.__init__(self)

        self.RouterPB = RouterPB
        self.stats = stats
        self.log = log

    def render(self, request):
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
            fields = {'username'    : {'optional': False, 'pattern': re.compile(r'^.{1,15}$')},
                      'password'    : {'optional': False, 'pattern': re.compile(r'^.{1,8}$')}}

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
        except Exception, e:
            self.log.error("Error: %s", e)

            if hasattr(e, 'code'):
                response = {'return': e.message, 'status': e.code}
            else:
                response = {'return': "Unknown error: %s" % e, 'status': 500}
        finally:
            self.log.debug("Returning %s to %s.", response, request.getClientIP())

            # Return message
            if response['return'] is None:
                response['return'] = 'System error'
                request.setResponseCode(500)
            else:
                request.setResponseCode(response['status'])
            return json.dumps(response['return'])


class Ping(Resource):
    isleaf = True

    def __init__(self, log):
        Resource.__init__(self)
        self.log = log

    def render(self, request):
        """
        /ping request processing

        Note: Ping is used to check Jasmin's http api
        """

        self.log.debug("Rendering /ping response with args: %s from %s",
                       request.args, request.getClientIP())
        self.log.info("Received ping from %s", request.getClientIP())
        request.setResponseCode(200)
        return 'Jasmin/PONG'


class HTTPApi(Resource):

    def __init__(self, RouterPB, SMPPClientManagerPB, config, interceptor=None):
        Resource.__init__(self)

        # Setup stats collector
        stats = HttpAPIStatsCollector().get()
        stats.set('created_at', datetime.now())

        # Set up a dedicated logger
        log = logging.getLogger(LOG_CATEGORY)
        if len(log.handlers) != 1:
            log.setLevel(config.log_level)
            handler = TimedRotatingFileHandler(filename=config.log_file, when=config.log_rotate)
            formatter = logging.Formatter(config.log_format, config.log_date_format)
            handler.setFormatter(formatter)
            log.addHandler(handler)
            log.propagate = False

        # Set http url routings
        log.debug("Setting http url routing for /send")
        self.putChild('send', Send(config, RouterPB, SMPPClientManagerPB, stats, log, interceptor))
        log.debug("Setting http url routing for /rate")
        self.putChild('rate', Rate(config, RouterPB, stats, log, interceptor))
        log.debug("Setting http url routing for /balance")
        self.putChild('balance', Balance(RouterPB, stats, log))
        log.debug("Setting http url routing for /ping")
        self.putChild('ping', Ping(log))
