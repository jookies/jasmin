import re

from twisted.internet import defer

from test_mxrouterm import MxRouterTestCases


class BasicTestCases(MxRouterTestCases):

    @defer.inlineCallbacks
    def test_add_with_minimum_args_http(self):
        extraCommands = [{'command': 'order 0'},
                         {'command': 'type DefaultRoute'},
                         {'command': 'connector http(http1)'}]
        yield self.add_moroute(r'jcli : ', extraCommands)

    @defer.inlineCallbacks
    def test_add_with_minimum_args_smpps(self):
        extraCommands = [{'command': 'order 0'},
                         {'command': 'type DefaultRoute'},
                         {'command': 'connector smpps(smpp_user)'}]
        yield self.add_moroute(r'jcli : ', extraCommands)

    @defer.inlineCallbacks
    def test_add_without_minimum_args(self):
        extraCommands = [{'command': 'ok', 'expect': r'You must set these options before saving: type, order'}]
        yield self.add_moroute(r'> ', extraCommands)

    @defer.inlineCallbacks
    def test_add_invalid_key(self):
        extraCommands = [{'command': 'order 0'},
                         {'command': 'type DefaultRoute'},
                         {'command': 'connector http(http1)'},
                         {'command': 'anykey anyvalue', 'expect': r'Unknown Route key: anykey'}]
        yield self.add_moroute(r'jcli : ', extraCommands)

    @defer.inlineCallbacks
    def test_cancel_add(self):
        extraCommands = [{'command': 'order 1'},
                         {'command': 'ko'}]
        yield self.add_moroute(r'jcli : ', extraCommands)

    @defer.inlineCallbacks
    def test_list(self):
        commands = [{'command': 'morouter -l', 'expect': r'Total MO Routes: 0'}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_add_and_list_http(self):
        extraCommands = [{'command': 'order 0'},
                         {'command': 'type DefaultRoute'},
                         {'command': 'connector http(http1)'}]
        yield self.add_moroute('jcli : ', extraCommands)

        expectedList = ['#Order Type                    Connector ID\(s\)                                  Filter\(s\)',
                        '#0     DefaultRoute            http\(http1\)',
                        'Total MO Routes: 1']
        commands = [{'command': 'morouter -l', 'expect': expectedList}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_add_and_list_smpps(self):
        extraCommands = [{'command': 'order 0'},
                         {'command': 'type DefaultRoute'},
                         {'command': 'connector smpps(smpp_user)'}]
        yield self.add_moroute('jcli : ', extraCommands)

        expectedList = ['#Order Type                    Connector ID\(s\)                                  Filter\(s\)',
                        '#0     DefaultRoute            smpps\(smpp_user\)',
                        'Total MO Routes: 1']
        commands = [{'command': 'morouter -l', 'expect': expectedList}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_add_cancel_and_list(self):
        extraCommands = [{'command': 'type defaultroute'},
                         {'command': 'ko'}, ]
        yield self.add_moroute(r'jcli : ', extraCommands)

        commands = [{'command': 'morouter -l', 'expect': r'Total MO Routes: 0'}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_show_http(self):
        order = '0'
        cid = 'http1'
        extraCommands = [{'command': 'order %s' % order},
                         {'command': 'type DefaultRoute'},
                         {'command': 'connector http(%s)' % cid}]
        yield self.add_moroute('jcli : ', extraCommands)

        commands = [{'command': 'morouter -s %s' % order, 'expect': 'DefaultRoute to %s\(%s\) NOT RATED' % ('http', cid)}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_show_smpps(self):
        order = '0'
        cid = 'smpp_user'
        extraCommands = [{'command': 'order %s' % order},
                         {'command': 'type DefaultRoute'},
                         {'command': 'connector smpps(%s)' % cid}]
        yield self.add_moroute('jcli : ', extraCommands)

        commands = [{'command': 'morouter -s %s' % order, 'expect': 'DefaultRoute to %s\(%s\) NOT RATED' % ('smpps', cid)}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_update_not_available(self):
        order = '0'
        extraCommands = [{'command': 'order %s' % order},
                         {'command': 'type DefaultRoute'},
                         {'command': 'connector http(http1)'}]
        yield self.add_moroute('jcli : ', extraCommands)

        commands = [{'command': 'morouter -u %s' % order, 'expect': 'no such option\: -u'}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_remove_invalid_order(self):
        commands = [{'command': 'morouter -r invalid_cid', 'expect': r'MO Route order must be a positive integer'}]
        yield self._test(r'jcli : ', commands)

        commands = [{'command': 'morouter -r 66', 'expect': r'Unknown MO Route: 66'}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_remove_http(self):
        order = '0'
        extraCommands = [{'command': 'order %s' % order},
                         {'command': 'type DefaultRoute'},
                         {'command': 'connector http(http1)'}]
        self.add_moroute('jcli : ', extraCommands)

        commands = [{'command': 'morouter -r %s' % order, 'expect': r'Successfully removed MO Route with order\:%s' % order}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_remove_smpps(self):
        order = '0'
        extraCommands = [{'command': 'order %s' % order},
                         {'command': 'type DefaultRoute'},
                         {'command': 'connector smpps(smpp_user)'}]
        yield self.add_moroute('jcli : ', extraCommands)

        commands = [{'command': 'morouter -r %s' % order, 'expect': r'Successfully removed MO Route with order\:%s' % order}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_flush(self):
        # Add 2 Routes:
        extraCommands = [{'command': 'order 0'},
                         {'command': 'type DefaultRoute'},
                         {'command': 'connector http(http1)'}]
        yield self.add_moroute('jcli : ', extraCommands)

        extraCommands = [{'command': 'order 20'},
                         {'command': 'type StaticMoRoute'},
                         {'command': 'connector smpps(smpp_user)'},
                         {'command': 'filters f1'}]
        yield self.add_moroute('jcli : ', extraCommands)

        # List
        expectedList = ['#Order Type                    Connector ID\(s\)                                  Filter\(s\)',
                        '#20    StaticMORoute           smpps\(smpp_user\)                                 \<T\>',
                        '#0     DefaultRoute            http\(http1\)',
                        'Total MO Routes: 2']
        commands = [{'command': 'morouter -l', 'expect': expectedList}]
        yield self._test(r'jcli : ', commands)

        # Flush
        commands = [{'command': 'morouter -f', 'expect': 'Successfully flushed MO Route table \(2 flushed entries\)'}]
        yield self._test(r'jcli : ', commands)

        # Relist
        commands = [{'command': 'morouter -l', 'expect': r'Total MO Routes: 0'}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_remove_and_list(self):
        # Add
        order = '0'
        extraCommands = [{'command': 'order %s' % order},
                         {'command': 'type DefaultRoute'},
                         {'command': 'connector http(http1)'}]
        yield self.add_moroute('jcli : ', extraCommands)

        expectedList = ['#Order Type                    Connector ID\(s\)                                  Filter\(s\)',
                        '#0     DefaultRoute            http\(http1\)',
                        'Total MO Routes: 1']
        commands = [{'command': 'morouter -l', 'expect': expectedList}]
        yield self._test(r'jcli : ', commands)

        # Remove
        commands = [{'command': 'morouter -r %s' % order, 'expect': r'Successfully removed MO Route with order\:%s' % order}]
        yield self._test(r'jcli : ', commands)

        # List again
        commands = [{'command': 'morouter -l', 'expect': r'Total MO Routes: 0'}]
        yield self._test(r'jcli : ', commands)

class MoRouteTypingTestCases(MxRouterTestCases):

    @defer.inlineCallbacks
    def test_available_moroutes(self):
        # Go to MORoute adding invite
        commands = [{'command': 'morouter -a'}]
        yield self._test(r'>', commands)

        # Send 'type' command without any arg in order to get
        # the available moroutes from the error string
        yield self.sendCommand('type')
        receivedLines = self.getBuffer(True)

        filters = []
        results = re.findall(' (\w+)Route', receivedLines[3])
        for item in results[:]:
            filters.append('%sRoute_http' % item)
            filters.append('%sRoute_smpps' % item)

        # Any new filter must be added here
        self.assertEqual(filters, ['DefaultRoute_http',
                                   'DefaultRoute_smpps',
                                   'StaticMORoute_http',
                                   'StaticMORoute_smpps',
                                   'RandomRoundrobinMORoute_http',
                                   'RandomRoundrobinMORoute_smpps',
                                   'FailoverMORoute_http',
                                   'FailoverMORoute_smpps'])

        # Check if MoRouteTypingTestCases is covering all the moroutes
        for f in filters:
            self.assertIn('test_add_%s' % f, dir(self), '%s moroute is not tested !' % f)

    @defer.inlineCallbacks
    def test_add_DefaultRoute_http(self):
        rorder = '0'
        rtype = 'DefaultRoute'
        cid = 'http1'
        typed_cid = 'http(%s)' % cid
        _str_ = '%s to %s NOT RATED' % (rtype, re.escape(typed_cid))

        # Add MORoute
        extraCommands = [{'command': 'order %s' % rorder},
                         {'command': 'type %s' % rtype},
                         {'command': 'connector %s' % typed_cid}]
        yield self.add_moroute('jcli : ', extraCommands)

        # Make asserts
        expectedList = ['%s' % _str_]
        yield self._test('jcli : ', [{'command': 'morouter -s %s' % rorder, 'expect': expectedList}])
        expectedList = ['#Order Type                    Connector ID\(s\)                                  Filter\(s\)',
                        '#%s %s %s ' % (rorder.ljust(5), rtype.ljust(23), re.escape(typed_cid).ljust(48)),
                        'Total MO Routes: 1']
        commands = [{'command': 'morouter -l', 'expect': expectedList}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_add_DefaultRoute_smpps(self):
        rorder = '0'
        rtype = 'DefaultRoute'
        cid = 'smpp_user'
        typed_cid = 'smpps(%s)' % cid
        _str_ = '%s to %s NOT RATED' % (rtype, re.escape(typed_cid))

        # Add MORoute
        extraCommands = [{'command': 'order %s' % rorder},
                         {'command': 'type %s' % rtype},
                         {'command': 'connector %s' % typed_cid}]
        yield self.add_moroute('jcli : ', extraCommands)

        # Make asserts
        expectedList = ['%s' % _str_]
        yield self._test('jcli : ', [{'command': 'morouter -s %s' % rorder, 'expect': expectedList}])
        expectedList = ['#Order Type                    Connector ID\(s\)                                  Filter\(s\)',
                        '#%s %s %s ' % (rorder.ljust(5), rtype.ljust(23), re.escape(typed_cid).ljust(48)),
                        'Total MO Routes: 1']
        commands = [{'command': 'morouter -l', 'expect': expectedList}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_add_StaticMORoute_http(self):
        rorder = '10'
        rtype = 'StaticMORoute'
        cid = 'http1'
        typed_cid = 'http(%s)' % cid
        fid = 'f1'
        _str_ = '%s to %s NOT RATED' % (rtype, re.escape(typed_cid))

        # Add MORoute
        extraCommands = [{'command': 'order %s' % rorder},
                         {'command': 'type %s' % rtype},
                         {'command': 'connector %s' % typed_cid},
                         {'command': 'filters %s' % fid}]
        yield self.add_moroute('jcli : ', extraCommands)

        # Make asserts
        expectedList = ['%s' % _str_]
        yield self._test('jcli : ', [{'command': 'morouter -s %s' % rorder, 'expect': expectedList}])
        expectedList = ['#Order Type                    Connector ID\(s\)                                  Filter\(s\)',
                        '#%s %s %s   <T>' % (rorder.ljust(5), rtype.ljust(23), re.escape(typed_cid).ljust(48)),
                        'Total MO Routes: 1']
        commands = [{'command': 'morouter -l', 'expect': expectedList}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_add_StaticMORoute_smpps(self):
        rorder = '10'
        rtype = 'StaticMORoute'
        cid = 'smppuser'
        typed_cid = 'smpps(%s)' % cid
        fid = 'f1'
        _str_ = '%s to %s NOT RATED' % (rtype, re.escape(typed_cid))

        # Add MORoute
        extraCommands = [{'command': 'order %s' % rorder},
                         {'command': 'type %s' % rtype},
                         {'command': 'connector %s' % typed_cid},
                         {'command': 'filters %s' % fid}]
        yield self.add_moroute('jcli : ', extraCommands)

        # Make asserts
        expectedList = ['%s' % _str_]
        yield self._test('jcli : ', [{'command': 'morouter -s %s' % rorder, 'expect': expectedList}])
        expectedList = ['#Order Type                    Connector ID\(s\)                                  Filter\(s\)',
                        '#%s %s %s   <T>' % (rorder.ljust(5), rtype.ljust(23), re.escape(typed_cid).ljust(48)),
                        'Total MO Routes: 1']
        commands = [{'command': 'morouter -l', 'expect': expectedList}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_add_RandomRoundrobinMORoute_http(self):
        rorder = '10'
        rtype = 'RandomRoundrobinMORoute'
        cid1 = 'http1'
        typed_cid1 = 'http(%s)' % cid1
        cid2 = 'http2'
        typed_cid2 = 'http(%s)' % cid2
        fid = 'f1'
        _str_ = ['%s to 2 connectors:' % rtype, '\t- %s' % re.escape(typed_cid1), '\t- %s' % re.escape(typed_cid2)]

        # Add MORoute
        extraCommands = [{'command': 'order %s' % rorder},
                         {'command': 'type %s' % rtype},
                         {'command': 'connectors %s;%s' % (typed_cid1, typed_cid2)},
                         {'command': 'filters %s' % fid}]
        yield self.add_moroute('jcli : ', extraCommands)

        # Make asserts
        expectedList = _str_
        yield self._test('jcli : ', [{'command': 'morouter -s %s' % rorder, 'expect': expectedList}])
        expectedList = ['#Order Type                    Connector ID\(s\)                                  Filter\(s\)',
                        '#%s %s %s     <T>' % (rorder.ljust(5), rtype.ljust(23), (re.escape(typed_cid1)+', '+re.escape(typed_cid2)).ljust(48)),
                        'Total MO Routes: 1']
        commands = [{'command': 'morouter -l', 'expect': expectedList}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_add_RandomRoundrobinMORoute_smpps(self):
        rorder = '10'
        rtype = 'RandomRoundrobinMORoute'
        cid1 = 'smppuser1'
        typed_cid1 = 'smpps(%s)' % cid1
        cid2 = 'smppuser2'
        typed_cid2 = 'smpps(%s)' % cid2
        fid = 'f1'
        _str_ = ['%s to 2 connectors:' % rtype, '\t- %s' % re.escape(typed_cid1), '\t- %s' % re.escape(typed_cid2)]

        # Add MORoute
        extraCommands = [{'command': 'order %s' % rorder},
                         {'command': 'type %s' % rtype},
                         {'command': 'connectors %s;%s' % (typed_cid1, typed_cid2)},
                         {'command': 'filters %s' % fid}]
        yield self.add_moroute('jcli : ', extraCommands)

        # Make asserts
        expectedList = _str_
        yield self._test('jcli : ', [{'command': 'morouter -s %s' % rorder, 'expect': expectedList}])
        expectedList = ['#Order Type                    Connector ID\(s\)                                  Filter\(s\)',
                        '#%s %s %s     <T>' % (rorder.ljust(5), rtype.ljust(23), (re.escape(typed_cid1)+', '+re.escape(typed_cid2)).ljust(48)),
                        'Total MO Routes: 1']
        commands = [{'command': 'morouter -l', 'expect': expectedList}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_add_RandomRoundrobinMORoute_hybrid(self):
        rorder = '10'
        rtype = 'RandomRoundrobinMORoute'
        cid1 = 'smppuser1'
        typed_cid1 = 'smpps(%s)' % cid1
        cid2 = 'http1'
        typed_cid2 = 'http(%s)' % cid2
        fid = 'f1'
        _str_ = ['%s to 2 connectors:' % rtype, '\t- %s' % re.escape(typed_cid1), '\t- %s' % re.escape(typed_cid2)]

        # Add MORoute
        extraCommands = [{'command': 'order %s' % rorder},
                         {'command': 'type %s' % rtype},
                         {'command': 'connectors %s;%s' % (typed_cid1, typed_cid2)},
                         {'command': 'filters %s' % fid}]
        yield self.add_moroute('jcli : ', extraCommands)

        # Make asserts
        expectedList = _str_
        yield self._test('jcli : ', [{'command': 'morouter -s %s' % rorder, 'expect': expectedList}])
        expectedList = ['#Order Type                    Connector ID\(s\)                                  Filter\(s\)',
                        '#%s %s %s     <T>' % (rorder.ljust(5), rtype.ljust(23), (re.escape(typed_cid1)+', '+re.escape(typed_cid2)).ljust(48)),
                        'Total MO Routes: 1']
        commands = [{'command': 'morouter -l', 'expect': expectedList}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_add_FailoverMORoute_http(self):
        rorder = '10'
        rtype = 'FailoverMORoute'
        cid1 = 'http1'
        typed_cid1 = 'http(%s)' % cid1
        cid2 = 'http2'
        typed_cid2 = 'http(%s)' % cid2
        fid = 'f1'
        _str_ = ['%s to 2 connectors:' % rtype, '\t- %s' % re.escape(typed_cid1), '\t- %s' % re.escape(typed_cid2)]

        # Add MORoute
        extraCommands = [{'command': 'order %s' % rorder},
                         {'command': 'type %s' % rtype},
                         {'command': 'connectors %s;%s' % (typed_cid1, typed_cid2)},
                         {'command': 'filters %s' % fid}]
        yield self.add_moroute('jcli : ', extraCommands)

        # Make asserts
        expectedList = _str_
        yield self._test('jcli : ', [{'command': 'morouter -s %s' % rorder, 'expect': expectedList}])
        expectedList = ['#Order Type                    Connector ID\(s\)                                  Filter\(s\)',
                        '#%s %s %s     <T>' % (
                        rorder.ljust(5), rtype.ljust(23), (re.escape(typed_cid1) + ', ' + re.escape(typed_cid2)).ljust(48)),
                        'Total MO Routes: 1']
        commands = [{'command': 'morouter -l', 'expect': expectedList}]
        yield self._test(r'jcli : ', commands)


    @defer.inlineCallbacks
    def test_add_FailoverMORoute_smpps(self):
        rorder = '10'
        rtype = 'FailoverMORoute'
        cid1 = 'smppuser1'
        typed_cid1 = 'smpps(%s)' % cid1
        cid2 = 'smppuser2'
        typed_cid2 = 'smpps(%s)' % cid2
        fid = 'f1'
        _str_ = ['%s to 2 connectors:' % rtype, '\t- %s' % re.escape(typed_cid1), '\t- %s' % re.escape(typed_cid2)]

        # Add MORoute
        extraCommands = [{'command': 'order %s' % rorder},
                         {'command': 'type %s' % rtype},
                         {'command': 'connectors %s;%s' % (typed_cid1, typed_cid2)},
                         {'command': 'filters %s' % fid}]
        yield self.add_moroute('jcli : ', extraCommands)

        # Make asserts
        expectedList = _str_
        yield self._test('jcli : ', [{'command': 'morouter -s %s' % rorder, 'expect': expectedList}])
        expectedList = ['#Order Type                    Connector ID\(s\)                                  Filter\(s\)',
                        '#%s %s %s     <T>' % (
                        rorder.ljust(5), rtype.ljust(23), (re.escape(typed_cid1) + ', ' + re.escape(typed_cid2)).ljust(48)),
                        'Total MO Routes: 1']
        commands = [{'command': 'morouter -l', 'expect': expectedList}]
        yield self._test(r'jcli : ', commands)


    @defer.inlineCallbacks
    def test_add_FailoverMORoute_hybrid(self):
        rorder = '10'
        rtype = 'FailoverMORoute'
        cid1 = 'smppuser1'
        typed_cid1 = 'smpps(%s)' % cid1
        cid2 = 'http1'
        typed_cid2 = 'http(%s)' % cid2
        fid = 'f1'
        _str_ = ['%s to 2 connectors:' % rtype, '\t- %s' % re.escape(typed_cid1), '\t- %s' % re.escape(typed_cid2)]

        # Add MORoute
        extraCommands = [{'command': 'order %s' % rorder},
                         {'command': 'type %s' % rtype},
                         {'command': 'connectors %s;%s' % (typed_cid1, typed_cid2)},
                         {'command': 'filters %s' % fid},
                         {'command': 'ok', 'expect': 'Error: FailoverMORoute cannot have mixed connector types'}]
        yield self.add_moroute('>', extraCommands)


class MoRouteArgsTestCases(MxRouterTestCases):

    @defer.inlineCallbacks
    def test_add_defaultroute_with_nonzero_order(self):
        """The route order will be forced to 0 when route type is set to
        DefaultRoute just after indicating the order
        """
        extraCommands = [{'command': 'order 20'},
                         {'command': 'type DefaultRoute'},
                         {'command': 'connector http(http1)'}]
        yield self.add_moroute(r'jcli : ', extraCommands)

        expectedList = ['#Order Type                    Connector ID\(s\)                                  Filter\(s\)',
                        '#0     DefaultRoute            http\(http1\)',
                        'Total MO Routes: 1']
        commands = [{'command': 'morouter -l', 'expect': expectedList}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_add_defaultroute_without_indicating_order(self):
        """The route order will be set to 0 when route type is set to
        DefaultRoute without the need to indicate the order
        """
        extraCommands = [{'command': 'type DefaultRoute'},
                         {'command': 'connector http(http1)'}]
        yield self.add_moroute(r'jcli : ', extraCommands)

        expectedList = ['#Order Type                    Connector ID\(s\)                                  Filter\(s\)',
                        '#0     DefaultRoute            http\(http1\)',
                        'Total MO Routes: 1']
        commands = [{'command': 'morouter -l', 'expect': expectedList}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_add_nondefaultroute_with_zero_order(self):
        """The only route that can have a Zero as it's order is the DefaultRoute
        """
        commands = [{'command': 'morouter -a'},
                    {'command': 'order 0'},
                    {'command': 'type StaticMORoute'},
                    {'command': 'connector http(http1)'},
                    {'command': 'filters f1'},
                    {'command': 'ok', 'expect': 'Failed adding MORoute, check log for details'}]
        yield self._test(r'> ', commands)

    @defer.inlineCallbacks
    def test_add_incompatible_connector(self):
        commands = [{'command': 'morouter -a'},
                    {'command': 'type DefaultRoute'},
                    {'command': 'connector http(smpp1)', 'expect': 'Unknown http cid: smpp1'},
                    {'command': 'ok', 'expect': 'You must set these options before saving: type, order, connector'}]
        yield self._test(r'> ', commands)

    @defer.inlineCallbacks
    def test_add_incompatible_filter(self):
        commands = [{'command': 'morouter -a'},
                    {'command': 'order 20'},
                    {'command': 'type StaticMORoute'},
                    {'command': 'connector http(http1)'},
                    {'command': 'filters uf1', 'expect': 'UserFilter#uf1 is not a valid filter for MORoute'},
                    {'command': 'ok', 'expect': 'You must set these options before saving: type, order, filters, connector'}]
        yield self._test(r'> ', commands)

    @defer.inlineCallbacks
    def test_list_with_filter(self):
        extraCommands = [{'command': 'order 20'},
                         {'command': 'type StaticMORoute'},
                         {'command': 'connector http(http1)'},
                         {'command': 'filters cf1'}]
        yield self.add_moroute(r'jcli : ', extraCommands)

        expectedList = ['#Order Type                    Connector ID\(s\)                                  Filter\(s\)',
                        '#20    StaticMORoute           http\(http1\)                                      <C \(cid=Any\)>',
                        'Total MO Routes: 1']
        commands = [{'command': 'morouter -l', 'expect': expectedList}]
        yield self._test(r'jcli : ', commands)
