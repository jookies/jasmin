# Copyright 2012 Fourat Zouari <fourat@gmail.com>
# See LICENSE for details.

import datetime
import pickle
from twisted.spread import pb
from twisted.internet import reactor
from jasmin.vendor.smpp.pdu.operations import SubmitSM
from jasmin.protocols.smpp.configs import SMPPClientConfig

class SMPPClientManagerPBProxy:
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
    
    def add(self, config):
        if self.isConnected == False:
            raise Exception("PB proxy is not connected !")
        if isinstance(config, SMPPClientConfig) == False:
            raise Exception("Object is not an instance of SMPPClientConfig")

        return self.pb.callRemote('connector_add', self.pickle(config))
    
    def remove(self, cid):
        if self.isConnected == False:
            raise Exception("PB proxy is not connected !")

        return self.pb.callRemote('connector_remove', cid)
    
    def connector_list(self):
        if self.isConnected == False:
            raise Exception("PB proxy is not connected !")

        return self.pb.callRemote('connector_list')

    def start(self, cid):
        if self.isConnected == False:
            raise Exception("PB proxy is not connected !")

        return self.pb.callRemote('connector_start', cid)

    def stop(self, cid, delQueues = False):
        if self.isConnected == False:
            raise Exception("PB proxy is not connected !")

        return self.pb.callRemote('connector_stop', cid, delQueues)

    def stopall(self, delQueues = False):
        if self.isConnected == False:
            raise Exception("PB proxy is not connected !")

        return self.pb.callRemote('connector_stopall', delQueues)

    def session_state(self, cid):
        if self.isConnected == False:
            raise Exception("PB proxy is not connected !")

        return self.pb.callRemote('session_state', cid)
    
    def service_status(self, cid):
        if self.isConnected == False:
            raise Exception("PB proxy is not connected !")

        return self.pb.callRemote('service_status', cid)
    
    def connector_details(self, cid):
        if self.isConnected == False:
            raise Exception("PB proxy is not connected !")

        return self.pb.callRemote('connector_details', cid)
    
    def connector_config(self, cid):
        """Once the returned deferred is fired, a pickled SMPPClientConfig
        is obtained as a result (if success)"""
        if self.isConnected == False:
            raise Exception("PB proxy is not connected !")

        return self.pb.callRemote('connector_config', cid)
    
    def submit_sm(self, cid, SubmitSmPDU):
        if self.isConnected == False:
            raise Exception("PB proxy is not connected !")
        if isinstance(SubmitSmPDU, SubmitSM) == False:
            raise Exception("Object is not an instance of SubmitSm")
        
        # Set the message priority
        if SubmitSmPDU.params['priority_flag'] != None:
            priority_flag = SubmitSmPDU.params['priority_flag'].index
        else:
            priority_flag = 0
            
        # Set the message validity date
        if SubmitSmPDU.params['validity_period'] != None:
            validity_period = SubmitSmPDU.params['validity_period'].strftime('%Y-%m-%d %H:%M:%S')
        else:
            # Validity period is not set, the SMS-C will set its own default vperiod to this message
            validity_period = None

        return self.pb.callRemote('submit_sm', cid, 
                                  SubmitSmPDU       = self.pickle(SubmitSmPDU), 
                                  priority          = priority_flag,
                                  validity_period   = validity_period
                                )
