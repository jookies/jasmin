from cmd2 import Cmd, StubbornDict

class JasminCliApp(Cmd):
    intro = 'Welcome to Jasmin console\nType help or ? to list commands.\n'
    prompt = 'jcli: '
    abbrev = False
    
    doc_header = 'Available commands:'
        
    stdout_suffix = '\n'
    controlCommands = ['do_help', 'do_quit']
    commands = []
    
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
            print funcname
            return self._default(statement)
        try:
            func = getattr(self, funcname)
        except AttributeError:
            return self._default(statement)
        stop = func(statement) 
        return stop                

    def __init__(self, *args, **kwargs):
        Cmd.__init__(self, *args, **kwargs)
        
        # Remove settable parameters
        Cmd.settable = StubbornDict()
    
    def print_stdout(self, _str):
        """Print to stdout
        """
        self.stdout.write(_str + self.stdout_suffix)
        
    def get_names(self):
        """Return self.commands and self.controlCommands instead of all Cmd commands
        """
        return self.commands+self.controlCommands
        
    
    def do_quit(self, arg):
        'Exit from cli or management sub-application'
        return self._STOP_AND_EXIT

    def do_help(self, arg = None):
        """Will provide help for available commands
        """
        
        if arg and 'do_'+arg not in self.commands+self.controlCommands:
            self.print_stdout("command '%s' not found" % arg)
            return
            
        Cmd.do_help(self, arg)

    # Command shortcuts
    Cmd.shortcuts.update({'q': 'quit'})
    Cmd.shortcuts.update({'?': 'help'})