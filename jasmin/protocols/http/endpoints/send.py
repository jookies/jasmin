from datetime import datetime, timedelta
import re
import json
import pickle

from twisted.internet import reactor, defer
from twisted.web.resource import Resource
from twisted.web.server import NOT_DONE_YET
from smpp.pdu.constants import priority_flag_value_map
from smpp.pdu.smpp_time import parse
from smpp.pdu.pdu_types import RegisteredDeliveryReceipt, RegisteredDelivery

from jasmin.routing.Routables import RoutableSubmitSm
from jasmin.protocols.smpp.configs import SMPPClientConfig
from jasmin.protocols.smpp.operations import SMPPOperationFactory
from jasmin.protocols.http.errors import UrlArgsValidationError
from jasmin.protocols.http.validation import UrlArgsValidator, HttpAPICredentialValidator
from jasmin.protocols.http.errors import (HttpApiError, AuthenticationError, ServerError, RouteNotFoundError, ConnectorNotFoundError,
                     ChargingError, ThroughputExceededError, InterceptorNotSetError,
                     InterceptorNotConnectedError, InterceptorRunError)
from jasmin.protocols.http.endpoints import hex2bin, authenticate_user


def update_submit_sm_pdu(routable, config, config_update_params=None):
    """Will set pdu parameters from smppclient configuration.
    Parameters that were locked through the routable.lockPduParam() method will not be updated.
    config parameter can be the connector config object or just a simple dict"""

    if config_update_params is None:
        # Set default config params to get from the config object
        config_update_params = [
            'protocol_id',
            'replace_if_present_flag',
            'dest_addr_ton',
            'source_addr_npi',
            'dest_addr_npi',
            'service_type',
            'source_addr_ton',
            'sm_default_msg_id',
        ]

    for param in config_update_params:
        _pdu = routable.pdu

        # Force setting param in main pdu
        if not routable.pduParamIsLocked(param):
            if isinstance(config, SMPPClientConfig) and hasattr(config, param):
                _pdu.params[param] = getattr(config, param)
            elif isinstance(config, dict) and param in config:
                _pdu.params[param] = config[param]

        # Force setting param in sub-pdus (multipart use case)
        while hasattr(_pdu, 'nextPdu'):
            _pdu = _pdu.nextPdu
            if not routable.pduParamIsLocked(param):
                if isinstance(config, SMPPClientConfig) and hasattr(config, param):
                    _pdu.params[param] = getattr(config, param)
                elif isinstance(config, dict) and param in config:
                    _pdu.params[param] = config[param]

    return routable


class Send(Resource):
    isleaf = True

    def __init__(self, HTTPApiConfig, RouterPB, SMPPClientManagerPB, stats, log, interceptorpb_client):
        Resource.__init__(self)

        self.SMPPClientManagerPB = SMPPClientManagerPB
        self.RouterPB = RouterPB
        self.stats = stats
        self.log = log
        self.interceptorpb_client = interceptorpb_client
        self.config = HTTPApiConfig

        # opFactory is initiated with a dummy SMPPClientConfig used for building SubmitSm only
        self.opFactory = SMPPOperationFactory(long_content_max_parts=HTTPApiConfig.long_content_max_parts,
                                              long_content_split=HTTPApiConfig.long_content_split)

    @defer.inlineCallbacks
    def route_routable(self, updated_request):
        try:
            # Do we have a hex-content ?
            if b'hex-content' not in updated_request.args:
                # Convert utf8 to GSM 03.38
                if updated_request.args[b'coding'][0] == b'0':
                    if isinstance(updated_request.args[b'content'][0], bytes):
                        short_message = updated_request.args[b'content'][0].decode().encode('gsm0338', 'replace')
                    else:
                        short_message = updated_request.args[b'content'][0].encode('gsm0338', 'replace')
                    updated_request.args[b'content'][0] = short_message
                else:
                    # Otherwise forward it as is
                    short_message = updated_request.args[b'content'][0]
            else:
                # Otherwise convert hex to bin
                short_message = hex2bin(updated_request.args[b'hex-content'][0])

            # Authentication
            user = authenticate_user(
                updated_request.args[b'username'][0],
                updated_request.args[b'password'][0],
                self.RouterPB,
                self.stats,
                self.log
            )

            # Update CnxStatus
            user.getCnxStatus().httpapi['connects_count'] += 1
            user.getCnxStatus().httpapi['submit_sm_request_count'] += 1
            user.getCnxStatus().httpapi['last_activity_at'] = datetime.now()

            # Build SubmitSmPDU
            SubmitSmPDU = self.opFactory.SubmitSM(
                source_addr=None if b'from' not in updated_request.args else updated_request.args[b'from'][0],
                destination_addr=updated_request.args[b'to'][0],
                short_message=short_message,
                data_coding=int(updated_request.args[b'coding'][0]),
                custom_tlvs=updated_request.args[b'custom_tlvs'][0])
            self.log.debug("Built base SubmitSmPDU: %s", SubmitSmPDU)

            # Make Credential validation
            v = HttpAPICredentialValidator('Send', user, updated_request, submit_sm=SubmitSmPDU)
            v.validate()

            # Update SubmitSmPDU by default values from user MtMessagingCredential
            SubmitSmPDU = v.updatePDUWithUserDefaults(SubmitSmPDU)

            # Prepare for interception then routing
            routedConnector = None  # init
            routable = RoutableSubmitSm(SubmitSmPDU, user)
            self.log.debug("Built Routable %s for SubmitSmPDU: %s", routable, SubmitSmPDU)

            # Should we tag the routable ?
            tags = []
            if b'tags' in updated_request.args:
                tags = updated_request.args[b'tags'][0].split(b',')
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

            # Set a placeholder for any parameter update to be applied on the pdu(s)
            param_updates = {}

            # Set priority
            priority = 0
            if b'priority' in updated_request.args:
                priority = int(updated_request.args[b'priority'][0])
                param_updates['priority_flag'] = priority_flag_value_map[priority]
            self.log.debug("SubmitSmPDU priority is set to %s", priority)

            # Set schedule_delivery_time
            if b'sdt' in updated_request.args:
                param_updates['schedule_delivery_time'] = parse(updated_request.args[b'sdt'][0])
                self.log.debug(
                    "SubmitSmPDU schedule_delivery_time is set to %s (%s)",
                    routable.pdu.params['schedule_delivery_time'],
                    updated_request.args[b'sdt'][0])

            # Set validity_period
            if b'validity-period' in updated_request.args:
                delta = timedelta(minutes=int(updated_request.args[b'validity-period'][0]))
                param_updates['validity_period'] = datetime.today() + delta
                self.log.debug(
                    "SubmitSmPDU validity_period is set to %s (+%s minutes)",
                    routable.pdu.params['validity_period'],
                    updated_request.args[b'validity-period'][0])

            # Got any updates to apply on pdu(s) ?
            if len(param_updates) > 0:
                routable = update_submit_sm_pdu(routable=routable, config=param_updates,
                                                config_update_params=list(param_updates))

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
            if updated_request.args[b'dlr'][0] == b'yes':
                _last_pdu.params['registered_delivery'] = RegisteredDelivery(
                    RegisteredDeliveryReceipt.SMSC_DELIVERY_RECEIPT_REQUESTED)
                self.log.debug(
                    "SubmitSmPDU registered_delivery is set to %s",
                    str(_last_pdu.params['registered_delivery']))

                dlr_level = int(updated_request.args[b'dlr-level'][0])
                if b'dlr-url' in updated_request.args:
                    dlr_url = updated_request.args[b'dlr-url'][0]
                else:
                    dlr_url = None
                if updated_request.args[b'dlr-level'][0] == b'1':
                    dlr_level_text = 'SMS-C'
                elif updated_request.args[b'dlr-level'][0] == b'2':
                    dlr_level_text = 'Terminal'
                else:
                    dlr_level_text = 'All'
                dlr_method = updated_request.args[b'dlr-method'][0]
            else:
                dlr_url = None
                dlr_level = 0
                dlr_level_text = 'No'
                dlr_method = None

            # QoS throttling
            if (user.mt_credential.getQuota('http_throughput') and user.mt_credential.getQuota('http_throughput') >= 0) and user.getCnxStatus().httpapi[
                'qos_last_submit_sm_at'] != 0:
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
                uid=user.uid,
                cid=routedConnector.cid,
                SubmitSmPDU=routable.pdu,
                submit_sm_bill=bill,
                priority=priority,
                pickled=False,
                dlr_url=dlr_url,
                dlr_level=dlr_level,
                dlr_method=dlr_method,
                dlr_connector=routedConnector.cid)

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
        except HttpApiError as e:
            self.log.error("Error: %s", e)
            response = {'return': e.message, 'status': e.code}
        except Exception as e:
            self.log.error("Error: %s", e)
            response = {'return': "Unknown error: %s" % e, 'status': 500}
            raise
        finally:
            self.log.debug("Returning %s to %s.", response, updated_request.getClientIP())
            updated_request.setResponseCode(response['status'])

            # Default return
            _return = 'Error "%s"' % response['return']

            # Success return
            if response['status'] == 200 and routedConnector is not None:
                # Do not log text for privacy reasons
                # Added in #691
                if self.config.log_privacy:
                    logged_content = '** %s byte content **' % len(short_message)
                else:
                    if isinstance(short_message, str):
                        short_message = short_message.encode()
                    logged_content = '%r' % re.sub(rb'[^\x20-\x7E]+', b'.', short_message)

                self.log.info(
                    'SMS-MT [uid:%s] [cid:%s] [msgid:%s] [prio:%s] [dlr:%s] [from:%s] [to:%s] [content:%s]',
                    user.uid,
                    routedConnector.cid,
                    response['return'],
                    priority,
                    dlr_level_text,
                    routable.pdu.params['source_addr'],
                    updated_request.args[b'to'][0],
                    logged_content)

                _return = 'Success "%s"' % response['return']

            updated_request.write(_return.encode())
            updated_request.finish()

    def render_POST(self, request):
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
            fields = {b'to': {'optional': False, 'pattern': re.compile(rb'^\+{0,1}\d+$')},
                      b'from': {'optional': True},
                      b'coding': {'optional': True, 'pattern': re.compile(rb'^(0|1|2|3|4|5|6|7|8|9|10|13|14){1}$')},
                      b'username': {'optional': False, 'pattern': re.compile(rb'^.{1,16}$')},
                      b'password': {'optional': False, 'pattern': re.compile(rb'^.{1,16}$')},
                      # Priority validation pattern can be validated/filtered further more
                      # through HttpAPICredentialValidator
                      b'priority': {'optional': True, 'pattern': re.compile(rb'^[0-3]$')},
                      b'sdt': {'optional': True,
                              'pattern': re.compile(rb'^\d{2}\d{2}\d{2}\d{2}\d{2}\d{2}\d{1}\d{2}(\+|-|R)$')},
                      # Validity period validation pattern can be validated/filtered further more
                      # through HttpAPICredentialValidator
                      b'validity-period': {'optional': True, 'pattern': re.compile(rb'^\d+$')},
                      b'dlr': {'optional': False, 'pattern': re.compile(rb'^(yes|no)$')},
                      b'dlr-url': {'optional': True, 'pattern': re.compile(rb'^(http|https)\://.*$')},
                      # DLR Level validation pattern can be validated/filtered further more
                      # through HttpAPICredentialValidator
                      b'dlr-level'   : {'optional': True, 'pattern': re.compile(rb'^[1-3]$')},
                      b'dlr-method'  : {'optional': True, 'pattern': re.compile(rb'^(get|post)$', re.IGNORECASE)},
                      b'tags'        : {'optional': True, 'pattern': re.compile(rb'^([-a-zA-Z0-9,])*$')},
                      b'content'     : {'optional': True},
                      b'hex-content' : {'optional': True},
                      b'custom_tlvs' : {'optional': True}}

            if updated_request.getHeader(b'content-type') == b'application/json':
                json_body = updated_request.content.read()
                json_data = json.loads(json_body)
                for key, value in json_data.items():
                    # Make the values look like they came from form encoding all surrounded by [ ]
                    if isinstance(value, str):
                        value = value.encode()

                    if isinstance(key, str):
                        key = key.encode()

                    updated_request.args[key] = [value]

            # If no custom TLVs present, defaujlt to an [] which will be passed down to SubmitSM
            if b'custom_tlvs' not in updated_request.args:
                updated_request.args[b'custom_tlvs'] = [[]]

            # Default coding is 0 when not provided
            if b'coding' not in updated_request.args:
                updated_request.args[b'coding'] = [b'0']

            # Set default for undefined updated_request.arguments
            if b'dlr-url' in updated_request.args or b'dlr-level' in updated_request.args:
                updated_request.args[b'dlr'] = [b'yes']
            if b'dlr' not in updated_request.args:
                # Setting DLR updated_request to 'no'
                updated_request.args[b'dlr'] = [b'no']

            # Set default values
            if updated_request.args[b'dlr'][0] == b'yes':
                if b'dlr-level' not in updated_request.args:
                    # If DLR is requested and no dlr-level were provided, assume minimum level (1)
                    updated_request.args[b'dlr-level'] = [1]
                if b'dlr-method' not in updated_request.args:
                    # If DLR is requested and no dlr-method were provided, assume default (POST)
                    updated_request.args[b'dlr-method'] = [b'POST']

            # DLR method must be uppercase
            if b'dlr-method' in updated_request.args:
                updated_request.args[b'dlr-method'][0] = updated_request.args[b'dlr-method'][0].upper()

            # Make validation
            v = UrlArgsValidator(updated_request, fields)
            v.validate()

            # Check if have content --OR-- hex-content
            # @TODO: make this inside UrlArgsValidator !
            if b'content' not in request.args and b'hex-content' not in request.args:
                raise UrlArgsValidationError("content or hex-content not present.")
            elif b'content' in request.args and b'hex-content' in request.args:
                raise UrlArgsValidationError("content and hex-content cannot be used both in same request.")

            # Continue routing in a separate thread
            reactor.callFromThread(self.route_routable, updated_request=updated_request)
        except HttpApiError as e:
            self.log.error("Error: %s", e)
            response = {'return': e.message, 'status': e.code}

            self.log.debug("Returning %s to %s.", response, updated_request.getClientIP())
            updated_request.setResponseCode(response['status'])

            return b'Error "%s"' % (response['return'] if isinstance(response['return'], bytes) else response['return'].encode())
        except Exception as e:
            self.log.error("Error: %s", e)
            response = {'return': "Unknown error: %s" % e, 'status': 500}

            self.log.debug("Returning %s to %s.", response, updated_request.getClientIP())
            updated_request.setResponseCode(response['status'])

            return b'Error "%s"' % response['return'].encode()
        else:
            return NOT_DONE_YET

    def render_GET(self, request):
        """Allow GET /send for backward compatibility"""
        return self.render_POST(request)
