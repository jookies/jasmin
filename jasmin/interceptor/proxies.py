from jasmin.tools.proxies import ConnectError, InvalidConnectResponseError, ConnectedPB
from jasmin.tools.proxies import JasminPBProxy

class InterceptorPBProxy(JasminPBProxy):
    'This is a proxy to InterceptorPB perspective broker'
    
    @ConnectedPB
    def run(self):
        return self.pb.callRemote('run')