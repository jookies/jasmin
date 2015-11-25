from test_jcli import jCliWithoutAuthTestCases

class GroupTestCases(jCliWithoutAuthTestCases):
    def add_group(self, finalPrompt, extraCommands = []):
        sessionTerminated = False
        commands = []
        commands.append({'command': 'group -a', 'expect': r'Adding a new Group\: \(ok\: save, ko\: exit\)'})
        for extraCommand in extraCommands:
            commands.append(extraCommand)

            if extraCommand['command'] in ['ok', 'ko']:
                sessionTerminated = True

        if not sessionTerminated:
            commands.append({'command': 'ok', 'expect': r'Successfully added Group \['})

        return self._test(finalPrompt, commands)

class BasicTestCases(GroupTestCases):

    def test_list(self):
        commands = [{'command': 'group -l', 'expect': r'Total Groups: 0'}]
        self._test(r'jcli : ', commands)

    def test_add_with_minimum_args(self):
        extraCommands = [{'command': 'gid group_1'}]
        self.add_group(r'jcli : ', extraCommands)

    def test_add_with_empty_gid(self):
        extraCommands = [{'command': 'gid '},
                         {'command': 'ok', 'expect': r'Error: Group gid syntax is invalid'},]
        self.add_group(r'> ', extraCommands)

    def test_add_with_invalid_gid(self):
        extraCommands = [{'command': 'gid With Space'},
                         {'command': 'ok', 'expect': r'Error: Group gid syntax is invalid'},]
        self.add_group(r'> ', extraCommands)

    def test_add_without_minimum_args(self):
        extraCommands = [{'command': 'ok', 'expect': r'You must set Group id \(gid\) before saving !'}]
        self.add_group(r'> ', extraCommands)

    def test_add_invalid_key(self):
        extraCommands = [{'command': 'gid group_2'}, {'command': 'anykey anyvalue', 'expect': r'Unknown Group key: anykey'}]
        self.add_group(r'jcli : ', extraCommands)

    def test_cancel_add(self):
        extraCommands = [{'command': 'gid group_3'},
                         {'command': 'ko'}, ]
        self.add_group(r'jcli : ', extraCommands)

    def test_add_and_list(self):
        extraCommands = [{'command': 'gid group_4'}]
        self.add_group('jcli : ', extraCommands)

        expectedList = ['#Group id        ',
                        '#group_4         ',
                        'Total Groups: 1']
        commands = [{'command': 'group -l', 'expect': expectedList}]
        self._test(r'jcli : ', commands)

    def test_add_cancel_and_list(self):
        extraCommands = [{'command': 'gid group_5'},
                         {'command': 'ko'}, ]
        self.add_group(r'jcli : ', extraCommands)

        commands = [{'command': 'group -l', 'expect': r'Total Groups: 0'}]
        self._test(r'jcli : ', commands)

    # Showing Group is not implemented since there's only one attribute (gid)
    def test_show_not_available(self):
        gid = 'group_6'
        extraCommands = [{'command': 'gid %s' % gid}]
        self.add_group('jcli : ', extraCommands)

        commands = [{'command': 'group -s %s' % gid, 'expect': 'no such option\: -s'}]
        self._test(r'jcli : ', commands)

    # Updating Group is not implemented since there's only one attribute (gid)
    # and gid is not updateable
    def test_update_not_available(self):
        gid = 'group_7'
        extraCommands = [{'command': 'gid %s' % gid}]
        self.add_group(r'jcli : ', extraCommands)

        commands = [{'command': 'group -u %s' % gid, 'expect': 'no such option\: -u'}]
        self._test(r'jcli : ', commands)

    def test_remove_invalid_gid(self):
        commands = [{'command': 'group -r invalid_cid', 'expect': r'Unknown Group\: invalid_cid'}]
        self._test(r'jcli : ', commands)

    def test_remove(self):
        gid = 'group_8'
        extraCommands = [{'command': 'gid %s' % gid}]
        self.add_group(r'jcli : ', extraCommands)

        commands = [{'command': 'group -r %s' % gid, 'expect': r'Successfully removed Group id\:%s' % gid}]
        self._test(r'jcli : ', commands)

    def test_remove_and_list(self):
        # Add
        gid = 'group_9'
        extraCommands = [{'command': 'gid %s' % gid}]
        self.add_group(r'jcli : ', extraCommands)

        # List
        expectedList = ['#Group id        ',
                        '#%s' % gid.ljust(16),
                        'Total Groups: 1']
        commands = [{'command': 'group -l', 'expect': expectedList}]
        self._test(r'jcli : ', commands)

        # Remove
        commands = [{'command': 'group -r %s' % gid, 'expect': r'Successfully removed Group id\:%s' % gid}]
        self._test(r'jcli : ', commands)

        # List again
        commands = [{'command': 'group -l', 'expect': r'Total Groups: 0'}]
        self._test(r'jcli : ', commands)

    def test_enabled_disable(self):
        "Related to #306"
        gid = 'group_10'
        extraCommands = [{'command': 'gid %s' % gid}]
        self.add_group(r'jcli : ', extraCommands)

        # Disable group
        commands = [{'command': 'group -d %s' % gid,
                     'expect': r'Successfully disabled Group id\:%s' % gid}]
        self._test(r'jcli : ', commands)

        # List
        expectedList = ['#Group id',
                        '#!%s' % gid,
                        'Total Groups: 1']
        commands = [{'command': 'group -l', 'expect': expectedList}]
        self._test(r'jcli : ', commands)

        # Enable group
        commands = [{'command': 'group -e %s' % gid,
                     'expect': r'Successfully enabled Group id\:%s' % gid}]
        self._test(r'jcli : ', commands)

        # List
        expectedList = ['#Group id',
                        '#%s' % gid,
                        'Total Groups: 1']
        commands = [{'command': 'group -l', 'expect': expectedList}]
        self._test(r'jcli : ', commands)
