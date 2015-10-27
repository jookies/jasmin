from jasmin.tools.proxies import ConnectedPB
from jasmin.tools.proxies import JasminPBProxy
from jasmin.routing.Routables import Routable
from jasmin.routing.jasminApi import InterceptorScript

class InvalidRoutableObject(Exception):
    'Raised when routable arg is not an instance of Routable'

class InvalidScriptObject(Exception):
    'Raised when script arg is not an instance of InterceptorScript'

class InterceptorPBProxy(JasminPBProxy):
    'This is a proxy to InterceptorPB perspective broker'

    @ConnectedPB
    def run_script(self, script, routable):
        """Will call InterceptorPB to run script with routable as argument

        It will return updated (or not) routable.
        """

        if isinstance(script, InterceptorScript) is False:
            raise InvalidScriptObject(script)
        if isinstance(routable, Routable) is False:
            raise InvalidRoutableObject(routable)

        return self.pb.callRemote('run_script', script.pyCode, self.pickle(routable))
