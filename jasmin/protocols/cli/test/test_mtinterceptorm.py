import re
from twisted.internet import defer
from test_mxinterceptorm import MxInterceptorTestCases

class BasicTestCases(MxInterceptorTestCases):

    @defer.inlineCallbacks
    def test_add_with_minimum_arg(self):
        extraCommands = [{'command': 'order 0'},
                         {'command': 'type DefaultInterceptor'},
                         {'command': 'script python2(%s)' % self.valid_script}]
        yield self.add_mtinterceptor(r'jcli : ', extraCommands)

    @defer.inlineCallbacks
    def test_add_without_minimum_args(self):
        extraCommands = [{'command': 'ok', 'expect': r'You must set these options before saving: type, order'}]
        yield self.add_mtinterceptor(r'> ', extraCommands)

    @defer.inlineCallbacks
    def test_add_invalid_key(self):
        extraCommands = [{'command': 'order 0'},
                         {'command': 'type DefaultInterceptor'},
                         {'command': 'script python2(%s)' % self.valid_script},
                         {'command': 'anykey anyvalue', 'expect': r'Unknown Interceptor key: anykey'}]
        yield self.add_mtinterceptor(r'jcli : ', extraCommands)

    @defer.inlineCallbacks
    def test_cancel_add(self):
        extraCommands = [{'command': 'order 1'},
                         {'command': 'ko'}]
        yield self.add_mtinterceptor(r'jcli : ', extraCommands)

    @defer.inlineCallbacks
    def test_list(self):
        commands = [{'command': 'mtinterceptor -l', 'expect': r'Total MT Interceptors: 0'}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_add_and_list(self):
        extraCommands = [{'command': 'order 0'},
                         {'command': 'type DefaultInterceptor'},
                         {'command': 'script python2(%s)' % self.valid_script}]
        yield self.add_mtinterceptor('jcli : ', extraCommands)

        expectedList = ['#Order Type                 Script                                          Filter\(s\)',
                        '#0     DefaultInterceptor   <MTIS \(pyCode=print "hello  world" ..\)>',
                        'Total MT Interceptors: 1']
        commands = [{'command': 'mtinterceptor -l', 'expect': expectedList}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_add_cancel_and_list(self):
        extraCommands = [{'command': 'type defaultinterceptor'},
                         {'command': 'ko'}, ]
        yield self.add_mtinterceptor(r'jcli : ', extraCommands)

        commands = [{'command': 'mtinterceptor -l', 'expect': r'Total MT Interceptors: 0'}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_show(self):
        order = '0'
        extraCommands = [{'command': 'order %s' % order},
                         {'command': 'type DefaultInterceptor'},
                         {'command': 'script python2(%s)' % self.valid_script}]
        yield self.add_mtinterceptor('jcli : ', extraCommands)

        commands = [{'command': 'mtinterceptor -s %s' % order,
                     'expect': 'DefaultInterceptor/<MTIS \(pyCode=print "hello  world" ..\)>'},
                    ]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_update_not_available(self):
        order = '0'
        extraCommands = [{'command': 'order %s' % order},
                         {'command': 'type DefaultInterceptor'},
                         {'command': 'script python2(%s)' % self.valid_script}]
        yield self.add_mtinterceptor('jcli : ', extraCommands)

        commands = [{'command': 'mtinterceptor -u %s' % order, 'expect': 'no such option\: -u'}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_remove_invalid_order(self):
        commands = [{'command': 'mtinterceptor -r invalid_cid', 'expect': r'MT Interceptor order must be a positive integer'}]
        yield self._test(r'jcli : ', commands)

        commands = [{'command': 'mtinterceptor -r 66', 'expect': r'Unknown MT Interceptor: 66'}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_remove(self):
        order = '0'
        extraCommands = [{'command': 'order %s' % order},
                         {'command': 'type DefaultInterceptor'},
                         {'command': 'script python2(%s)' % self.valid_script}]
        yield self.add_mtinterceptor('jcli : ', extraCommands)

        commands = [{'command': 'mtinterceptor -r %s' % order, 'expect': r'Successfully removed MT Interceptor with order\:%s' % order}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_flush(self):
        # Add 2 Interceptors:
        extraCommands = [{'command': 'order 0'},
                         {'command': 'type DefaultInterceptor'},
                         {'command': 'script python2(%s)' % self.valid_script}]
        yield self.add_mtinterceptor('jcli : ', extraCommands)

        extraCommands = [{'command': 'order 20'},
                         {'command': 'type StaticMTInterceptor'},
                         {'command': 'script python2(%s)' % self.valid_script},
                         {'command': 'filters f1'}]
        yield self.add_mtinterceptor('jcli : ', extraCommands)

        # List
        expectedList = ['#Order Type                 Script                                          Filter\(s\)',
                        '#20    StaticMTInterceptor  <MTIS \(pyCode=print "hello  world" ..\)>         <T>',
                        '#0     DefaultInterceptor   <MTIS \(pyCode=print "hello  world" ..\)>',
                        'Total MT Interceptors: 2']
        commands = [{'command': 'mtinterceptor -l', 'expect': expectedList}]
        yield self._test(r'jcli : ', commands)

        # Flush
        commands = [{'command': 'mtinterceptor -f', 'expect': 'Successfully flushed MT Interceptor table \(2 flushed entries\)'}]
        yield self._test(r'jcli : ', commands)

        # Relist
        commands = [{'command': 'mtinterceptor -l', 'expect': r'Total MT Interceptors: 0'}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_remove_and_list(self):
        # Add
        order = '0'
        extraCommands = [{'command': 'order %s' % order},
                         {'command': 'type DefaultInterceptor'},
                         {'command': 'script python2(%s)' % self.valid_script}]
        yield self.add_mtinterceptor('jcli : ', extraCommands)

        expectedList = ['#Order Type                 Script                                          Filter\(s\)',
                        '#0     DefaultInterceptor   <MTIS \(pyCode=print "hello  world" ..\)>',
                        'Total MT Interceptors: 1']
        commands = [{'command': 'mtinterceptor -l', 'expect': expectedList}]
        yield self._test(r'jcli : ', commands)

        # Remove
        commands = [{'command': 'mtinterceptor -r %s' % order, 'expect': r'Successfully removed MT Interceptor with order\:%s' % order}]
        yield self._test(r'jcli : ', commands)

        # List again
        commands = [{'command': 'mtinterceptor -l', 'expect': r'Total MT Interceptors: 0'}]
        yield self._test(r'jcli : ', commands)

class MtInterceptorTypingTestCases(MxInterceptorTestCases):

    @defer.inlineCallbacks
    def test_available_mtinterceptors(self):
        # Go to MTInterceptor adding invite
        commands = [{'command': 'mtinterceptor -a'}]
        yield self._test(r'>', commands)

        # Send 'type' command without any arg in order to get
        # the available mtinterceptors from the error string
        yield self.sendCommand('type')
        receivedLines = self.getBuffer(True)

        filters = []
        results = re.findall(' (\w+)Interceptor', receivedLines[3])
        for item in results[:]:
            filters.append('%sInterceptor' % item)

        # Any new filter must be added here
        self.assertEqual(filters, ['DefaultInterceptor',
            'StaticMTInterceptor'
        ])

        # Check if MtInterceptorTypingTestCases is covering all the mtinterceptors
        for f in filters:
            self.assertIn('test_add_%s' % f, dir(self), '%s mtinterceptor is not tested !' % f)

    @defer.inlineCallbacks
    def test_add_DefaultInterceptor(self):
        iorder = '0'
        itype = 'DefaultInterceptor'
        script = self.valid_script
        typed_script = 'python2(%s)' % script
        _str_ = '%s/<MTIS \(pyCode=print "hello  world" ..\)>' % (itype)

        # Add MTInterceptor
        extraCommands = [{'command': 'order %s' % iorder},
                         {'command': 'type %s' % itype},
                         {'command': 'script %s' % typed_script}]
        yield self.add_mtinterceptor('jcli : ', extraCommands)

        # Make asserts
        expectedList = ['%s' % _str_]
        yield self._test('jcli : ', [{'command': 'mtinterceptor -s %s' % iorder, 'expect': expectedList}])
        expectedList = ['#Order Type                 Script                                          Filter\(s\)',
                        '#%s %s %s ' % (iorder.ljust(5), itype.ljust(20), '<MTIS \(pyCode=print "hello  world" ..\)>'.ljust(47)),
                        'Total MT Interceptors: 1']
        commands = [{'command': 'mtinterceptor -l', 'expect': expectedList}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_add_StaticMTInterceptor(self):
        iorder = '10'
        itype = 'StaticMTInterceptor'
        script = self.valid_script
        typed_script = 'python2(%s)' % script
        _str_ = '%s/<MTIS \(pyCode=print "hello  world" ..\)>' % (itype)

        # Add MTInterceptor
        extraCommands = [{'command': 'order %s' % iorder},
                         {'command': 'type %s' % itype},
                         {'command': 'filters f1'},
                         {'command': 'script %s' % typed_script}]
        yield self.add_mtinterceptor('jcli : ', extraCommands)

        # Make asserts
        expectedList = ['%s' % _str_]
        yield self._test('jcli : ', [{'command': 'mtinterceptor -s %s' % iorder, 'expect': expectedList}])
        expectedList = ['#Order Type                 Script                                          Filter\(s\)',
                        '#%s %s %s ' % (iorder.ljust(5), itype.ljust(20), '<MTIS \(pyCode=print "hello  world" ..\)>'.ljust(47)),
                        'Total MT Interceptors: 1']
        commands = [{'command': 'mtinterceptor -l', 'expect': expectedList}]
        yield self._test(r'jcli : ', commands)

class MtInterceptorArgsTestCases(MxInterceptorTestCases):

    @defer.inlineCallbacks
    def test_add_defaultinterceptor_with_nonzero_order(self):
        """The interceptor order will be forced to 0 when interceptor type is set to
        DefaultInterceptor just after indicating the order
        """
        extraCommands = [{'command': 'order 20'},
                         {'command': 'type DefaultInterceptor'},
                         {'command': 'script python2(%s)' % self.valid_script}]
        yield self.add_mtinterceptor(r'jcli : ', extraCommands)

        expectedList = ['#Order Type                 Script                                          Filter\(s\)',
                        '#0     DefaultInterceptor   <MTIS \(pyCode=print "hello  world" ..\)>',
                        'Total MT Interceptors: 1']
        commands = [{'command': 'mtinterceptor -l', 'expect': expectedList}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_add_defaultinterceptor_without_indicating_order(self):
        """The interceptor order will be set to 0 when interceptor type is set to
        DefaultInterceptor without the need to indicate the order
        """
        extraCommands = [{'command': 'type DefaultInterceptor'},
                         {'command': 'script python2(%s)' % self.valid_script}]
        yield self.add_mtinterceptor(r'jcli : ', extraCommands)

        expectedList = ['#Order Type                 Script                                          Filter\(s\)',
                        '#0     DefaultInterceptor   <MTIS \(pyCode=print "hello  world" ..\)>',
                        'Total MT Interceptors: 1']
        commands = [{'command': 'mtinterceptor -l', 'expect': expectedList}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_add_nondefaultinterceptor_with_zero_order(self):
        """The only interceptor that can have a Zero as it's order is the DefaultInterceptor
        """
        commands = [{'command': 'mtinterceptor -a'},
                    {'command': 'order 0'},
                    {'command': 'type StaticMTInterceptor'},
                    {'command': 'script python2(%s)' % self.valid_script},
                    {'command': 'filters f1'},
                    {'command': 'ok', 'expect': 'Failed adding MTInterceptor, check log for details'}]
        yield self._test(r'> ', commands)

    @defer.inlineCallbacks
    def test_add_invalid_script(self):
        commands = [{'command': 'mtinterceptor -a'},
                    {'command': 'type DefaultInterceptor'},
                    {'command': 'script python2(%s)' % self.invalid_syntax, 'expect': '\[Syntax\]: invalid syntax \(, line 1\)'},
                    {'command': 'ok', 'expect': 'You must set these options before saving: type, order, script'}]
        yield self._test(r'> ', commands)

    @defer.inlineCallbacks
    def test_add_unknown_script(self):
        commands = [{'command': 'mtinterceptor -a'},
                    {'command': 'type DefaultInterceptor'},
                    {'command': 'script python2(%s)' % self.file_not_found, 'expect': '\[IO\]: \[Errno 2\] No such file or directory: \'\/file\/not\/found\''},
                    {'command': 'ok', 'expect': 'You must set these options before saving: type, order, script'}]
        yield self._test(r'> ', commands)

    @defer.inlineCallbacks
    def test_add_invalid_script_interpreter(self):
        commands = [{'command': 'mtinterceptor -a'},
                    {'command': 'type DefaultInterceptor'},
                    {'command': 'script php(%s)' % self.valid_script, 'expect': 'Invalid syntax for script, must be python2\(\/path\/to\/script\).'},
                    {'command': 'ok', 'expect': 'You must set these options before saving: type, order, script'}]
        yield self._test(r'> ', commands)

    @defer.inlineCallbacks
    def test_add_incompatible_filter(self):
        commands = [{'command': 'mtinterceptor -a'},
                    {'command': 'order 20'},
                    {'command': 'type StaticMTInterceptor'},
                    {'command': 'script python2(%s)' % self.valid_script},
                    {'command': 'filters cf1', 'expect': 'ConnectorFilter#cf1 is not a valid filter for MTInterceptor'},
                    {'command': 'ok', 'expect': 'You must set these options before saving: type, order, filters, script'}]
        yield self._test(r'> ', commands)

    @defer.inlineCallbacks
    def test_list_with_filter(self):
        extraCommands = [{'command': 'order 20'},
                         {'command': 'type StaticMTInterceptor'},
                         {'command': 'script python2(%s)' % self.valid_script},
                         {'command': 'filters uf1'}]
        yield self.add_mtinterceptor(r'jcli : ', extraCommands)

        expectedList = ['#Order Type                 Script                                          Filter\(s\)',
                        '#20    StaticMTInterceptor  <MTIS \(pyCode=print "hello  world" ..\)>         <U \(uid=Any\)>',
                        'Total MT Interceptors: 1']
        commands = [{'command': 'mtinterceptor -l', 'expect': expectedList}]
        yield self._test(r'jcli : ', commands)
