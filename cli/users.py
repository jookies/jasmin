from jasmin.cli.base import JasminCliApp

class ManageUsers(JasminCliApp):
    prompt = 'jcli (!u): '
    commands = ['do_show', 'do_remove', 'do_add']
    
    def do_show(self, arg = None):
        'List available users or details of one user with "show username".'
        self.print_stdout('List users')

    def do_remove(self, arg = None):
        'Remove one user with "remove username".'
        self.print_stdout('Remove user')

    def do_add(self, arg = None):
        'Add one user with "add username".'
        self.print_stdout('Add user')