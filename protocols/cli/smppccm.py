import pickle
from twisted.internet import defer
from jasmin.protocols.smpp.configs import SMPPClientConfig
from managers import Manager, FilterSessionArgs
from vendor.smpp.pdu.constants import (addr_npi_name_map, addr_ton_name_map,
                                       replace_if_present_flap_name_map, priority_flag_name_map)

# A config map between console-configuration keys and SMPPClientConfig keys.
SMPPClientConfigKeyMap = {'cid': 'id', 'host': 'host', 'port': 'port', 'username': 'username',
                       'password': 'password', 'systype': 'systemType', 'logfile': 'log_file', 'loglevel': 'log_level',
                       'bind_to': 'sessionInitTimerSecs', 'elink_interval': 'enquireLinkTimerSecs', 'trx_to': 'inactivityTimerSecs',
                       'res_to': 'responseTimerSecs', 'con_loss_retry': 'reconnectOnConnectionLoss', 'con_fail_retry': 'reconnectOnConnectionFailure',
                       'con_loss_delay': 'reconnectOnConnectionLossDelay', 'con_fail_delay': 'reconnectOnConnectionFailureDelay',
                       'pdu_red_to': 'pduReadTimerSecs', 'bind': 'bindOperation', 'bind_ton': 'bind_addr_ton', 'bind_npi': 'bind_addr_npi',
                       'src_ton': 'source_addr_ton', 'src_npi': 'source_addr_npi', 'dst_ton': 'dest_addr_ton', 'dst_npi': 'dest_addr_npi',
                       'addr_range': 'address_range', 'src_addr': 'source_addr', 'proto_id': 'protocol_id',
                       'priority': 'priority_flag', 'validity': 'validity_period', 'ripf': 'replace_if_present_flag',
                       'def_msg_id': 'sm_default_msg_id', 'coding': 'data_coding', 'requeue_delay': 'requeue_delay', 'submit_throughput': 'submit_sm_throughput',
                       'dlr_expiry': 'dlr_expiry'
                       }
# When updating a key from RequireRestartKeys, the connector need restart for update to take effect
RequireRestartKeys = ['host', 'port', 'username', 'password', 'systemType', 'logfile', 'loglevel']

class JCliSMPPClientConfig(SMPPClientConfig):
    'Overload SMPPClientConfig with getters and setters for JCli'
    PendingRestart = False

    def castToBuiltInType(self, key, value):
        if isinstance(value, bool):
            return 1 if value else 0
        if key in ['bind_npi', 'dst_npi', 'src_npi']:
            return addr_npi_name_map[str(value)]
        if key in ['bind_ton', 'dst_ton', 'src_ton']:
            return addr_ton_name_map[str(value)]
        if key == 'ripf':
            return replace_if_present_flap_name_map[str(value)]
        if key == 'priority':
            return priority_flag_name_map[str(value)]
        return value
    
    def set(self, key, value):
        setattr(self, key, value)
        
        if key in RequireRestartKeys:
            self.PendingRestart = True

    def getAll(self):
        r = {}
        for key, value in SMPPClientConfigKeyMap.iteritems():
            r[key] = self.castToBuiltInType(key, getattr(self, value))
        return r

def SMPPClientConfigBuild(fn):
    'Parse args and try to build a JCliSMPPClientConfig instance to pass it to fn'
    def parse_args_and_call_with_instance(self, *args, **kwargs):
        cmd = args[0]
        arg = args[1]

        # Empty line
        if cmd is None:
            return self.protocol.sendData()
        # Initiate JCliSMPPClientConfig with sessBuffer content
        if cmd == 'ok':
            if len(self.sessBuffer) == 0:
                return self.protocol.sendData('You must set at least connector id (cid) before saving !')
                
            connector = {}
            for key, value in self.sessBuffer.iteritems():
                connector[key] = value
            try:
                SMPPClientConfigInstance = JCliSMPPClientConfig(**connector)
                # Hand the instance to fn
                return fn(self, SMPPClientConfigInstance)
            except Exception, e:
                return self.protocol.sendData('Error: %s' % str(e))
        else:
            # Unknown key
            if not SMPPClientConfigKeyMap.has_key(cmd):
                return self.protocol.sendData('Unknown SMPPClientConfig key: %s' % cmd)
            
            # Buffer key for later SMPPClientConfig initiating
            SMPPClientConfigKey = SMPPClientConfigKeyMap[cmd]
            self.sessBuffer[SMPPClientConfigKey] = self.protocol.str2num(arg)
            
            return self.protocol.sendData()
    return parse_args_and_call_with_instance

def SMPPClientConfigUpdate(fn):
    '''Get connector configuration and log update requests passing to fn
    The log will be handed to fn when 'ok' is received'''
    def log_update_requests_and_call(self, *args, **kwargs):
        cmd = args[0]
        arg = args[1]

        # Empty line
        if cmd is None:
            return self.protocol.sendData()
        # Pass sessBuffer as updateLog to fn
        if cmd == 'ok':
            if len(self.sessBuffer) == 0:
                return self.protocol.sendData('Nothing to save')
               
            return fn(self, self.sessBuffer)
        else:
            # Unknown key
            if not SMPPClientConfigKeyMap.has_key(cmd):
                return self.protocol.sendData('Unknown SMPPClientConfig key: %s' % cmd)
            if cmd == 'cid':
                return self.protocol.sendData('Connector id can not be modified !')
            
            # Buffer key for later (when receiving 'ok')
            SMPPClientConfigKey = SMPPClientConfigKeyMap[cmd]
            self.sessBuffer[SMPPClientConfigKey] = self.protocol.str2num(arg)
            
            return self.protocol.sendData()
    return log_update_requests_and_call

class ConnectorExist:
    'Check if connector cid exist before passing it to fn'
    def __init__(self, cid_key):
        self.cid_key = cid_key
    def __call__(self, fn):
        cid_key = self.cid_key
        def exist_connector_and_call(self, *args, **kwargs):
            opts = args[1]
            cid = getattr(opts, cid_key)
    
            if self.pb['smppcm'].getConnector(cid) is not None:
                return fn(self, *args, **kwargs)
                
            return self.protocol.sendData('Unknown connector: %s' % cid)
        return exist_connector_and_call

class SmppCCManager(Manager):
    managerName = 'smppccm'
    
    def persist(self, arg, opts):
        if self.pb['smppcm'].remote_persist(opts.profile):
            self.protocol.sendData('%s configuration persisted (profile:%s)' % (self.managerName, opts.profile), prompt = False)
        else:
            self.protocol.sendData('Failed to persist %s configuration (profile:%s)' % (self.managerName, opts.profile), prompt = False)
    
    @defer.inlineCallbacks
    def load(self, arg, opts):
        r = yield self.pb['smppcm'].remote_load(opts.profile)

        if r:
            self.protocol.sendData('%s configuration loaded (profile:%s)' % (self.managerName, opts.profile), prompt = False)
        else:
            self.protocol.sendData('Failed to load %s configuration (profile:%s)' % (self.managerName, opts.profile), prompt = False)
    
    def list(self, arg, opts):
        connectors = self.pb['smppcm'].remote_connector_list()
        counter = 0
        
        if (len(connectors)) > 0:
            self.protocol.sendData("#%s %s %s %s %s" % ('Connector id'.ljust(35),
                                                                        'Service'.ljust(7),
                                                                        'Session'.ljust(16),
                                                                        'Starts'.ljust(6),
                                                                        'Stops'.ljust(5),
                                                                        ), prompt=False)
            for connector in connectors:
                counter += 1
                self.protocol.sendData("#%s %s %s %s %s" % (str(connector['id']).ljust(35),
                                                                  str('started' if connector['service_status'] == 1 else 'stopped').ljust(7),
                                                                  str(connector['session_state']).ljust(16),
                                                                  str(connector['start_count']).ljust(6),
                                                                  str(connector['stop_count']).ljust(5),
                                                                  ), prompt=False)
                self.protocol.sendData(prompt=False)        
        
        self.protocol.sendData('Total connectors: %s' % counter)
    
    @FilterSessionArgs
    @SMPPClientConfigBuild
    @defer.inlineCallbacks
    def add_session(self, SMPPClientConfigInstance):
        st = yield self.pb['smppcm'].remote_connector_add(pickle.dumps(SMPPClientConfigInstance, 2))
        
        if st:
            self.protocol.sendData('Successfully added connector [%s]' % SMPPClientConfigInstance.id, prompt=False)
            self.stopSession()
        else:
            self.protocol.sendData('Failed adding connector, check log for details')
    def add(self, arg, opts):
        return self.startSession(self.add_session,
                                 annoucement='Adding a new connector: (ok: save, ko: exit)',
                                 completitions=SMPPClientConfigKeyMap.keys())
    
    @FilterSessionArgs
    @SMPPClientConfigUpdate
    @defer.inlineCallbacks
    def update_session(self, updateLog):
        connector = self.pb['smppcm'].getConnector(self.sessionContext['cid'])
        connectorDetails = self.pb['smppcm'].getConnectorDetails(self.sessionContext['cid'])
        for key, value in updateLog.iteritems():
            connector['config'].set(key, value)
        
        if connector['config'].PendingRestart and connectorDetails['service_status'] == 1:
            self.protocol.sendData('Restarting connector [%s] for updates to take effect ...' % self.sessionContext['cid'], prompt=False)
            st = yield self.pb['smppcm'].remote_connector_stop(self.sessionContext['cid'])
            if not st:
                self.protocol.sendData('Failed stopping connector, check log for details', prompt=False)
            else:
                self.pb['smppcm'].remote_connector_start(self.sessionContext['cid'])
        
        self.protocol.sendData('Successfully updated connector [%s]' % self.sessionContext['cid'], prompt=False)
        self.stopSession()
    @ConnectorExist(cid_key='update')
    def update(self, arg, opts):
        return self.startSession(self.update_session,
                                 annoucement='Updating connector id [%s]: (ok: save, ko: exit)' % opts.update,
                                 completitions=SMPPClientConfigKeyMap.keys(),
                                 sessionContext={'cid': opts.update})
    
    @ConnectorExist(cid_key='remove')
    @defer.inlineCallbacks
    def remove(self, arg, opts):
        st = yield self.pb['smppcm'].remote_connector_remove(opts.remove)
        
        if st:
            self.protocol.sendData('Successfully removed connector id:%s' % opts.remove)
        else:
            self.protocol.sendData('Failed removing connector, check log for details')
    
    @ConnectorExist(cid_key='show')
    def show(self, arg, opts):
        connector = self.pb['smppcm'].getConnector(opts.show)
        for k, v in connector['config'].getAll().iteritems():
            self.protocol.sendData('%s %s' % (k, v), prompt=False)
        self.protocol.sendData()
    
    @ConnectorExist(cid_key='stop')
    @defer.inlineCallbacks
    def stop(self, arg, opts):
        st = yield self.pb['smppcm'].remote_connector_stop(opts.stop)
    
        if st:
            self.protocol.sendData('Successfully stopped connector id:%s' % opts.stop)
        else:
            self.protocol.sendData('Failed stopping connector, check log for details')

    @ConnectorExist(cid_key='start')
    def start(self, arg, opts):
        st = self.pb['smppcm'].remote_connector_start(opts.start)
    
        if st:
            self.protocol.sendData('Successfully started connector id:%s' % opts.start)
        else:
            self.protocol.sendData('Failed starting connector, check log for details')
