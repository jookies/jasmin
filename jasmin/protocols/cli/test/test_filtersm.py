import os
import re
from test_jcli import jCliWithoutAuthTestCases
from test.test_support import unlink

class FiltersTestCases(jCliWithoutAuthTestCases):
    def add_filter(self, finalPrompt, extraCommands = []):
        sessionTerminated = False
        commands = []
        commands.append({'command': 'filter -a', 'expect': r'Adding a new Filter\: \(ok\: save, ko\: exit\)'})
        for extraCommand in extraCommands:
            commands.append(extraCommand)

            if extraCommand['command'] in ['ok', 'ko']:
                sessionTerminated = True

        if not sessionTerminated:
            commands.append({'command': 'ok', 'expect': r'Successfully added Filter \['})

        return self._test(finalPrompt, commands)

class BasicTestCases(FiltersTestCases):

    def test_add_with_minimum_args(self):
        extraCommands = [{'command': 'fid filter_1'},
                         {'command': 'type TransparentFilter'}]
        return self.add_filter(r'jcli : ', extraCommands)

    def test_add_with_empty_fid(self):
        extraCommands = [{'command': 'fid  ', 'expect': 'Invalid Filter fid syntax: '},
                         {'command': 'type TransparentFilter'},
                         {'command': 'ok', 'expect': r'You must set these options before saving: type, fid'}]
        return self.add_filter(r'> ', extraCommands)

    def test_add_with_invalid_fid(self):
        extraCommands = [{'command': 'fid With Space', 'expect': 'Invalid Filter fid syntax: With Space'},
                         {'command': 'type TransparentFilter'},
                         {'command': 'ok', 'expect': r'You must set these options before saving: type, fid'}]
        return self.add_filter(r'> ', extraCommands)

    def test_add_without_minimum_args(self):
        extraCommands = [{'command': 'ok', 'expect': r'You must set these options before saving: type, fid'}]
        return self.add_filter(r'> ', extraCommands)

    def test_add_invalid_key(self):
        extraCommands = [{'command': 'fid filter_2'},
                         {'command': 'type TransparentFilter'},
                         {'command': 'anykey anyvalue', 'expect': r'Unknown Filter key: anykey'}]
        return self.add_filter(r'jcli : ', extraCommands)

    def test_cancel_add(self):
        extraCommands = [{'command': 'fid filter_3'},
                         {'command': 'ko'}, ]
        return self.add_filter(r'jcli : ', extraCommands)

    def test_list(self):
        commands = [{'command': 'filter -l', 'expect': r'Total Filters: 0'}]
        return self._test(r'jcli : ', commands)

    def test_add_and_list(self):
        extraCommands = [{'command': 'fid filter_4'},
                         {'command': 'type TransparentFilter'}]
        self.add_filter('jcli : ', extraCommands)

        expectedList = ['#Filter id        Type                   Routes Description',
                        '#filter_4         TransparentFilter      MO MT  <T>',
                        'Total Filters: 1']
        commands = [{'command': 'filter -l', 'expect': expectedList}]
        return self._test(r'jcli : ', commands)

    def test_add_cancel_and_list(self):
        extraCommands = [{'command': 'fid filter_5'},
                         {'command': 'ko'}, ]
        self.add_filter(r'jcli : ', extraCommands)

        commands = [{'command': 'filter -l', 'expect': r'Total Filters: 0'}]
        return self._test(r'jcli : ', commands)

    def test_show(self):
        fid = 'filter_6'
        extraCommands = [{'command': 'fid %s' % fid},
                         {'command': 'type TransparentFilter'}]
        self.add_filter('jcli : ', extraCommands)

        expectedList = ['TransparentFilter']
        commands = [{'command': 'filter -s %s' % fid, 'expect': expectedList}]
        return self._test(r'jcli : ', commands)

    def test_update_not_available(self):
        fid = 'filter_7'
        extraCommands = [{'command': 'fid %s' % fid},
                         {'command': 'type TransparentFilter'}]
        self.add_filter(r'jcli : ', extraCommands)

        commands = [{'command': 'filter -u %s' % fid, 'expect': 'no such option\: -u'}]
        return self._test(r'jcli : ', commands)

    def test_remove_invalid_fid(self):
        commands = [{'command': 'filter -r invalid_fid', 'expect': r'Unknown Filter\: invalid_fid'}]
        return self._test(r'jcli : ', commands)

    def test_remove(self):
        fid = 'filter_8'
        extraCommands = [{'command': 'fid %s' % fid},
                         {'command': 'type TransparentFilter'}]
        self.add_filter(r'jcli : ', extraCommands)

        commands = [{'command': 'filter -r %s' % fid, 'expect': r'Successfully removed Filter id\:%s' % fid}]
        return self._test(r'jcli : ', commands)

    def test_remove_and_list(self):
        # Add
        fid = 'filter_9'
        extraCommands = [{'command': 'fid %s' % fid},
                         {'command': 'type TransparentFilter'}]
        self.add_filter(r'jcli : ', extraCommands)

        # List
        expectedList = ['#Filter id        Type                   Routes Description',
                        '#%s TransparentFilter      MO MT  <T>' % fid.ljust(16),
                        'Total Filters: 1']
        commands = [{'command': 'filter -l', 'expect': expectedList}]
        self._test(r'jcli : ', commands)

        # Remove
        commands = [{'command': 'filter -r %s' % fid, 'expect': r'Successfully removed Filter id\:%s' % fid}]
        self._test(r'jcli : ', commands)

        # List again
        commands = [{'command': 'filter -l', 'expect': r'Total Filters: 0'}]
        return self._test(r'jcli : ', commands)

class FilterTypingTestCases(FiltersTestCases):

    def test_available_filters(self):
        # Go to Filter adding invite
        commands = [{'command': 'filter -a'}]
        self._test(r'>', commands)

        # Send 'type' command without any arg in order to get
        # the available filters from the error string
        self.sendCommand('type')
        receivedLines = self.getBuffer(True)

        filters = []
        results = re.findall(' (\w+)Filter', receivedLines[3])
        filters.extend('%sFilter' % item for item in results[:])

        # Any new filter must be added here
        self.assertEqual(filters, ['TransparentFilter', 'UserFilter',
                                   'GroupFilter', 'ConnectorFilter',
                                   'SourceAddrFilter', 'DestinationAddrFilter',
                                   'ShortMessageFilter', 'DateIntervalFilter',
                                   'TimeIntervalFilter', 'EvalPyFilter',
                                   'TagFilter'])

        # Check if FilterTypingTestCases is covering all the filters
        for f in filters:
            self.assertIn('test_add_%s' % f, dir(self), '%s filter is not tested !' % f)

    def test_add_TransparentFilter(self):
        ftype = 'TransparentFilter'
        _str_ = '%s' % ftype
        _repr_ = '<T>'

        # Add filter
        extraCommands = [{'command': 'fid filter_id'},
                         {'command': 'type %s' % ftype}]
        self.add_filter(r'jcli : ', extraCommands)

        # Make asserts
        expectedList = ['%s' % _str_]
        self._test('jcli : ', [{'command': 'filter -s filter_id', 'expect': expectedList}])
        expectedList = ['#Filter id        Type                   Routes Description',
                        '#filter_id        %s      MO MT  %s' % (ftype, _repr_),
                        'Total Filters: 1']
        self._test('jcli : ', [{'command': 'filter -l', 'expect': expectedList}])

    def test_add_UserFilter(self):
        uid = '1'
        ftype = 'UserFilter'
        _str_ = ['%s:' % ftype, 'uid = %s' % uid]
        _repr_ = '<U \(uid=%s\)>' % (uid)

        # Add filter
        extraCommands = [{'command': 'fid filter_id'},
                         {'command': 'type %s' % ftype},
                         {'command': 'uid %s' % uid},]
        self.add_filter(r'jcli : ', extraCommands)

        # Make asserts
        expectedList = _str_
        self._test('jcli : ', [{'command': 'filter -s filter_id', 'expect': expectedList}])
        expectedList = ['#Filter id        Type                   Routes Description',
                        '#filter_id        %s             MT     %s' % (ftype, _repr_),
                        'Total Filters: 1']
        self._test('jcli : ', [{'command': 'filter -l', 'expect': expectedList}])

    def test_add_GroupFilter(self):
        gid = '1'
        ftype = 'GroupFilter'
        _str_ = ['%s:' % ftype, 'gid = %s' % gid]
        _repr_ = '<G \(gid=%s\)>' % (gid)

        # Add filter
        extraCommands = [{'command': 'fid filter_id'},
                         {'command': 'type %s' % ftype},
                         {'command': 'gid %s' % gid},]
        self.add_filter(r'jcli : ', extraCommands)

        # Make asserts
        expectedList = _str_
        self._test('jcli : ', [{'command': 'filter -s filter_id', 'expect': expectedList}])
        expectedList = ['#Filter id        Type                   Routes Description',
                        '#filter_id        %s            MT     %s' % (ftype, _repr_),
                        'Total Filters: 1']
        self._test('jcli : ', [{'command': 'filter -l', 'expect': expectedList}])

    def test_add_ConnectorFilter(self):
        cid = '1'
        ftype = 'ConnectorFilter'
        _str_ = ['%s:' % ftype, 'cid = %s' % cid]
        _repr_ = '<C \(cid=%s\)>' % (cid)

        # Add filter
        extraCommands = [{'command': 'fid filter_id'},
                         {'command': 'type %s' % ftype},
                         {'command': 'cid %s' % cid},]
        self.add_filter(r'jcli : ', extraCommands)

        # Make asserts
        expectedList = _str_
        self._test('jcli : ', [{'command': 'filter -s filter_id', 'expect': expectedList}])
        expectedList = ['#Filter id        Type                   Routes Description',
                        '#filter_id        %s        MO     %s' % (ftype, _repr_),
                        'Total Filters: 1']
        self._test('jcli : ', [{'command': 'filter -l', 'expect': expectedList}])

    def test_add_SourceAddrFilter(self):
        source_addr = '16'
        ftype = 'SourceAddrFilter'
        _str_ = ['%s:' % ftype, 'source_addr = %s' % source_addr]
        _repr_ = '<SA \(src_addr=%s\)>' % (source_addr)

        # Add filter
        extraCommands = [{'command': 'fid filter_id'},
                         {'command': 'type %s' % ftype},
                         {'command': 'source_addr %s' % source_addr},]
        self.add_filter(r'jcli : ', extraCommands)

        # Make asserts
        expectedList = _str_
        self._test('jcli : ', [{'command': 'filter -s filter_id', 'expect': expectedList}])
        expectedList = ['#Filter id        Type                   Routes Description',
                        '#filter_id        %s       MO MT  %s' % (ftype, _repr_),
                        'Total Filters: 1']
        self._test('jcli : ', [{'command': 'filter -l', 'expect': expectedList}])

    def test_add_DestinationAddrFilter(self):
        destination_addr = '16'
        ftype = 'DestinationAddrFilter'
        _str_ = ['%s:' % ftype, 'destination_addr = %s' % destination_addr]
        _repr_ = '<DA \(dst_addr=%s\)>' % (destination_addr)

        # Add filter
        extraCommands = [{'command': 'fid filter_id'},
                         {'command': 'type %s' % ftype},
                         {'command': 'destination_addr %s' % destination_addr},]
        self.add_filter(r'jcli : ', extraCommands)

        # Make asserts
        expectedList = _str_
        self._test('jcli : ', [{'command': 'filter -s filter_id', 'expect': expectedList}])
        expectedList = ['#Filter id        Type                   Routes Description',
                        '#filter_id        %s  MO MT  %s' % (ftype, _repr_),
                        'Total Filters: 1']
        self._test('jcli : ', [{'command': 'filter -l', 'expect': expectedList}])

    def test_add_ShortMessageFilter(self):
        short_message = 'Hello'
        ftype = 'ShortMessageFilter'
        _str_ = ['%s:' % ftype, 'short_message = %s' % short_message]
        _repr_ = '<SM \(msg=%s\)>' % (short_message)

        # Add filter
        extraCommands = [{'command': 'fid filter_id'},
                         {'command': 'type %s' % ftype},
                         {'command': 'short_message %s' % short_message},]
        self.add_filter(r'jcli : ', extraCommands)

        # Make asserts
        expectedList = _str_
        self._test('jcli : ', [{'command': 'filter -s filter_id', 'expect': expectedList}])
        expectedList = ['#Filter id        Type                   Routes Description',
                        '#filter_id        %s     MO MT  %s' % (ftype, _repr_),
                        'Total Filters: 1']
        self._test('jcli : ', [{'command': 'filter -l', 'expect': expectedList}])

    def test_add_DateIntervalFilter(self):
        leftBorder = '2016-05-01'
        rightBorder = '2016-05-02'
        dateInterval = '%s;%s' % (leftBorder, rightBorder)
        ftype = 'DateIntervalFilter'
        _str_ = ['%s:' % ftype, 'Left border = %s' % leftBorder, 'Right border = %s' % rightBorder]
        _repr_ = '<DI \(%s,%s\)>' % (leftBorder, rightBorder)

        # Add filter
        extraCommands = [{'command': 'fid filter_id'},
                         {'command': 'type %s' % ftype},
                         {'command': 'dateInterval %s' % dateInterval},]
        self.add_filter(r'jcli : ', extraCommands)

        # Make asserts
        expectedList = _str_
        self._test('jcli : ', [{'command': 'filter -s filter_id', 'expect': expectedList}])
        expectedList = ['#Filter id        Type                   Routes Description',
                        '#filter_id        %s     MO MT  %s' % (ftype, _repr_),
                        'Total Filters: 1']
        self._test('jcli : ', [{'command': 'filter -l', 'expect': expectedList}])

    def test_add_TimeIntervalFilter(self):
        leftBorder = '08:00:00'
        rightBorder = '14:00:00'
        timeInterval = '%s;%s' % (leftBorder, rightBorder)
        ftype = 'TimeIntervalFilter'
        _str_ = ['%s:' % ftype, 'Left border = %s' % leftBorder, 'Right border = %s' % rightBorder]
        _repr_ = '<TI \(%s,%s\)>' % (leftBorder, rightBorder)

        # Add filter
        extraCommands = [{'command': 'fid filter_id'},
                         {'command': 'type %s' % ftype},
                         {'command': 'timeInterval %s' % timeInterval},]
        self.add_filter(r'jcli : ', extraCommands)

        # Make asserts
        expectedList = _str_
        self._test('jcli : ', [{'command': 'filter -s filter_id', 'expect': expectedList}])
        expectedList = ['#Filter id        Type                   Routes Description',
                        '#filter_id        %s     MO MT  %s' % (ftype, _repr_),
                        'Total Filters: 1']
        self._test('jcli : ', [{'command': 'filter -l', 'expect': expectedList}])

    def test_add_EvalPyFilter(self):
        pyCodeFile = 'pyCode.py'
        # Create an empty pyCode file
        open(pyCodeFile, 'w')

        ftype = 'EvalPyFilter'
        _str_ = ['%s:' % ftype, '']
        _repr_ = '<Ev \(pyCode= ..\)>'

        # Add filter
        extraCommands = [{'command': 'fid filter_id'},
                         {'command': 'type %s' % ftype},
                         {'command': 'pyCode %s' % pyCodeFile},]
        self.add_filter(r'jcli : ', extraCommands)

        # Make asserts
        expectedList = _str_
        self._test('jcli : ', [{'command': 'filter -s filter_id', 'expect': expectedList}])
        expectedList = ['#Filter id        Type                   Routes Description',
                        '#filter_id        %s           MO MT  %s' % (ftype, _repr_),
                        'Total Filters: 1']
        self._test('jcli : ', [{'command': 'filter -l', 'expect': expectedList}])

        # Delete pyCode file
        unlink(pyCodeFile)

    def test_add_EvalPyFilter_withCode(self):
        pyCodeFile = 'pyCode.py'
        pyCode = """# Will filter all messages having 'hello world' in their content
if routable.pdu.params['short_message'] == 'hello world':
    result = True
else:
    result = False
"""

        # Create an pyCode file
        with open(pyCodeFile, 'w') as f:
            f.write(pyCode)

        ftype = 'EvalPyFilter'
        _str_ = ['%s:' % ftype]
        _str_.extend([y for y in (re.escape(x.strip()) for x in pyCode.splitlines()) if y])
        _repr_ = '<Ev \(pyCode=%s ..\)>' % (pyCode[:10].replace('\n', ''))

        # Add filter
        extraCommands = [{'command': 'fid filter_id'},
                         {'command': 'type %s' % ftype},
                         {'command': 'pyCode %s' % pyCodeFile},]
        self.add_filter(r'jcli : ', extraCommands)

        # Make asserts
        expectedList = _str_
        self._test('jcli : ', [{'command': 'filter -s filter_id', 'expect': expectedList}])
        expectedList = ['#Filter id        Type                   Routes Description',
                        '#filter_id        %s           MO MT  %s' % (ftype, _repr_),
                        'Total Filters: 1']
        self._test('jcli : ', [{'command': 'filter -l', 'expect': expectedList}])

        # Delete pyCode file
        unlink(pyCodeFile)

    def test_add_TagFilter(self):
        tag = 11
        ftype = 'TagFilter'
        _str_ = ['%s:' % ftype, 'has tag = %s' % tag]
        _repr_ = '<TG \(tag=%s\)>' % (tag)

        # Add filter
        extraCommands = [{'command': 'fid filter_id'},
                         {'command': 'type %s' % ftype},
                         {'command': 'tag %s' % tag},]
        self.add_filter(r'jcli : ', extraCommands)

        # Make asserts
        expectedList = _str_
        self._test('jcli : ', [{'command': 'filter -s filter_id', 'expect': expectedList}])
        expectedList = ['#Filter id        Type                   Routes Description',
                        '#filter_id        %s              MO MT  %s' % (ftype, _repr_),
                        'Total Filters: 1']
        self._test('jcli : ', [{'command': 'filter -l', 'expect': expectedList}])

class FilterPersistenceTestCases(FiltersTestCases):

    def tearDown(self):
        FiltersTestCases.tearDown(self)

        # Delete any previously persisted configuration
        persistenceFolder = self.RouterPBConfigInstance.store_path
        for the_file in os.listdir(persistenceFolder):
            if the_file == '.gitignore':
                # Dont delete any hidden file
                continue
            file_path = os.path.join(persistenceFolder, the_file)
            if os.path.isfile(file_path):
                os.unlink(file_path)

    def test_TransparentFilter(self):
        ftype = 'TransparentFilter'
        _str_ = '%s' % ftype
        _repr_ = '<T>'

        # Add filter
        extraCommands = [{'command': 'fid filter_id'},
                         {'command': 'type %s' % ftype}]
        self.add_filter(r'jcli : ', extraCommands)

        # Persist & load
        self._test('jcli : ', [{'command': 'persist'},
                               {'command': 'filter -r filter_id'},
                               {'command': 'load'}])

        # Make asserts
        expectedList = ['%s' % _str_]
        self._test('jcli : ', [{'command': 'filter -s filter_id', 'expect': expectedList}])
        expectedList = ['#Filter id        Type                   Routes Description',
                        '#filter_id        %s      MO MT  %s' % (ftype, _repr_),
                        'Total Filters: 1']
        self._test('jcli : ', [{'command': 'filter -l', 'expect': expectedList}])

    def test_UserFilter(self):
        uid = '1'
        ftype = 'UserFilter'
        _str_ = ['%s:' % ftype, 'uid = %s' % uid]
        _repr_ = '<U \(uid=%s\)>' % (uid)

        # Add filter
        extraCommands = [{'command': 'fid filter_id'},
                         {'command': 'type %s' % ftype},
                         {'command': 'uid %s' % uid},]
        self.add_filter(r'jcli : ', extraCommands)

        # Persist & load
        self._test('jcli : ', [{'command': 'persist'},
                               {'command': 'filter -r filter_id'},
                               {'command': 'load'}])

        # Make asserts
        expectedList = _str_
        self._test('jcli : ', [{'command': 'filter -s filter_id', 'expect': expectedList}])
        expectedList = ['#Filter id        Type                   Routes Description',
                        '#filter_id        %s             MT     %s' % (ftype, _repr_),
                        'Total Filters: 1']
        self._test('jcli : ', [{'command': 'filter -l', 'expect': expectedList}])

    def test_GroupFilter(self):
        gid = '1'
        ftype = 'GroupFilter'
        _str_ = ['%s:' % ftype, 'gid = %s' % gid]
        _repr_ = '<G \(gid=%s\)>' % (gid)

        # Add filter
        extraCommands = [{'command': 'fid filter_id'},
                         {'command': 'type %s' % ftype},
                         {'command': 'gid %s' % gid},]
        self.add_filter(r'jcli : ', extraCommands)

        # Persist & load
        self._test('jcli : ', [{'command': 'persist'},
                               {'command': 'filter -r filter_id'},
                               {'command': 'load'}])

        # Make asserts
        expectedList = _str_
        self._test('jcli : ', [{'command': 'filter -s filter_id', 'expect': expectedList}])
        expectedList = ['#Filter id        Type                   Routes Description',
                        '#filter_id        %s            MT     %s' % (ftype, _repr_),
                        'Total Filters: 1']
        self._test('jcli : ', [{'command': 'filter -l', 'expect': expectedList}])

    def test_ConnectorFilter(self):
        cid = '1'
        ftype = 'ConnectorFilter'
        _str_ = ['%s:' % ftype, 'cid = %s' % cid]
        _repr_ = '<C \(cid=%s\)>' % (cid)

        # Add filter
        extraCommands = [{'command': 'fid filter_id'},
                         {'command': 'type %s' % ftype},
                         {'command': 'cid %s' % cid},]
        self.add_filter(r'jcli : ', extraCommands)

        # Persist & load
        self._test('jcli : ', [{'command': 'persist'},
                               {'command': 'filter -r filter_id'},
                               {'command': 'load'}])

        # Make asserts
        expectedList = _str_
        self._test('jcli : ', [{'command': 'filter -s filter_id', 'expect': expectedList}])
        expectedList = ['#Filter id        Type                   Routes Description',
                        '#filter_id        %s        MO     %s' % (ftype, _repr_),
                        'Total Filters: 1']
        self._test('jcli : ', [{'command': 'filter -l', 'expect': expectedList}])

    def test_SourceAddrFilter(self):
        source_addr = '16'
        ftype = 'SourceAddrFilter'
        _str_ = ['%s:' % ftype, 'source_addr = %s' % source_addr]
        _repr_ = '<SA \(src_addr=%s\)>' % (source_addr)

        # Add filter
        extraCommands = [{'command': 'fid filter_id'},
                         {'command': 'type %s' % ftype},
                         {'command': 'source_addr %s' % source_addr},]
        self.add_filter(r'jcli : ', extraCommands)

        # Persist & load
        self._test('jcli : ', [{'command': 'persist'},
                               {'command': 'filter -r filter_id'},
                               {'command': 'load'}])

        # Make asserts
        expectedList = _str_
        self._test('jcli : ', [{'command': 'filter -s filter_id', 'expect': expectedList}])
        expectedList = ['#Filter id        Type                   Routes Description',
                        '#filter_id        %s       MO MT  %s' % (ftype, _repr_),
                        'Total Filters: 1']
        self._test('jcli : ', [{'command': 'filter -l', 'expect': expectedList}])

    def test_DestinationAddrFilter(self):
        destination_addr = '16'
        ftype = 'DestinationAddrFilter'
        _str_ = ['%s:' % ftype, 'destination_addr = %s' % destination_addr]
        _repr_ = '<DA \(dst_addr=%s\)>' % (destination_addr)

        # Add filter
        extraCommands = [{'command': 'fid filter_id'},
                         {'command': 'type %s' % ftype},
                         {'command': 'destination_addr %s' % destination_addr},]
        self.add_filter(r'jcli : ', extraCommands)

        # Persist & load
        self._test('jcli : ', [{'command': 'persist'},
                               {'command': 'filter -r filter_id'},
                               {'command': 'load'}])

        # Make asserts
        expectedList = _str_
        self._test('jcli : ', [{'command': 'filter -s filter_id', 'expect': expectedList}])
        expectedList = ['#Filter id        Type                   Routes Description',
                        '#filter_id        %s  MO MT  %s' % (ftype, _repr_),
                        'Total Filters: 1']
        self._test('jcli : ', [{'command': 'filter -l', 'expect': expectedList}])

    def test_ShortMessageFilter(self):
        short_message = 'Hello'
        ftype = 'ShortMessageFilter'
        _str_ = ['%s:' % ftype, 'short_message = %s' % short_message]
        _repr_ = '<SM \(msg=%s\)>' % (short_message)

        # Add filter
        extraCommands = [{'command': 'fid filter_id'},
                         {'command': 'type %s' % ftype},
                         {'command': 'short_message %s' % short_message},]
        self.add_filter(r'jcli : ', extraCommands)

        # Persist & load
        self._test('jcli : ', [{'command': 'persist'},
                               {'command': 'filter -r filter_id'},
                               {'command': 'load'}])

        # Make asserts
        expectedList = _str_
        self._test('jcli : ', [{'command': 'filter -s filter_id', 'expect': expectedList}])
        expectedList = ['#Filter id        Type                   Routes Description',
                        '#filter_id        %s     MO MT  %s' % (ftype, _repr_),
                        'Total Filters: 1']
        self._test('jcli : ', [{'command': 'filter -l', 'expect': expectedList}])

    def test_DateIntervalFilter(self):
        leftBorder = '2016-05-01'
        rightBorder = '2016-05-02'
        dateInterval = '%s;%s' % (leftBorder, rightBorder)
        ftype = 'DateIntervalFilter'
        _str_ = ['%s:' % ftype, 'Left border = %s' % leftBorder, 'Right border = %s' % rightBorder]
        _repr_ = '<DI \(%s,%s\)>' % (leftBorder, rightBorder)

        # Add filter
        extraCommands = [{'command': 'fid filter_id'},
                         {'command': 'type %s' % ftype},
                         {'command': 'dateInterval %s' % dateInterval},]
        self.add_filter(r'jcli : ', extraCommands)

        # Persist & load
        self._test('jcli : ', [{'command': 'persist'},
                               {'command': 'filter -r filter_id'},
                               {'command': 'load'}])

        # Make asserts
        expectedList = _str_
        self._test('jcli : ', [{'command': 'filter -s filter_id', 'expect': expectedList}])
        expectedList = ['#Filter id        Type                   Routes Description',
                        '#filter_id        %s     MO MT  %s' % (ftype, _repr_),
                        'Total Filters: 1']
        self._test('jcli : ', [{'command': 'filter -l', 'expect': expectedList}])

    def test_TimeIntervalFilter(self):
        leftBorder = '08:00:00'
        rightBorder = '14:00:00'
        timeInterval = '%s;%s' % (leftBorder, rightBorder)
        ftype = 'TimeIntervalFilter'
        _str_ = ['%s:' % ftype, 'Left border = %s' % leftBorder, 'Right border = %s' % rightBorder]
        _repr_ = '<TI \(%s,%s\)>' % (leftBorder, rightBorder)

        # Add filter
        extraCommands = [{'command': 'fid filter_id'},
                         {'command': 'type %s' % ftype},
                         {'command': 'timeInterval %s' % timeInterval},]
        self.add_filter(r'jcli : ', extraCommands)

        # Persist & load
        self._test('jcli : ', [{'command': 'persist'},
                               {'command': 'filter -r filter_id'},
                               {'command': 'load'}])

        # Make asserts
        expectedList = _str_
        self._test('jcli : ', [{'command': 'filter -s filter_id', 'expect': expectedList}])
        expectedList = ['#Filter id        Type                   Routes Description',
                        '#filter_id        %s     MO MT  %s' % (ftype, _repr_),
                        'Total Filters: 1']
        self._test('jcli : ', [{'command': 'filter -l', 'expect': expectedList}])

    def test_EvalPyFilter(self):
        pyCodeFile = 'pyCode.py'
        # Create an empty pyCode file
        open(pyCodeFile, 'w')

        ftype = 'EvalPyFilter'
        _str_ = ['%s:' % ftype, '']
        _repr_ = '<Ev \(pyCode= ..\)>'

        # Add filter
        extraCommands = [{'command': 'fid filter_id'},
                         {'command': 'type %s' % ftype},
                         {'command': 'pyCode %s' % pyCodeFile},]
        self.add_filter(r'jcli : ', extraCommands)

        # Persist & load
        self._test('jcli : ', [{'command': 'persist'},
                               {'command': 'filter -r filter_id'},
                               {'command': 'load'}])

        # Make asserts
        expectedList = _str_
        self._test('jcli : ', [{'command': 'filter -s filter_id', 'expect': expectedList}])
        expectedList = ['#Filter id        Type                   Routes Description',
                        '#filter_id        %s           MO MT  %s' % (ftype, _repr_),
                        'Total Filters: 1']
        self._test('jcli : ', [{'command': 'filter -l', 'expect': expectedList}])

        # Delete pyCode file
        unlink(pyCodeFile)

    def test_EvalPyFilter_withCode(self):
        pyCodeFile = 'pyCode.py'
        pyCode = """# Will filter all messages having 'hello world' in their content
if routable.pdu.params['short_message'] == 'hello world':
    result = True
else:
    result = False
"""

        # Create an pyCode file
        with open(pyCodeFile, 'w') as f:
            f.write(pyCode)

        ftype = 'EvalPyFilter'
        _str_ = ['%s:' % ftype]
        _str_.extend([y for y in (re.escape(x.strip()) for x in pyCode.splitlines()) if y])
        _repr_ = '<Ev \(pyCode=%s ..\)>' % (pyCode[:10].replace('\n', ''))

        # Add filter
        extraCommands = [{'command': 'fid filter_id'},
                         {'command': 'type %s' % ftype},
                         {'command': 'pyCode %s' % pyCodeFile},]
        self.add_filter(r'jcli : ', extraCommands)

        # Persist & load
        self._test('jcli : ', [{'command': 'persist'},
                               {'command': 'filter -r filter_id'},
                               {'command': 'load'}])

        # Make asserts
        expectedList = _str_
        self._test('jcli : ', [{'command': 'filter -s filter_id', 'expect': expectedList}])
        expectedList = ['#Filter id        Type                   Routes Description',
                        '#filter_id        %s           MO MT  %s' % (ftype, _repr_),
                        'Total Filters: 1']
        self._test('jcli : ', [{'command': 'filter -l', 'expect': expectedList}])

        # Delete pyCode file
        unlink(pyCodeFile)

    def test_TagFilter(self):
        tag = 22
        ftype = 'TagFilter'
        _str_ = ['%s:' % ftype, 'has tag = %s' % tag]
        _repr_ = '<TG \(tag=%s\)>' % (tag)

        # Add filter
        extraCommands = [{'command': 'fid filter_id'},
                         {'command': 'type %s' % ftype},
                         {'command': 'tag %s' % tag},]
        self.add_filter(r'jcli : ', extraCommands)

        # Persist & load
        self._test('jcli : ', [{'command': 'persist'},
                               {'command': 'filter -r filter_id'},
                               {'command': 'load'}])

        # Make asserts
        expectedList = _str_
        self._test('jcli : ', [{'command': 'filter -s filter_id', 'expect': expectedList}])
        expectedList = ['#Filter id        Type                   Routes Description',
                        '#filter_id        %s              MO MT  %s' % (ftype, _repr_),
                        'Total Filters: 1']
        self._test('jcli : ', [{'command': 'filter -l', 'expect': expectedList}])
