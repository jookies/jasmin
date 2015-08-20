#pylint: disable-msg=W0401,W0611
import re
import inspect
import pickle
import time
import jasmin
from dateutil import parser
from jasmin.protocols.cli.managers import PersistableManager, Session
from jasmin.routing.jasminApi import *
from jasmin.routing.Filters import (TransparentFilter, UserFilter, GroupFilter,
                                    ConnectorFilter, SourceAddrFilter, DestinationAddrFilter,
                                    ShortMessageFilter, DateIntervalFilter, TimeIntervalFilter, 
                                    EvalPyFilter)

# Since FiltersManager does not have any PB, there's no configuration for it
# Persist and Load are using CONFIG_STORE_PATH for persisting/loading filters
CONFIG_STORE_PATH = '/etc/jasmin/store'

# A config map between console-configuration keys and Filter keys.
FilterKeyMap = {'fid': 'fid', 'type': 'type'}

# Used to validate filter type while adding a new one
FILTERS = ['TransparentFilter', 'UserFilter', 'GroupFilter', 'ConnectorFilter', 'SourceAddrFilter', 'DestinationAddrFilter', 
             'ShortMessageFilter', 'DateIntervalFilter', 'TimeIntervalFilter', 'EvalPyFilter']

MOFILTERS = ['TransparentFilter', 'ConnectorFilter', 'SourceAddrFilter', 'DestinationAddrFilter', 
             'ShortMessageFilter', 'DateIntervalFilter', 'TimeIntervalFilter', 'EvalPyFilter']
MTFILTERS = ['TransparentFilter', 'UserFilter', 'GroupFilter', 'DestinationAddrFilter', 
             'ShortMessageFilter', 'DateIntervalFilter', 'TimeIntervalFilter', 'EvalPyFilter']

def FilterBuild(fCallback):
    '''Parse args and try to build a filter from  one of the filters in 
       jasmin.routing.Filters instance to pass it to fCallback'''
    def parse_args_and_call_with_instance(self, *args, **kwargs):
        cmd = args[0]
        arg = args[1]

        # Empty line
        if cmd is None:
            return self.protocol.sendData()
        # Initiate a filter from jasmin.routing.Filters with sessBuffer content
        if cmd == 'ok':
            # Remove filter_class and filter_args from self.sessBuffer before checking options
            # as these 2 options are not user-typed
            if len(self.sessBuffer) - 2 < len(self.protocol.sessionCompletitions):
                return self.protocol.sendData('You must set these options before saving: %s' % ', '.join(self.protocol.sessionCompletitions))
                
            _filter = {}
            for key, value in self.sessBuffer.iteritems():
                if key not in ['fid', 'type', 'filter_class', 'filter_args']:
                    _filter[key] = value
            try:
                # Prepare arguments
                if self.sessBuffer['type'] == 'TransparentFilter':
                    args = None
                elif self.sessBuffer['type'] == 'UserFilter':
                    args = {'user': User(_filter['uid'], None, None, None)}
                elif self.sessBuffer['type'] == 'GroupFilter':
                    args = {'group': Group(_filter['gid'])}
                elif self.sessBuffer['type'] == 'ConnectorFilter':
                    args = {'connector': Connector(_filter['cid'])}
                else:
                    args = _filter

                # Instanciate a Filter
                if args is not None:
                    FilterInstance = self.sessBuffer['filter_class'](**args)
                else:
                    FilterInstance = self.sessBuffer['filter_class']()
                    
                # Hand the instance to fCallback
                return fCallback(self, self.sessBuffer['fid'], FilterInstance)
            except Exception, e:
                return self.protocol.sendData('Error: %s' % str(e))
        else:
            # Unknown key
            fa = []
            if 'filter_args' in self.sessBuffer:
                fa = self.sessBuffer['filter_args']
            if cmd not in FilterKeyMap and cmd not in fa:
                return self.protocol.sendData('Unknown Filter key: %s' % cmd)

            # Validate fid syntax
            if cmd == 'fid':
                regex = re.compile(r'^[A-Za-z0-9_-]{1,16}$')
                if regex.match(arg) == None:
                    return self.protocol.sendData('Invalid Filter fid syntax: %s' % arg)
            
            # IF we got the type, check if it's a correct one
            if cmd == 'type':
                _type = None
                for _filter in FILTERS:
                    if arg.lower() == _filter.lower():
                        _type = _filter
                        break
                
                if _type is None:
                    return self.protocol.sendData('Unknown Filter type: "%s", available types: %s' % (arg, ', '.join(FILTERS)))
                
                # Before setting a new filter class, remove any previous filter
                # sessBuffer keys
                if 'fid' in self.sessBuffer:
                    self.sessBuffer = {'fid': self.sessBuffer['fid']}
                else:
                    self.sessBuffer = {}
                
                self.sessBuffer['type'] = _type
                # Filter class name must be already imported from jasmin.routing.Filters
                # in order to get it from globals()
                self.sessBuffer['filter_class'] = globals()[_type]

                # Show Filter help and save Filter args
                fargs = inspect.getargspec(self.sessBuffer['filter_class'].__init__).args
                # Remove 'self' from args
                del(fargs[0])
                FilterClassArgs = []
                # Format args
                for arg in fargs:
                    if arg == 'user':
                        FilterClassArgs.append('uid')
                    elif arg == 'group':
                        FilterClassArgs.append('gid')
                    elif arg == 'connector':
                        FilterClassArgs.append('cid')
                    else:
                        FilterClassArgs.append(arg)
                self.sessBuffer['filter_args'] = FilterClassArgs
                
                if len(FilterClassArgs) > 0:
                    # Update completitions
                    self.protocol.sessionCompletitions = FilterKeyMap.keys()+FilterClassArgs
                    
                    return self.protocol.sendData('%s arguments:\n%s' % (self.sessBuffer['filter_class'], ', '.join(FilterClassArgs)))
            else:
                # Validate regex options
                if cmd in ['destination_addr', 'source_addr', 'short_message']:
                    try:
                        re.compile(arg)
                    except Exception:
                        return self.protocol.sendData('%s option is not a valid regular expression' % (cmd))
                    
                # Validate & transform timeInterval and dateInterval options
                if cmd in ['timeInterval', 'dateInterval']:
                    limits = arg.split(';')
                    if len(limits) != 2:
                        return self.protocol.sendData('%s option value must be composed of 2 values with a ";" separator.' % (cmd))
                    
                    # Regex validation
                    re_time = re.compile(r'^\d{2}:\d{2}:\d{2}$')
                    re_date = re.compile(r'^\d{4}-\d{2}-\d{2}$')
                    
                    # Validate format and type
                    for l in limits:
                        try:
                            # Validate format with regex
                            if (cmd == 'dateInterval' and re_date.match(l) is None) or (cmd == 'timeInterval' and re_time.match(l) is None):
                                raise ValueError('Format error: %s' % l)
                            
                            # Validate type
                            parser.parse(l)
                        except (TypeError, ValueError):
                            return self.protocol.sendData('%s is not a valid %s value' % (l, cmd))
                        
                    # Transform values
                    if cmd == 'dateInterval':
                        _arg = [parser.parse(limits[0]).date(), parser.parse(limits[1]).date()]
                    elif cmd == 'timeInterval':
                        _arg = [parser.parse(limits[0]).time(), parser.parse(limits[1]).time()]

                    # Right border must be greater than left border
                    if _arg[0] >= _arg[1]:
                        return self.protocol.sendData('Right border must be greater than left border')
                    else:
                        arg = _arg
                # Validate pyCode options
                if cmd == 'pyCode':
                    try:
                        # Open file and get its content
                        with open(arg, 'r') as content_file:
                            pyCode = content_file.read()
                            
                        # Test compilation of the script
                        compile(pyCode, '', 'exec')
                    except IOError, e:
                        return self.protocol.sendData('[IO]: %s' % str(e))
                    except SyntaxError, e:
                        return self.protocol.sendData('[Syntax]: %s' % str(e))
                    except e:
                        return self.protocol.sendData('[Unknown]: %s' % str(e))
                    
                    arg = pyCode

                # Buffer key for later Filter initiating
                if cmd not in fa:
                    FilterKey = FilterKeyMap[cmd]
                else:
                    FilterKey = cmd
                self.sessBuffer[FilterKey] = arg
            
            return self.protocol.sendData()
    return parse_args_and_call_with_instance

class FilterExist:
    'Check if filter fid exist before passing it to fCallback'
    def __init__(self, fid_key):
        self.fid_key = fid_key
    def __call__(self, fCallback):
        fid_key = self.fid_key
        def exist_filter_and_call(self, *args, **kwargs):
            opts = args[1]
            fid = getattr(opts, fid_key)
    
            for _filterId in self.filters.iterkeys():
                if fid == _filterId:
                    return fCallback(self, *args, **kwargs)                
                
            return self.protocol.sendData('Unknown Filter: %s' % fid)
        return exist_filter_and_call

class FiltersManager(PersistableManager):
    '''FiltersManager does not have a PB like other managers (router, users, groups ...), it is
    used to simplify route adding syntax by creating reusable filters, these filters are saved in
    self.filters'''
    managerName = 'filter'
    
    def __init__(self, protocol):
        PersistableManager.__init__(self, protocol, None)
        
        self.filters = {}
        
        # Load filters from disk on each instanciation with a jcli session
        # Since there's no PB, the filters belong to the current jcli session context
        try:
            self._load()
            
            protocol.log.info('%s configuration loaded (default profile)' % (self.managerName))
        except Exception, e:
            protocol.log.error('Config loading error: %s' % str(e))
    
    def persist(self, arg, opts):
        path = '%s/%s.filters' % (CONFIG_STORE_PATH, opts.profile)
        
        try:
            # Write configuration with datetime stamp
            fh = open(path,'w')
            fh.write('Persisted on %s [Jasmin %s]\n' % (time.strftime("%c"), jasmin.get_release()))
            fh.write(pickle.dumps(self.filters, 2))
            fh.close()
        except IOError:
            return self.protocol.sendData('Cannot persist to %s' % path)
        except Exception, e:
            return self.protocol.sendData('Unknown error occurred while persisting configuration: %s' % e)
        
        self.protocol.sendData('%s configuration persisted (profile:%s)' % (self.managerName, opts.profile), prompt = False)
        
    def load(self, arg, opts):
        try:
            self._load(opts.profile)
            
            self.protocol.sendData('%s configuration loaded (profile:%s)' % (self.managerName, opts.profile), prompt = False)
        except:
            self.protocol.sendData('Failed to load %s configuration (profile:%s)' % (self.managerName, opts.profile), prompt = False)
        
    def _load(self, profile = 'jcli-prod'):
        path = '%s/%s.filters' % (CONFIG_STORE_PATH, profile)

        try:
            # Load configuration from file
            fh = open(path,'r')
            lines = fh.readlines()
            fh.close()
                        
            # Apply configuration
            self.filters = pickle.loads(''.join(lines[1:]))
        except IOError, e:
            raise Exception('Cannot load from %s: %s' % (path, str(e)))
        except Exception, e:
            raise Exception('Unknown error while loading configuration: %s' % e)
                    
    def list(self, arg, opts):
        counter = 0
        
        if (len(self.filters)) > 0:
            self.protocol.sendData("#%s %s %s %s" % ('Filter id'.ljust(16),
                                                                        'Type'.ljust(22),
                                                                        'Routes'.ljust(6),
                                                                        'Description'.ljust(32),
                                                                        ), prompt=False)
            for fid, _filter in self.filters.iteritems():
                counter += 1
                routes = ''
                if _filter.__class__.__name__ in MOFILTERS:
                    routes += 'MO '
                if _filter.__class__.__name__ in MTFILTERS:
                    routes += 'MT'
                self.protocol.sendData("#%s %s %s %s" % (str(fid).ljust(16),
                                                                  str(_filter.__class__.__name__).ljust(22),
                                                                  routes.ljust(6),
                                                                  repr(_filter).ljust(32),
                                                                  ), prompt=False)
                self.protocol.sendData(prompt=False)        
        
        self.protocol.sendData('Total Filters: %s' % counter)
    
    @Session
    @FilterBuild
    def add_session(self, fid, FilterInstance):
        self.filters[fid] = FilterInstance
        self.protocol.sendData('Successfully added Filter [%s] with fid:%s' % (FilterInstance.__class__.__name__, fid), prompt=False)
        self.stopSession()
    def add(self, arg, opts):
        return self.startSession(self.add_session,
                                 annoucement='Adding a new Filter: (ok: save, ko: exit)',
                                 completitions=FilterKeyMap.keys(),
                                 )
    
    @FilterExist(fid_key='remove')
    def remove(self, arg, opts):
        del(self.filters[opts.remove])
        self.protocol.sendData('Successfully removed Filter id:%s' % opts.remove)
    
    @FilterExist(fid_key='show')
    def show(self, arg, opts):
        self.protocol.sendData('%s' % str(self.filters[opts.show]))