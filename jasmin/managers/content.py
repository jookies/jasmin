"""
Multiple classes extending of txamqp.content.Content
"""

import uuid
import pickle
import datetime
from txamqp.content import Content
from jasmin.protocols.smpp.protocol import SMPPServerProtocol

class InvalidParameterError(Exception):
    """Raised when a parameter is invalid
    """

def randomUniqueId():
    "Returns a UUID4 unique message id"
    msgid = str(uuid.uuid4())
    
    return msgid

class PDU(Content):
    "A generick SMPP PDU Content"
    
    pickleProtocol = 2
    
    def __init__(self, body = "", children = None, properties = None, pickleProtocol = 2, prePickle = False):
        self.pickleProtocol = pickleProtocol
        
        if prePickle is True:
            body = pickle.dumps(body, self.pickleProtocol)
        
        # Add creation date in header
        if 'headers' not in properties:
            properties['headers'] = {}
        properties['headers']['created_at'] = str(datetime.datetime.now())

        Content.__init__(self, body, children, properties)

class DLRContentForHttpapi(Content):
    """A DLR Content holding information about the origin SubmitSm sent from httpapi and 
    receipt acknowledgment details"""

    def __init__(self, message_status, msgid, dlr_url, dlr_level, id_smsc = '', sub = '', 
                 dlvrd = '', subdate = '', donedate = '', err = '', text = '', method = 'POST', trycount = 0):
        properties = {}
        
        # ESME_* statuses are returned from SubmitSmResp
        # Others are returned from DeliverSm, values must be the same as Table B-2
        if message_status[:5] != 'ESME_' and message_status not in ['DELIVRD', 'EXPIRED', 'DELETED', 
                                  'UNDELIV', 'ACCEPTD', 'UNKNOWN', 'REJECTD']:
            raise InvalidParameterError("Invalid message_status: %s", message_status)
        if dlr_level not in [1, 2, 3]:
            raise InvalidParameterError("Invalid dlr_level: %s", dlr_level)
        if method not in ['POST', 'GET']:
            raise InvalidParameterError('Invalid method: %s', method)
        
        properties['message-id'] = msgid
        properties['headers'] = {'try-count': 0,
                                 'url': dlr_url, 
                                 'method': method,
                                 'message_status': message_status,
                                 'level': dlr_level,
                                 'id_smsc': id_smsc, 
                                 'sub': sub, 
                                 'dlvrd': dlvrd, 
                                 'subdate': subdate, 
                                 'donedate': donedate, 
                                 'err': err, 
                                 'text': text}
        
        Content.__init__(self, msgid, properties = properties)
        
class DLRContentForSmpps(Content):
    """A DLR Content holding information about the origin SubmitSm sent from smpps and 
    receipt acknowledgment details"""

    def __init__(self, message_status, msgid, system_id, source_addr, destination_addr, sub_date):
        properties = {}
        
        # ESME_* statuses are returned from SubmitSmResp
        # Others are returned from DeliverSm, values must be the same as Table B-2
        if message_status[:5] != 'ESME_' and message_status not in ['DELIVRD', 'EXPIRED', 'DELETED', 
                                  'UNDELIV', 'ACCEPTD', 'UNKNOWN', 'REJECTD']:
            raise InvalidParameterError("Invalid message_status: %s", message_status)
        
        properties['message-id'] = msgid
        properties['headers'] = {'try-count': 0,
                                 'message_status': message_status,
                                 'system_id': system_id,
                                 'source_addr': source_addr,
                                 'destination_addr': destination_addr,
                                 'sub_date': str(sub_date)}
        
        Content.__init__(self, msgid, properties = properties)

class SubmitSmContent(PDU):
    "A SMPP SubmitSm Content"

    def __init__(self, body, replyto, priority = 1, expiration = None, msgid = None, submit_sm_resp_bill = None, source_connector = 'httpapi'):
        props = {}
        
        # RabbitMQ does not support priority (yet), anyway, we may use any other amqp broker that supports it
        if isinstance(priority, int) == False:
            raise InvalidParameterError("Invalid priority argument: %s" % priority)
        if priority < 0 or priority > 3:
            raise InvalidParameterError("Priority must be set from 0 to 3, it is actually set to %s" % 
                                        priority)
        if source_connector not in ['httpapi', 'smppsapi']:
            raise InvalidParameterError('Invalid source_connector value: %s.')
        if msgid is None:
            msgid = randomUniqueId()
        
        props['priority'] = priority
        props['message-id'] = msgid
        props['reply-to'] = replyto
        
        props['headers'] = {'source_connector': source_connector}
        if submit_sm_resp_bill is not None:
            props['headers']['submit_sm_resp_bill'] = submit_sm_resp_bill
        if expiration is not None:
            props['headers']['expiration'] = expiration

        PDU.__init__(self, body, properties = props)
        
class SubmitSmRespContent(PDU):
    "A SMPP SubmitSmResp Content"

    def __init__(self, body, msgid, pickleProtocol = 2, prePickle = True):
        props = {}
        
        props['message-id'] = msgid
        PDU.__init__(self, 
            body, 
            properties = props, 
            pickleProtocol = pickleProtocol, 
            prePickle = prePickle)
        
class DeliverSmContent(PDU):
    "A SMPP DeliverSm Content"

    def __init__(self, body, sourceCid, pickleProtocol = 2, prePickle = True, 
        concatenated = False, will_be_concatenated = False):
        props = {}
        
        props['message-id'] = randomUniqueId()
        
        # For routing purpose, connector-id indicates the source connector of the PDU
        # the connector-id is used to instanciate RoutableDeliverSm when checking for
        # routes
        props['headers'] = {'try-count': 0,
                            'connector-id': sourceCid,
                            'concatenated': concatenated,
                            'will_be_concatenated': will_be_concatenated}
        
        PDU.__init__(self, 
            body, 
            properties = props, 
            pickleProtocol = pickleProtocol, 
            prePickle = prePickle)

class SubmitSmRespBillContent(Content):
    "A Bill Content holding amount to be charged to user (uid)"
    
    def __init__(self, bid, uid, amount):
        if type(amount) != float and type(amount) != int:
            raise InvalidParameterError('Amount is not float or int: %s' % amount)
        if amount < 0:
            raise InvalidParameterError('Amount cannot be a negative value: %s' % amount)
        
        properties = {}
        
        properties['message-id'] = bid
        properties['headers'] = {'user-id': uid, 'amount': str(amount)}
        
        Content.__init__(self, bid, properties = properties)