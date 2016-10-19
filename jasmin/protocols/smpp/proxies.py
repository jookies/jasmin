from jasmin.tools.proxies import ConnectedPB
from jasmin.tools.proxies import JasminPBProxy


class SMPPServerPBProxy(JasminPBProxy):
    'This is a proxy to SMPPServerPB perspective broker'

    @ConnectedPB
    def smpps_list(self):
        return self.pb.callRemote('smpps_list')
