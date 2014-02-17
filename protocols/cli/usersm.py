import pickle
from managers import Manager, FilterSessionArgs
from jasmin.routing.jasminApi import User

# A config map between console-configuration keys and User keys.
UserKeyMap = {'uid': 'uid', 'gid': 'gid', 'username': 'username', 'password': 'password'}

def UserBuild(fn):
    'Parse args and try to build a jasmin.routing.jasminApi.User instance to pass it to fn'
    def parse_args_and_call_with_instance(self, *args, **kwargs):
        cmd = args[0]
        arg = args[1]

        # Empty line
        if cmd is None:
            return self.protocol.sendData()
        # Initiate jasmin.routing.jasminApi.User with sessBuffer content
        if cmd == 'ok':
            if len(self.sessBuffer) != 4:
                return self.protocol.sendData('You must set user id (uid), group (gid), username and password before saving !')
                
            user = {}
            for key, value in self.sessBuffer.iteritems():
                user[key] = value
            try:
                UserInstance = User(**user)
                # Hand the instance to fn
                return fn(self, UserInstance)
            except Exception, e:
                return self.protocol.sendData('Error: %s' % str(e))
        else:
            # Unknown key
            if not UserKeyMap.has_key(cmd):
                return self.protocol.sendData('Unknown User key: %s' % cmd)
            
            # IF we got the gid, instanciate a Group if gid exists or return an error
            if cmd == 'gid':
                groups = pickle.loads(self.pb['router'].remote_group_get_all())
                group = None
                for _group in groups:
                    if _group.gid == arg:
                        group = _group
                        break
                
                if group is None:
                    return self.protocol.sendData('Unknown Group gid:%s, you must first create the Group' % arg)
                
                self.sessBuffer['group'] = group
            else:
                # Buffer key for later SMPPClientConfig initiating
                UserKey = UserKeyMap[cmd]
                self.sessBuffer[UserKey] = arg
            
            return self.protocol.sendData()
    return parse_args_and_call_with_instance

class UsersManager(Manager):
    managerName = 'user'
    
    def persist(self, arg, opts):
        # @todo
        raise NotImplementedError
        
    def load(self, arg, opts):
        # @todo
        raise NotImplementedError
    
    def list(self, arg, opts):
        if arg != '':
            gid = arg
        else:
            gid = None
        users = pickle.loads(self.pb['router'].remote_user_get_all(gid))
        counter = 0
        
        if (len(users)) > 0:
            self.protocol.sendData("#%s %s %s" % ('User id'.ljust(16),
                                                                        'Group id'.ljust(16),
                                                                        'Username'.ljust(16),
                                                                        ), prompt=False)
            for user in users:
                counter += 1
                self.protocol.sendData("#%s %s %s" % (str(user.uid).ljust(16),
                                                                  str(user.group.gid).ljust(16),
                                                                  str(user.username).ljust(16),
                                                                  ), prompt=False)
                self.protocol.sendData(prompt=False)        
        
        if gid is None:
            self.protocol.sendData('Total users: %s' % counter)
        else:
            self.protocol.sendData('Total users in group [gid:%s] : %s' % (gid, counter))
    
    @FilterSessionArgs
    @UserBuild
    def add_session(self, UserInstance):
        st = self.pb['router'].remote_user_add(pickle.dumps(UserInstance, 2))
        
        if st:
            self.protocol.sendData('Successfully added User [%s] to Group [%s]' % (UserInstance.uid, UserInstance.group.gid), prompt=False)
            self.stopSession()
        else:
            self.protocol.sendData('Failed adding user, check log for details')
    def add(self, arg, opts):
        return self.startSession(self.add_session,
                                 annoucement='Adding a new user: (ok: save, ko: exit)',
                                 completitions=UserKeyMap.keys())
    
    def update(self, arg, opts):
        # @todo
        raise NotImplementedError
    
    def remove(self, arg, opts):
        # @todo
        raise NotImplementedError
    
    def show(self, arg, opts):
        # @todo
        raise NotImplementedError