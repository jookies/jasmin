import inspect
import pickle
from managers import Manager, Session
from filtersm import MTFILTERS
from jasmin.routing.jasminApi import SmppClientConnector
from jasmin.routing.Routes import (DefaultRoute, StaticMTRoute, RandomRoundrobinMTRoute)

MTROUTES = ['DefaultRoute', 'StaticMTRoute', 'RandomRoundrobinMTRoute']

# A config map between console-configuration keys and Route keys.
MTRouteKeyMap = {'order': 'order', 'type': 'type'}

def MTRouteBuild(fn):
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
            if not MTRouteKeyMap.has_key(cmd) and cmd not in ra:
                return self.protocol.sendData('Unknown Route key: %s' % cmd)
            
            # IF we got the type, check if it's a correct one
            if cmd == 'type':
                _type = None
                for route in MTROUTES:
                    if arg.lower() == route.lower():
                        _type = route
                        break
                
                if _type is None:
                    return self.protocol.sendData('Unknown MT Route type:%s, available types: %s' % (arg, ', '.join(MTROUTES)))
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
                    self.protocol.sessionCompletitions = MTRouteKeyMap.keys()+RouteClassArgs
                    
                    return self.protocol.sendData('%s arguments:\n%s' % (self.sessBuffer['route_class'], ', '.join(RouteClassArgs)))
            else:
                # DefaultRoute's order is always zero
                if cmd == 'order':
                    if arg != '0' and 'type' in self.sessBuffer and self.sessBuffer['type'] == 'DefaultRoute':
                        self.sessBuffer['order'] = 0
                        return self.protocol.sendData('Route order forced to 0 since it is a DefaultRoute')
                    elif arg == '0' and 'type' in self.sessBuffer and self.sessBuffer['type'] != 'DefaultRoute':
                        return self.protocol.sendData('This route order (0) is reserved for DefaultRoute only')
                    elif not arg.isdigit() or int(arg) < 0:
                        return self.protocol.sendData('Route order must be a positive integer')
                    else:
                        arg = int(arg)
                    
                # Validate connector
                if cmd == 'connector':
                    if self.pb['smppcm'].getConnector(arg) is None:
                        return self.protocol.sendData('Unknown cid: %s' % (arg))
                    else:
                        # Make instance of SmppClientConnector
                        arg = SmppClientConnector(self.pb['smppcm'].getConnector(arg)['id'])
                    
                # Validate connectors
                if cmd == 'connectors':
                    CIDs = arg.split(';')
                    if len(CIDs) == 1:
                        return self.protocol.sendData('%s option value must contain a minimum of 2 connector IDs separated with ";".' % (cmd))

                    arg = []
                    for cid in CIDs:
                        if self.pb['smppcm'].getConnector(cid) is None:
                            return self.protocol.sendData('Unknown cid: %s' % (cid))
                        else:
                            # Make instance of SmppClientConnector
                            arg.append(SmppClientConnector(self.pb['smppcm'].getConnector(arg)['id']))

                # Validate filters
                if cmd == 'filters':
                    FIDs = arg.split(';')
                    
                    arg = []
                    for fid in FIDs:
                        if fid not in self.protocol.managers['filter'].filters:
                            return self.protocol.sendData('Unknown fid: %s' % (fid))
                        else:
                            _Filter = self.protocol.managers['filter'].filters[fid]
                            
                            if _Filter.__class__.__name__ not in MTFILTERS:
                                return self.protocol.sendData('%s#%s is not a valid filter for MTRoute' % (_Filter.__class__.__name__, fid))
                            else:
                                arg.append(_Filter)

                # Buffer key for later Route initiating
                if cmd not in ra:
                    RouteKey = MTRouteKeyMap[cmd]
                else:
                    RouteKey = cmd
                self.sessBuffer[RouteKey] = arg
            
            return self.protocol.sendData()
    return parse_args_and_call_with_instance

class MTRouteExist:
    'Check if a mt route exist with a given order before passing it to fn'
    def __init__(self, order_key):
        self.order_key = order_key
    def __call__(self, fn):
        order_key = self.order_key
        def exist_mtroute_and_call(self, *args, **kwargs):
            opts = args[1]
            order = getattr(opts, order_key)
            
            if not order.isdigit() or int(order) < 0:
                return self.protocol.sendData('MT Route order must be a positive integer')
    
            if self.pb['router'].getMTRoute(int(order)) is not None:
                return fn(self, *args, **kwargs)
                
            return self.protocol.sendData('Unknown MT Route: %s' % order)
        return exist_mtroute_and_call
    
class MtRouterManager(Manager):
    managerName = 'mtrouter'
    
    def persist(self, arg, opts):
        if self.pb['router'].remote_persist(opts.profile, 'mtroutes'):
            self.protocol.sendData('%s configuration persisted (profile:%s)' % (self.managerName, opts.profile), prompt = False)
        else:
            self.protocol.sendData('Failed to persist %s configuration (profile:%s)' % (self.managerName, opts.profile), prompt = False)
    
    def load(self, arg, opts):
        r = self.pb['router'].remote_load(opts.profile, 'mtroutes')

        if r:
            self.protocol.sendData('%s configuration loaded (profile:%s)' % (self.managerName, opts.profile), prompt = False)
        else:
            self.protocol.sendData('Failed to load %s configuration (profile:%s)' % (self.managerName, opts.profile), prompt = False)
           
    def list(self, arg, opts):
        mtroutes = pickle.loads(self.pb['router'].remote_mtroute_get_all())
        counter = 0
        
        if (len(mtroutes)) > 0:
            self.protocol.sendData("#%s %s %s %s" % ('MT Route order'.ljust(16),
                                                                        'Type'.ljust(23),
                                                                        'Connector ID(s)'.ljust(32),
                                                                        'Filter(s)'.ljust(64),
                                                                        ), prompt=False)
            for e in mtroutes:
                order = e.keys()[0]
                mtroute = e[order]
                counter += 1
                
                connectors = ''
                # Prepare display for connectors
                if type(mtroute.connector) is list:
                    for c in mtroute.connector:
                        if connectors != '':
                            connectors+= ', '
                        connectors+= c.cid
                else:
                    connectors = mtroute.connector.cid
                    
                filters = ''
                # Prepare display for filters
                for f in mtroute.filters:
                    if filters != '':
                        filters+= ', '
                    filters+= repr(f)

                self.protocol.sendData("#%s %s %s %s" % (str(order).ljust(16),
                                                                  str(mtroute.__class__.__name__).ljust(23),
                                                                  connectors.ljust(32),
                                                                  filters.ljust(64),
                                                                  ), prompt=False)
                self.protocol.sendData(prompt=False)        
        
        self.protocol.sendData('Total MT Routes: %s' % counter)
    
    @Session
    @MTRouteBuild
    def add_session(self, order, RouteInstance):
        st = self.pb['router'].remote_mtroute_add(pickle.dumps(RouteInstance, 2), order)
        
        if st:
            self.protocol.sendData('Successfully added MTRoute [%s] with order:%s' % (RouteInstance.__class__.__name__, order), prompt=False)
            self.stopSession()
        else:
            self.protocol.sendData('Failed adding MTRoute, check log for details')
    def add(self, arg, opts):
        return self.startSession(self.add_session,
                                 annoucement='Adding a new MT Route: (ok: save, ko: exit)',
                                 completitions=MTRouteKeyMap.keys())
    
    @MTRouteExist(order_key='remove')
    def remove(self, arg, opts):
        st = self.pb['router'].remote_mtroute_remove(int(opts.remove))
        
        if st:
            self.protocol.sendData('Successfully removed MT Route with order:%s' % opts.remove)
        else:
            self.protocol.sendData('Failed removing MT Route, check log for details')
    
    @MTRouteExist(order_key='show')
    def show(self, arg, opts):
        r = self.pb['router'].getMTRoute(int(opts.show))
        self.protocol.sendData(str(r))
        
    def flush(self, arg, opts):
        tableSize = len(pickle.loads(self.pb['router'].remote_mtroute_get_all()))
        self.pb['router'].remote_mtroute_flush()
        self.protocol.sendData('Successfully flushed MT Route table (%s flushed entries)' % tableSize)