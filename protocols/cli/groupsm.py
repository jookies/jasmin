import pickle
from managers import Manager, FilterSessionArgs
from jasmin.routing.jasminApi import Group

# A config map between console-configuration keys and Group keys.
GroupKeyMap = {'gid': 'gid'}

def GroupBuild(fn):
    'Parse args and try to build a jasmin.routing.jasminApi.Group instance to pass it to fn'
    def parse_args_and_call_with_instance(self, *args, **kwargs):
        cmd = args[0]
        arg = args[1]

        # Empty line
        if cmd is None:
            return self.protocol.sendData()
        # Initiate jasmin.routing.jasminApi.Group with sessBuffer content
        if cmd == 'ok':
            if len(self.sessBuffer) != 1:
                return self.protocol.sendData('You must set group id (uid) before saving !')
                
            group = {}
            for key, value in self.sessBuffer.iteritems():
                group[key] = value
            try:
                GroupInstance = Group(**group)
                # Hand the instance to fn
                return fn(self, GroupInstance)
            except Exception, e:
                return self.protocol.sendData('Error: %s' % str(e))
        else:
            # Unknown key
            if not GroupKeyMap.has_key(cmd):
                return self.protocol.sendData('Unknown Group key: %s' % cmd)
            
            # Buffer key for later SMPPClientConfig initiating
            GroupKey = GroupKeyMap[cmd]
            self.sessBuffer[GroupKey] = arg
            
            return self.protocol.sendData()
    return parse_args_and_call_with_instance

class GroupsManager(Manager):
    managerName = 'group'
    
    def persist(self, arg, opts):
        # @todo
        raise NotImplementedError
        
    def load(self, arg, opts):
        # @todo
        raise NotImplementedError
    
    def list(self, arg, opts):
        groups = pickle.loads(self.pb['router'].remote_group_get_all())
        counter = 0
        
        if (len(groups)) > 0:
            self.protocol.sendData("#%s" % ('Group id'.ljust(16),
                                                                        ), prompt=False)
            for group in groups:
                counter += 1
                self.protocol.sendData("#%s" % (str(group.gid).ljust(16),
                                                                  ), prompt=False)
                self.protocol.sendData(prompt=False)        
        
        self.protocol.sendData('Total groups: %s' % counter)
    
    @FilterSessionArgs
    @GroupBuild
    def add_session(self, GroupInstance):
        st = self.pb['router'].remote_group_add(pickle.dumps(GroupInstance, 2))
        
        if st:
            self.protocol.sendData('Successfully added Group [%s]' % (GroupInstance.gid), prompt=False)
            self.stopSession()
        else:
            self.protocol.sendData('Failed adding group, check log for details')
    def add(self, arg, opts):
        return self.startSession(self.add_session,
                                 annoucement='Adding a new group: (ok: save, ko: exit)',
                                 completitions=GroupKeyMap.keys())
    
    def update(self, arg, opts):
        # @todo
        raise NotImplementedError
    
    def remove(self, arg, opts):
        # @todo
        raise NotImplementedError
    
    def show(self, arg, opts):
        # @todo
        raise NotImplementedError