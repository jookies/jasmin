import jasmin
from hashlib import md5
from optparse import make_option
from jasmin.protocols.cli.managers import PersistableManager
from jasmin.protocols.cli.options import options
from jasmin.protocols.cli.protocol import CmdProtocol
from jasmin.protocols.cli.smppccm import SmppCCManager
from jasmin.protocols.cli.usersm import UsersManager
from jasmin.protocols.cli.groupsm import GroupsManager
from jasmin.protocols.cli.morouterm import MoRouterManager
from jasmin.protocols.cli.mtrouterm import MtRouterManager
from jasmin.protocols.cli.filtersm import FiltersManager
from jasmin.protocols.cli.httpccm import HttpccManager
from jasmin.protocols.cli.statsm import StatsManager
        
class JCliProtocol(CmdProtocol):
    motd = 'Welcome to Jasmin %s console\nType help or ? to list commands.\n' % jasmin.get_release()
    prompt = 'jcli : '
    
    def __init__(self, log_category = 'jcli'):
        CmdProtocol.__init__(self, log_category)

        # Init authentication
        self.authentication = {'username': None, 'password': None, 'printedPassword': None, 'auth': False}
                    
        # Provision commands
        if 'persist' not in self.commands:
            self.commands.append('persist')
        if 'load' not in self.commands:
            self.commands.append('load')
        if 'user' not in self.commands:
            self.commands.append('user')
        if 'group' not in self.commands:
            self.commands.append('group')
        if 'filter' not in self.commands:
            self.commands.append('filter')
        if 'morouter' not in self.commands:
            self.commands.append('morouter')
        if 'mtrouter' not in self.commands:
            self.commands.append('mtrouter')
        if 'smppccm' not in self.commands:
            self.commands.append('smppccm')
        if 'httpccm' not in self.commands:
            self.commands.append('httpccm')
        if 'stats' not in self.commands:
            self.commands.append('stats')

    def connectionMade(self):
        # Provision security
        if not self.factory.config.authentication:
            # Will not require an authentication from client
            self.authentication = {'username': 'Anonymous', 'password': None, 'printedPassword': None, 'auth': True}

        # Call CmdProtocol.connectionMade() depending on the security policy
        if self.authentication['auth']:
            CmdProtocol.connectionMade(self)
        elif self.authentication['username'] is None:
            self.oldPrompt = self.prompt
            self.prompt = 'Username: '
            CmdProtocol.connectionMade(self, False)
            self.terminal.write('Authentication required.\n\n')

        # Provision managers
        self.managers = {'user': UsersManager(self, self.factory.pb), 
                         'group': GroupsManager(self, self.factory.pb), 
                         'morouter': MoRouterManager(self, self.factory.pb), 
                         'mtrouter': MtRouterManager(self, self.factory.pb), 
                         'smppccm': SmppCCManager(self, self.factory.pb), 
                         'filter': FiltersManager(self),
                         'httpccm': HttpccManager(self),
                         'stats': StatsManager(self, self.factory.pb), 
                         }
        
    def lineReceived(self, line):
        "Go to CmdProtocol.lineReceived when authenticated only"
        
        if self.authentication['auth']:
            return CmdProtocol.lineReceived(self, line)
        elif self.authentication['username'] is None:
            return self.AUTH_username(line)
        elif self.authentication['password'] is None:
            return self.AUTH_password(line)
        
    def characterReceived(self, ch, moreCharactersComing):
        if self.mode == 'insert':
            self.lineBuffer.insert(self.lineBufferIndex, ch)
        else:
            self.lineBuffer[self.lineBufferIndex:self.lineBufferIndex+1] = [ch]
        self.lineBufferIndex += 1
        
        # Dont print back chars if password is being entered
        if not self.authentication['auth'] and self.authentication['username'] is not None and self.authentication['password'] is None:
            return
        else:
            self.terminal.write(ch)
        
    def handle_TAB(self):
        "TABulation is only enabled when authenticated"
        
        if self.authentication['auth']:
            return CmdProtocol.handle_TAB(self)
        
    def AUTH_username(self, username):
        "Save typed username and prompt for password"

        username = username.strip()
        if username:
            self.authentication['username'] = username
            self.prompt = 'Password: '
            self.log.debug('[sref:%s] Received AUTH Username: %s' % (self.sessionRef, self.authentication['username']))
        
        return self.sendData()

    def AUTH_password(self, password):
        """Authentify Username & Password against configured (jasmin.cfg) credentials
        """

        self.authentication['password'] = password.strip()
        self.authentication['printedPassword'] = ''
        for _ in password:
            self.authentication['printedPassword'] += '*'
        self.log.debug('[sref:%s] Received AUTH Password: %s' % (self.sessionRef, self.authentication['printedPassword']))
        
        # Authentication check against configured admin
        if (self.authentication['username'] == self.factory.config.admin_username and 
          md5(self.authentication['password']).digest() == self.factory.config.admin_password):
            # Authenticated user
            self.authentication['auth'] = True
            self.prompt = self.oldPrompt
            self.drawMotd()
        else:
            self.prompt = 'Username: '
            self.authentication = {'username': None, 'password': None, 'printedPassword': None, 'auth': False}
            return self.sendData('Incorrect Username/Password.\n')
        
        return self.sendData()

    @options([make_option('-l', '--list', action="store_true",
                          help = "List all users or a group users when provided with GID"),
              make_option('-a', '--add', action="store_true",
                          help = "Add user"),
              make_option('-u', '--update', type="string", metavar="UID", 
                          help = "Update user using it's UID"),
              make_option('-r', '--remove', type="string", metavar="UID", 
                          help = "Remove user using it's UID"),
              make_option('-s', '--show', type="string", metavar="UID", 
                          help = "Show user using it's UID"),
              ], '')
    def do_user(self, arg, opts):
        'User management'

        if opts.list:
            self.managers['user'].list(arg, opts)
        elif opts.add:
            self.managers['user'].add(arg, opts)
        elif opts.update:
            self.managers['user'].update(arg, opts)
        elif opts.remove:
            self.managers['user'].remove(arg, opts)
        elif opts.show:
            self.managers['user'].show(arg, opts)
        else:
            return self.sendData('Missing required option')
        
    @options([make_option('-l', '--list', action="store_true",
                          help = "List groups"),
              make_option('-a', '--add', action="store_true",
                          help = "Add group"),
              #make_option('-u', '--update', type="string", metavar="GID", 
              #            help = "Update group using it's GID"),
              make_option('-r', '--remove', type="string", metavar="GID", 
                          help = "Remove group using it's GID"),
              #make_option('-s', '--show', type="string", metavar="GID", 
              #            help = "Show group using it's GID"),
              ], '')
    def do_group(self, arg, opts):
        'Group management'

        if opts.list:
            self.managers['group'].list(arg, opts)
        elif opts.add:
            self.managers['group'].add(arg, opts)
        #elif opts.update:
        #    self.managers['group'].update(arg, opts)
        elif opts.remove:
            self.managers['group'].remove(arg, opts)
        #elif opts.show:
        #    self.managers['group'].show(arg, opts)
        else:
            return self.sendData('Missing required option')
        
    @options([make_option('-l', '--list', action="store_true",
                          help = "List filters"),
              make_option('-a', '--add', action="store_true",
                          help = "Add filter"),
              make_option('-r', '--remove', type="string", metavar="FID", 
                          help = "Remove filter using it's FID"),
              make_option('-s', '--show', type="string", metavar="FID", 
                          help = "Show filter using it's FID"),
              ], '')
    def do_filter(self, arg, opts):
        'Filter management'

        if opts.list:
            self.managers['filter'].list(arg, opts)
        elif opts.add:
            self.managers['filter'].add(arg, opts)
        elif opts.remove:
            self.managers['filter'].remove(arg, opts)
        elif opts.show:
            self.managers['filter'].show(arg, opts)
        else:
            return self.sendData('Missing required option')

    @options([make_option('-l', '--list', action="store_true",
                          help = "List HTTP client connectors"),
              make_option('-a', '--add', action="store_true",
                          help = "Add a new HTTP client connector"),
              make_option('-r', '--remove', type="string", metavar="CID", 
                          help = "Remove HTTP client connector using it's CID"),
              make_option('-s', '--show', type="string", metavar="CID", 
                          help = "Show HTTP client connector using it's CID"),
              ], '')
    def do_httpccm(self, arg, opts = None):
        'HTTP client connector management'

        if opts.list:
            self.managers['httpccm'].list(arg, opts)
        elif opts.add:
            self.managers['httpccm'].add(arg, opts)
        elif opts.remove:
            self.managers['httpccm'].remove(arg, opts)
        elif opts.show:
            self.managers['httpccm'].show(arg, opts)
        else:
            return self.sendData('Missing required option')

    @options([make_option('-l', '--list', action="store_true",
                          help = "List MO routes"),
              make_option('-a', '--add', action="store_true",
                          help = "Add a new MO route"),
              make_option('-r', '--remove', type="string", metavar="ORDER", 
                          help = "Remove MO route using it's ORDER"),
              make_option('-s', '--show', type="string", metavar="ORDER", 
                          help = "Show MO route using it's ORDER"),
              make_option('-f', '--flush', action="store_true",
                          help = "Flush MO routing table"),
              ], '')
    def do_morouter(self, arg, opts = None):
        'MO Router management'

        if opts.list:
            self.managers['morouter'].list(arg, opts)
        elif opts.add:
            self.managers['morouter'].add(arg, opts)
        elif opts.remove:
            self.managers['morouter'].remove(arg, opts)
        elif opts.show:
            self.managers['morouter'].show(arg, opts)
        elif opts.flush:
            self.managers['morouter'].flush(arg, opts)
        else:
            return self.sendData('Missing required option')
        
    @options([make_option('-l', '--list', action="store_true",
                          help = "List MT routes"),
              make_option('-a', '--add', action="store_true",
                          help = "Add a new MT route"),
              make_option('-r', '--remove', type="string", metavar="ORDER", 
                          help = "Remove MT route using it's ORDER"),
              make_option('-s', '--show', type="string", metavar="ORDER", 
                          help = "Show MT route using it's ORDER"),
              make_option('-f', '--flush', action="store_true",
                          help = "Flush MT routing table"),
              ], '')
    def do_mtrouter(self, arg, opts = None):
        'MT Router management'

        if opts.list:
            self.managers['mtrouter'].list(arg, opts)
        elif opts.add:
            self.managers['mtrouter'].add(arg, opts)
        elif opts.remove:
            self.managers['mtrouter'].remove(arg, opts)
        elif opts.show:
            self.managers['mtrouter'].show(arg, opts)
        elif opts.flush:
            self.managers['mtrouter'].flush(arg, opts)
        else:
            return self.sendData('Missing required option')
    
    @options([make_option('-l', '--list', action="store_true",
                          help = "List SMPP connectors"),
              make_option('-a', '--add', action="store_true",
                          help = "Add SMPP connector"),
              make_option('-u', '--update', type="string", metavar="CID", 
                          help = "Update SMPP connector configuration using it's CID"),
              make_option('-r', '--remove', type="string", metavar="CID", 
                          help = "Remove SMPP connector using it's CID"),
              make_option('-s', '--show', type="string", metavar="CID", 
                          help = "Show SMPP connector using it's CID"),
              make_option('-1', '--start', type="string", metavar="CID", 
                          help = "Start SMPP connector using it's CID"),
              make_option('-0', '--stop', type="string", metavar="CID", 
                          help = "Start SMPP connector using it's CID"),
              ], '')
    def do_smppccm(self, arg, opts):
        'SMPP connector management'

        if opts.list:
            self.managers['smppccm'].list(arg, opts)
        elif opts.add:
            self.managers['smppccm'].add(arg, opts)
        elif opts.update:
            self.managers['smppccm'].update(arg, opts)
        elif opts.remove:
            self.managers['smppccm'].remove(arg, opts)
        elif opts.show:
            self.managers['smppccm'].show(arg, opts)
        elif opts.start:
            self.managers['smppccm'].start(arg, opts)
        elif opts.stop:
            self.managers['smppccm'].stop(arg, opts)
        else:
            return self.sendData('Missing required option')
        
    @options([make_option('-p', '--profile', type="string", default="jcli-prod", 
                          help = "Configuration profile, default: jcli-prod"),
              ], '')
    def do_persist(self, arg, opts):
        'Persist current configuration profile to disk in PROFILE'
        
        for _, manager in self.managers.iteritems():
            if manager is not None and isinstance(manager, PersistableManager):
                manager.persist(arg, opts)
        self.sendData()

    @options([make_option('-p', '--profile', type="string", default="jcli-prod", 
                          help = "Configuration profile, default: jcli-prod"),
              ], '')
    def do_load(self, arg, opts):
        'Load configuration PROFILE profile from disk'
        
        for _, manager in self.managers.iteritems():
            if manager is not None and isinstance(manager, PersistableManager):
                manager.load(arg, opts)
        self.sendData()

    @options([make_option(None, '--user', type="string", metavar="UID", 
                          help = "Show user stats using it's UID"),
              make_option(None, '--users', action="store_true",
                          help = "Show all users stats"),
              make_option(None, '--smppc', type="string", metavar="CID", 
                          help = "Show smpp connector stats using it's CID"),
              make_option(None, '--smppcs', action="store_true",
                          help = "Show all smpp connectors stats"),
              make_option(None, '--httpapi', action="store_true",
                          help = "Show HTTP API stats"),
              make_option(None, '--smppsapi', action="store_true",
                          help = "Show SMPP Server API stats"),
              ], '')
    def do_stats(self, arg, opts = None):
        'Stats management'

        if opts.user:
            self.managers['stats'].user(arg, opts)
        elif opts.users:
            self.managers['stats'].users(arg, opts)
        elif opts.smppc:
            self.managers['stats'].smppc(arg, opts)
        elif opts.smppcs:
            self.managers['stats'].smppcs(arg, opts)
        elif opts.httpapi:
            self.managers['stats'].httpapi(arg, opts)
        elif opts.smppsapi:
            self.managers['stats'].smppsapi(arg, opts)
        else:
            return self.sendData('Missing required option')