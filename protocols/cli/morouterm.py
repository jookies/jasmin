import inspect
import pickle
from managers import Manager, Session
from filtersm import MOFILTERS
from jasmin.routing.jasminApi import SmppClientConnector
from jasmin.routing.Routes import (DefaultRoute, StaticMORoute, RandomRoundrobinMORoute)

MOROUTES = ['DefaultRoute', 'StaticMORoute', 'RandomRoundrobinMORoute']

# A config map between console-configuration keys and Route keys.
MORouteKeyMap = {'order': 'order', 'type': 'type'}

def MORouteBuild(fn):
    '''Parse args and try to build a route from  one of the routes in 
       jasmin.routing.Routes instance to pass it to fn'''
    def parse_args_and_call_with_instance(self, *args, **kwargs):
        cmd = args[0]
        arg = args[1]

        # Empty line
        if cmd is None:
            return self.protocol.sendData()
        # Initiate a route from jasmin.routing.Routes with sessBuffer content
        if cmd == 'ok':
            # Remove route_class and route_args from self.sessBuffer before checking options
            # as these 2 options are not user-typed
            if len(self.sessBuffer) - 2 < len(self.protocol.sessionCompletitions):
                return self.protocol.sendData('You must set these options before saving: %s' % ', '.join(self.protocol.sessionCompletitions))
                
            route = {}
            for key, value in self.sessBuffer.iteritems():
                if key not in ['order', 'type', 'route_class', 'route_args']:
                    route[key] = value
            try:
                # Instanciate a Route
                RouteInstance = self.sessBuffer['route_class'](**route)
                    
                # Hand the instance to fn
                return fn(self, self.sessBuffer['order'], RouteInstance)
            except Exception, e:
                return self.protocol.sendData('Error: %s' % str(e))
        else:
            # Unknown key
            ra = []
            if 'route_args' in self.sessBuffer:
                ra = self.sessBuffer['route_args']
            if not MORouteKeyMap.has_key(cmd) and cmd not in ra:
                return self.protocol.sendData('Unknown Route key: %s' % cmd)
            
            # IF we got the type, check if it's a correct one
            if cmd == 'type':
                _type = None
                for route in MOROUTES:
                    if arg.lower() == route.lower():
                        _type = route
                        break
                
                if _type is None:
                    return self.protocol.sendData('Unknown MO Route type:%s, available types: %s' % (arg, ', '.join(MOROUTES)))
                elif _type == 'DefaultRoute':
                    self.sessBuffer['order'] = 0
                
                # Before setting a new route class, remove any previous route
                # sessBuffer keys
                if 'order' in self.sessBuffer:
                    self.sessBuffer = {'order': self.sessBuffer['order']}
                else:
                    self.sessBuffer = {}
                
                self.sessBuffer['type'] = _type
                # Route class name must be already imported from jasmin.routing.Routes
                # in order to get it from globals()
                self.sessBuffer['route_class'] = globals()[_type]

                # Show Route help and save Route args
                RouteClassArgs = inspect.getargspec(self.sessBuffer['route_class'].__init__).args
                # Remove 'self' from args
                del(RouteClassArgs[0])
                self.sessBuffer['route_args'] = RouteClassArgs
                
                if len(RouteClassArgs) > 0:
                    # Update completitions
                    self.protocol.sessionCompletitions = MORouteKeyMap.keys()+RouteClassArgs
                    
                    return self.protocol.sendData('%s arguments:\n%s' % (self.sessBuffer['route_class'], ', '.join(RouteClassArgs)))
            else:
                # DefaultRoute's order is always zero
                if cmd == 'order':
                    if arg != 0 and 'type' in self.sessBuffer and self.sessBuffer['type'] == 'DefaultRoute':
                        self.sessBuffer['order'] = 0
                        return self.protocol.sendData('Route order forced to 0 since it is a DefaultRoute')
                    elif not arg.isdigit() or int(arg) < 0:
                        return self.protocol.sendData('Route order must be a positive integer')
                    else:
                        arg = int(arg)
                    
                # Validate connector
                if cmd == 'connector':
                    c = self.pb['smppcm'].getConnector(arg)
                    if c is None:
                        return self.protocol.sendData('Unknown cid: %s' % (arg))
                    else:
                        arg = SmppClientConnector(arg) # Can be a HttpConnector also
                    
                # Validate connectors
                if cmd == 'connectors':
                    CIDs = arg.split(';')
                    if len(CIDs) == 1:
                        return self.protocol.sendData('%s option value must contain a minimum of 2 connector IDs separated with ";".' % (cmd))

                    arg = []
                    for cid in CIDs:
                        c = self.pb['smppcm'].getConnector(cid)
                        if c is None:
                            return self.protocol.sendData('Unknown cid: %s' % (cid))
                        else:
                            arg.append(SmppClientConnector(cid)) # Can be a HttpConnector also

                # Validate filters
                if cmd == 'filters':
                    FIDs = arg.split(';')
                    
                    arg = []
                    for fid in FIDs:
                        if fid not in self.protocol.managers['filter'].filters:
                            return self.protocol.sendData('Unknown fid: %s' % (fid))
                        else:
                            _Filter = self.protocol.managers['filter'].filters[fid]
                            
                            if _Filter.__class__.__name__ not in MOFILTERS:
                                return self.protocol.sendData('%s#%s is not a valid filter for MORoute' % (_Filter.__class__.__name__, fid))
                            else:
                                arg.append(_Filter)

                # Buffer key for later Route initiating
                if cmd not in ra:
                    RouteKey = MORouteKeyMap[cmd]
                else:
                    RouteKey = cmd
                self.sessBuffer[RouteKey] = arg
            
            return self.protocol.sendData()
    return parse_args_and_call_with_instance

class MORouteExist:
    'Check if a mo route exist with a given order before passing it to fn'
    def __init__(self, order_key):
        self.order_key = order_key
    def __call__(self, fn):
        order_key = self.order_key
        def exist_moroute_and_call(self, *args, **kwargs):
            opts = args[1]
            order = getattr(opts, order_key)
    
            raise NotImplementedError('TODO (ORDER:%s)' % order)
            #if self.pb['router'].getUser(order) is not None:
            #    return fn(self, *args, **kwargs)
                
            return self.protocol.sendData('No mo routes at order %s' % order)
        return exist_moroute_and_call
    
class MoRouterManager(Manager):
    managerName = 'morouter'
    
    def persist(self, arg, opts):
        print 'NotImplemented persist method in %s manager' % self.managerName
        
    def load(self, arg, opts):
        print 'NotImplemented persist method in %s manager' % self.managerName
            
    def list(self, arg, opts):
        raise NotImplementedError
    
    @Session
    @MORouteBuild
    def add_session(self, order, RouteInstance):
        self.pb['router'].remote_moroute_add(pickle.dumps(RouteInstance, 2), order)
        
        self.protocol.sendData('Successfully added MORoute [%s] with order:%s' % (RouteInstance.__class__.__name__, order), prompt=False)
        self.stopSession()
    def add(self, arg, opts):
        return self.startSession(self.add_session,
                                 annoucement='Adding a new MO Route: (ok: save, ko: exit)',
                                 completitions=MORouteKeyMap.keys())
    
    @MORouteExist(order_key='remove')
    def remove(self, arg, opts):
        raise NotImplementedError
    
    @MORouteExist(order_key='show')
    def show(self, arg, opts):
        raise NotImplementedError
        
    def flush(self, arg, opts):
        raise NotImplementedError