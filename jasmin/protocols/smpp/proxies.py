import cPickle as pickle

from jasmin.tools.proxies import ConnectedPB
from jasmin.tools.proxies import JasminPBProxy


class SMPPServerPBProxy(JasminPBProxy):
    """This is a proxy to SMPPServerPB perspective broker
    used mainly for delivering dlr and deliver_sm from a standalone process"""

    @ConnectedPB
    def version_release(self):
        return self.pb.callRemote('version_release')

    @ConnectedPB
    def version(self):
        return self.pb.callRemote('version')

    @ConnectedPB
    def list_bound_systemids(self):
        return self.pb.callRemote('list_bound_systemids')

    @ConnectedPB
    def deliverer_send_request(self, system_id, pdu):
        # TODO: pickle may get swaped with msgpack in future ...
        pdu = pickle.dumps(pdu, pickle.HIGHEST_PROTOCOL)

        return self.pb.callRemote('deliverer_send_request', system_id, pdu)
