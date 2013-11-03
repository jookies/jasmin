import uuid
import pickle
from txamqp.content import Content

class InvalidParameterError(Exception):
    """Raised when a parameter is invalid
    """

class UndefinedParameterError(Exception):
    """Raised when a parameter is undefined
    """

class PDU(Content):
    pickleProtocol = 2
    
    def __init__(self, body = "", children = None, properties = None, pickleProtocol = 2, prePickle = False):
        self.pickleProtocol = pickleProtocol
        
        if prePickle == True:
            body = pickle.dumps(body, self.pickleProtocol)

        Content.__init__(self, body, children, properties)
            
    def randomUniqueId(self):
        msgid = str(uuid.uuid4())
        
        return msgid
    
class DLRContent(Content):
    def __init__(self, message_status, msgid, dlr_url, dlr_level, id_smsc = '', sub = '', 
                 dlvrd = '', subdate = '', donedate = '', err = '', text = '', method = 'POST', trycount = 0):
        properties = {}
        
        # ESME_* statuses are returned from SubmitSmResp
        # Others are returned from DeliverSm, values must be the same as Table B-2
        if message_status[:5] != 'ESME_' and message_status not in ['DELIVRD', 'EXPIRED', 'DELETED', 
                                  'UNDELIV', 'ACCEPTED', 'UNKNOWN', 'REJECTD']:
            raise InvalidParameterError("Invalid message_status: %s", message_status)
        if dlr_level not in [1, 2, 3]:
            raise InvalidParameterError("Invalid dlr_level: %s", dlr_level)
        if dlr_level in [2, 3] and (id_smsc is None or sub is None or dlvrd is None or subdate is None or donedate is None or err is None or text is None):
            raise UndefinedParameterError("dlr_level is %s but some parameters were not defined: id_smsc:%s sub:%s dlvrd:%s subdate:%s donedate:%s err:%s text:%s"
                            % (dlr_level, id_smsc, sub, dlvrd, subdate, donedate, err, text))
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
        
class SubmitSmContent(PDU):
    def __init__(self, body, replyto, priority = 1, expiration = None, msgid = None):
        props = {}
        
        # RabbitMQ does not support priority (yet), anyway, we may use any other amqp broker that supports it
        if isinstance(priority, int) == False:
            raise InvalidParameterError("Invalid priority argument: %s" % priority)
        if priority < 0 or priority > 3:
            raise InvalidParameterError("Priority must be set from 0 to 3, it is actually set to %s" % priority)
        if msgid is None:
            msgid = self.randomUniqueId()
        
        props['priority'] = priority
        props['message-id'] = msgid
        props['expiration'] = expiration
        props['reply-to'] = replyto
        PDU.__init__(self, body, properties = props)
        
class SubmitSmRespContent(PDU):
    def __init__(self, body, msgid, pickleProtocol = 2, prePickle = True):
        props = {}
        
        props['message-id'] = msgid
        PDU.__init__(self, body, properties = props, pickleProtocol = pickleProtocol, prePickle = prePickle)
        
class DeliverSmContent(PDU):
    def __init__(self, body, cid, pickleProtocol = 2, prePickle = True):
        props = {}
        
        props['message-id'] = self.randomUniqueId()
        
        # For routing purpose, connector-id indicates the source connector of the PDU
        # the connector-id is used to instanciate RoutableDeliverSm when checking for
        # routes
        props['headers'] = {'connector-id': cid}
        
        PDU.__init__(self, body, properties = props, pickleProtocol = pickleProtocol, prePickle = prePickle)