from test_jcli import jCliTestCases
    
class UserTestCases(jCliTestCases):
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

        expectedList = ['#User id          Group id         Username        ', 
                        '#user_4           AnyGroup         AnyUsername     ', 
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
        expectedList = ['#User id          Group id         Username        ', 
                        '#%s %s %s' % (uid1.ljust(16), gid1.ljust(16), username1.ljust(16)), 
                        '#%s %s %s' % (uid2.ljust(16), gid2.ljust(16), username2.ljust(16)), 
                        'Total Users: 2']
        commands = [{'command': 'user -l', 'expect': expectedList}]
        self._test(r'jcli : ', commands)
    
        # List gid1 only users
        expectedList = ['#User id          Group id         Username        ', 
                        '#%s %s %s' % (uid1.ljust(16), gid1.ljust(16), username1.ljust(16)), 
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
        expectedList = ['#User id          Group id         Username        ', 
                        '#%s %s AnyUsername     ' % (uid.ljust(16), gid.ljust(16)), 
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
        expectedList = ['#User id          Group id         Username        ', 
                        '#%s %s AnyUsername     ' % (uid.ljust(16), newGID.ljust(16)), 
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
        expectedList = ['#User id          Group id         Username        ', 
                        '#%s AnyGroup         AnyUsername     ' % (uid.ljust(16)), 
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
        expectedList = ['#User id          Group id         Username        ', 
                        '#%s %s %s' % (uid1.ljust(16), gid.ljust(16), username1.ljust(16)), 
                        '#%s %s %s' % (uid2.ljust(16), gid.ljust(16), username2.ljust(16)), 
                        'Total Users: 2']
        commands = [{'command': 'user -l', 'expect': expectedList}]
        self._test(r'jcli : ', commands)

        # Remove group
        commands = [{'command': 'group -r %s' % gid, 'expect': r'Successfully removed Group id\:%s' % gid}]
        self._test(r'jcli : ', commands)

        # List again
        commands = [{'command': 'user -l', 'expect': r'Total Users: 0'}]
        return self._test(r'jcli : ', commands)