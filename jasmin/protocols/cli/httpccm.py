import cPickle as pickle
import time
import jasmin
import os
from jasmin.protocols.cli.managers import PersistableManager, Session
from jasmin.tools.migrations.configuration import ConfigurationMigrator
from jasmin.routing.jasminApi import HttpConnector

# A config map between console-configuration keys and Httpcc keys.
HttpccKeyMap = {'cid': 'cid', 'url': 'baseurl', 'method': 'method'}

# Related to travis-ci builds
ROOT_PATH = os.getenv('ROOT_PATH', '/')

# Since HttpccManager does not have any PB, there's no configuration for it
# Persist and Load are using CONFIG_STORE_PATH for persisting/loading httpc connectors
CONFIG_STORE_PATH = '%s/etc/jasmin/store' % ROOT_PATH

def HttpccBuild(fCallback):
    '''Parse args and try to build a filter from  one of the filters in
       jasmin.routing.Filters instance to pass it to fCallback'''
    def parse_args_and_call_with_instance(self, *args, **kwargs):
        cmd = args[0]
        arg = args[1]

        # Empty line
        if cmd is None:
            return self.protocol.sendData()
        # Initiate jasmin.routing.jasminApi.Group with sessBuffer content
        if cmd == 'ok':
            if len(self.sessBuffer) < len(self.protocol.sessionCompletitions):
                return self.protocol.sendData(
                    'You must set these options before saving: %s' % ', '.join(
                        self.protocol.sessionCompletitions))

            httpcc = {}
            for key, value in self.sessBuffer.iteritems():
                httpcc[key] = value
            try:
                HttpccInstance = HttpConnector(**httpcc)
                # Hand the instance to fCallback
                return fCallback(self, httpcc['cid'], HttpccInstance)
            except Exception, e:
                return self.protocol.sendData('Error: %s' % str(e))
        else:
            # Unknown key
            if cmd not in HttpccKeyMap:
                return self.protocol.sendData('Unknown Httpcc key: %s' % cmd)

            # Buffer key for later SMPPClientConfig initiating
            HttpccKey = HttpccKeyMap[cmd]
            self.sessBuffer[HttpccKey] = arg

            return self.protocol.sendData()
    return parse_args_and_call_with_instance

class HttpccExist(object):
    'Check if httpcc cid exist before passing it to fCallback'
    def __init__(self, cid_key):
        self.cid_key = cid_key
    def __call__(self, fCallback):
        cid_key = self.cid_key
        def exist_httpcc_and_call(self, *args, **kwargs):
            opts = args[1]
            cid = getattr(opts, cid_key)

            for httpcc_id in self.httpccs.iterkeys():
                if cid == httpcc_id:
                    return fCallback(self, *args, **kwargs)

            return self.protocol.sendData('Unknown Httpcc: %s' % cid)
        return exist_httpcc_and_call

class HttpccManager(PersistableManager):
    '''HttpccManager does not have a PB like other managers (router, users, groups ...), it is
    used to simplify route adding syntax by creating reusable httpccs, these httpccs are saved in
    self.httpccs'''
    managerName = 'httpcc'

    def __init__(self, protocol):
        PersistableManager.__init__(self, protocol, None)

        self.httpccs = {}

        # Load httpccs from disk on each instanciation with a jcli session
        # Since there's no PB, the httpcs belong to the current jcli session context
        try:
            self._load()

            protocol.log.info('%s configuration loaded (default profile)' % (self.managerName))
        except Exception, e:
            protocol.log.error('Config loading error: %s' % str(e))

    def persist(self, arg, opts):
        path = '%s/%s.httpccs' % (CONFIG_STORE_PATH, opts.profile)

        try:
            # Write configuration with datetime stamp
            fh = open(path, 'w')
            fh.write('Persisted on %s [Jasmin %s]\n' % (time.strftime("%c"), jasmin.get_release()))
            fh.write(pickle.dumps(self.httpccs, pickle.HIGHEST_PROTOCOL))
            fh.close()
        except IOError:
            return self.protocol.sendData('Cannot persist to %s' % path)
        except Exception, e:
            return self.protocol.sendData('Unknown error occurred while persisting configuration: %s' % e)

        self.protocol.sendData(
            '%s configuration persisted (profile:%s)' % (self.managerName, opts.profile), prompt=False)

    def load(self, arg, opts):
        try:
            self._load(opts.profile)

            self.protocol.sendData(
                '%s configuration loaded (profile:%s)' % (self.managerName, opts.profile), prompt=False)
        except:
            self.protocol.sendData(
                'Failed to load %s configuration (profile:%s)' % (self.managerName, opts.profile),
                prompt=False)

    def _load(self, profile='jcli-prod'):
        path = '%s/%s.httpccs' % (CONFIG_STORE_PATH, profile)

        try:
            # Load configuration from file
            fh = open(path, 'r')
            lines = fh.readlines()
            fh.close()

            # Init migrator
            cf = ConfigurationMigrator(context='httpcs', header=lines[0], data=''.join(lines[1:]))

            # Apply configuration
            self.httpccs = cf.getMigratedData()
        except IOError, e:
            raise Exception('Cannot load from %s: %s' % (path, str(e)))
        except Exception, e:
            raise Exception('Unknown error while loading configuration: %s' % e)

    def list(self, arg, opts):
        counter = 0

        if (len(self.httpccs)) > 0:
            self.protocol.sendData("#%s %s %s %s" % (
                'Httpcc id'.ljust(16),
                'Type'.ljust(22),
                'Method'.ljust(6),
                'URL'.ljust(64),
                ), prompt=False)
            for cid, _httpcc in self.httpccs.iteritems():
                counter += 1
                self.protocol.sendData("#%s %s %s %s" % (
                    str(cid).ljust(16),
                    str(_httpcc.__class__.__name__).ljust(22),
                    _httpcc.method.upper().ljust(6),
                    _httpcc.baseurl.ljust(64),
                    ), prompt=False)
                self.protocol.sendData(prompt=False)

        self.protocol.sendData('Total Httpccs: %s' % counter)

    @Session
    @HttpccBuild
    def add_session(self, cid, HttpccInstance):
        self.httpccs[cid] = HttpccInstance
        self.protocol.sendData(
            'Successfully added Httpcc [%s] with cid:%s' % (HttpccInstance.__class__.__name__, cid),
            prompt=False)
        self.stopSession()
    def add(self, arg, opts):
        return self.startSession(self.add_session,
                                 annoucement='Adding a new Httpcc: (ok: save, ko: exit)',
                                 completitions=HttpccKeyMap.keys())

    @HttpccExist(cid_key='remove')
    def remove(self, arg, opts):
        del self.httpccs[opts.remove]
        self.protocol.sendData('Successfully removed Httpcc id:%s' % opts.remove)

    @HttpccExist(cid_key='show')
    def show(self, arg, opts):
        self.protocol.sendData('%s' % str(self.httpccs[opts.show]))
