import pickle
from twisted.spread import pb
from twisted.internet import reactor
from twisted.cred.credentials import UsernamePassword, Anonymous
from twisted.spread.pb import RemoteReference

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

class RouterPBProxy:
    'This is a proxy to RouterPB perspective broker'

    pb = None
    isConnected = False
    pickleProtocol = 2
    
    def connect(self, host, port, username = None, password = None):
        # Launch a client
        self.pbClientFactory = pb.PBClientFactory()
        reactor.connectTCP(host, port, self.pbClientFactory)
        
        if username is None and password is None:
            return self.pbClientFactory.login(Anonymous()).addCallback(self._connected)
        else:
            return self.pbClientFactory.login(UsernamePassword(username, password)).addCallback(self._connected)
    
    def disconnect(self):
        self.isConnected = False

        if hasattr(self, 'pbClientFactory'):
            return self.pbClientFactory.disconnect()
    
    def _connected(self, rootObj):
        if isinstance(rootObj, RemoteReference):
            self.isConnected = True
            self.pb = rootObj
        elif type(rootObj) == tuple and type(rootObj[0]) == bool and rootObj[0] is False and type(rootObj[1]) == str:
            raise ConnectError(rootObj[1])
        else:
            raise InvalidConnectResponseError(rootObj)
        
    def pickle(self, obj):
        return pickle.dumps(obj, self.pickleProtocol)
    
    def unpickle(self, obj):
        return pickle.loads(obj)
    
    @ConnectedPB
    def version_release(self):
        return self.pb.callRemote('version_release')
    
    @ConnectedPB
    def persist(self, profile = "jcli-prod", scope = 'all'):
        return self.pb.callRemote('persist', profile, scope)
    
    @ConnectedPB
    def load(self, profile = "jcli-prod", scope = 'all'):
        return self.pb.callRemote('load', profile, scope)
    
    @ConnectedPB
    def is_persisted(self):
        return self.pb.callRemote('is_persisted')
    
    @ConnectedPB
    def user_add(self, user):
        return self.pb.callRemote('user_add', self.pickle(user))
    
    @ConnectedPB
    def user_authenticate(self, username, password):
        return self.pb.callRemote('user_authenticate', username, password)
    
    @ConnectedPB
    def user_remove(self, uid):
        return self.pb.callRemote('user_remove', uid)

    @ConnectedPB
    def user_remove_all(self):
        return self.pb.callRemote('user_remove_all')

    @ConnectedPB
    def user_get_all(self, gid = None):
        return self.pb.callRemote('user_get_all', gid)

    @ConnectedPB
    def user_update_quota(self, uid, cred, quota, value):
        return self.pb.callRemote('user_update_quota', uid, cred, quota, value)

    @ConnectedPB
    def group_add(self, group):
        return self.pb.callRemote('group_add', self.pickle(group))
    
    @ConnectedPB
    def group_remove(self, gid):
        return self.pb.callRemote('group_remove', gid)

    @ConnectedPB
    def group_remove_all(self):
        return self.pb.callRemote('group_remove_all')

    @ConnectedPB
    def group_get_all(self):
        return self.pb.callRemote('group_get_all')

    @ConnectedPB
    def mtroute_add(self, route, order):
        return self.pb.callRemote('mtroute_add', self.pickle(route), order)
    
    @ConnectedPB
    def moroute_add(self, route, order):
        return self.pb.callRemote('moroute_add', self.pickle(route), order)
    
    @ConnectedPB
    def mtroute_remove(self, order):
        return self.pb.callRemote('mtroute_remove', order)

    @ConnectedPB
    def moroute_remove(self, order):
        return self.pb.callRemote('moroute_remove', order)

    @ConnectedPB
    def mtroute_flush(self):
        return self.pb.callRemote('mtroute_flush')
    
    @ConnectedPB
    def moroute_flush(self):
        return self.pb.callRemote('moroute_flush')
    
    @ConnectedPB
    def mtroute_get_all(self):
        return self.pb.callRemote('mtroute_get_all')
    
    @ConnectedPB
    def moroute_get_all(self):
        return self.pb.callRemote('moroute_get_all')    
