import pickle
from twisted.spread import pb
from twisted.internet import reactor
from jasmin.vendor.smpp.pdu.operations import SubmitSM
from jasmin.protocols.smpp.configs import SMPPClientConfig
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

class SMPPClientManagerPBProxy:
    'This is a proxy to SMPPClientManagerPB perspective broker'
    
    pb = None
    isConnected = False
    pickleProtocol = 2
    
    def connect(self, host, port, username = None, password = None):
        # Launch a client
        self.pbClientFactory = pb.PBClientFactory()
        reactor.connectTCP(host, port, self.pbClientFactory)
        
        if username is None and password is None:
            return self.pbClientFactory.login(
                                              Anonymous()
                                              ).addCallback(self._connected)
        else:
            return self.pbClientFactory.login(
                                              UsernamePassword(
                                                               username, 
                                                               password)
                                              ).addCallback(self._connected)
    
    def disconnect(self):
        self.isConnected = False
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
    
    @ConnectedPB
    def version_release(self):
        return self.pb.callRemote('version_release')
    
    @ConnectedPB
    def persist(self, profile = "jcli-prod"):
        return self.pb.callRemote('persist', profile)
    
    @ConnectedPB
    def load(self, profile = "jcli-prod"):
        return self.pb.callRemote('load', profile)
    
    @ConnectedPB
    def is_persisted(self):
        return self.pb.callRemote('is_persisted')
    
    @ConnectedPB
    def add(self, config):
        if isinstance(config, SMPPClientConfig) is False:
            raise Exception("Object is not an instance of SMPPClientConfig")

        return self.pb.callRemote('connector_add', self.pickle(config))
    
    @ConnectedPB
    def remove(self, cid):
        return self.pb.callRemote('connector_remove', cid)
    
    @ConnectedPB
    def connector_list(self):
        return self.pb.callRemote('connector_list')

    @ConnectedPB
    def start(self, cid):
        return self.pb.callRemote('connector_start', cid)

    @ConnectedPB
    def stop(self, cid, delQueues = False):
        return self.pb.callRemote('connector_stop', cid, delQueues)

    @ConnectedPB
    def stopall(self, delQueues = False):
        return self.pb.callRemote('connector_stopall', delQueues)

    @ConnectedPB
    def session_state(self, cid):
        return self.pb.callRemote('session_state', cid)
    
    @ConnectedPB
    def service_status(self, cid):
        return self.pb.callRemote('service_status', cid)
    
    @ConnectedPB
    def connector_details(self, cid):
        return self.pb.callRemote('connector_details', cid)
    
    @ConnectedPB
    def connector_config(self, cid):
        """Once the returned deferred is fired, a pickled SMPPClientConfig
        is obtained as a result (if success)"""
        return self.pb.callRemote('connector_config', cid)
    
    @ConnectedPB
    def submit_sm(self, cid, SubmitSmPDU):
        if isinstance(SubmitSmPDU, SubmitSM) is False:
            raise Exception("Object is not an instance of SubmitSm")
        
        # Remove schedule_delivery_time / not supported right now
        if SubmitSmPDU.params['schedule_delivery_time'] is not None:
            SubmitSmPDU.params['schedule_delivery_time'] = None

        # Set the message priority
        if SubmitSmPDU.params['priority_flag'] is not None:
            priority_flag = SubmitSmPDU.params['priority_flag'].index
        else:
            priority_flag = 0
            
        # Set the message validity date
        if SubmitSmPDU.params['validity_period'] is not None:
            validity_period = SubmitSmPDU.params[
                                                 'validity_period'
                                                 ].strftime('%Y-%m-%d %H:%M:%S')
        else:
            # Validity period is not set, the SMS-C will set its own default 
            # validity_period to this message
            validity_period = None

        return self.pb.callRemote('submit_sm', cid, 
                                  SubmitSmPDU       = self.pickle(SubmitSmPDU), 
                                  priority          = priority_flag,
                                  validity_period   = validity_period
                                )