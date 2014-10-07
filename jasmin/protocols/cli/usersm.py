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
                                                  'src_addr': 'set_source_address',
                                                  'priority': 'set_priority'},
                               'ValueFilter': {'dst_addr': 'destination_address',
                                                'src_addr': 'source_address',
                                                'priority': 'priority',
                                                'content': 'content'},
                               'DefaultValue': {'src_addr': 'source_address'},
                               'Quota': {'balance': 'balance',
                                          'early_percent': 'early_decrement_balance_percent',
                                          'sms_count': 'submit_sm_count'},
                                }

# A config map between console-configuration keys and User keys.
UserKeyMap = {'uid': 'uid', 'gid': 'gid', 'username': 'username', 'password': 'password', 'mt_messaging_cred': MtMessagingCredentialKeyMap}

TrueBoolCastMap = ['true', '1', 't', 'y', 'yes']
FalseBoolCastMap = ['false', '0', 'f', 'n', 'no']

def castToBuiltCorrectCredType(cred, section, key, value):
    'Will cast value to the correct type depending on the cred class, section and key'
    
    if cred == 'MtMessagingCredential':
        if section == 'Authorization':
            if value.lower() in TrueBoolCastMap:
                value = True
            elif value.lower() in FalseBoolCastMap:
                value = False
        elif section == 'Quota':
            if value.lower() == 'none':
                value = None
            elif key == 'balance' or key == 'early_decrement_balance_percent':
                value = float(value)
            elif key == 'submit_sm_count':
                value = int(value)

        # Make a final validation: pass value to a temporarly MtMessagingCredential
        # object, an exception will be raised if the type is not correct
        _o = MtMessagingCredential()
        getattr(_o, 'set%s' % section)(key, value)
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
                _r = re.match(r'^(\S+) (\S+) (\S+.*$)', arg)
                if not _r:
                    return self.protocol.sendData('Error: expected syntax: %s section key value' % cmd)

                section = _r.group(1)
                key = _r.group(2)
                value = _r.group(3)

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

                try:
                    # Input value are received in string type, castToBuiltCorrectCredType will fix the
                    # type depending on class, section and SectionKey
                    SectionValue = castToBuiltCorrectCredType(subKeyMap['class'], section, SectionKey, value)

                    # Instanciate a new sub-User object
                    if subKeyMap['keyMapValue'] not in self.sessBuffer:
                        self.sessBuffer[subKeyMap['keyMapValue']] = globals()[subKeyMap['class']]()

                    # Set sub-User object value
                    getattr(self.sessBuffer[subKeyMap['keyMapValue']],  'set%s' % section)(SectionKey, SectionValue)
                except (jasminApiCredentialError, ValueError) as e:
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
            
            if type(UserKeyMap[cmd]) == dict:
                # Provisioning a sub-User instance (MtMessagingCredential ...)
                subKeyMap = UserKeyMap[cmd]
                
                # Syntax validation
                _r = re.match(r'^(\S+) (\S+) (\S+.*$)', arg)
                if not _r:
                    return self.protocol.sendData('Error: expected syntax: %s section key value' % cmd)

                section = _r.group(1)
                key = _r.group(2)
                value = _r.group(3)

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

                try:
                    # Input value are received in string type, castToBuiltCorrectCredType will fix the
                    # type depending on class, section and SectionKey
                    SectionValue = castToBuiltCorrectCredType(subKeyMap['class'], section, SectionKey, value)

                    # Instanciate a new sub-User dict to receive update-log to be applied
                    # once 'ok' is received
                    sessBufferKey = '_%s' % subKeyMap['keyMapValue']
                    if sessBufferKey not in self.sessBuffer:
                        self.sessBuffer[sessBufferKey] = {section: {}}
                    if section not in self.sessBuffer[sessBufferKey]:
                        self.sessBuffer[sessBufferKey][section] = {}

                    # Set sub-User object value
                    self.sessBuffer[sessBufferKey][section][SectionKey] = SectionValue
                except (jasminApiCredentialError, ValueError) as e:
                    return self.protocol.sendData('Error: %s' % str(e))
            else:
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
            self.protocol.sendData("#%s %s %s %s %s" % ('User id'.ljust(16),
                                                        'Group id'.ljust(16),
                                                        'Username'.ljust(16),
                                                        'Balance'.ljust(7),
                                                        'MT SMS'.ljust(6),
                                                       ), prompt=False)
            for user in users:
                counter += 1
                balance = user.mt_credential.getQuota('balance')
                if balance is None:
                    balance = 'ND'
                sms_count = user.mt_credential.getQuota('submit_sm_count')
                if sms_count is None:
                    sms_count = 'ND'
                self.protocol.sendData("#%s %s %s %s %s" % (
                    str(user.uid).ljust(16),
                    str(user.group.gid).ljust(16),
                    str(user.username).ljust(16),
                    str(balance).ljust(7),
                    str(sms_count).ljust(6),
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
            if key[:1] == '_':
                # When key is prefixed by '_' it must be treated exceptionnally since the value
                # is holding advanced update log of a sub-User object
                subUserObject = getattr(user, key[1:])
                for update in value.iteritems():
                    section = update[0]
                    if update[1] is None:
                        continue

                    for SectionKey, SectionValue in update[1].iteritems():
                        getattr(subUserObject,  'set%s' % section)(SectionKey, SectionValue)
            else:
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
                self.protocol.sendData('gid %s' % (user.group.gid), prompt = False)
            elif type(value) == str:
                self.protocol.sendData('%s %s' % (key, getattr(user, value)), prompt = False)
            elif type(value) == dict and 'class' in value:
                if value['class'] == 'MtMessagingCredential':
                    for section, sectionData in value.iteritems():
                        if section in ['class', 'keyMapValue']:
                            continue
                        for SectionShortKey, SectionLongKey in value[section].iteritems():
                            sectionValue = getattr(user.mt_credential, 'get%s' % section)(SectionLongKey)
                            if section == 'ValueFilter':
                                sectionValue = sectionValue.pattern
                            elif section == 'Quota' and sectionValue is None:
                                sectionValue = 'ND'
                            self.protocol.sendData('%s %s %s %s' % 
                                (key, section.lower(), SectionShortKey, sectionValue), prompt = False)
        self.protocol.sendData()