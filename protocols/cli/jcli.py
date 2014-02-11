# Copyright 2012 Fourat Zouari <fourat@gmail.com>
# See LICENSE for details.

from protocol import CmdProtocol
from options import options
from smppccm import SmppCCManager
from optparse import make_option

class JCliProtocol(CmdProtocol):
    motd = 'Welcome to Jasmin console\nType help or ? to list commands.\n'
    prompt = 'jcli : '
    
    def __init__(self, factory, log):
        CmdProtocol.__init__(self, factory, log)
        if 'persist' not in self.commands:
            self.commands.append('persist')
        if 'load' not in self.commands:
            self.commands.append('load')
        if 'user' not in self.commands:
            self.commands.append('user')
        if 'group' not in self.commands:
            self.commands.append('group')
        if 'router' not in self.commands:
            self.commands.append('router')
        if 'smppccm' not in self.commands:
            self.commands.append('smppccm')
        
        # Provision managers
        self.managers = {'user': None, 'group': None, 
                         'router': None, 'smppccm': SmppCCManager(self, factory.pb), }
        
    def manageModule(self, moduleName, arg, opts):
        if opts.list:
            self.managers[moduleName].list(arg, opts)
        elif opts.add:
            self.managers[moduleName].add(arg, opts)
        elif opts.update:
            self.managers[moduleName].update(arg, opts)
        elif opts.remove:
            self.managers[moduleName].remove(arg, opts)
        elif opts.show:
            self.managers[moduleName].show(arg, opts)
        elif opts.start:
            self.managers[moduleName].start(arg, opts)
        elif opts.stop:
            self.managers[moduleName].stop(arg, opts)

    @options([make_option('-l', '--list', action="store_true",
                          help="List users"),
              make_option('-a', '--add', action="store_true",
                          help="Add user"),
              make_option('-r', '--remove', action="store_true",
                          help="Remove user"),], '')    
    def do_user(self, arg, opts):
        'User management'
        self.manageModule('user', arg, opts)
        
    @options([make_option('-l', '--list', action="store_true",
                          help="List groups"),
              make_option('-a', '--add', action="store_true",
                          help="Add group"),
              make_option('-r', '--remove', action="store_true",
                          help="Remove group"),], '')    
    def do_group(self, arg, opts):
        'Group management'
        self.manageModule('group', arg, opts)
        
    def do_router(self, arg, opts = None):
        'Router management'
        self.manageModule('router', arg, opts)
        
    @options([make_option('-l', '--list', action="store_true",
                          help="List SMPP connectors"),
              make_option('-a', '--add', action="store_true",
                          help="Add SMPP connector"),
              make_option('-u', '--update', type="string", metavar="CID", 
                          help="Update SMPP connector configuration using it's CID"),
              make_option('-r', '--remove', type="string", metavar="CID", 
                          help="Remove SMPP connector using it's CID"),
              make_option('-s', '--show', type="string", metavar="CID", 
                          help="Show SMPP connector using it's CID"),
              make_option('-1', '--start', type="string", metavar="CID", 
                          help="Start SMPP connector using it's CID"),
              make_option('-0', '--stop', type="string", metavar="CID", 
                          help="Start SMPP connector using it's CID"),
              ], '')
    def do_smppccm(self, arg, opts):
        'SMPP connector management'

        if (opts.list is None and opts.add is None and opts.remove is None and 
            opts.show is None and opts.start is None and opts.stop is None and
            opts.update is None):
            return self.sendData('Missing required option')
        
        self.manageModule('smppccm', arg, opts)
        
    @options([make_option('-p', '--profile', type="string", default="jcli-prod", 
                          help="Configuration profile, default: jcli-prod"),
              ], '')
    def do_persist(self, arg, opts):
        'Persist current configuration profile to disk in PROFILE'
        
        for module, manager in self.managers.iteritems():
            if manager is not None:
                manager.persist(arg, opts)
        self.sendData()

    @options([make_option('-p', '--profile', type="string", default="jcli-prod", 
                          help="Configuration profile, default: jcli-prod"),
              ], '')
    def do_load(self, arg, opts):
        'Load configuration PROFILE profile from disk'
        
        for module, manager in self.managers.iteritems():
            if manager is not None:
                manager.load(arg, opts)
        self.sendData()