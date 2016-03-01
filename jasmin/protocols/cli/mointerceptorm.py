#pylint: disable=W0611
import re
import inspect
import cPickle as pickle
from jasmin.protocols.cli.managers import PersistableManager, Session
from jasmin.protocols.cli.filtersm import MOFILTERS
from jasmin.routing.jasminApi import MOInterceptorScript
from jasmin.routing.Interceptors import (DefaultInterceptor, StaticMOInterceptor)

MOINTERCEPTORS = ['DefaultInterceptor', 'StaticMOInterceptor']

# A config map between console-configuration keys and Interceptor keys.
MOInterceptorKeyMap = {'order': 'order', 'type': 'type'}

class InvalidScriptSyntax(Exception):
    pass

def validate_typed_script(script):
    'Will ensure the script exists and compilable'

    m = re.match(r'(python2)\((.*)\)', script, re.I)
    if not m:
        raise InvalidScriptSyntax('Invalid syntax for script, must be python2(/path/to/script).')
    else:
        language = m.group(1).lower()
        script_path = m.group(2)

    return language, script_path

def MOInterceptorBuild(fCallback):
    '''Parse args and try to build an interceptor from  one of the interceptors in
       jasmin.routing.Interceptors instance to pass it to fCallback'''
    def parse_args_and_call_with_instance(self, *args, **kwargs):
        cmd = args[0]
        arg = args[1]

        # Empty line
        if cmd is None:
            return self.protocol.sendData()
        # Initiate an interceptor from jasmin.routing.Interceptors with sessBuffer content
        if cmd == 'ok':
            # Remove interceptor_class and interceptor_args from self.sessBuffer before checking options
            # as these 2 options are not user-typed
            if len(self.sessBuffer) - 2 < len(self.protocol.sessionCompletitions):
                return self.protocol.sendData('You must set these options before saving: %s' % ', '.join(
                        self.protocol.sessionCompletitions))

            interceptor = {}
            for key, value in self.sessBuffer.iteritems():
                if key not in ['order', 'type', 'interceptor_class', 'interceptor_args']:
                    interceptor[key] = value
            try:
                # Instanciate an Interceptor
                InterceptorInstance = self.sessBuffer['interceptor_class'](**interceptor)

                # Hand the instance to fCallback
                return fCallback(self, self.sessBuffer['order'], InterceptorInstance)
            except Exception, e:
                return self.protocol.sendData('Error: %s' % str(e))
        else:
            # Unknown key
            ia = []
            if 'interceptor_args' in self.sessBuffer:
                ia = self.sessBuffer['interceptor_args']
            if cmd not in MOInterceptorKeyMap and cmd not in ia:
                return self.protocol.sendData('Unknown Interceptor key: %s' % cmd)

            # IF we got the type, check if it's a correct one
            if cmd == 'type':
                _type = None
                for interceptor in MOINTERCEPTORS:
                    if arg.lower() == interceptor.lower():
                        _type = interceptor
                        break

                if _type is None:
                    return self.protocol.sendData(
                        'Unknown MO Interceptor type:%s, available types: %s' % (
                            arg, ', '.join(MOINTERCEPTORS)))
                elif _type == 'DefaultInterceptor':
                    self.sessBuffer['order'] = 0

                # Before setting a new interceptor class, remove any previous interceptor
                # sessBuffer keys
                if 'order' in self.sessBuffer:
                    self.sessBuffer = {'order': self.sessBuffer['order']}
                else:
                    self.sessBuffer = {}

                self.sessBuffer['type'] = _type
                # Interceptor class name must be already imported from jasmin.routing.Interceptors
                # in order to get it from globals()
                self.sessBuffer['interceptor_class'] = globals()[_type]

                # Show Interceptor help and save Interceptor args
                InterceptorClassArgs = inspect.getargspec(self.sessBuffer['interceptor_class'].__init__).args
                if 'self' in InterceptorClassArgs:
                    # Remove 'self' from args
                    InterceptorClassArgs.remove('self')
                self.sessBuffer['interceptor_args'] = InterceptorClassArgs

                if len(InterceptorClassArgs) > 0:
                    # Update completitions
                    self.protocol.sessionCompletitions = MOInterceptorKeyMap.keys()+InterceptorClassArgs

                    return self.protocol.sendData(
                        '%s arguments:\n%s' % (
                            self.sessBuffer['interceptor_class'], ', '.join(InterceptorClassArgs)))
            else:
                # DefaultInterceptor's order is always zero
                if cmd == 'order':
                    if (arg != '0' and 'type' in self.sessBuffer
                            and self.sessBuffer['type'] == 'DefaultInterceptor'):
                        self.sessBuffer['order'] = 0
                        return self.protocol.sendData(
                            'Interceptor order forced to 0 since it is a DefaultInterceptor')
                    elif (arg == '0' and 'type' in self.sessBuffer and
                        self.sessBuffer['type'] != 'DefaultInterceptor'):
                        return self.protocol.sendData(
                            'This interceptor order (0) is reserved for DefaultInterceptor only')
                    elif not arg.isdigit() or int(arg) < 0:
                        return self.protocol.sendData('Interceptor order must be a positive integer')
                    else:
                        arg = int(arg)

                # Validate script
                if cmd == 'script':
                    try:
                        stype, script_path = validate_typed_script(arg)

                        if stype == 'python2':
                            # Open file and get its content
                            with open(script_path, 'r') as content_file:
                                pyCode = content_file.read()

                            # Test compilation of the script
                            compile(pyCode, '', 'exec')
                        else:
                            raise NotImplementedError("Not implemented yet !")
                    except IOError, e:
                        return self.protocol.sendData('[IO]: %s' % str(e))
                    except SyntaxError, e:
                        return self.protocol.sendData('[Syntax]: %s' % str(e))
                    except Exception, e:
                        return self.protocol.sendData('%s' % str(e))
                    else:
                        arg = MOInterceptorScript(pyCode)

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
                                return self.protocol.sendData(
                                    '%s#%s is not a valid filter for MOInterceptor (not in MOFILTERS)' % (
                                        _Filter.__class__.__name__, fid
                                    )
                                )
                            else:
                                arg.append(_Filter)

                # Buffer key for later Interceptor initiating
                if cmd not in ia:
                    InterceptorKey = MOInterceptorKeyMap[cmd]
                else:
                    InterceptorKey = cmd
                self.sessBuffer[InterceptorKey] = arg

            return self.protocol.sendData()
    return parse_args_and_call_with_instance

class MOInterceptorExist:
    'Check if a mo interceptor exist with a given order before passing it to fCallback'
    def __init__(self, order_key):
        self.order_key = order_key
    def __call__(self, fCallback):
        order_key = self.order_key
        def exist_mointerceptor_and_call(self, *args, **kwargs):
            opts = args[1]
            order = getattr(opts, order_key)

            if not order.isdigit() or int(order) < 0:
                return self.protocol.sendData('MO Interceptor order must be a positive integer')

            if self.pb['router'].getMOInterceptor(int(order)) is not None:
                return fCallback(self, *args, **kwargs)

            return self.protocol.sendData('Unknown MO Interceptor: %s' % order)
        return exist_mointerceptor_and_call

class MoInterceptorManager(PersistableManager):
    "MO Interceptor manager logics"
    managerName = 'mointerceptor'

    def persist(self, arg, opts):
        if self.pb['router'].perspective_persist(opts.profile, 'mointerceptors'):
            self.protocol.sendData(
                '%s configuration persisted (profile:%s)' % (self.managerName, opts.profile), prompt=False)
        else:
            self.protocol.sendData(
                'Failed to persist %s configuration (profile:%s)' % (self.managerName, opts.profile),
                prompt=False)

    def load(self, arg, opts):
        r = self.pb['router'].perspective_load(opts.profile, 'mointerceptors')

        if r:
            self.protocol.sendData(
                '%s configuration loaded (profile:%s)' % (self.managerName, opts.profile), prompt=False)
        else:
            self.protocol.sendData(
                'Failed to load %s configuration (profile:%s)' % (self.managerName, opts.profile),
                prompt=False)

    def list(self, arg, opts):
        mointerceptors = pickle.loads(self.pb['router'].perspective_mointerceptor_get_all())
        counter = 0

        if (len(mointerceptors)) > 0:
            self.protocol.sendData("#%s %s %s %s" % (
                'Order'.ljust(5),
                'Type'.ljust(20),
                'Script'.ljust(47),
                'Filter(s)'.ljust(64),
                ), prompt=False)

            for e in mointerceptors:
                order = e.keys()[0]
                mointerceptor = e[order]
                counter += 1

                filters = ''
                # Prepare display for filters
                for _filter in mointerceptor.filters:
                    if filters != '':
                        filters += ', '
                    filters += repr(_filter)

                self.protocol.sendData("#%s %s %s %s" % (
                    str(order).ljust(5),
                    str(mointerceptor.__class__.__name__).ljust(20),
                    repr(mointerceptor.script).ljust(47),
                    filters.ljust(64),
                    ), prompt=False)
                self.protocol.sendData(prompt=False)

        self.protocol.sendData('Total MO Interceptors: %s' % counter)

    @Session
    @MOInterceptorBuild
    def add_session(self, order, InterceptorInstance):
        st = self.pb['router'].perspective_mointerceptor_add(
            pickle.dumps(InterceptorInstance, pickle.HIGHEST_PROTOCOL), order)

        if st:
            self.protocol.sendData('Successfully added MOInterceptor [%s] with order:%s' % (
                InterceptorInstance.__class__.__name__, order
            ), prompt=False)
            self.stopSession()
        else:
            self.protocol.sendData('Failed adding MOInterceptor, check log for details')
    def add(self, arg, opts):
        return self.startSession(self.add_session,
                                 annoucement='Adding a new MO Interceptor: (ok: save, ko: exit)',
                                 completitions=MOInterceptorKeyMap.keys())

    @MOInterceptorExist(order_key='remove')
    def remove(self, arg, opts):
        st = self.pb['router'].perspective_mointerceptor_remove(int(opts.remove))

        if st:
            self.protocol.sendData('Successfully removed MO Interceptor with order:%s' % opts.remove)
        else:
            self.protocol.sendData('Failed removing MO Interceptor, check log for details')

    @MOInterceptorExist(order_key='show')
    def show(self, arg, opts):
        r = self.pb['router'].getMOInterceptor(int(opts.show))
        self.protocol.sendData(str(r))

    def flush(self, arg, opts):
        tableSize = len(pickle.loads(self.pb['router'].perspective_mointerceptor_get_all()))
        self.pb['router'].perspective_mointerceptor_flush()
        self.protocol.sendData('Successfully flushed MO Interceptor table (%s flushed entries)' % tableSize)
