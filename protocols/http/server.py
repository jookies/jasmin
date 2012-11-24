import logging
import re
from twisted.web.resource import Resource
from smpp.pdu.constants import priority_flag_value_map
from jasmin.protocols.http.validation import UrlArgsValidator
from jasmin.protocols.smpp.operations import SMPPOperationFactory
from jasmin.routing.Routables import RoutableSubmitSm

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
            fields = {'to':{'optional': False, 'pattern': re.compile(r'^\+{0,1}\d+$')}, 
                      'from':{'optional':True, 'pattern': re.compile(r'^\+{0,1}\d+$')},
                      'username':{'optional':False, 'pattern': re.compile(r'^.{1,30}$')},
                      'password':{'optional':False, 'pattern': re.compile(r'^.{1,30}$')},
                      'priority':{'optional':True, 'pattern': re.compile(r'^[0-3]$')},
                      'content':{'optional':False},
                      }
            v = UrlArgsValidator(request, fields)
            v.validate()
            
            # Authentication
            user = self.RouterPB.authenticateUser(username = request.args['username'][0], password = request.args['password'][0])
            if user is None:
                self.log.debug("Authentication failure for username:%s and password:%s" % (request.args['username'][0], request.args['password'][0]))
                raise Exception('Authentication failure')
            
            # Build SubmitSmPDU
            SubmitSmPDU = self.opFactory.SubmitSM(
                source_addr=None if 'from' not in request.args else request.args['source_addr'][0],
                destination_addr=request.args['to'][0],
                short_message=request.args['content'][0],
            )
            self.log.debug("Built SubmitSmPDU: %s" % SubmitSmPDU)
            
            # Set priority
            priority = 1
            if 'priority' in request.args:
                priority = request.args['priority'][0]
                SubmitSmPDU.params['priority_flag'] = priority_flag_value_map[priority]
            self.log.debug("SubmitSmPDU priority is set to %s" % priority)
                
            # Routing
            routable = RoutableSubmitSm(SubmitSmPDU, user)
            routedConnector = self.RouterPB.getMTRoutingTable().getConnectorFor(routable)
            if routedConnector is None:
                self.log.debug("No route matched this SubmitSmPDU")
                raise Exception('No route matches')
            else:
                self.log.debug("Connector '%s' is set to be a route for this SubmitSmPDU" % routedConnector.cid)
                # Send SubmitSmPDU through smpp client manager PB server
                c = self.SMPPClientManagerPB.remote_submit_sm(routedConnector.cid, SubmitSmPDU, priority, pickled = False)
            
            # Build final response
            if not c.result:
                raise Exception('Cannot send submit_sm, check log file for details')
            else:
                response = {'return': c.result, 'status': 200}
        except Exception, e:
            self.log.error("Error: %s" % e)
            response = {'return': str(e), 'status': 403}
        finally:
            self.log.debug("Returning %s to %s." % (response, request.getClientIP()))
            request.setResponseCode(response['status'])
            
            if response['status'] == 200:
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