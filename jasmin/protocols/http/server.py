"""
This is the http server module serving the /send API
"""

import logging
import re
from twisted.web.resource import Resource
from jasmin.vendor.smpp.pdu.constants import priority_flag_value_map
from jasmin.vendor.smpp.pdu.pdu_types import RegisteredDeliveryReceipt, RegisteredDelivery
from jasmin.protocols.smpp.operations import SMPPOperationFactory
from jasmin.routing.Routables import RoutableSubmitSm
from jasmin.protocols.http.errors import AuthenticationError, ServerError, RouteNotFoundError, ChargingError
from jasmin.protocols.http.validation import UrlArgsValidator, CredentialValidator
from jasmin.routing.Bills import SubmitSmRespBill

LOG_CATEGORY = "jasmin-http-api"

class Send(Resource):
    def __init__(self, HTTPApiConfig, RouterPB, SMPPClientManagerPB, log):
        Resource.__init__(self)
        
        self.SMPPClientManagerPB = SMPPClientManagerPB
        self.RouterPB = RouterPB
        self.log = log
        
        # opFactory is initiated with a dummy SMPPClientConfig used for building SubmitSm only
        self.opFactory = SMPPOperationFactory(long_content_max_parts = HTTPApiConfig.long_content_max_parts,
                                              long_content_split = HTTPApiConfig.long_content_split)
    
    def render(self, request):
        """
        /send request processing
        """
        
        self.log.debug("Rendering /send response with args: %s from %s" % (
                                                                           request.args, 
                                                                           request.getClientIP()))
        response = {'return': None, 'status': 200}
        
        # updated_request will be filled with default values where request will never get modified
        # updated_request is used for sending the SMS, request is just kept as an original request object
        updated_request = request
        
        try:
            # Validation
            fields = {'to'          :{'optional': False,    'pattern': re.compile(r'^\+{0,1}\d+$')}, 
                      'from'        :{'optional': True},
                      'coding'      :{'optional': True,     'pattern': re.compile(r'^(0|1|2|3|4|5|6|7|8|9|10|13|14){1}$')},
                      'username'    :{'optional': False,    'pattern': re.compile(r'^.{1,30}$')},
                      'password'    :{'optional': False,    'pattern': re.compile(r'^.{1,30}$')},
                      # Priority validation pattern can be validated/filtered further more through CredentialValidator
                      'priority'    :{'optional': True,     'pattern': re.compile(r'^[0-3]$')},
                      'dlr'         :{'optional': False,    'pattern': re.compile(r'^(yes|no)$')},
                      'dlr-url'     :{'optional': True,     'pattern': re.compile(r'^(http|https)\://.*$')},
                      # DLR Level validation pattern can be validated/filtered further more through CredentialValidator
                      'dlr-level'   :{'optional': True,     'pattern': re.compile(r'^[1-3]$')},
                      'dlr-method'  :{'optional': True,     'pattern': re.compile(r'^(get|post)$', re.IGNORECASE)},
                      'content'     :{'optional': False},
                      }
            
            # Default coding is 0 when not provided
            if 'coding' not in updated_request.args:
                updated_request.args['coding'] = ['0']
            
            # Set default for undefined updated_request.arguments
            if 'dlr-url' in updated_request.args:
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
                self.log.debug("Authentication failure for username:%s and password:%s" % (updated_request.args['username'][0], updated_request.args['password'][0]))
                raise AuthenticationError('Authentication failure for username:%s' % updated_request.args['username'][0])
            
            # Build SubmitSmPDU
            SubmitSmPDU = self.opFactory.SubmitSM(
                source_addr = None if 'from' not in updated_request.args else updated_request.args['from'][0],
                destination_addr = updated_request.args['to'][0],
                short_message = updated_request.args['content'][0],
                data_coding = int(updated_request.args['coding'][0]),
            )                
            self.log.debug("Built base SubmitSmPDU: %s" % SubmitSmPDU)
            
            # Make Credential validation
            v = CredentialValidator('Send', user, SubmitSmPDU, request, fields)
            v.validate()
            
            # Update SubmitSmPDU by default values from user MtMessagingCredential
            SubmitSmPDU = v.updatePDUWithSendDefaults(SubmitSmPDU)

            # Routing
            routedConnector = None # init
            routable = RoutableSubmitSm(SubmitSmPDU, user)
            route = self.RouterPB.getMTRoutingTable().getRouteFor(routable)
            if route is None:
                self.log.debug("No route matched this SubmitSmPDU")
                raise RouteNotFoundError("No route found")
            else:
                # Get connector from selected route
                self.log.debug("RouterPB selected %s for this SubmitSmPDU" % route)
                routedConnector = route.getConnector()
                
                # Set priority
                priority = 0
                if 'priority' in updated_request.args:
                    priority = int(updated_request.args['priority'][0])
                    SubmitSmPDU.params['priority_flag'] = priority_flag_value_map[priority]
                self.log.debug("SubmitSmPDU priority is set to %s" % priority)

                # Set DLR bit mask
                # c.f. 5.2.17 registered_delivery
                ####################################################################
                # dlr-level # Signification                  # registered_delivery #
                ####################################################################
                # 1         # SMS-C level                    # x x x x x x 1 0     #
                # 2         # Terminal level (only)          # x x x x x x 0 1     #
                # 3         # SMS-C level and Terminal level # x x x x x x 0 1     #
                ####################################################################
                if updated_request.args['dlr'][0] == 'yes' and 'dlr-url' in updated_request.args:
                    if updated_request.args['dlr-level'][0] == '1':
                        SubmitSmPDU.params['registered_delivery'] = RegisteredDelivery(RegisteredDeliveryReceipt.NO_SMSC_DELIVERY_RECEIPT_REQUESTED)
                    elif updated_request.args['dlr-level'][0] == '2' or updated_request.args['dlr-level'][0] == '3':
                        SubmitSmPDU.params['registered_delivery'] = RegisteredDelivery(RegisteredDeliveryReceipt.SMSC_DELIVERY_RECEIPT_REQUESTED_FOR_FAILURE)
                    self.log.debug("SubmitSmPDU registered_delivery is set to %s" % str(SubmitSmPDU.params['registered_delivery']))

                    dlr_url = updated_request.args['dlr-url'][0]
                    dlr_level = int(updated_request.args['dlr-level'][0])
                    if updated_request.args['dlr-level'][0] == '1':
                        dlr_level_text = 'SMS-C'
                    elif updated_request.args['dlr-level'][0] == '2':
                        dlr_level_text = 'Terminal'
                    else:
                        dlr_level_text = 'All'
                    dlr_method = updated_request.args['dlr-method'][0]
                else:
                    dlr_url = None
                    dlr_level = 1
                    dlr_level_text = 'No'
                    dlr_method = None

                # Get number of PDUs to be sent (for billing purpose)
                _pdu = SubmitSmPDU
                submit_sm_count = 1
                while hasattr(_pdu, 'nextPdu'):
                    _pdu = _pdu.nextPdu
                    submit_sm_count += 1
                    
                # Pre-sending submit_sm: Billing processing
                bill = route.getBillFor(user)
                self.log.debug("SubmitSmBill [bid:%s] [ttlamounts:%s] generated for this SubmitSmPDU (x%s)" % (bill.bid, bill.getTotalAmounts(), submit_sm_count))
                charging_requirements = []
                u_balance = user.mt_credential.getQuota('balance')
                u_subsm_count = user.mt_credential.getQuota('submit_sm_count')
                if u_balance is not None:
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
                raise ServerError('Cannot send submit_sm, check SMPPClientManagerPB log file for details')
            else:
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
            
            if response['status'] == 200 and routedConnector is not None:
                self.log.info('SMS-MT [cid:%s] [msgid:%s] [prio:%s] [dlr:%s] [from:%s] [to:%s] [content:%s]' 
                              % (routedConnector.cid,
                              response['return'], 
                              priority, 
                              dlr_level_text, 
                              SubmitSmPDU.params['source_addr'], 
                              updated_request.args['to'][0], 
                              updated_request.args['content'][0]))
                return 'Success "%s"' % response['return']
            else:
                return 'Error "%s"' % response['return']
    
class HTTPApi(Resource):
    
    def __init__(self, RouterPB, SMPPClientManagerPB, config):
        Resource.__init__(self)
        
        self.config = config
        self.SMPPClientManagerPB = SMPPClientManagerPB
        self.RouterPB = RouterPB

        # Set up a dedicated logger
        self.log = logging.getLogger(LOG_CATEGORY)
        if len(self.log.handlers) != 1:
            self.log.setLevel(config.log_level)
            handler = logging.FileHandler(filename=config.log_file)
            formatter = logging.Formatter(config.log_format, config.log_date_format)
            handler.setFormatter(formatter)
            self.log.addHandler(handler)

        # Set http url routings
        self.log.debug("Setting http url routing for /send")
        self.putChild('send', Send(self.config, self.RouterPB, self.SMPPClientManagerPB, self.log))
