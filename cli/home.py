from jasmin.cli.base import JasminCliApp
from jasmin.cli.users import ManageUsers
from jasmin.cli.groups import ManageGroups
from jasmin.cli.router import ManageRouter
from jasmin.cli.smppclient import ManageSmppClient

class Home(JasminCliApp):
    commands = ['do_manage']

    def __init__(self, *args, **kwargs):
        JasminCliApp.__init__(self, *args, **kwargs)
        
        self.do_show = self.default
        
    def do_manage(self, arg):
        'Enter management console for: user, group, router or smppcm'
        
        if self.router is None or not self.router.isConnected or self.smppcm is None or not self.smppcm.isConnected:
            self.print_stdout('Cannot enter a management console while router or smppcm are offline')
            return

        if arg == 'user':
            # Go to ManageUsers nested app
            napp = ManageUsers(router = self.router, smppcm = self.smppc)
        elif arg == 'group':
            # Go to ManageGroups nested app
            napp = ManageGroups(router = self.router, smppcm = self.smppc)
        elif arg == 'router':
            # Go to ManageRouter nested app
            napp = ManageRouter(router = self.router, smppcm = self.smppc)
        elif arg == 'smppc':
            # Go to ManageSmppClient nested app
            napp = ManageSmppClient(router = self.router, smppcm = self.smppc)
        else:
            self.print_stdout('Invalid console, should be: user, group, router or smppcm')
            return

        napp.cmdloop()
        
    def complete_manage(self, text, line, begidx, endidx):
        completions = ['user', 'group', 'router', 'smppcm']
        
        mline = line.partition(' ')[2]
        offs = len(mline) - len(text)
        return [s[offs:] for s in completions if s.startswith(mline)]

    # Command shortcuts
    JasminCliApp.shortcuts.update({'m': 'manage'})