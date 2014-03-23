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
from optparse import make_option, Option, OptionValueError, OptParseError
from copy import copy

MOROUTES = ['DefaultRoute', 'StaticMORoute', 'RandomRoundrobinMORoute']
MTROUTES = ['DefaultRoute', 'StaticMTRoute', 'RandomRoundrobinMTRoute']

def check_moroute(option, opt, value):
    """Will check for correct moroute option syntax,
    the right syntax is as below:
    ORDER:ROUTENAME:CID1,CID2,...:FILTERNAME1,FILTERNAME2,...;
    """
    try:
        order = None
        routename = None
        connectors = []
        filters = []
        
        if value[-1:] != ';':
            raise ValueError('Route must end with a semi column (;)')
        value = value[:-1]
        
        v = value.split(':')
        if len(v) < 4:
            raise ValueError('Incorrect syntax, a moroute must include an ORDER,\na ROUTENAME and at least one couple of CID and FILTERNAME, example: "33:StaticMORoute:MTN_SMPP:SourceAddrFilter;"')
        
        if not v[0].isdigit():
            raise ValueError('Given ORDER [%s] is not an integer' % v[0])
        order = int(v[0])
        
        if v[1] not in MOROUTES:
            raise ValueError('Given ROUTENAME [%s] is not a valid moroute' % v[1])
        routename = v[1]
        
        _connectors = v[2].split(',')
        for _connector in _connectors:
            connectors.append(_connector)
        
        _filters = v[3].split(',')
        #for _filter in _filters:
        #    if _filter not in MOFILTERS:
        #        raise ValueError('Given FILTERNAME [%s] is not a valid MO filter' % _filter)
        #    filters.append(_filter)
        raise NotImplementedError('TODO')
        return order, routename, connectors, filters
    except ValueError, e:
        raise OptionValueError(
            "option %s: invalid moroute value: %s" % (opt, e))
        
class JcliOptions (Option):
    # Add "moroute" option
    Option.TYPES = Option.TYPES + ("moroute",)
    Option.TYPE_CHECKER["moroute"] = check_moroute
    
def jclioptions(option_list, arg_desc="arg"):
    '''This is a slightly modified version of cli's options where JcliOptions
    is used when instanciating optionParser in order to add customized options
    c.f. http://docs.python.org/2/library/optparse.html#adding-new-types
    '''
    if not isinstance(option_list, list):
        option_list = [option_list]
    for opt in option_list:
        options_defined.append(pyparsing.Literal(opt.get_opt_string()))
    def option_setup(func):
        optionParser = OptionParser(option_class=JcliOptions)
        for opt in option_list:
            optionParser.add_option(opt)
        optionParser.set_usage("%s [options] %s" % (func.__name__[3:], arg_desc))
        optionParser._func = func
        def new_func(instance, arg):
            try:
                opts, newArgList = optionParser.parse_args(arg.split())
                newArgs = remaining_args(arg, newArgList)
                arg = newArgs
            except OptParseError as e:
                instance.sendData(str(e))
                return instance.sendData(optionParser.format_help())
            return func(instance, arg, opts)
        new_func.__doc__ = func.__doc__
        new_func.__extended_doc__ = optionParser.format_help()
        return new_func
    return option_setup
        
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
        
        # Provision managers
        self.managers = {'user': UsersManager(self, factory.pb), 'group': GroupsManager(self, factory.pb), 
                         'morouter': MoRouterManager(self, factory.pb), 'mtrouter': MtRouterManager(self, factory.pb), 
                         'smppccm': SmppCCManager(self, factory.pb), 'filter': FiltersManager(self, None)}
        
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

    @jclioptions([make_option('-l', '--list', action="store_true",
                          help="List MO routes"),
              make_option('-a', '--add', type="moroute", metavar="ROUTE", 
                          help="Add a new MO route"),
              make_option('-f', '--flush', action="store_true",
                          help="Flush MO routing table"),
              ], '')
    def do_morouter(self, arg, opts = None):
        'MO Router management'

        if opts.list:
            self.managers['morouter'].list(arg, opts)
        elif opts.add:
            self.managers['morouter'].add(arg, opts)
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