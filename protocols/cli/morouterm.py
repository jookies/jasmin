from managers import Manager

class ConnectorsExist:
    'Check if connectors CIDs exist before passing it to fn'
    def __init__(self, cids_key, subkey_position):
        self.cids_key = cids_key
        self.subkey_position = subkey_position
    def __call__(self, fn):
        cids_key = self.cids_key
        subkey_position = self.subkey_position
        def exist_connectors_and_call(self, *args, **kwargs):
            opts = args[1]
            cids = getattr(opts, cids_key)[subkey_position]
            
            for cid in cids:
                if self.pb['smppcm'].getConnector(cid) is None:
                    return self.protocol.sendData('Unknown connector: %s' % cid)
                
            return fn(self, *args, **kwargs)
        return exist_connectors_and_call
    
class RouteExist:
    '''Check if a route is already set within a given ORDER and return an error depending
       on mustExist parameter where an error will be returned if:
       - if mustExist and route is not found
       - if not mustExist and route is found'''
    def __init__(self, order_key, subkey_position, mustExist = True):
        self.order_key = order_key
        self.subkey_position = subkey_position
        self.mustExist = mustExist
    def __call__(self, fn):
        order_key = self.order_key
        subkey_position = self.subkey_position
        mustExist = self.mustExist
        def exist_connectors_and_call(self, *args, **kwargs):
            opts = args[1]
            order = getattr(opts, order_key)[subkey_position]
            
            # @TODO: Check existence of the route
            
            return fn(self, *args, **kwargs)
        return exist_connectors_and_call

class MoRouterManager(Manager):
    managerName = 'morouter'
    
    def persist(self, arg, opts):
        print 'NotImplemented persist method in %s manager' % self.managerName
        
    def load(self, arg, opts):
        print 'NotImplemented persist method in %s manager' % self.managerName
            
    def list(self, arg, opts):
        raise NotImplementedError
    
    @RouteExist(order_key='add', subkey_position = 0, mustExist=False)
    @ConnectorsExist(cids_key='add', subkey_position = 2)
    def add(self, arg, opts):
        order, routename, connectors, filters = opts.add
    
    def flush(self, arg, opts):
        raise NotImplementedError