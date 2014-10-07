import re
from test_jcli import jCliWithoutAuthTestCases
from jasmin.routing.jasminApi import MtMessagingCredential
    
class UserTestCases(jCliWithoutAuthTestCases):
    def add_user(self, finalPrompt, extraCommands = [], GID = None, Username = None):
        sessionTerminated = False
        commands = []
        
        if GID:
            commands.append({'command': 'group -a'})
            commands.append({'command': 'gid %s' % GID})
            commands.append({'command': 'ok', 'expect': r'Successfully added Group \['})
        
        commands.append({'command': 'user -a', 'expect': r'Adding a new User\: \(ok\: save, ko\: exit\)'})
        if GID:
            commands.append({'command': 'gid %s' % GID})
        if Username:
            password = 'RANDOM_PASSWORD'
            commands.append({'command': 'username %s' % Username})
            commands.append({'command': 'password %s' % password})
        for extraCommand in extraCommands:
            commands.append(extraCommand)
            
            if extraCommand['command'] in ['ok', 'ko']:
                sessionTerminated = True
        
        if not sessionTerminated:
            commands.append({'command': 'ok', 'expect': r'Successfully added User \['})

        return self._test(finalPrompt, commands)

    def update_user(self, finalPrompt, uid, extraCommands = []):
        sessionTerminated = False
        commands = []
        
        commands.append({'command': 'user -u %s' % uid, 'expect': r'Updating User id \[%s\]\: \(ok\: save, ko\: exit\)' % uid})
        for extraCommand in extraCommands:
            commands.append(extraCommand)
            
            if extraCommand['command'] in ['ok', 'ko']:
                sessionTerminated = True
        
        if not sessionTerminated:
            commands.append({'command': 'ok', 'expect': r'Successfully updated User \['})

        return self._test(finalPrompt, commands)
    
class BasicTestCases(UserTestCases):
    
    def test_list(self):
        commands = [{'command': 'user -l', 'expect': r'Total Users: 0'}]
        return self._test(r'jcli : ', commands)
    
    def test_add_with_minimum_args(self):
        extraCommands = [{'command': 'uid user_1'}]
        return self.add_user(r'jcli : ', extraCommands, GID = 'AnyGroup', Username = 'AnyUsername')
    
    def test_add_without_minimum_args(self):
        extraCommands = [{'command': 'ok', 'expect': r'You must set User id \(uid\), group \(gid\), username and password before saving !'}]
        return self.add_user(r'> ', extraCommands)
    
    def test_add_invalid_userkey(self):
        extraCommands = [{'command': 'uid user_2'}, {'command': 'anykey anyvalue', 'expect': r'Unknown User key: anykey'}]
        return self.add_user(r'jcli : ', extraCommands, GID = 'AnyGroup', Username = 'AnyUsername')
    
    def test_cancel_add(self):
        extraCommands = [{'command': 'uid user_3'},
                         {'command': 'ko'}, ]
        return self.add_user(r'jcli : ', extraCommands)
    
    def test_add_and_list(self):
        extraCommands = [{'command': 'uid user_4'}]
        self.add_user('jcli : ', extraCommands, GID = 'AnyGroup', Username = 'AnyUsername')

        expectedList = ['#User id          Group id         Username         Balance MT SMS', 
                        '#user_4           AnyGroup         AnyUsername      ND      ND', 
                        'Total Users: 1']
        commands = [{'command': 'user -l', 'expect': expectedList}]
        return self._test(r'jcli : ', commands)
    
    def test_add_and_list_group_users(self):
        # Add 2 users
        gid1 = 'gid1'
        uid1 = 'user_4-1'
        username1 = 'username1'
        extraCommands = [{'command': 'uid %s' % uid1}]
        self.add_user(r'jcli : ', extraCommands, GID = gid1, Username = username1)
    
        gid2 = 'gid2'
        uid2 = 'user_4-2'
        username2 = 'username2'
        extraCommands = [{'command': 'uid %s' % uid2}]
        self.add_user(r'jcli : ', extraCommands, GID = gid2, Username = username2)

        # List all users
        expectedList = ['#User id          Group id         Username         Balance MT SMS', 
                        '#%s %s %s %s %s' % (uid1.ljust(16), gid1.ljust(16), username1.ljust(16), 'ND'.ljust(7), 'ND'.ljust(6)),
                        '#%s %s %s %s %s' % (uid2.ljust(16), gid2.ljust(16), username2.ljust(16), 'ND'.ljust(7), 'ND'.ljust(6)),
                        'Total Users: 2']
        commands = [{'command': 'user -l', 'expect': expectedList}]
        self._test(r'jcli : ', commands)
    
        # List gid1 only users
        expectedList = ['#User id          Group id         Username         Balance MT SMS', 
                        '#%s %s %s %s %s' % (uid1.ljust(16), gid1.ljust(16), username1.ljust(16), 'ND'.ljust(7), 'ND'.ljust(6)),
                        'Total Users in group \[%s\]\: 1' % gid1]
        commands = [{'command': 'user -l %s' % gid1, 'expect': expectedList}]
        self._test(r'jcli : ', commands)
    
    def test_add_cancel_and_list(self):
        extraCommands = [{'command': 'uid user_5'},
                         {'command': 'ko'}, ]
        self.add_user(r'jcli : ', extraCommands)

        commands = [{'command': 'user -l', 'expect': r'Total Users: 0'}]
        return self._test(r'jcli : ', commands)

    def test_add_and_show(self):
        uid = 'user_6'
        gid = 'group_bla'
        username = 'foobar'
        extraCommands = [{'command': 'uid %s' % uid}]
        self.add_user('jcli : ', extraCommands, GID = gid, Username = username)

        expectedList = ['username %s' % username, 
                        'mt_messaging_cred defaultvalue src_addr None',
                        'mt_messaging_cred quota balance ND',
                        'mt_messaging_cred quota sms_count ND',
                        'mt_messaging_cred quota early_percent ND',
                        'mt_messaging_cred valuefilter priority \^\[0-3\]\$',
                        'mt_messaging_cred valuefilter content .*',
                        'mt_messaging_cred valuefilter src_addr .*',
                        'mt_messaging_cred valuefilter dst_addr .*',
                        'mt_messaging_cred authorization dlr_level True',
                        'mt_messaging_cred authorization dlr_method True',
                        'mt_messaging_cred authorization long_content True',
                        'mt_messaging_cred authorization src_addr True',
                        'mt_messaging_cred authorization http_send True',
                        'mt_messaging_cred authorization priority True',
                        'gid %s' % gid,
                        'uid %s' % uid, 
                        ]
        commands = [{'command': 'user -s %s' % uid, 'expect': expectedList}]
        return self._test(r'jcli : ', commands)
        
    def test_show_invalid_uid(self):
        commands = [{'command': 'user -s invalid_uid', 'expect': r'Unknown User\: invalid_uid'}]
        return self._test(r'jcli : ', commands)
    
    def test_update_uid(self):
        uid = 'user_7-1'
        extraCommands = [{'command': 'uid %s' % uid}]
        self.add_user(r'jcli : ', extraCommands, GID = 'AnyGroup', Username = 'AnyUsername')

        commands = [{'command': 'user -u user_7-1', 'expect': r'Updating User id \[%s\]\: \(ok\: save, ko\: exit\)' % uid},
                    {'command': 'uid 2222', 'expect': r'User id can not be modified !'}]
        return self._test(r'> ', commands)
    
    def test_update_username(self):
        uid = 'user_7-2'
        extraCommands = [{'command': 'uid %s' % uid}]
        self.add_user(r'jcli : ', extraCommands, GID = 'AnyGroup', Username = 'AnyUsername')

        commands = [{'command': 'user -u user_7-2', 'expect': r'Updating User id \[%s\]\: \(ok\: save, ko\: exit\)' % uid},
                    {'command': 'username AnotherUsername', 'expect': r'User username can not be modified !'}]
        return self._test(r'> ', commands)
    
    def test_update_gid(self):
        uid = 'user_8'
        gid = 'CurrentGID'
        newGID = 'NewGID'
        extraCommands = [{'command': 'uid %s' % uid}]
        self.add_user(r'jcli : ', extraCommands, GID = gid, Username = 'AnyUsername')
        
        # List
        expectedList = ['#User id          Group id         Username         Balance MT SMS', 
                        '#%s %s AnyUsername      %s %s' % (uid.ljust(16), gid.ljust(16), 'ND'.ljust(7), 'ND'.ljust(6)), 
                        'Total Users: 1']
        commands = [{'command': 'user -l', 'expect': expectedList}]
        self._test(r'jcli : ', commands)

        # Add a new group
        commands = [{'command': 'group -a'},
                    {'command': 'gid %s' % newGID},
                    {'command': 'ok', 'expect': r'Successfully added Group \['}]
        self._test(r'jcli : ', commands)

        # Place the user into this new group
        commands = [{'command': 'user -u %s' % uid, 'expect': r'Updating User id \[%s\]\: \(ok\: save, ko\: exit\)' % uid},
                    {'command': 'gid %s' % newGID},
                    {'command': 'ok', 'expect': r'Successfully updated User \[%s\]' % uid}]
        self._test(r'jcli : ', commands)

        # List again
        expectedList = ['#User id          Group id         Username         Balance MT SMS', 
                        '#%s %s AnyUsername      %s %s' % (uid.ljust(16), newGID.ljust(16), 'ND'.ljust(7), 'ND'.ljust(6)), 
                        'Total Users: 1']
        commands = [{'command': 'user -l', 'expect': expectedList}]
        return self._test(r'jcli : ', commands)

    def test_update_and_show(self):
        uid = 'user_9'
        gid = 'CurrentGID'
        username = 'AnyUsername'
        newGID = 'NewGID'
        extraCommands = [{'command': 'uid %s' % uid}]
        self.add_user(r'jcli : ', extraCommands, GID = gid, Username = username)
        
        # Add a new group
        commands = [{'command': 'group -a'},
                    {'command': 'gid %s' % newGID},
                    {'command': 'ok', 'expect': r'Successfully added Group \['}]
        self._test(r'jcli : ', commands)

        # Place the user into this new group
        commands = [{'command': 'user -u %s' % uid, 'expect': r'Updating User id \[%s\]\: \(ok\: save, ko\: exit\)' % uid},
                    {'command': 'gid %s' % newGID},
                    {'command': 'ok', 'expect': r'Successfully updated User \[%s\]' % uid}]
        self._test(r'jcli : ', commands)

        # Show and assert
        expectedList = ['username %s' % username, 
                        'mt_messaging_cred defaultvalue src_addr None',
                        'mt_messaging_cred quota balance ND',
                        'mt_messaging_cred quota sms_count ND',
                        'mt_messaging_cred quota early_percent ND',
                        'mt_messaging_cred valuefilter priority \^\[0-3\]\$',
                        'mt_messaging_cred valuefilter content .*',
                        'mt_messaging_cred valuefilter src_addr .*',
                        'mt_messaging_cred valuefilter dst_addr .*',
                        'mt_messaging_cred authorization dlr_level True',
                        'mt_messaging_cred authorization dlr_method True',
                        'mt_messaging_cred authorization long_content True',
                        'mt_messaging_cred authorization src_addr True',
                        'mt_messaging_cred authorization http_send True',
                        'mt_messaging_cred authorization priority True',
                        'gid %s' % newGID,
                        'uid %s' % uid, 
                        ]
        commands = [{'command': 'user -s %s' % uid, 'expect': expectedList}]
        return self._test(r'jcli : ', commands)

    def test_remove_invalid_uid(self):
        commands = [{'command': 'user -r invalid_uid', 'expect': r'Unknown User\: invalid_uid'}]
        return self._test(r'jcli : ', commands)
    
    def test_remove(self):
        uid = 'user_10'
        extraCommands = [{'command': 'uid %s' % uid}]
        return self.add_user(r'jcli : ', extraCommands, GID = 'AnyGroup', Username = 'AnyUsername')
    
        commands = [{'command': 'user -r %s' % uid, 'expect': r'Successfully removed User id\:%s' % uid}]
        return self._test(r'jcli : ', commands)

    def test_remove_and_list(self):
        # Add
        uid = 'user_12'
        extraCommands = [{'command': 'uid %s' % uid}]
        self.add_user(r'jcli : ', extraCommands, GID = 'AnyGroup', Username = 'AnyUsername')
    
        # List
        expectedList = ['#User id          Group id         Username         Balance MT SMS', 
                        '#%s AnyGroup         AnyUsername      %s %s' % (uid.ljust(16), 'ND'.ljust(7), 'ND'.ljust(6)), 
                        'Total Users: 1']
        commands = [{'command': 'user -l', 'expect': expectedList}]
        self._test(r'jcli : ', commands)

        # Remove
        commands = [{'command': 'user -r %s' % uid, 'expect': r'Successfully removed User id\:%s' % uid}]
        self._test(r'jcli : ', commands)

        # List again
        commands = [{'command': 'user -l', 'expect': r'Total Users: 0'}]
        return self._test(r'jcli : ', commands)
    
    def test_remove_group_will_remove_its_users(self):
        gid = 'a_group'
        # Add 2 users to gid
        uid1 = 'user_13-1'
        username1 = 'username1'
        extraCommands = [{'command': 'uid %s' % uid1}]
        self.add_user(r'jcli : ', extraCommands, GID = gid, Username = username1)
    
        uid2 = 'user_13-2'
        username2 = 'username2'
        extraCommands = [{'command': 'uid %s' % uid2}]
        self.add_user(r'jcli : ', extraCommands, GID = gid, Username = username2)

        # List
        expectedList = ['#User id          Group id         Username         Balance MT SMS', 
                        '#%s %s %s %s %s' % (uid1.ljust(16), gid.ljust(16), username1.ljust(16), 'ND'.ljust(7), 'ND'.ljust(6)), 
                        '#%s %s %s %s %s' % (uid2.ljust(16), gid.ljust(16), username2.ljust(16), 'ND'.ljust(7), 'ND'.ljust(6)), 
                        'Total Users: 2']
        commands = [{'command': 'user -l', 'expect': expectedList}]
        self._test(r'jcli : ', commands)

        # Remove group
        commands = [{'command': 'group -r %s' % gid, 'expect': r'Successfully removed Group id\:%s' % gid}]
        self._test(r'jcli : ', commands)

        # List again
        commands = [{'command': 'user -l', 'expect': r'Total Users: 0'}]
        return self._test(r'jcli : ', commands)

class MtMessagingCredentialTestCases(UserTestCases):

    def _test_user_with_MtMessagingCredential(self, uid, gid, username, mtcred):
        if mtcred.getQuota('balance') is None:
            assertBalance = 'ND'
        else:
            assertBalance = str(float(mtcred.getQuota('balance')))

        if mtcred.getQuota('submit_sm_count') is None:
            assertSmsCount = 'ND'
        else:
            assertSmsCount = str(mtcred.getQuota('submit_sm_count'))

        if mtcred.getQuota('early_decrement_balance_percent') is None:
            assertEarlyPercent = 'ND'
        else:
            assertEarlyPercent = str(float(mtcred.getQuota('early_decrement_balance_percent')))

        # Show and assert
        expectedList = ['username AnyUsername', 
                        'mt_messaging_cred defaultvalue src_addr %s' % mtcred.getDefaultValue('source_address'),
                        'mt_messaging_cred quota balance %s' % assertBalance,
                        'mt_messaging_cred quota sms_count %s' % assertSmsCount,
                        'mt_messaging_cred quota early_percent %s' % assertEarlyPercent,
                        'mt_messaging_cred valuefilter priority %s' % re.escape(mtcred.getValueFilter('priority').pattern),
                        'mt_messaging_cred valuefilter content %s' % re.escape(mtcred.getValueFilter('content').pattern),
                        'mt_messaging_cred valuefilter src_addr %s' % re.escape(mtcred.getValueFilter('source_address').pattern),
                        'mt_messaging_cred valuefilter dst_addr %s' % re.escape(mtcred.getValueFilter('destination_address').pattern),
                        'mt_messaging_cred authorization dlr_level %s' % mtcred.getAuthorization('set_dlr_level'),
                        'mt_messaging_cred authorization dlr_method %s' % mtcred.getAuthorization('set_dlr_method'),
                        'mt_messaging_cred authorization long_content %s' % mtcred.getAuthorization('long_content'),
                        'mt_messaging_cred authorization src_addr %s' % mtcred.getAuthorization('set_source_address'),
                        'mt_messaging_cred authorization http_send %s' % mtcred.getAuthorization('http_send'),
                        'mt_messaging_cred authorization priority %s' % mtcred.getAuthorization('set_priority'),
                        'gid AnyGroup',
                        'uid user_1', 
                        ]
        commands = [{'command': 'user -s user_1', 'expect': expectedList}]
        self._test(r'jcli : ', commands)

        # List and assert
        expectedList = ['#.*', 
                        '#%s %s %s %s %s' % (uid.ljust(16), gid.ljust(16), username.ljust(16), assertBalance.ljust(7), assertSmsCount.ljust(6)),
                        ]
        commands = [{'command': 'user -l', 'expect': expectedList}]
        self._test(r'jcli : ', commands)
    
    def test_default(self):
        "Default user is created with a default MtMessagingCredential() instance"

        # Assert User adding
        extraCommands = [{'command': 'uid user_1'}]
        self.add_user(r'jcli : ', extraCommands, GID = 'AnyGroup', Username = 'AnyUsername')
        self._test_user_with_MtMessagingCredential('user_1', 'AnyGroup', 'AnyUsername', MtMessagingCredential())

        # Assert User updating
        extraCommands = [{'command': 'password anypassword'}]
        self.update_user(r'jcli : ', 'user_1', extraCommands)
        self._test_user_with_MtMessagingCredential('user_1', 'AnyGroup', 'AnyUsername', MtMessagingCredential())

    def test_authorization(self):
        _cred = MtMessagingCredential()
        _cred.setAuthorization('http_send', False)

        # Assert User adding
        extraCommands = [{'command': 'uid user_1'},
                         {'command': 'mt_messaging_cred authorization http_send no'}]
        self.add_user(r'jcli : ', extraCommands, GID = 'AnyGroup', Username = 'AnyUsername')
        self._test_user_with_MtMessagingCredential('user_1', 'AnyGroup', 'AnyUsername', _cred)

        # Assert User updating
        _cred.setAuthorization('http_send', True)
        extraCommands = [{'command': 'password anypassword'},
                         {'command': 'mt_messaging_cred authorization http_send 1'}]
        self.update_user(r'jcli : ', 'user_1', extraCommands)
        self._test_user_with_MtMessagingCredential('user_1', 'AnyGroup', 'AnyUsername', _cred)

    def test_valuefilter(self):
        _cred = MtMessagingCredential()
        _cred.setValueFilter('content', '^HELLO$')

        # Assert User adding
        extraCommands = [{'command': 'uid user_1'},
                         {'command': 'mt_messaging_cred valuefilter content ^HELLO$'}]
        self.add_user(r'jcli : ', extraCommands, GID = 'AnyGroup', Username = 'AnyUsername')
        self._test_user_with_MtMessagingCredential('user_1', 'AnyGroup', 'AnyUsername', _cred)

        # Assert User updating
        _cred.setValueFilter('content', '^WORLD$')
        extraCommands = [{'command': 'password anypassword'},
                         {'command': 'mt_messaging_cred valuefilter content ^WORLD$'}]
        self.update_user(r'jcli : ', 'user_1', extraCommands)
        self._test_user_with_MtMessagingCredential('user_1', 'AnyGroup', 'AnyUsername', _cred)

    def test_defaultvalue(self):
        _cred = MtMessagingCredential()
        _cred.setDefaultValue('source_address', 'World')

        # Assert User adding
        extraCommands = [{'command': 'uid user_1'},
                         {'command': 'mt_messaging_cred defaultvalue src_addr World'}]
        self.add_user(r'jcli : ', extraCommands, GID = 'AnyGroup', Username = 'AnyUsername')
        self._test_user_with_MtMessagingCredential('user_1', 'AnyGroup', 'AnyUsername', _cred)

        # Assert User updating
        _cred.setDefaultValue('source_address', 'HELLO')
        extraCommands = [{'command': 'password anypassword'},
                         {'command': 'mt_messaging_cred defaultvalue src_addr HELLO'}]
        self.update_user(r'jcli : ', 'user_1', extraCommands)
        self._test_user_with_MtMessagingCredential('user_1', 'AnyGroup', 'AnyUsername', _cred)

    def test_quota(self):
        _cred = MtMessagingCredential()
        _cred.setQuota('balance', 40.3)

        # Assert User adding
        extraCommands = [{'command': 'uid user_1'},
                         {'command': 'mt_messaging_cred quota balance 40.3'}]
        self.add_user(r'jcli : ', extraCommands, GID = 'AnyGroup', Username = 'AnyUsername')
        self._test_user_with_MtMessagingCredential('user_1', 'AnyGroup', 'AnyUsername', _cred)

        # Assert User updating
        _cred.setQuota('balance', 42)
        extraCommands = [{'command': 'password anypassword'},
                         {'command': 'mt_messaging_cred quota balance 42'}]
        self.update_user(r'jcli : ', 'user_1', extraCommands)
        self._test_user_with_MtMessagingCredential('user_1', 'AnyGroup', 'AnyUsername', _cred)

    def test_all(self):
        _cred = MtMessagingCredential()
        _cred.setAuthorization('http_send', False)
        _cred.setAuthorization('long_content', False)
        _cred.setAuthorization('set_dlr_level', False)
        _cred.setAuthorization('set_dlr_method', False)
        _cred.setAuthorization('set_source_address', False)
        _cred.setAuthorization('set_priority', False)
        _cred.setValueFilter('destination_address', '^HELLO$')
        _cred.setValueFilter('source_address', '^World$')
        _cred.setValueFilter('priority', '^1$')
        _cred.setValueFilter('content', '[0-9].*')
        _cred.setDefaultValue('source_address', 'BRAND NAME')
        _cred.setQuota('balance', 40.3)

        # Assert User adding
        extraCommands = [{'command': 'uid user_1'},
                         {'command': 'mt_messaging_cred authorization http_send no'},
                         {'command': 'mt_messaging_cred authorization long_content n'},
                         {'command': 'mt_messaging_cred authorization dlr_level 0'},
                         {'command': 'mt_messaging_cred authorization dlr_method NO'},
                         {'command': 'mt_messaging_cred authorization src_addr false'},
                         {'command': 'mt_messaging_cred authorization priority f'},
                         {'command': 'mt_messaging_cred valuefilter dst_addr ^HELLO$'},
                         {'command': 'mt_messaging_cred valuefilter src_addr ^World$'},
                         {'command': 'mt_messaging_cred valuefilter priority ^1$'},
                         {'command': 'mt_messaging_cred valuefilter content [0-9].*'},
                         {'command': 'mt_messaging_cred defaultvalue src_addr BRAND NAME'},
                         {'command': 'mt_messaging_cred quota balance 40.3'}]
        self.add_user(r'jcli : ', extraCommands, GID = 'AnyGroup', Username = 'AnyUsername')
        self._test_user_with_MtMessagingCredential('user_1', 'AnyGroup', 'AnyUsername', _cred)

        # Assert User updating
        _cred.setAuthorization('http_send', True)
        _cred.setAuthorization('long_content', True)
        _cred.setAuthorization('set_dlr_level', True)
        _cred.setAuthorization('set_dlr_method', True)
        _cred.setAuthorization('set_source_address', True)
        _cred.setAuthorization('set_priority', True)
        _cred.setValueFilter('destination_address', '^WORLD$')
        _cred.setValueFilter('source_address', '^HELLO$')
        _cred.setValueFilter('priority', '^2$')
        _cred.setValueFilter('content', '[2-6].*')
        _cred.setDefaultValue('source_address', 'SEXY NAME')
        _cred.setQuota('balance', 66)
        extraCommands = [{'command': 'password anypassword'},
                         {'command': 'mt_messaging_cred authorization http_send yes'},
                         {'command': 'mt_messaging_cred authorization long_content y'},
                         {'command': 'mt_messaging_cred authorization dlr_level 1'},
                         {'command': 'mt_messaging_cred authorization dlr_method YES'},
                         {'command': 'mt_messaging_cred authorization src_addr true'},
                         {'command': 'mt_messaging_cred authorization priority t'},
                         {'command': 'mt_messaging_cred valuefilter dst_addr ^WORLD$'},
                         {'command': 'mt_messaging_cred valuefilter src_addr ^HELLO$'},
                         {'command': 'mt_messaging_cred valuefilter priority ^2$'},
                         {'command': 'mt_messaging_cred valuefilter content [2-6].*'},
                         {'command': 'mt_messaging_cred defaultvalue src_addr SEXY NAME'},
                         {'command': 'mt_messaging_cred quota balance 66'}]
        self.update_user(r'jcli : ', 'user_1', extraCommands)
        self._test_user_with_MtMessagingCredential('user_1', 'AnyGroup', 'AnyUsername', _cred)

    def test_invalid_syntax(self):
        # Assert User adding
        extraCommands = [{'command': 'uid user_1'},
                         {'command': 'mt_messaging_red authorization http_send no', 
                         'expect': 'Unknown User key: mt_messaging_red'},
                         {'command': 'mt_messaging_cred quta balance 40.3',
                         'expect': 'Error: invalid section name: quta, possible values: DefaultValue, Quota, ValueFilter, Authorization'},
                         {'command': 'mt_messaging_cred DefaultValue Anything AnyValue',
                         'expect': 'Error: invalid section name: DefaultValue, possible values: DefaultValue, Quota, ValueFilter, Authorization'},
                         {'command': 'mt_messaging_cred defaultvalue Anything AnyValue',
                         'expect': 'Error: invalid key: Anything, possible keys: src_addr'},
                         {'command': 'mt_messaging_cred quota balance incorrectvalue',
                         'expect':  'Error: could not convert string to float: incorrectvalue'},
                         {'command': 'mt_messaging_cred authorization http_send incorrectvalue',
                         'expect':  'Error: http_send is not a boolean value: incorrectvalue'},
                         ]
        self.add_user(r'jcli : ', extraCommands, GID = 'AnyGroup', Username = 'AnyUsername')

        # Assert User updating
        extraCommands = [{'command': 'password anypassword'},
                         {'command': 'mt_messaging_red authorization http_send no', 
                         'expect': 'Unknown User key: mt_messaging_red'},
                         {'command': 'mt_messaging_cred quta balance 40.3',
                         'expect': 'Error: invalid section name: quta, possible values: DefaultValue, Quota, ValueFilter, Authorization'},
                         {'command': 'mt_messaging_cred DefaultValue Anything AnyValue',
                         'expect': 'Error: invalid section name: DefaultValue, possible values: DefaultValue, Quota, ValueFilter, Authorization'},
                         {'command': 'mt_messaging_cred defaultvalue Anything AnyValue',
                         'expect': 'Error: invalid key: Anything, possible keys: src_addr'},
                         {'command': 'mt_messaging_cred quota balance incorrectvalue',
                         'expect':  'Error: could not convert string to float: incorrectvalue'},
                         {'command': 'mt_messaging_cred authorization http_send incorrectvalue',
                         'expect':  'Error: http_send is not a boolean value: incorrectvalue'},
                         ]
        self.update_user(r'jcli : ', 'user_1', extraCommands)