import logging
import re
from twisted.web.resource import Resource
from jasmin.vendor.smpp.pdu.constants import priority_flag_value_map
from jasmin.vendor.smpp.pdu.pdu_types import RegisteredDeliveryReceipt, RegisteredDelivery
from jasmin.protocols.http.validation import UrlArgsValidator
from jasmin.protocols.smpp.operations import SMPPOperationFactory
from jasmin.routing.Routables import RoutableSubmitSm
from jasmin.protocols.http.errors import AuthenticationError, ServerError, RouteNotFoundError

LOG_CATEGORY = "jasmin-http-api"

class Send(Resource):
    def __init__(self, RouterPB, SMPPClientManagerPB, log):
        Resource.__init__(self)
        
        self.SMPPClientManagerPB = SMPPClientManagerPB
        self.RouterPB = RouterPB
        self.log = log
        self.opFactory = SMPPOperationFactory()
    
    def render(self, request):
        self.log.debug("Rendering /send response with args: %s from %s" % (request.args, request.getClientIP()))
        response = {'return': None, 'status': 200}
        
        try:
            # Validation
            fields = {'to'          :{'optional': False,    'pattern': re.compile(r'^\+{0,1}\d+$')}, 
                      'from'        :{'optional': True},
                      'username'    :{'optional': False,    'pattern': re.compile(r'^.{1,30}$')},
                      'password'    :{'optional': False,    'pattern': re.compile(r'^.{1,30}$')},
                      'priority'    :{'optional': True,     'pattern': re.compile(r'^[0-3]$')},
                      'dlr'         :{'optional': False,    'pattern': re.compile(r'^(yes|no)$')},
                      'dlr-url'     :{'optional': True,     'pattern': re.compile(r'^(http|https)\://.*$')},
                      'dlr-level'   :{'optional': True,     'pattern': re.compile(r'^[1-3]$')},
                      'dlr-method'  :{'optional': True,     'pattern': re.compile(r'^(get|post)$', re.IGNORECASE)},
                      'content'     :{'optional': False},
                      }
            
            # Set default for undefined request.arguments
            if 'dlr-url' in request.args:
                request.args['dlr'] = ['yes']
            if 'dlr' not in request.args:
                # Setting DLR request to 'no'
                request.args['dlr'] = ['no']
            
            # Set default values
            if request.args['dlr'][0] == 'yes':
                if 'dlr-level' not in request.args:
                    # If DLR is requested and no dlr-level were provided, assume minimum level (1)
                    request.args['dlr-level'] = [1]
                if 'dlr-method' not in request.args:
                    # If DLR is requested and no dlr-method were provided, assume default (POST)
                    request.args['dlr-method'] = ['POST']
            
            # DLR method must be uppercase
            if 'dlr-method' in request.args:
                request.args['dlr-method'][0] = request.args['dlr-method'][0].upper()
            
            # Make validation
            v = UrlArgsValidator(request, fields)
            v.validate()
            
            # Authentication
            user = self.RouterPB.authenticateUser(username = request.args['username'][0], password = request.args['password'][0])
            if user is None:
                self.log.debug("Authentication failure for username:%s and password:%s" % (request.args['username'][0], request.args['password'][0]))
                raise AuthenticationError('Authentication failure for username:%s' % request.args['username'][0])
            
            # Build SubmitSmPDU
            SubmitSmPDU = self.opFactory.SubmitSM(
                source_addr=None if 'from' not in request.args else request.args['from'][0],
                destination_addr=request.args['to'][0],
                short_message=request.args['content'][0],
            )
            self.log.debug("Built SubmitSmPDU: %s" % SubmitSmPDU)
            
            # Routing
            routedConnector = None # init
            routable = RoutableSubmitSm(SubmitSmPDU, user)
            routedConnector = self.RouterPB.getMTRoutingTable().getConnectorFor(routable)
            if routedConnector is None:
                self.log.debug("No route matched this SubmitSmPDU")
                raise RouteNotFoundError()
            else:
                # Set priority
                priority = 1
                if 'priority' in request.args:
                    priority = request.args['priority'][0]
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
                if request.args['dlr'][0] == 'yes' and 'dlr-url' in request.args:
                    if request.args['dlr-level'][0] == '1':
                        SubmitSmPDU.params['registered_delivery'] = RegisteredDelivery(RegisteredDeliveryReceipt.NO_SMSC_DELIVERY_RECEIPT_REQUESTED)
                    elif request.args['dlr-level'][0] == '2' or request.args['dlr-level'][0] == '3':
                        SubmitSmPDU.params['registered_delivery'] = RegisteredDelivery(RegisteredDeliveryReceipt.SMSC_DELIVERY_RECEIPT_REQUESTED_FOR_FAILURE)
                    self.log.debug("SubmitSmPDU registered_delivery is set to %s" % str(SubmitSmPDU.params['registered_delivery']))

                    dlr_url = request.args['dlr-url'][0]
                    dlr_level = int(request.args['dlr-level'][0])
                    if request.args['dlr-level'][0] == '1':
                        dlr_level_text = 'SMS-C'
                    elif request.args['dlr-level'][0] == '2':
                        dlr_level_text = 'Terminal'
                    else:
                        dlr_level_text = 'All'
                    dlr_method = request.args['dlr-method'][0]
                else:
                    dlr_url = None
                    dlr_level = 1
                    dlr_level_text = 'No'
                    dlr_method = None

                ########################################################
                # Send SubmitSmPDU through smpp client manager PB server
                self.log.debug("Connector '%s' is set to be a route for this SubmitSmPDU" % routedConnector.cid)
                c = self.SMPPClientManagerPB.remote_submit_sm(routedConnector.cid, 
                                                              SubmitSmPDU, 
                                                              priority, 
                                                              pickled = False, 
                                                              dlr_url = dlr_url, 
                                                              dlr_level = dlr_level,
                                                              dlr_method = dlr_method)
            
            # Build final response
            if not c.result:
                raise ServerError('Cannot send submit_sm, check SMPPClientManagerPB log file for details')
            else:
                response = {'return': c.result, 'status': 200}
        except Exception, e:
            self.log.error("Error: %s" % e)
            response = {'return': e.message, 'status': 500}
        finally:
            self.log.debug("Returning %s to %s." % (response, request.getClientIP()))
            request.setResponseCode(response['status'])
            
            if response['status'] == 200 and routedConnector is not None:
                self.log.info('SMS-MT [cid:%s] [msgid:%s] [prio:%s] [dlr:%s] [from:%s] [to:%s] [content:%s]' % (routedConnector.cid,
                              response['return'], priority, dlr_level_text, SubmitSmPDU.params['source_addr'], request.args['to'][0], request.args['content'][0]))
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
        self.log.setLevel(config.log_level)
        handler = logging.FileHandler(filename=config.log_file)
        formatter = logging.Formatter(config.log_format, config.log_date_format)
        handler.setFormatter(formatter)
        self.log.addHandler(handler)

        # Set http url routings
        self.log.debug("Setting http url routing for /send")
        self.putChild('send', Send(self.RouterPB, self.SMPPClientManagerPB, self.log))
