import pickle
from twisted.internet import defer
from jasmin.protocols.smpp.configs import SMPPClientConfigMap, SMPPClientConfig
from managers import Manager, FilterSessionArgs

def SMPPClientConfigBuild(fn):
    'Parse args and try to build a SMPPClientConfig instance to pass it to fn'
    def parse_args_and_call_with_instance(self, *args, **kwargs):
        cmd = args[0]
        arg = args[1]

        # Empty line
        if cmd is None:
            return self.protocol.sendData()
        # Initiate SMPPClientConfig with sessBuffer content
        if cmd == 'ok':
            if len(self.sessBuffer) == 0:
                return self.protocol.sendData('You must set at least connector id (cid) before saving !')
                
            connector = {}
            for key, value in self.sessBuffer.iteritems():
                connector[key] = value
            try:
                SMPPClientConfigInstance = SMPPClientConfig(**connector)
                # Hand the instance to fn
                return fn(self, SMPPClientConfigInstance = SMPPClientConfigInstance, *args, **kwargs)
            except Exception, e:
                return self.protocol.sendData('Error: %s' % str(e))
        # Unknown key
        if not SMPPClientConfigMap.has_key(cmd):
            return self.protocol.sendData('Unknown SMPPClientConfig key ! %s' % cmd)
        
        # Buffer key for later SMPPClientConfig initiating
        SMPPClientConfigKey = SMPPClientConfigMap[cmd]
        self.sessBuffer[SMPPClientConfigKey] = self.protocol.str2num(arg)
        
        return self.protocol.sendData()
    return parse_args_and_call_with_instance

def ConnectorExist(fn):
    'Check if connector cid exist before passing it to fn'
    def exist_connector_and_call(self, *args, **kwargs):
        opts = args[1]

        for c in self.pb['smppcm'].remote_connector_list():
            if opts.remove == c['id']:
                return fn(self, *args, **kwargs)
            
        return self.protocol.sendData('Unknown connector ! %s' % opts.remove)
    return exist_connector_and_call

class SmppCCManager(Manager):
    def list(self):
        connectors = self.pb['smppcm'].remote_connector_list()
        counter = 0
        
        if (len(connectors)) > 0:
            self.protocol.sendData("#%s %s %s %s %s" % ('Connector id'.ljust(35),
                                                                        'Service'.ljust(7),
                                                                        'Session'.ljust(16),
                                                                        'Starts'.ljust(6),
                                                                        'Stops'.ljust(5),
                                                                        ), prompt = False)
            for connector in connectors:
                counter+= 1
                self.protocol.sendData("#%s %s %s %s %s" % (str(connector['id']).ljust(35), 
                                                                  str('started' if connector['service_status'] == 1 else 'stopped').ljust(7), 
                                                                  str(connector['session_state']).ljust(16),
                                                                  str(connector['start_count']).ljust(6),
                                                                  str(connector['stop_count']).ljust(5),
                                                                  ), prompt = False)
                self.protocol.sendData(prompt = False)        
        
        self.protocol.sendData('Total: %s' % counter)
    
    @FilterSessionArgs
    @SMPPClientConfigBuild
    @defer.inlineCallbacks
    def add_session(self, cmd = None, args = None, line = None, SMPPClientConfigInstance = None):
        if cmd == 'ok' and SMPPClientConfigInstance is not None:
            st = yield self.pb['smppcm'].remote_connector_add(pickle.dumps(SMPPClientConfigInstance))
            
            if st:
                self.protocol.sendData('Successfully added connector id:%s' % SMPPClientConfigInstance.id, prompt = False)
                self.stopSession()
            else:
                self.protocol.sendData('Failed adding connector, check log for details')
    def add(self, arg):
        return self.startSession(self.add_session, 'Adding a new connector: (ok: save, ko: exit)')
    
    @ConnectorExist
    @defer.inlineCallbacks
    def remove(self, arg, opts):
        st = yield self.pb['smppcm'].remote_connector_remove(opts.remove)
        
        if st:
            self.protocol.sendData('Successfully removed connector id:%s' % opts.remove)
        else:
            self.protocol.sendData('Failed removing connector, check log for details')
    
    @ConnectorExist
    def show(self, arg, opts):
        pass
    
    def update(self, arg):
        pass
    
    def stop(self, arg):
        pass
    
    def start(self, arg):
        pass