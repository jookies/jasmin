import pickle
from managers import Manager, FilterSessionArgs
from jasmin.routing.jasminApi import User, Group

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
            print self.sessBuffer
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
                found = False
                for _group in groups:
                    if _group.gid == arg:
                        found = True
                        break
                
                if not found:
                    return self.protocol.sendData('Unknown Group gid:%s, you must first create the Group' % arg)
                
                self.sessBuffer['group'] = Group(arg)
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
        # @todo List users of a given group (optionnal arg)
        users = self.pb['router'].remote_user_get_all()
        counter = 0
        
        print users
        #if (len(users)) > 0:
        #    self.protocol.sendData("#%s %s %s %s %s" % ('Connector id'.ljust(35),
        #                                                                'Service'.ljust(7),
        #                                                                'Session'.ljust(16),
        #                                                                'Starts'.ljust(6),
        #                                                                'Stops'.ljust(5),
        #                                                                ), prompt=False)
        #    for connector in connectors:
        #        counter += 1
        #        self.protocol.sendData("#%s %s %s %s %s" % (str(connector['id']).ljust(35),
        #                                                          str('started' if connector['service_status'] == 1 else 'stopped').ljust(7),
        #                                                          str(connector['session_state']).ljust(16),
        #                                                          str(connector['start_count']).ljust(6),
        #                                                          str(connector['stop_count']).ljust(5),
        #                                                          ), prompt=False)
        #        self.protocol.sendData(prompt=False)        
    
    @FilterSessionArgs
    @UserBuild
    def add_session(self, UserInstance):
        st = self.pb['router'].remote_user_add(pickle.dumps(UserInstance, 2))
        
        if st:
            self.protocol.sendData('Successfully added User [%s] to Group [%S]' % (UserInstance.uid, UserInstance.group.gid), prompt=False)
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