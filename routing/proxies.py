# Copyright 2012 Fourat Zouari <fourat@gmail.com>
# See LICENSE for details.

import pickle
from twisted.spread import pb
from twisted.internet import reactor

class RouterPBProxy:
    pb = None
    isConnected = False
    pickleProtocol = 2
    
    def connect(self, host, port):
        # Launch a client
        self.pbClientFactory = pb.PBClientFactory()
        reactor.connectTCP(host, port, self.pbClientFactory)
        
        return self.pbClientFactory.getRootObject( ).addCallback(self._connected)
    
    def disconnect(self):
        self.isConnected = False
        return self.pbClientFactory.disconnect()
    
    def _connected(self, rootObj):
        self.isConnected = True
        self.pb = rootObj
        
    def pickle(self, obj):
        return pickle.dumps(obj, self.pickleProtocol)
    
    def unpickle(self, obj):
        return pickle.loads(obj)
    
    def user_add(self, user):
        if self.isConnected == False:
            raise Exception("PB proxy is not connected !")

        return self.pb.callRemote('user_add', self.pickle(user))
    
    def user_authenticate(self, username, password):
        if self.isConnected == False:
            raise Exception("PB proxy is not connected !")

        return self.pb.callRemote('user_authenticate', username, password)
    
    def user_remove(self, user):
        if self.isConnected == False:
            raise Exception("PB proxy is not connected !")

        return self.pb.callRemote('user_remove', self.pickle(user))

    def user_remove_all(self):
        if self.isConnected == False:
            raise Exception("PB proxy is not connected !")

        return self.pb.callRemote('user_remove_all')

    def user_get_all(self):
        if self.isConnected == False:
            raise Exception("PB proxy is not connected !")

        return self.pb.callRemote('user_get_all')

    def mtroute_add(self, route, order):
        if self.isConnected == False:
            raise Exception("PB proxy is not connected !")

        return self.pb.callRemote('mtroute_add', self.pickle(route), order)
    
    def moroute_add(self, route, order):
        if self.isConnected == False:
            raise Exception("PB proxy is not connected !")

        return self.pb.callRemote('moroute_add', self.pickle(route), order)
    
    def mtroute_flush(self):
        if self.isConnected == False:
            raise Exception("PB proxy is not connected !")

        return self.pb.callRemote('mtroute_flush')
    
    def moroute_flush(self):
        if self.isConnected == False:
            raise Exception("PB proxy is not connected !")

        return self.pb.callRemote('moroute_flush')
    
    def mtroute_get_all(self):
        if self.isConnected == False:
            raise Exception("PB proxy is not connected !")

        return self.pb.callRemote('mtroute_get_all')
    
    def moroute_get_all(self):
        if self.isConnected == False:
            raise Exception("PB proxy is not connected !")

        return self.pb.callRemote('moroute_get_all')    