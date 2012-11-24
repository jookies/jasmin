import uuid
import pickle
from txamqp.content import Content

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
        
class SubmitSmContent(PDU):
    def __init__(self, body, replyto, priority = 1, expiration = None, msgid = None):
        props = {}
        
        # RabbitMQ does not support priority (yet), anyway, we may use any other amqp broker that supports it
        if isinstance(priority, int) == False:
            raise Exception("Incorrect priority argument: %s" % priority)
        if priority < 0 or priority > 3:
            raise Exception("Priority must be set from 0 to 3, it is actually set to %s" % priority)
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
    def __init__(self, body, pickleProtocol = 2, prePickle = True):
        props = {}
        
        props['message-id'] = self.randomUniqueId()
        PDU.__init__(self, body, properties = props, pickleProtocol = pickleProtocol, prePickle = prePickle)