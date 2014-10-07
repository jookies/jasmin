import pickle
from txamqp.content import Content

class PDU(Content):
    pickleProtocol = 2
    
    def pickle(self, data):
        return pickle.dumps(data, self.pickleProtocol)
    
    def __init__(self, body = "", children = None, properties = None, pickleProtocol = 2):
        self.pickleProtocol = pickleProtocol
        
        body = self.pickle(body)

        Content.__init__(self, body, children, properties)

class RoutedDeliverSmContent(PDU):
    def __init__(self, deliver_sm, msgid, scid, dc, trycount = 0, pickleProtocol = 2):
        props = {}
        
        props['message-id'] = msgid
        props['headers'] = {'src-connector-id': scid, 'dst-connector-id': dc.cid, 'dst-connector': self.pickle(dc), 'try-count': trycount}
        
        PDU.__init__(self, deliver_sm, properties = props, pickleProtocol = pickleProtocol)