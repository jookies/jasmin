import pickle
from twisted.spread import pb
from twisted.internet import defer, reactor
from twisted.spread.pb import RemoteReference
from twisted.cred.credentials import UsernamePassword, Anonymous

class ConnectError(Exception):
    'Raised when PB connection can not be established'
    pass

class InvalidConnectResponseError(Exception):
    'Raised when an invalid response is received when trying to establish PB connection'
    pass

def ConnectedPB(fCallback):
    '''
    Used as a decorator to check for PB connection, it will raise an exception
    if connection is not established
    '''
    def check_cnx_and_call(self, *args, **kwargs):
        if self.isConnected is False:
            raise Exception("PB proxy is not connected !")
        
        return fCallback(self, *args, **kwargs)
    return check_cnx_and_call

class JasminPBProxy:
    '''This is a factorised PBProxy to be used by all proxies in Jasmin

    It's holding connection related methods as well as picklings
    '''

    pb = None
    isConnected = False
    pickleProtocol = 2

    @defer.inlineCallbacks
    def connect(self, host, port, username = None, password = None):
        # Launch a client
        self.pbClientFactory = pb.PBClientFactory()
        reactor.connectTCP(host, port, self.pbClientFactory)
        yield self.pbClientFactory.getRootObject()
        
        if username is None and password is None:
            yield self.pbClientFactory.login(
                Anonymous()
            ).addCallback(self._connected)
        else:
            yield self.pbClientFactory.login(
                UsernamePassword(
                    username, 
                    password)
                ).addCallback(self._connected)
    
    def disconnect(self):
        self.isConnected = False
        
        # .connect has been called ?
        if hasattr(self, 'pbClientFactory'):
            return self.pbClientFactory.disconnect()
    
    def _connected(self, rootObj):
        if isinstance(rootObj, RemoteReference):
            self.isConnected = True
            self.pb = rootObj
        elif (type(rootObj) == tuple and type(rootObj[0]) == bool and
              rootObj[0] is False and type(rootObj[1]) == str):
            raise ConnectError(rootObj[1])
        else:
            raise InvalidConnectResponseError(rootObj)
        
    def pickle(self, obj):
        return pickle.dumps(obj, self.pickleProtocol)
    
    def unpickle(self, obj):
        return pickle.loads(obj)