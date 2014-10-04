import pickle
import re
from jasmin.protocols.cli.managers import Manager, Session
from jasmin.protocols.cli.protocol import str2num
from jasmin.routing.jasminApi import User, MtMessagingCredential, jasminApiCredentialError

MtMessagingCredentialKeyMap = {'class': 'MtMessagingCredential',
                               'keyMapValue': 'mt_credential', 
                               'Authorization': {'http_send': 'http_send',
                                                  'long_content': 'long_content',
                                                  'dlr_level': 'set_dlr_level',
                                                  'dlr_method': 'set_dlr_method',
                                                  'src_address': 'set_source_address',
                                                  'priority': 'set_priority'}}

# A config map between console-configuration keys and User keys.
UserKeyMap = {'uid': 'uid', 'gid': 'gid', 'username': 'username', 'password': 'password', 'mt_messaging_cred': MtMessagingCredentialKeyMap}

TrueBoolCastMap = ['true', '1', 't', 'y', 'yes']
FalseBoolCastMap = ['false', '0', 'f', 'n', 'no']

def castToBuiltCorrectCredType(cred, section, key, value):
    'Will cast value to the correct type depending on the cred class, section and key'
    
    if cred == 'MtMessagingCredential':
        if section == 'Authorization':
            if value in TrueBoolCastMap:
                value = True
            elif value in FalseBoolCastMap:
                value = False
    return value

def UserBuild(fCallback):
    'Parse args and try to build a jasmin.routing.jasminApi.User instance to pass it to fCallback'
    def parse_args_and_call_with_instance(self, *args, **kwargs):
        cmd = args[0]
        arg = args[1]

        # Empty line
        if cmd is None:
            return self.protocol.sendData()
        # Initiate jasmin.routing.jasminApi.User with sessBuffer content
        if cmd == 'ok':
            if ('uid' not in self.sessBuffer or 
                'group' not in self.sessBuffer or 
                'username' not in self.sessBuffer or
                'password' not in self.sessBuffer):
                return self.protocol.sendData('You must set User id (uid), group (gid), username and password before saving !')
            
            # Set defaults when not defined
            if 'mt_credential' not in self.sessBuffer:
                self.sessBuffer[UserKeyMap['mt_messaging_cred']['keyMapValue']] = globals()[UserKeyMap['mt_messaging_cred']['class']]()

            user = {}
            for key, value in self.sessBuffer.iteritems():
                user[key] = value
            try:
                UserInstance = User(**user)
                # Hand the instance to fCallback
                return fCallback(self, UserInstance)
            except Exception, e:
                return self.protocol.sendData('Error: %s' % str(e))
        else:
            # Unknown key
            if not UserKeyMap.has_key(cmd):
                return self.protocol.sendData('Unknown User key: %s' % cmd)
            
            if type(UserKeyMap[cmd]) == dict:
                # Provisioning a sub-User instance (MtMessagingCredential ...)
                subKeyMap = UserKeyMap[cmd]
                
                # Syntax validation
                if not re.match(r'^(\S+) (\S+) (\S+)$', arg):
                    return self.protocol.sendData('Error: expected syntax: %s section key value' % cmd)

                section, key, value = arg.split()

                # Validate section
                possible_values = subKeyMap.keys()
                possible_values.remove('class')
                possible_values.remove('keyMapValue')
                valid_section = False
                for pv in possible_values:
                    if section == pv.lower():
                        section = pv
                        valid_section = True
                        break
                if not valid_section:
                    return self.protocol.sendData('Error: invalid section name: %s, possible values: %s' % (
                        section, ', '.join(possible_values)))

                # Validate key
                if key not in subKeyMap[section].keys():
                    return self.protocol.sendData('Error: invalid key: %s, possible keys: %s' % (
                        key, ', '.join(subKeyMap[section].keys())))
                SectionKey = subKeyMap[section][key]

                # Instanciate a new sub-User object
                sessBufferKey = subKeyMap['keyMapValue']
                if subKeyMap['keyMapValue'] not in self.sessBuffer:
                    self.sessBuffer[subKeyMap['keyMapValue']] = globals()[subKeyMap['class']]()

                try:
                    value = castToBuiltCorrectCredType(subKeyMap['class'], section, SectionKey, value)

                    # Set sub-User object value
                    getattr(self.sessBuffer[subKeyMap['keyMapValue']],  'set%s' % section)(SectionKey, value)
                except jasminApiCredentialError, e:
                    return self.protocol.sendData('Error: %s' % str(e))
            else:
                # Provisioning User instance                
                # IF we got the gid, instanciate a Group if gid exists or return an error
                if cmd == 'gid':
                    group = self.pb['router'].getGroup(arg)
                    if group is None:
                        return self.protocol.sendData('Unknown Group gid:%s, you must first create the Group' % arg)
                    
                    self.sessBuffer['group'] = group
                else:
                    # Buffer key for later User initiating
                    UserKey = UserKeyMap[cmd]
                    self.sessBuffer[UserKey] = arg
            
            return self.protocol.sendData()
    return parse_args_and_call_with_instance

class UserExist:
    'Check if user uid exist before passing it to fCallback'
    def __init__(self, uid_key):
        self.uid_key = uid_key
    def __call__(self, fCallback):
        uid_key = self.uid_key
        def exist_user_and_call(self, *args, **kwargs):
            opts = args[1]
            uid = getattr(opts, uid_key)
    
            if self.pb['router'].getUser(uid) is not None:
                return fCallback(self, *args, **kwargs)
                
            return self.protocol.sendData('Unknown User: %s' % uid)
        return exist_user_and_call

def UserUpdate(fCallback):
    '''Get User and log update requests passing to fCallback
    The log will be handed to fCallback when 'ok' is received'''
    def log_update_requests_and_call(self, *args, **kwargs):
        cmd = args[0]
        arg = args[1]

        # Empty line
        if cmd is None:
            return self.protocol.sendData()
        # Pass sessBuffer as updateLog to fCallback
        if cmd == 'ok':
            if len(self.sessBuffer) == 0:
                return self.protocol.sendData('Nothing to save')
               
            return fCallback(self, self.sessBuffer)
        else:
            # Unknown key
            if not UserKeyMap.has_key(cmd):
                return self.protocol.sendData('Unknown User key: %s' % cmd)
            if cmd == 'uid':
                return self.protocol.sendData('User id can not be modified !')
            if cmd == 'username':
                return self.protocol.sendData('User username can not be modified !')
            
            # IF we got the gid, instanciate a Group if gid exists or return an error
            if cmd == 'gid':
                group = self.pb['router'].getGroup(arg)
                if group is None:
                    return self.protocol.sendData('Unknown Group gid:%s, you must first create the Group' % arg)
                
                self.sessBuffer['group'] = group
            else:
                # Buffer key for later (when receiving 'ok')
                UserKey = UserKeyMap[cmd]
                self.sessBuffer[UserKey] = str2num(arg)
            
            return self.protocol.sendData()
    return log_update_requests_and_call

class UsersManager(Manager):
    managerName = 'user'
    
    def persist(self, arg, opts):
        if self.pb['router'].perspective_persist(opts.profile, 'users'):
            self.protocol.sendData('%s configuration persisted (profile:%s)' % (self.managerName, opts.profile), prompt = False)
        else:
            self.protocol.sendData('Failed to persist %s configuration (profile:%s)' % (self.managerName, opts.profile), prompt = False)
    
    def load(self, arg, opts):
        r = self.pb['router'].perspective_load(opts.profile, 'users')

        if r:
            self.protocol.sendData('%s configuration loaded (profile:%s)' % (self.managerName, opts.profile), prompt = False)
        else:
            self.protocol.sendData('Failed to load %s configuration (profile:%s)' % (self.managerName, opts.profile), prompt = False)
            
    def list(self, arg, opts):
        if arg != '':
            gid = arg
        else:
            gid = None
        users = pickle.loads(self.pb['router'].perspective_user_get_all(gid))
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
            self.protocol.sendData('Total Users: %s' % counter)
        else:
            self.protocol.sendData('Total Users in group [%s]: %s' % (gid, counter))
    
    @Session
    @UserBuild
    def add_session(self, UserInstance):
        st = self.pb['router'].perspective_user_add(pickle.dumps(UserInstance, 2))
        
        if st:
            self.protocol.sendData('Successfully added User [%s] to Group [%s]' % (UserInstance.uid, UserInstance.group.gid), prompt=False)
            self.stopSession()
        else:
            self.protocol.sendData('Failed adding User, check log for details')
    def add(self, arg, opts):
        return self.startSession(self.add_session,
                                 annoucement='Adding a new User: (ok: save, ko: exit)',
                                 completitions=UserKeyMap.keys())
    
    @Session
    @UserUpdate
    def update_session(self, updateLog):
        user = self.pb['router'].getUser(self.sessionContext['uid'])
        # user object must be allways found through the above iteration since it is secured by
        # the @UserExist annotation on update() method

        for key, value in updateLog.iteritems():
            setattr(user, key, value)
        
        self.protocol.sendData('Successfully updated User [%s]' % self.sessionContext['uid'], prompt=False)
        self.stopSession()
    @UserExist(uid_key='update')
    def update(self, arg, opts):
        return self.startSession(self.update_session,
                                 annoucement='Updating User id [%s]: (ok: save, ko: exit)' % opts.update,
                                 completitions=UserKeyMap.keys(),
                                 sessionContext={'uid': opts.update})
    
    @UserExist(uid_key='remove')
    def remove(self, arg, opts):
        st = self.pb['router'].perspective_user_remove(opts.remove)
        
        if st:
            self.protocol.sendData('Successfully removed User id:%s' % opts.remove)
        else:
            self.protocol.sendData('Failed removing User, check log for details')
    
    @UserExist(uid_key='show')
    def show(self, arg, opts):
        user = self.pb['router'].getUser(opts.show)
        
        for key, value in UserKeyMap.iteritems():
            if key == 'password':
                # Dont show password
                pass
            elif key == 'gid':
                self.protocol.sendData('gid %s' % (user.group.gid), prompt=False)
            else:
                self.protocol.sendData('%s %s' % (key, getattr(user, value)), prompt=False)
        self.protocol.sendData()