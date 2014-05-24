# Copyright 2012 Fourat Zouari <fourat@gmail.com>
# See LICENSE for details.

import pyparsing
from protocol import CmdProtocol
from options import options, options_defined, OptionParser, remaining_args
from smppccm import SmppCCManager
from usersm import UsersManager
from groupsm import GroupsManager
from morouterm import MoRouterManager
from mtrouterm import MtRouterManager
from filtersm import FiltersManager
from httpccm import HttpccManager
from optparse import make_option, Option, OptionValueError, OptParseError
from copy import copy
        
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
        
        # Provision managers
        self.managers = {'user': UsersManager(self, factory.pb), 'group': GroupsManager(self, factory.pb), 
                         'morouter': MoRouterManager(self, factory.pb), 'mtrouter': MtRouterManager(self, factory.pb), 
                         'smppccm': SmppCCManager(self, factory.pb), 'filter': FiltersManager(self),
                         'httpccm': HttpccManager(self)}
        
    @options([make_option('-l', '--list', action="store_true",
                          help="List all users or a group users when provided with GID"),
              make_option('-a', '--add', action="store_true",
                          help="Add user"),
              make_option('-u', '--update', type="string", metavar="UID", 
                          help="Update user using it's UID"),
              make_option('-r', '--remove', type="string", metavar="UID", 
                          help="Remove user using it's UID"),
              make_option('-s', '--show', type="string", metavar="UID", 
                          help="Show user using it's UID"),
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
                          help="List groups"),
              make_option('-a', '--add', action="store_true",
                          help="Add group"),
              #make_option('-u', '--update', type="string", metavar="GID", 
              #            help="Update group using it's GID"),
              make_option('-r', '--remove', type="string", metavar="GID", 
                          help="Remove group using it's GID"),
              #make_option('-s', '--show', type="string", metavar="GID", 
              #            help="Show group using it's GID"),
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
                          help="List filters"),
              make_option('-a', '--add', action="store_true",
                          help="Add filter"),
              make_option('-r', '--remove', type="string", metavar="FID", 
                          help="Remove filter using it's FID"),
              make_option('-s', '--show', type="string", metavar="FID", 
                          help="Show filter using it's FID"),
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
                          help="List HTTP client connectors"),
              make_option('-a', '--add', action="store_true",
                          help="Add a new HTTP client connector"),
              make_option('-r', '--remove', type="string", metavar="CID", 
                          help="Remove HTTP client connector using it's CID"),
              make_option('-s', '--show', type="string", metavar="CID", 
                          help="Show HTTP client connector using it's CID"),
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
                          help="List MO routes"),
              make_option('-a', '--add', action="store_true",
                          help="Add a new MO route"),
              make_option('-r', '--remove', type="string", metavar="ORDER", 
                          help="Remove MO route using it's ORDER"),
              make_option('-s', '--show', type="string", metavar="ORDER", 
                          help="Show MO route using it's ORDER"),
              make_option('-f', '--flush', action="store_true",
                          help="Flush MO routing table"),
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
        
    def do_mtrouter(self, arg, opts = None):
        'MT Router management'

        pass
    
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