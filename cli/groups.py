from jasmin.cli.base import JasminCliApp

class ManageGroups(JasminCliApp):
    prompt = 'jcli (!g): '
    commands = ['do_show', 'do_remove', 'do_add']
    
    def do_show(self, arg = None):
        'List available groups or details of one group with "show groupname".'
        self.print_stdout('List groups')

    def do_remove(self, arg = None):
        'Remove one user with "remove groupname".'
        self.print_stdout('Remove group')

    def do_add(self, arg = None):
        'Add one user with "add groupname".'
        self.print_stdout('Add group')