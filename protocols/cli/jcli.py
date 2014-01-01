# Copyright 2012 Fourat Zouari <fourat@gmail.com>
# See LICENSE for details.

from jasmin.protocols.cli.protocol import CmdProtocol
from jasmin.protocols.cli.options import options
from optparse import make_option

class JCliProtocol(CmdProtocol):
    motd = 'Welcome to Jasmin console\nType help or ? to list commands.\n'
    CmdProtocol.commands.append('manage')
    prompt = 'jcli : '
    
    @options([make_option('-m', '--module', choices=['user', 'group', 'router', 'smppcm'], type="choice", 
                          help="Module management, values: user, group, router or smppcm"),], '')    
    def do_manage(self, arg, opts):
        'Enter management console for: user, group, router or smppcm'
 
        if opts.module is None:
            return self.sendData('Missing required option: module')
        
        if opts.module == 'user':
            # Go to ManageUsers nested app
            napp = None
        elif opts.module == 'group':
            # Go to ManageGroups nested app
            napp = None
        elif opts.module == 'router':
            # Go to ManageRouter nested app
            napp = None
        elif opts.module == 'smppcm':
            # Go to ManageSmppClient nested app
            napp = None
            
        #napp.lauch()

        return self.sendData('--%s was choosen' % opts.module)