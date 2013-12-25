from jasmin.cli.base import JasminCliApp
from jasmin.cli.users import ManageUsers
from jasmin.cli.groups import ManageGroups
from jasmin.cli.router import ManageRouter
from jasmin.cli.smppclient import ManageSmppClient

class Home(JasminCliApp):
    commands = ['do_manage_users', 'do_manage_groups', 'do_manage_smpp_client', 'do_manage_router']

    def __init__(self, *args, **kwargs):
        JasminCliApp.__init__(self, *args, **kwargs)
        
        self.do_show = self.default
        
    def do_manage_users(self, arg):
        'Enter user management console'
        
        # Go to ManageUsers nested app
        i = ManageUsers()
        i.cmdloop()

    def do_manage_groups(self, arg):
        'Enter user group management console'
        
        # Go to ManageGroups nested app
        i = ManageGroups()
        i.cmdloop()

    def do_manage_router(self, arg):
        'Enter router management console'
        
        # Go to ManageSmppClient nested app
        i = ManageRouter()
        i.cmdloop()

    def do_manage_smpp_client(self, arg):
        'Enter SMPP client management console'
        
        # Go to ManageSmppClient nested app
        i = ManageSmppClient()
        i.cmdloop()

    # Command shortcuts
    JasminCliApp.shortcuts.update({'!u': 'manage_users'})
    JasminCliApp.shortcuts.update({'!g': 'manage_groups'})
    JasminCliApp.shortcuts.update({'!r': 'manage_router'})
    JasminCliApp.shortcuts.update({'!smppc': 'manage_smpp_client'})