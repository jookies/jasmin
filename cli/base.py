from cmd2 import Cmd, StubbornDict, ParsedString, options, make_option
from jasmin.routing.proxies import RouterPBProxy
from jasmin.managers.proxies import SMPPClientManagerPBProxy
from twisted.internet import reactor, defer

class JasminCliApp(Cmd):
    intro = 'Welcome to Jasmin console\nType help or ? to list commands.\n'
    prompt = 'jcli : '
    abbrev = False
    
    doc_header = 'Available commands:'
        
    stdout_suffix = '\n'
    controlCommands = ['do_shortcuts', 'do_help', 'do_quit', 'do_connect', 'do_disconnect','do_status']
    commands = []
    router = None
    smppcm = None
    
    @defer.inlineCallbacks
    def connectToProxy(self, proxyObj, host, port):
        try:
            proxy = proxyObj()
            
            self.print_stdout('Connecting to %s at %s:%s' % (proxy.__class__.__name__, host, port))
            yield proxy.connect(host, port)
            
            if proxy.__class__.__name__ == 'RouterPBProxy':
                self.router = proxy
            elif proxy.__class__.__name__ == 'SMPPClientManagerPBProxy':
                self.smppcm = proxy

            self.print_stdout("Connected to %s" % (proxy.__class__.__name__))
        except Exception, e:
            self.print_stdout("Error connecting to %s: %s" % (proxy.__class__.__name__, str(e)))

    @defer.inlineCallbacks
    def disconnectFromProxy(self, proxy):
        try:
            if proxy is None or not proxy.isConnected:
                raise Exception('%s is already disconnected !' % proxy.__class__.__name__)

            self.print_stdout("Disconnecting from %s ..." % (proxy.__class__.__name__))
            yield proxy.disconnect()
            
            if proxy.__class__.__name__ == 'RouterPBProxy':
                self.router = None
            elif proxy.__class__.__name__ == 'SMPPClientManagerPBProxy':
                self.smppcm = None

            self.print_stdout("Disconnected from %s" % (proxy.__class__.__name__))
        except Exception, e:
            self.print_stdout("Error disconnecting from %s: %s" % (proxy.__class__.__name__, str(e)))

    def __init__(self, router = None, smppcm = None, *args, **kwargs):
        Cmd.__init__(self, *args, **kwargs)
        
        # Init connections to router and smppcm
        self.router = router
        self.smppcm = smppcm
        
        # Remove settable parameters
        Cmd.settable = StubbornDict()

    def parsed(self, raw, **kwargs):
        """Overrides cmd2's parsed to fix a bug in handling shortcuts.
        Pull request started at:
        https://bitbucket.org/catherinedevlin/cmd2/pull-request/5/fixed-a-bug-in-shortcuts-handling
        """
        if isinstance(raw, ParsedString):
            p = raw
        else:
            # preparse is an overridable hook; default makes no changes
            s = self.preparse(raw, **kwargs)
            s = self.inputParser.transformString(s.lstrip())
            s = self.commentGrammars.transformString(s)
            for (shortcut, expansion) in self.shortcuts:
                _s = s.lower()+' '
                if _s.startswith(shortcut+' '):
                    s = s.replace(shortcut, expansion + ' ', 1)
                    break
            result = self.parser.parseString(s)
            result['raw'] = raw            
            result['command'] = result.multilineCommand or result.command        
            result = self.postparse(result)
            p = ParsedString(result.args)
            p.parsed = result
            p.parser = self.parsed
        for (key, val) in kwargs.items():
            p.parsed[key] = val
        return p

    def onecmd(self, line):
        """Interpret the argument as though it had been typed in response
        to the prompt.
        Will not execute command if not explicity defined in self.commands or self.controlCommands
        
        This (`JasminCliApp`) version of `onecmd` already override's `cmd2`'s `onecmd`.
        """
        
        statement = self.parsed(line)
        self.lastcmd = statement.parsed.raw   
        funcname = self.func_named(statement.parsed.command)
        if not funcname or funcname not in self.commands+self.controlCommands:
            return self._default(statement)
        try:
            func = getattr(self, funcname)
        except AttributeError:
            return self._default(statement)
        stop = func(statement) 
        return stop                

    def print_stdout(self, _str):
        """Print to stdout
        """
        self.stdout.write(_str + self.stdout_suffix)
        
    def get_names(self):
        """Return self.commands and self.controlCommands instead of all Cmd commands
        """
        return self.commands+self.controlCommands
        
    @options([make_option('-b', '--backend',type="string", help="Connect to backend, values: r for router, s for smppcm and r,s for both"),
              make_option('--router_host',  type="string", help="Router host, default 127.0.0.1"),
              make_option('--router_port',  type="int",    help="Router port, default 8988"),
              make_option('--smppcm_host',  type="string", help="SMPPCM host, default 127.0.0.1"),
              make_option('--smppcm_port',  type="int",    help="SMPPCM host, default 8989"),])    
    def do_connect(self, arg, opts):
        'Start a connection to router (router) or smpp client manager (smppcm)'

        if opts.backend is None:
            self.print_stdout('Argument -b required.')
            return
        if opts.backend not in ['r', 's', 'r,s']:
            self.print_stdout('Invalid argument (%s) for command connect, must be "r", "s" or "r,s".' % opts.backend)
            return
        
        # @todo: host and port must be configurable
        router_host = '127.0.0.1'
        router_port = 8988
        smppcm_host = '127.0.0.1'
        smppcm_port = 8989
        
        # If set from options:
        if opts.router_host:
            router_host = opts.router_host
        if opts.router_port:
            router_port = int(opts.router_port)
        if opts.smppcm_host:
            smppcm_host = opts.smppcm_host
        if opts.smppcm_port:
            smppcm_port = int(opts.smppcm_port)
        
        if opts.backend in ['r', 'r,s'] and self.router is None:
            reactor.callFromThread(self.connectToProxy, RouterPBProxy, router_host, router_port)
        if opts.backend in ['s', 'r,s'] and self.smppcm is None:
            reactor.callFromThread(self.connectToProxy, SMPPClientManagerPBProxy, smppcm_host, smppcm_port)
            
    @options([make_option('-b', '--backend',type="string", help="Connect to backend, values: r for router, s for smppcm and r,s for both")])
    def do_disconnect(self, arg, opts):
        'Stop a connection from router (router) or smpp client manager (smppcm)'
        
        if opts.backend is None:
            self.print_stdout('Argument -b required.')
            return
        if opts.backend not in ['r', 's', 'r,s']:
            self.print_stdout('Invalid argument (%s) for command disconnect, must be "r", "s" or "r,s".' % opts.backend)
            return

        if opts.backend in ['r', 'r,s']:
            reactor.callFromThread(self.disconnectFromProxy, self.router)
        if opts.backend in ['s', 'r,s']:
            reactor.callFromThread(self.disconnectFromProxy, self.smppcm)
                        
    def do_status(self, arg):
        'Show connection status of router and smppcm'
        
        router = 'offline'
        if self.router is not None and self.router.isConnected:
            router = 'online'
        smppcm = 'offline'
        if self.smppcm is not None and self.smppcm.isConnected:
            smppcm = 'online'
        
        self.print_stdout('router: %s, smppcm: %s' % (router, smppcm))

    def do_quit(self, arg):
        'Exit from cli or management sub-application'
        
        if self.prompt == 'jcli : ' and ((self.router is not None and self.router.isConnected) or (self.smppcm is not None and self.smppcm.isConnected)):
            self.print_stdout('Please disconnect first')
        else:
            if self.prompt == 'jcli : ':
                reactor.callFromThread(reactor.stop)

            return self._STOP_AND_EXIT

    def do_help(self, arg = None):
        """Will provide help for available commands
        """
        
        if arg and 'do_'+arg not in self.commands+self.controlCommands:
            self.print_stdout("command '%s' not found" % arg)
            return
            
        Cmd.do_help(self, arg)

    # Command shortcuts
    Cmd.shortcuts.update({'?': 'help'})
    Cmd.shortcuts.update({'c': 'connect'})
    Cmd.shortcuts.update({'d': 'disconnect'})
    Cmd.shortcuts.update({'s': 'status'})
    Cmd.shortcuts.update({'q': 'quit'})