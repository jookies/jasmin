import logging
import re
import json
from logging.handlers import TimedRotatingFileHandler
from datetime import datetime, timedelta
from twisted.web.resource import Resource
from jasmin.vendor.smpp.pdu.constants import priority_flag_value_map
from jasmin.vendor.smpp.pdu.pdu_types import RegisteredDeliveryReceipt, RegisteredDelivery
from jasmin.protocols.smpp.operations import SMPPOperationFactory
from jasmin.routing.Routables import RoutableSubmitSm
from .errors import (AuthenticationError, 
                     ServerError, 
                     RouteNotFoundError, 
                     ChargingError,
                     ThroughputExceededError,
                    )
from .validation import UrlArgsValidator, HttpAPICredentialValidator
from .stats import HttpAPIStatsCollector

LOG_CATEGORY = "jasmin-http-api"

class Send(Resource):
    isleaf = True

    def __init__(self, HTTPApiConfig, RouterPB, SMPPClientManagerPB, stats, log):
        Resource.__init__(self)
        
        self.SMPPClientManagerPB = SMPPClientManagerPB
        self.RouterPB = RouterPB
        self.log = log
        self.stats = stats

        # opFactory is initiated with a dummy SMPPClientConfig used for building SubmitSm only
        self.opFactory = SMPPOperationFactory(long_content_max_parts = HTTPApiConfig.long_content_max_parts,
                                              long_content_split = HTTPApiConfig.long_content_split)
    
    def render(self, request):
        """
        /send request processing

        Note: This method MUST behave exactly like jasmin.protocols.smpp.factory.SMPPServerFactory.submit_sm_event
        """
        
        self.log.debug("Rendering /send response with args: %s from %s" % (
                                                                           request.args, 
                                                                           request.getClientIP()))
        request.responseHeaders.addRawHeader(b"content-type", b"text/plain")
        response = {'return': None, 'status': 200}
        
        self.stats.inc('request_count')
        self.stats.set('last_request_at', datetime.now())

        # updated_request will be filled with default values where request will never get modified
        # updated_request is used for sending the SMS, request is just kept as an original request object
        updated_request = request
        
        try:
            # Validation (must have almost the same params as /rate service)
            fields = {'to'          :{'optional': False,    'pattern': re.compile(r'^\+{0,1}\d+$')}, 
                      'from'        :{'optional': True},
                      'coding'      :{'optional': True,     'pattern': re.compile(r'^(0|1|2|3|4|5|6|7|8|9|10|13|14){1}$')},
                      'username'    :{'optional': False,    'pattern': re.compile(r'^.{1,15}$')},
                      'password'    :{'optional': False,    'pattern': re.compile(r'^.{1,8}$')},
                      # Priority validation pattern can be validated/filtered further more through HttpAPICredentialValidator
                      'priority'    :{'optional': True,     'pattern': re.compile(r'^[0-3]$')},
                      # Validity period validation pattern can be validated/filtered further more through HttpAPICredentialValidator
                      'validity-period' :{'optional': True,     'pattern': re.compile(r'^\d+$')},
                      'dlr'         :{'optional': False,    'pattern': re.compile(r'^(yes|no)$')},
                      'dlr-url'     :{'optional': True,     'pattern': re.compile(r'^(http|https)\://.*$')},
                      # DLR Level validation pattern can be validated/filtered further more through HttpAPICredentialValidator
                      'dlr-level'   :{'optional': True,     'pattern': re.compile(r'^[1-3]$')},
                      'dlr-method'  :{'optional': True,     'pattern': re.compile(r'^(get|post)$', re.IGNORECASE)},
                      'content'     :{'optional': False},
                      }
            
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
            
            # Authentication
            user = self.RouterPB.authenticateUser(username = updated_request.args['username'][0], password = updated_request.args['password'][0])
            if user is None:
                self.stats.inc('auth_error_count')
                
                self.log.debug("Authentication failure for username:%s and password:%s" % (
                    updated_request.args['username'][0], updated_request.args['password'][0]))
                self.log.error("Authentication failure for username:%s" % 
                    updated_request.args['username'][0])
                raise AuthenticationError('Authentication failure for username:%s' % 
                    updated_request.args['username'][0])
            
            # Update CnxStatus
            user.getCnxStatus().httpapi['connects_count']+= 1
            user.getCnxStatus().httpapi['submit_sm_request_count']+= 1
            user.getCnxStatus().httpapi['last_activity_at'] = datetime.now()

            # Build SubmitSmPDU
            SubmitSmPDU = self.opFactory.SubmitSM(
                source_addr = None if 'from' not in updated_request.args else updated_request.args['from'][0],
                destination_addr = updated_request.args['to'][0],
                short_message = updated_request.args['content'][0],
                data_coding = int(updated_request.args['coding'][0]),
            )
            self.log.debug("Built base SubmitSmPDU: %s" % SubmitSmPDU)
            
            # Make Credential validation
            v = HttpAPICredentialValidator('Send', user, request, submit_sm = SubmitSmPDU)
            v.validate()
            
            # Update SubmitSmPDU by default values from user MtMessagingCredential
            SubmitSmPDU = v.updatePDUWithUserDefaults(SubmitSmPDU)

            # Routing
            routedConnector = None # init
            routable = RoutableSubmitSm(SubmitSmPDU, user)
            route = self.RouterPB.getMTRoutingTable().getRouteFor(routable)
            if route is None:
                self.stats.inc('route_error_count')
                self.log.error("No route matched from user %s for SubmitSmPDU: %s" % (user, SubmitSmPDU))
                raise RouteNotFoundError("No route found")

            # Get connector from selected route
            self.log.debug("RouterPB selected %s for this SubmitSmPDU" % route)
            routedConnector = route.getConnector()
            
            # Set priority
            priority = 0
            if 'priority' in updated_request.args:
                priority = int(updated_request.args['priority'][0])
                SubmitSmPDU.params['priority_flag'] = priority_flag_value_map[priority]
            self.log.debug("SubmitSmPDU priority is set to %s" % priority)

            # Set validity_period
            if 'validity-period' in updated_request.args:
                delta = timedelta(minutes=int(updated_request.args['validity-period'][0]))
                SubmitSmPDU.params['validity_period'] = datetime.today() + delta
                self.log.debug("SubmitSmPDU validity_period is set to %s (+%s minutes)" % (
                    SubmitSmPDU.params['validity_period'],
                    updated_request.args['validity-period'][0]))

            # Set DLR bit mask
            # DLR setting is clearly described in #107
            SubmitSmPDU.params['registered_delivery'] = RegisteredDelivery(RegisteredDeliveryReceipt.NO_SMSC_DELIVERY_RECEIPT_REQUESTED)
            if updated_request.args['dlr'][0] == 'yes':
                SubmitSmPDU.params['registered_delivery'] = RegisteredDelivery(RegisteredDeliveryReceipt.SMSC_DELIVERY_RECEIPT_REQUESTED)
                self.log.debug("SubmitSmPDU registered_delivery is set to %s" % str(SubmitSmPDU.params['registered_delivery']))

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
                qos_throughput_ysecond_td = timedelta( microseconds = qos_throughput_second * 1000000)
                qos_delay = datetime.now() - user.getCnxStatus().httpapi['qos_last_submit_sm_at']
                if qos_delay < qos_throughput_ysecond_td:
                    self.stats.inc('throughput_error_count')
                    self.log.error("QoS: submit_sm_event is faster (%s) than fixed throughput (%s) for user (%s), rejecting message." % (
                                qos_delay,
                                qos_throughput_ysecond_td,
                                user
                                ))

                    raise ThroughputExceededError("User throughput exceeded")
            user.getCnxStatus().httpapi['qos_last_submit_sm_at'] = datetime.now()

            # Get number of PDUs to be sent (for billing purpose)
            _pdu = SubmitSmPDU
            submit_sm_count = 1
            while hasattr(_pdu, 'nextPdu'):
                _pdu = _pdu.nextPdu
                submit_sm_count += 1
                
            # Pre-sending submit_sm: Billing processing
            bill = route.getBillFor(user)
            self.log.debug("SubmitSmBill [bid:%s] [ttlamounts:%s] generated for this SubmitSmPDU (x%s)" % 
                                                                (bill.bid, bill.getTotalAmounts(), submit_sm_count))
            charging_requirements = []
            u_balance = user.mt_credential.getQuota('balance')
            u_subsm_count = user.mt_credential.getQuota('submit_sm_count')
            if u_balance is not None and bill.getTotalAmounts() > 0:
                # Ensure user have enough balance to pay submit_sm and submit_sm_resp
                charging_requirements.append({'condition': bill.getTotalAmounts() * submit_sm_count <= u_balance,
                                              'error_message': 'Not enough balance (%s) for charging: %s' % 
                                              (u_balance, bill.getTotalAmounts())})
            if u_subsm_count is not None:
                # Ensure user have enough submit_sm_count to to cover the bill action (decrement_submit_sm_count)
                charging_requirements.append({'condition': bill.getAction('decrement_submit_sm_count') * submit_sm_count <= u_subsm_count,
                                              'error_message': 'Not enough submit_sm_count (%s) for charging: %s' % 
                                              (u_subsm_count, bill.getAction('decrement_submit_sm_count'))})

            if self.RouterPB.chargeUserForSubmitSms(user, bill, submit_sm_count, charging_requirements) is None:
                self.stats.inc('charging_error_count')
                self.log.error('Charging user %s failed, [bid:%s] [ttlamounts:%s] SubmitSmPDU (x%s)' % 
                                                                (user, bill.bid, bill.getTotalAmounts(), submit_sm_count))
                raise ChargingError('Cannot charge submit_sm, check RouterPB log file for details')
            
            ########################################################
            # Send SubmitSmPDU through smpp client manager PB server
            self.log.debug("Connector '%s' is set to be a route for this SubmitSmPDU" % routedConnector.cid)
            c = self.SMPPClientManagerPB.perspective_submit_sm(routedConnector.cid, 
                                                               SubmitSmPDU, 
                                                               priority, 
                                                               pickled = False, 
                                                               dlr_url = dlr_url, 
                                                               dlr_level = dlr_level,
                                                               dlr_method = dlr_method,
                                                               submit_sm_resp_bill = bill.getSubmitSmRespBill())
            
            # Build final response
            if not c.result:
                self.stats.inc('server_error_count')
                self.log.error('Failed to send SubmitSmPDU to [cid:%s]' % routedConnector.cid)
                raise ServerError('Cannot send submit_sm, check SMPPClientManagerPB log file for details')
            else:
                self.stats.inc('success_count')
                self.stats.set('last_success_at', datetime.now())
                self.log.debug('SubmitSmPDU sent to [cid:%s], result = %s' % (routedConnector.cid, c.result))
                response = {'return': c.result, 'status': 200}
        except Exception, e:
            self.log.error("Error: %s" % e)
            
            if hasattr(e, 'code'):
                response = {'return': e.message, 'status': e.code}
            else:
                response = {'return': "Unknown error: %s" % e, 'status': 500}
        finally:
            self.log.debug("Returning %s to %s." % (response, updated_request.getClientIP()))
            updated_request.setResponseCode(response['status'])
            
            # Default return
            _return = 'Error "%s"' % response['return']

            # Success return
            if response['status'] == 200 and routedConnector is not None:
                self.log.info('SMS-MT [uid:%s] [cid:%s] [msgid:%s] [prio:%s] [dlr:%s] [from:%s] [to:%s] [content:%s]' 
                              % (user.uid,
                              routedConnector.cid,
                              response['return'], 
                              priority, 
                              dlr_level_text, 
                              SubmitSmPDU.params['source_addr'], 
                              updated_request.args['to'][0], 
                              re.sub(r'[^\x20-\x7E]+','.', updated_request.args['content'][0])))
                _return = 'Success "%s"' % response['return']
            
            return _return
    
class Rate(Resource):
    isleaf = True

    def __init__(self, HTTPApiConfig, RouterPB, stats, log):
        Resource.__init__(self)
        
        self.RouterPB = RouterPB
        self.log = log
        self.stats = stats

        # opFactory is initiated with a dummy SMPPClientConfig used for building SubmitSm only
        self.opFactory = SMPPOperationFactory(long_content_max_parts = HTTPApiConfig.long_content_max_parts,
                                              long_content_split = HTTPApiConfig.long_content_split)
    
    def render(self, request):
        """
        /rate request processing

        Note: This method will indicate the rate of the message once sent
        """
        
        self.log.debug("Rendering /rate response with args: %s from %s" % (
                                                                           request.args, 
                                                                           request.getClientIP()))
        request.responseHeaders.addRawHeader(b"content-type", b"application/json")
        response = {'return': None, 'status': 200}
        
        self.stats.inc('request_count')
        self.stats.set('last_request_at', datetime.now())

        try:
            # Validation (must be almost the same params as /send service)
            fields = {'to'          :{'optional': False,    'pattern': re.compile(r'^\+{0,1}\d+$')}, 
                      'from'        :{'optional': True},
                      'coding'      :{'optional': True,     'pattern': re.compile(r'^(0|1|2|3|4|5|6|7|8|9|10|13|14){1}$')},
                      'username'    :{'optional': False,    'pattern': re.compile(r'^.{1,15}$')},
                      'password'    :{'optional': False,    'pattern': re.compile(r'^.{1,8}$')},
                      # Priority validation pattern can be validated/filtered further more through HttpAPICredentialValidator
                      'priority'    :{'optional': True,     'pattern': re.compile(r'^[0-3]$')},
                      # Validity period validation pattern can be validated/filtered further more through HttpAPICredentialValidator
                      'validity-period' :{'optional': True,     'pattern': re.compile(r'^\d+$')},
                      'content'     :{'optional': True},
                      }
            
            # Default coding is 0 when not provided
            if 'coding' not in request.args:
                request.args['coding'] = ['0']
            
            # Content is optional, defaults to empty string
            if 'content' not in request.args:
                request.args['content'] = ['']

            # Make validation
            v = UrlArgsValidator(request, fields)
            v.validate()
            
            # Authentication
            user = self.RouterPB.authenticateUser(username = request.args['username'][0], password = request.args['password'][0])
            if user is None:
                self.stats.inc('auth_error_count')
                
                self.log.debug("Authentication failure for username:%s and password:%s" % (
                    request.args['username'][0], request.args['password'][0]))
                self.log.error("Authentication failure for username:%s" % 
                    request.args['username'][0])
                raise AuthenticationError('Authentication failure for username:%s' % 
                    request.args['username'][0])
            
            # Update CnxStatus
            user.getCnxStatus().httpapi['connects_count']+= 1
            user.getCnxStatus().httpapi['rate_request_count']+= 1
            user.getCnxStatus().httpapi['last_activity_at'] = datetime.now()

            # Build SubmitSmPDU
            SubmitSmPDU = self.opFactory.SubmitSM(
                source_addr = None if 'from' not in request.args else request.args['from'][0],
                destination_addr = request.args['to'][0],
                short_message = request.args['content'][0],
                data_coding = int(request.args['coding'][0]),
            )
            self.log.debug("Built base SubmitSmPDU: %s" % SubmitSmPDU)
            
            # Make Credential validation
            v = HttpAPICredentialValidator('Rate', user, request, submit_sm = SubmitSmPDU)
            v.validate()
            
            # Update SubmitSmPDU by default values from user MtMessagingCredential
            SubmitSmPDU = v.updatePDUWithUserDefaults(SubmitSmPDU)

            # Routing
            routedConnector = None # init
            routable = RoutableSubmitSm(SubmitSmPDU, user)
            route = self.RouterPB.getMTRoutingTable().getRouteFor(routable)
            if route is None:
                self.log.error("No route matched from user %s for SubmitSmPDU: %s" % (user, SubmitSmPDU))
                raise RouteNotFoundError("No route found")

            # Get connector from selected route
            self.log.debug("RouterPB selected %s for this SubmitSmPDU" % route)
            routedConnector = route.getConnector()
            
            # Get number of PDUs to be sent (for billing purpose)
            _pdu = SubmitSmPDU
            submit_sm_count = 1
            while hasattr(_pdu, 'nextPdu'):
                _pdu = _pdu.nextPdu
                submit_sm_count += 1
                
            # Get the bill
            bill = route.getBillFor(user)

            response = {'return': {'unit_rate': bill.getTotalAmounts(), 'submit_sm_count': submit_sm_count}, 'status': 200}
        except Exception, e:
            self.log.error("Error: %s" % e)
            
            if hasattr(e, 'code'):
                response = {'return': e.message, 'status': e.code}
            else:
                response = {'return': "Unknown error: %s" % e, 'status': 500}
        finally:
            self.log.debug("Returning %s to %s." % (response, request.getClientIP()))

            # Return message
            if response['return'] is None:
                response['return'] = 'System error'
                request.setResponseCode(500)
            else:
                request.setResponseCode(response['status'])
            return json.dumps(response['return'])

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
        
        self.log.debug("Rendering /balance response with args: %s from %s" % (
                                                                           request.args, 
                                                                           request.getClientIP()))
        request.responseHeaders.addRawHeader(b"content-type", b"application/json")
        response = {'return': None, 'status': 200}
        
        self.stats.inc('request_count')
        self.stats.set('last_request_at', datetime.now())

        try:
            # Validation
            fields = {'username'    :{'optional': False,    'pattern': re.compile(r'^.{1,15}$')},
                      'password'    :{'optional': False,    'pattern': re.compile(r'^.{1,8}$')},
                      }
                        
            # Make validation
            v = UrlArgsValidator(request, fields)
            v.validate()
            
            # Authentication
            user = self.RouterPB.authenticateUser(username = request.args['username'][0], password = request.args['password'][0])
            if user is None:
                self.stats.inc('auth_error_count')
                
                self.log.debug("Authentication failure for username:%s and password:%s" % (
                    request.args['username'][0], request.args['password'][0]))
                self.log.error("Authentication failure for username:%s" % 
                    request.args['username'][0])
                raise AuthenticationError('Authentication failure for username:%s' % 
                    request.args['username'][0])
            
            # Update CnxStatus
            user.getCnxStatus().httpapi['connects_count']+= 1
            user.getCnxStatus().httpapi['balance_request_count']+= 1
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
            self.log.error("Error: %s" % e)
            
            if hasattr(e, 'code'):
                response = {'return': e.message, 'status': e.code}
            else:
                response = {'return': "Unknown error: %s" % e, 'status': 500}
        finally:
            self.log.debug("Returning %s to %s." % (response, request.getClientIP()))

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
        
        self.log.debug("Rendering /ping response with args: %s from %s" % (
                                                                           request.args, 
                                                                           request.getClientIP()))
        self.log.info("Received ping from %s" % request.getClientIP())
        request.setResponseCode(200)
        return 'Jasmin/PONG'

class HTTPApi(Resource):
    
    def __init__(self, RouterPB, SMPPClientManagerPB, config):
        Resource.__init__(self)
        
        self.config = config
        self.SMPPClientManagerPB = SMPPClientManagerPB
        self.RouterPB = RouterPB

        # Setup stats collector
        self.stats = HttpAPIStatsCollector().get()
        self.stats.set('created_at', datetime.now())

        # Set up a dedicated logger
        self.log = logging.getLogger(LOG_CATEGORY)
        if len(self.log.handlers) != 1:
            self.log.setLevel(config.log_level)
            handler = TimedRotatingFileHandler(filename=self.config.log_file, 
                when = self.config.log_rotate)
            formatter = logging.Formatter(config.log_format, config.log_date_format)
            handler.setFormatter(formatter)
            self.log.addHandler(handler)
            self.log.propagate = False

        # Set http url routings
        self.log.debug("Setting http url routing for /send")
        self.putChild('send', Send(self.config, self.RouterPB, self.SMPPClientManagerPB, self.stats, self.log))
        self.log.debug("Setting http url routing for /rate")
        self.putChild('rate', Rate(self.config, self.RouterPB, self.stats, self.log))
        self.log.debug("Setting http url routing for /balance")
        self.putChild('balance', Balance(self.RouterPB, self.stats, self.log))
        self.log.debug("Setting http url routing for /ping")
        self.putChild('ping', Ping(self.log))