import re
from twisted.internet import defer
from test_mxinterceptorm import MxInterceptorTestCases

class BasicTestCases(MxInterceptorTestCases):

    @defer.inlineCallbacks
    def test_add_with_minimum_arg(self):
        extraCommands = [{'command': 'order 0'},
                         {'command': 'type DefaultInterceptor'},
                         {'command': 'script python2(%s)' % self.valid_script}]
        yield self.add_mointerceptor(r'jcli : ', extraCommands)

    @defer.inlineCallbacks
    def test_add_without_minimum_args(self):
        extraCommands = [{'command': 'ok', 'expect': r'You must set these options before saving: type, order'}]
        yield self.add_mointerceptor(r'> ', extraCommands)

    @defer.inlineCallbacks
    def test_add_invalid_key(self):
        extraCommands = [{'command': 'order 0'},
                         {'command': 'type DefaultInterceptor'},
                         {'command': 'script python2(%s)' % self.valid_script},
                         {'command': 'anykey anyvalue', 'expect': r'Unknown Interceptor key: anykey'}]
        yield self.add_mointerceptor(r'jcli : ', extraCommands)

    @defer.inlineCallbacks
    def test_cancel_add(self):
        extraCommands = [{'command': 'order 1'},
                         {'command': 'ko'}]
        yield self.add_mointerceptor(r'jcli : ', extraCommands)

    @defer.inlineCallbacks
    def test_list(self):
        commands = [{'command': 'mointerceptor -l', 'expect': r'Total MO Interceptors: 0'}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_add_and_list(self):
        extraCommands = [{'command': 'order 0'},
                         {'command': 'type DefaultInterceptor'},
                         {'command': 'script python2(%s)' % self.valid_script}]
        yield self.add_mointerceptor('jcli : ', extraCommands)

        expectedList = ['#Order Type                 Script                                          Filter\(s\)',
                        '#0     DefaultInterceptor   <MOIS \(pyCode=print "hello  world" ..\)>',
                        'Total MO Interceptors: 1']
        commands = [{'command': 'mointerceptor -l', 'expect': expectedList}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_add_cancel_and_list(self):
        extraCommands = [{'command': 'type defaultinterceptor'},
                         {'command': 'ko'}, ]
        yield self.add_mointerceptor(r'jcli : ', extraCommands)

        commands = [{'command': 'mointerceptor -l', 'expect': r'Total MO Interceptors: 0'}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_show(self):
        order = '0'
        extraCommands = [{'command': 'order %s' % order},
                         {'command': 'type DefaultInterceptor'},
                         {'command': 'script python2(%s)' % self.valid_script}]
        yield self.add_mointerceptor('jcli : ', extraCommands)

        commands = [{'command': 'mointerceptor -s %s' % order,
                     'expect': 'DefaultInterceptor/<MOIS \(pyCode=print "hello  world" ..\)>'},
                    ]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_update_not_available(self):
        order = '0'
        extraCommands = [{'command': 'order %s' % order},
                         {'command': 'type DefaultInterceptor'},
                         {'command': 'script python2(%s)' % self.valid_script}]
        yield self.add_mointerceptor('jcli : ', extraCommands)

        commands = [{'command': 'mointerceptor -u %s' % order, 'expect': 'no such option\: -u'}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_remove_invalid_order(self):
        commands = [{'command': 'mointerceptor -r invalid_cid', 'expect': r'MO Interceptor order must be a positive integer'}]
        yield self._test(r'jcli : ', commands)

        commands = [{'command': 'mointerceptor -r 66', 'expect': r'Unknown MO Interceptor: 66'}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_remove(self):
        order = '0'
        extraCommands = [{'command': 'order %s' % order},
                         {'command': 'type DefaultInterceptor'},
                         {'command': 'script python2(%s)' % self.valid_script}]
        yield self.add_mointerceptor('jcli : ', extraCommands)

        commands = [{'command': 'mointerceptor -r %s' % order, 'expect': r'Successfully removed MO Interceptor with order\:%s' % order}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_flush(self):
        # Add 2 Interceptors:
        extraCommands = [{'command': 'order 0'},
                         {'command': 'type DefaultInterceptor'},
                         {'command': 'script python2(%s)' % self.valid_script}]
        yield self.add_mointerceptor('jcli : ', extraCommands)

        extraCommands = [{'command': 'order 20'},
                         {'command': 'type StaticMOInterceptor'},
                         {'command': 'script python2(%s)' % self.valid_script},
                         {'command': 'filters f1'}]
        yield self.add_mointerceptor('jcli : ', extraCommands)

        # List
        expectedList = ['#Order Type                 Script                                          Filter\(s\)',
                        '#20    StaticMOInterceptor  <MOIS \(pyCode=print "hello  world" ..\)>         <T>',
                        '#0     DefaultInterceptor   <MOIS \(pyCode=print "hello  world" ..\)>',
                        'Total MO Interceptors: 2']
        commands = [{'command': 'mointerceptor -l', 'expect': expectedList}]
        yield self._test(r'jcli : ', commands)

        # Flush
        commands = [{'command': 'mointerceptor -f', 'expect': 'Successfully flushed MO Interceptor table \(2 flushed entries\)'}]
        yield self._test(r'jcli : ', commands)

        # Relist
        commands = [{'command': 'mointerceptor -l', 'expect': r'Total MO Interceptors: 0'}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_remove_and_list(self):
        # Add
        order = '0'
        extraCommands = [{'command': 'order %s' % order},
                         {'command': 'type DefaultInterceptor'},
                         {'command': 'script python2(%s)' % self.valid_script}]
        yield self.add_mointerceptor('jcli : ', extraCommands)

        expectedList = ['#Order Type                 Script                                          Filter\(s\)',
                        '#0     DefaultInterceptor   <MOIS \(pyCode=print "hello  world" ..\)>',
                        'Total MO Interceptors: 1']
        commands = [{'command': 'mointerceptor -l', 'expect': expectedList}]
        yield self._test(r'jcli : ', commands)

        # Remove
        commands = [{'command': 'mointerceptor -r %s' % order, 'expect': r'Successfully removed MO Interceptor with order\:%s' % order}]
        yield self._test(r'jcli : ', commands)

        # List again
        commands = [{'command': 'mointerceptor -l', 'expect': r'Total MO Interceptors: 0'}]
        yield self._test(r'jcli : ', commands)

class MoInterceptorTypingTestCases(MxInterceptorTestCases):

    @defer.inlineCallbacks
    def test_available_mointerceptors(self):
        # Go to MOInterceptor adding invite
        commands = [{'command': 'mointerceptor -a'}]
        yield self._test(r'>', commands)

        # Send 'type' command without any arg in order to get
        # the available mointerceptors from the error string
        yield self.sendCommand('type')
        receivedLines = self.getBuffer(True)

        filters = []
        results = re.findall(' (\w+)Interceptor', receivedLines[3])
        for item in results[:]:
            filters.append('%sInterceptor' % item)

        # Any new filter must be added here
        self.assertEqual(filters, ['DefaultInterceptor',
            'StaticMOInterceptor'
        ])

        # Check if MoInterceptorTypingTestCases is covering all the mointerceptors
        for f in filters:
            self.assertIn('test_add_%s' % f, dir(self), '%s mointerceptor is not tested !' % f)

    @defer.inlineCallbacks
    def test_add_DefaultInterceptor(self):
        iorder = '0'
        itype = 'DefaultInterceptor'
        script = self.valid_script
        typed_script = 'python2(%s)' % script
        _str_ = '%s/<MOIS \(pyCode=print "hello  world" ..\)>' % (itype)

        # Add MOInterceptor
        extraCommands = [{'command': 'order %s' % iorder},
                         {'command': 'type %s' % itype},
                         {'command': 'script %s' % typed_script}]
        yield self.add_mointerceptor('jcli : ', extraCommands)

        # Make asserts
        expectedList = ['%s' % _str_]
        yield self._test('jcli : ', [{'command': 'mointerceptor -s %s' % iorder, 'expect': expectedList}])
        expectedList = ['#Order Type                 Script                                          Filter\(s\)',
                        '#%s %s %s ' % (iorder.ljust(5), itype.ljust(20), '<MOIS \(pyCode=print "hello  world" ..\)>'.ljust(47)),
                        'Total MO Interceptors: 1']
        commands = [{'command': 'mointerceptor -l', 'expect': expectedList}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_add_StaticMOInterceptor(self):
        iorder = '10'
        itype = 'StaticMOInterceptor'
        script = self.valid_script
        typed_script = 'python2(%s)' % script
        _str_ = '%s/<MOIS \(pyCode=print "hello  world" ..\)>' % (itype)

        # Add MOInterceptor
        extraCommands = [{'command': 'order %s' % iorder},
                         {'command': 'type %s' % itype},
                         {'command': 'filters f1'},
                         {'command': 'script %s' % typed_script}]
        yield self.add_mointerceptor('jcli : ', extraCommands)

        # Make asserts
        expectedList = ['%s' % _str_]
        yield self._test('jcli : ', [{'command': 'mointerceptor -s %s' % iorder, 'expect': expectedList}])
        expectedList = ['#Order Type                 Script                                          Filter\(s\)',
                        '#%s %s %s ' % (iorder.ljust(5), itype.ljust(20), '<MOIS \(pyCode=print "hello  world" ..\)>'.ljust(47)),
                        'Total MO Interceptors: 1']
        commands = [{'command': 'mointerceptor -l', 'expect': expectedList}]
        yield self._test(r'jcli : ', commands)

class MoInterceptorArgsTestCases(MxInterceptorTestCases):

    @defer.inlineCallbacks
    def test_add_defaultinterceptor_with_nonzero_order(self):
        """The interceptor order will be forced to 0 when interceptor type is set to
        DefaultInterceptor just after indicating the order
        """
        extraCommands = [{'command': 'order 20'},
                         {'command': 'type DefaultInterceptor'},
                         {'command': 'script python2(%s)' % self.valid_script}]
        yield self.add_mointerceptor(r'jcli : ', extraCommands)

        expectedList = ['#Order Type                 Script                                          Filter\(s\)',
                        '#0     DefaultInterceptor   <MOIS \(pyCode=print "hello  world" ..\)>',
                        'Total MO Interceptors: 1']
        commands = [{'command': 'mointerceptor -l', 'expect': expectedList}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_add_defaultinterceptor_without_indicating_order(self):
        """The interceptor order will be set to 0 when interceptor type is set to
        DefaultInterceptor without the need to indicate the order
        """
        extraCommands = [{'command': 'type DefaultInterceptor'},
                         {'command': 'script python2(%s)' % self.valid_script}]
        yield self.add_mointerceptor(r'jcli : ', extraCommands)

        expectedList = ['#Order Type                 Script                                          Filter\(s\)',
                        '#0     DefaultInterceptor   <MOIS \(pyCode=print "hello  world" ..\)>',
                        'Total MO Interceptors: 1']
        commands = [{'command': 'mointerceptor -l', 'expect': expectedList}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_add_nondefaultinterceptor_with_zero_order(self):
        """The only interceptor that can have a Zero as it's order is the DefaultInterceptor
        """
        commands = [{'command': 'mointerceptor -a'},
                    {'command': 'order 0'},
                    {'command': 'type StaticMOInterceptor'},
                    {'command': 'script python2(%s)' % self.valid_script},
                    {'command': 'filters f1'},
                    {'command': 'ok', 'expect': 'Failed adding MOInterceptor, check log for details'}]
        yield self._test(r'> ', commands)

    @defer.inlineCallbacks
    def test_add_invalid_script(self):
        commands = [{'command': 'mointerceptor -a'},
                    {'command': 'type DefaultInterceptor'},
                    {'command': 'script python2(%s)' % self.invalid_syntax, 'expect': '\[Syntax\]: invalid syntax \(, line 1\)'},
                    {'command': 'ok', 'expect': 'You must set these options before saving: type, order, script'}]
        yield self._test(r'> ', commands)

    @defer.inlineCallbacks
    def test_add_unknown_script(self):
        commands = [{'command': 'mointerceptor -a'},
                    {'command': 'type DefaultInterceptor'},
                    {'command': 'script python2(%s)' % self.file_not_found, 'expect': '\[IO\]: \[Errno 2\] No such file or directory: \'\/file\/not\/found\''},
                    {'command': 'ok', 'expect': 'You must set these options before saving: type, order, script'}]
        yield self._test(r'> ', commands)

    @defer.inlineCallbacks
    def test_add_invalid_script_interpreter(self):
        commands = [{'command': 'mointerceptor -a'},
                    {'command': 'type DefaultInterceptor'},
                    {'command': 'script php(%s)' % self.valid_script, 'expect': 'Invalid syntax for script, must be python2\(\/path\/to\/script\).'},
                    {'command': 'ok', 'expect': 'You must set these options before saving: type, order, script'}]
        yield self._test(r'> ', commands)

    @defer.inlineCallbacks
    def test_add_incompatible_filter(self):
        commands = [{'command': 'mointerceptor -a'},
                    {'command': 'order 20'},
                    {'command': 'type StaticMOInterceptor'},
                    {'command': 'script python2(%s)' % self.valid_script},
                    {'command': 'filters uf1', 'expect': 'UserFilter#uf1 is not a valid filter for MOInterceptor'},
                    {'command': 'ok', 'expect': 'You must set these options before saving: type, order, filters, script'}]
        yield self._test(r'> ', commands)

    @defer.inlineCallbacks
    def test_list_with_filter(self):
        extraCommands = [{'command': 'order 20'},
                         {'command': 'type StaticMOInterceptor'},
                         {'command': 'script python2(%s)' % self.valid_script},
                         {'command': 'filters cf1'}]
        yield self.add_mointerceptor(r'jcli : ', extraCommands)

        expectedList = ['#Order Type                 Script                                          Filter\(s\)',
                        '#20    StaticMOInterceptor  <MOIS \(pyCode=print "hello  world" ..\)>         <C \(cid=Any\)>',
                        'Total MO Interceptors: 1']
        commands = [{'command': 'mointerceptor -l', 'expect': expectedList}]
        yield self._test(r'jcli : ', commands)
