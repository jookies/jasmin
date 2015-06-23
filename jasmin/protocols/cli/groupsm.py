import pickle
from jasmin.protocols.cli.managers import PersistableManager, Session
from jasmin.routing.jasminApi import Group

# A config map between console-configuration keys and Group keys.
GroupKeyMap = {'gid': 'gid'}

def GroupBuild(fCallback):
    'Parse args and try to build a jasmin.routing.jasminApi.Group instance to pass it to fCallback'
    def parse_args_and_call_with_instance(self, *args, **kwargs):
        cmd = args[0]
        arg = args[1]

        # Empty line
        if cmd is None:
            return self.protocol.sendData()
        # Initiate jasmin.routing.jasminApi.Group with sessBuffer content
        if cmd == 'ok':
            if len(self.sessBuffer) != 1:
                return self.protocol.sendData('You must set Group id (gid) before saving !')
                
            group = {}
            for key, value in self.sessBuffer.iteritems():
                group[key] = value
            try:
                GroupInstance = Group(**group)
                # Hand the instance to fCallback
                return fCallback(self, GroupInstance)
            except Exception, e:
                return self.protocol.sendData('Error: %s' % str(e))
        else:
            # Unknown key
            if cmd not in GroupKeyMap:
                return self.protocol.sendData('Unknown Group key: %s' % cmd)
            
            # Buffer key for later Group initiating
            GroupKey = GroupKeyMap[cmd]
            self.sessBuffer[GroupKey] = arg
            
            return self.protocol.sendData()
    return parse_args_and_call_with_instance

class GroupExist:
    'Check if Group gid exist before passing it to fCallback'
    def __init__(self, gid_key):
        self.gid_key = gid_key
    def __call__(self, fCallback):
        gid_key = self.gid_key
        def exist_group_and_call(self, *args, **kwargs):
            opts = args[1]
            gid = getattr(opts, gid_key)
    
            if self.pb['router'].getGroup(gid) is not None:
                return fCallback(self, *args, **kwargs)
                
            return self.protocol.sendData('Unknown Group: %s' % gid)
        return exist_group_and_call

class GroupsManager(PersistableManager):
    managerName = 'group'
    
    def persist(self, arg, opts):
        if self.pb['router'].perspective_persist(opts.profile, 'groups'):
            self.protocol.sendData('%s configuration persisted (profile:%s)' % (self.managerName, opts.profile), prompt = False)
        else:
            self.protocol.sendData('Failed to persist %s configuration (profile:%s)' % (self.managerName, opts.profile), prompt = False)
    
    def load(self, arg, opts):
        r = self.pb['router'].perspective_load(opts.profile, 'groups')

        if r:
            self.protocol.sendData('%s configuration loaded (profile:%s)' % (self.managerName, opts.profile), prompt = False)
        else:
            self.protocol.sendData('Failed to load %s configuration (profile:%s)' % (self.managerName, opts.profile), prompt = False)

    def list(self, arg, opts):
        groups = pickle.loads(self.pb['router'].perspective_group_get_all())
        counter = 0
        
        if (len(groups)) > 0:
            self.protocol.sendData("#%s" % ('Group id'.ljust(16),
                                                                        ), prompt=False)
            for group in groups:
                counter += 1
                self.protocol.sendData("#%s" % (str(group.gid).ljust(16),
                                                                  ), prompt=False)
                self.protocol.sendData(prompt=False)        
        
        self.protocol.sendData('Total Groups: %s' % counter)
    
    @Session
    @GroupBuild
    def add_session(self, GroupInstance):
        st = self.pb['router'].perspective_group_add(pickle.dumps(GroupInstance, 2))
        
        if st:
            self.protocol.sendData('Successfully added Group [%s]' % (GroupInstance.gid), prompt=False)
            self.stopSession()
        else:
            self.protocol.sendData('Failed adding Group, check log for details')
    def add(self, arg, opts):
        return self.startSession(self.add_session,
                                 annoucement='Adding a new Group: (ok: save, ko: exit)',
                                 completitions=GroupKeyMap.keys())
    
    @GroupExist(gid_key='remove')
    def remove(self, arg, opts):
        st = self.pb['router'].perspective_group_remove(opts.remove)
        
        if st:
            self.protocol.sendData('Successfully removed Group id:%s' % opts.remove)
        else:
            self.protocol.sendData('Failed removing Group, check log for details')