import pickle as _pickle

from txamqp.content import Content


class PDU(Content):
    pickleProtocol = _pickle.HIGHEST_PROTOCOL

    def pickle(self, data):
        return _pickle.dumps(data, self.pickleProtocol)

    def __init__(self, body="", children=None, properties=None, pickleProtocol=_pickle.HIGHEST_PROTOCOL):
        self.pickleProtocol = pickleProtocol

        body = self.pickle(body)

        Content.__init__(self, body, children, properties)


class RoutedDeliverSmContent(PDU):
    def __init__(self, deliver_sm, msgid, scid, dcs, route_type='simple', trycount=0, pickleProtocol=_pickle.HIGHEST_PROTOCOL):
        props = {}

        if type(dcs) != list:
            # Since #467 this class must accept a list of destination connector(s)
            #   this test/conversion is done to preserve backward compatibility
            dcs = [dcs]

        props['message-id'] = msgid
        props['headers'] = {
            'route-type': route_type,
            'src-connector-id': scid,
            'dst-connectors': self.pickle(dcs),
            'try-count': trycount}

        PDU.__init__(self, deliver_sm, properties=props, pickleProtocol=pickleProtocol)
