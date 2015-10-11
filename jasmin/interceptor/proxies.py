from jasmin.tools.proxies import ConnectError, InvalidConnectResponseError, ConnectedPB
from jasmin.tools.proxies import JasminPBProxy
from jasmin.routing.Routables import Routable
from twisted.internet import defer

class InterceptorPBProxy(JasminPBProxy):
    'This is a proxy to InterceptorPB perspective broker'

    @ConnectedPB
    def run(self, script, routable):
        """Will call InterceptorPB to run script with routable as argument

        It will return updated (or not) routable.
        """

        if isinstance(routable, Routable) is False:
            raise Exception("Object is not an instance of Routable")

        return self.pb.callRemote('run', script.pyCode, self.pickle(routable))
