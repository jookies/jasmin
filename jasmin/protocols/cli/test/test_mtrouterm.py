import re

from twisted.internet import defer

from test_mxrouterm import MxRouterTestCases


class BasicTestCases(MxRouterTestCases):

    @defer.inlineCallbacks
    def test_add_with_minimum_args(self):
        extraCommands = [{'command': 'order 0'},
                         {'command': 'type DefaultRoute'},
                         {'command': 'rate 0.0'},
                         {'command': 'connector smppc(smpp1)'}]
        yield self.add_mtroute(r'jcli : ', extraCommands)

    @defer.inlineCallbacks
    def test_add_without_minimum_args(self):
        extraCommands = [{'command': 'ok', 'expect': r'You must set these options before saving: type, order'}]
        yield self.add_mtroute(r'> ', extraCommands)

    @defer.inlineCallbacks
    def test_add_invalid_key(self):
        extraCommands = [{'command': 'order 0'},
                         {'command': 'type DefaultRoute'},
                         {'command': 'connector smppc(smpp1)'},
                         {'command': 'rate 0.0'},
                         {'command': 'anykey anyvalue', 'expect': r'Unknown Route key: anykey'}]
        yield self.add_mtroute(r'jcli : ', extraCommands)

    @defer.inlineCallbacks
    def test_cancel_add(self):
        extraCommands = [{'command': 'order 1'},
                         {'command': 'ko'}]
        yield self.add_mtroute(r'jcli : ', extraCommands)

    @defer.inlineCallbacks
    def test_list(self):
        commands = [{'command': 'mtrouter -l', 'expect': r'Total MT Routes: 0'}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_add_and_list(self):
        extraCommands = [{'command': 'order 0'},
                         {'command': 'type DefaultRoute'},
                         {'command': 'rate 0.0'},
                         {'command': 'connector smppc(smpp1)'}]
        yield self.add_mtroute('jcli : ', extraCommands)

        expectedList = ['#Order Type                    Rate       Connector ID\(s\)                                  Filter\(s\)',
                        '#0     DefaultRoute            0 \(!\)      smppc\(smpp1\)',
                        'Total MT Routes: 1']
        commands = [{'command': 'mtrouter -l', 'expect': expectedList}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_add_cancel_and_list(self):
        extraCommands = [{'command': 'type defaultroute'},
                         {'command': 'ko'}, ]
        yield self.add_mtroute(r'jcli : ', extraCommands)

        commands = [{'command': 'mtrouter -l', 'expect': r'Total MT Routes: 0'}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_show(self):
        order = '0'
        cid = 'smpp1'
        typed_cid = 'smppc(%s)' % cid
        extraCommands = [{'command': 'order %s' % order},
                         {'command': 'type DefaultRoute'},
                         {'command': 'rate 0.0'},
                         {'command': 'connector %s' % typed_cid}]
        yield self.add_mtroute('jcli : ', extraCommands)

        commands = [{'command': 'mtrouter -s %s' % order, 'expect': 'DefaultRoute to %s' % re.escape(typed_cid)}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_update_not_available(self):
        order = '0'
        extraCommands = [{'command': 'order %s' % order},
                         {'command': 'type DefaultRoute'},
                         {'command': 'rate 0.0'},
                         {'command': 'connector smppc(smpp1)'}]
        yield self.add_mtroute('jcli : ', extraCommands)

        commands = [{'command': 'mtrouter -u %s' % order, 'expect': 'no such option\: -u'}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_remove_invalid_order(self):
        commands = [{'command': 'mtrouter -r invalid_cid', 'expect': r'MT Route order must be a positive integer'}]
        yield self._test(r'jcli : ', commands)

        commands = [{'command': 'mtrouter -r 66', 'expect': r'Unknown MT Route: 66'}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_remove(self):
        order = '0'
        extraCommands = [{'command': 'order %s' % order},
                         {'command': 'type DefaultRoute'},
                         {'command': 'rate 0.0'},
                         {'command': 'connector smppc(smpp1)'}]
        yield self.add_mtroute('jcli : ', extraCommands)

        commands = [{'command': 'mtrouter -r %s' % order, 'expect': r'Successfully removed MT Route with order\:%s' % order}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_flush(self):
        # Add 2 Routes:
        extraCommands = [{'command': 'order 0'},
                         {'command': 'type DefaultRoute'},
                         {'command': 'rate 0.0'},
                         {'command': 'connector smppc(smpp1)'}]
        yield self.add_mtroute('jcli : ', extraCommands)

        extraCommands = [{'command': 'order 20'},
                         {'command': 'type StaticMtRoute'},
                         {'command': 'connector smppc(smpp1)'},
                         {'command': 'rate 0.0'},
                         {'command': 'filters f1'}]
        yield self.add_mtroute('jcli : ', extraCommands)

        # List
        expectedList = ['#Order Type                    Rate       Connector ID\(s\)                                  Filter\(s\)',
                        '#20    StaticMTRoute           0 \(!\)      smppc\(smpp1\)                                     \<T\>',
                        '#0     DefaultRoute            0 \(!\)      smppc\(smpp1\)',
                        'Total MT Routes: 2']
        commands = [{'command': 'mtrouter -l', 'expect': expectedList}]
        yield self._test(r'jcli : ', commands)

        # Flush
        commands = [{'command': 'mtrouter -f', 'expect': 'Successfully flushed MT Route table \(2 flushed entries\)'}]
        yield self._test(r'jcli : ', commands)

        # Relist
        commands = [{'command': 'mtrouter -l', 'expect': r'Total MT Routes: 0'}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_remove_and_list(self):
        # Add
        order = '0'
        extraCommands = [{'command': 'order %s' % order},
                         {'command': 'type DefaultRoute'},
                         {'command': 'rate 0.0'},
                         {'command': 'connector smppc(smpp1)'}]
        yield self.add_mtroute('jcli : ', extraCommands)

        expectedList = ['#Order Type                    Rate       Connector ID\(s\)                                  Filter\(s\)',
                        '#0     DefaultRoute            0 \(!\)      smppc\(smpp1\)',
                        'Total MT Routes: 1']
        commands = [{'command': 'mtrouter -l', 'expect': expectedList}]
        yield self._test(r'jcli : ', commands)

        # Remove
        commands = [{'command': 'mtrouter -r %s' % order, 'expect': r'Successfully removed MT Route with order\:%s' % order}]
        yield self._test(r'jcli : ', commands)

        # List again
        commands = [{'command': 'mtrouter -l', 'expect': r'Total MT Routes: 0'}]
        yield self._test(r'jcli : ', commands)

class MtRouteTypingTestCases(MxRouterTestCases):

    @defer.inlineCallbacks
    def test_available_mtroutes(self):
        # Go to MTRoute adding invite
        commands = [{'command': 'mtrouter -a'}]
        yield self._test(r'>', commands)

        # Send 'type' command without any arg in order to get
        # the available mtroutes from the error string
        yield self.sendCommand('type')
        receivedLines = self.getBuffer(True)

        filters = []
        results = re.findall(' (\w+)Route', receivedLines[3])
        filters.extend('%sRoute' % item for item in results[:])

        # Any new filter must be added here
        self.assertEqual(filters, ['DefaultRoute', 'StaticMTRoute',
                                   'RandomRoundrobinMTRoute', 'FailoverMTRoute'])

        # Check if MtRouteTypingTestCases is covering all the mtroutes
        for f in filters:
            self.assertIn('test_add_%s' % f, dir(self), '%s mtroute is not tested !' % f)

    @defer.inlineCallbacks
    def test_add_DefaultRoute(self):
        rorder = '0'
        rtype = 'DefaultRoute'
        cid = 'smpp1'
        typed_cid = 'smppc(%s)' % (cid)
        rate = '0'
        _str_ = '%s to %s NOT RATED' % (rtype, re.escape(typed_cid))

        # Add MTRoute
        extraCommands = [{'command': 'order %s' % rorder},
                         {'command': 'type %s' % rtype},
                         {'command': 'rate %s' % rate},
                         {'command': 'connector %s' % typed_cid}]
        yield self.add_mtroute('jcli : ', extraCommands)

        # Make asserts
        expectedList = ['%s' % _str_]
        yield self._test('jcli : ', [{'command': 'mtrouter -s %s' % rorder, 'expect': expectedList}])
        expectedList = ['#Order Type                    Rate       Connector ID\(s\)                                  Filter\(s\)',
                        '#%s %s %s %s ' % (rorder.ljust(5), rtype.ljust(23), '0 \(\!\)'.ljust(13), re.escape(typed_cid).ljust(48)),
                        'Total MT Routes: 1']
        commands = [{'command': 'mtrouter -l', 'expect': expectedList}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_add_StaticMTRoute(self):
        rorder = '10'
        rtype = 'StaticMTRoute'
        cid = 'smpp1'
        typed_cid = 'smppc(%s)' % (cid)
        rate = '0'
        fid = 'f1'
        _str_ = '%s to %s NOT RATED' % (rtype, re.escape(typed_cid))

        # Add MTRoute
        extraCommands = [{'command': 'order %s' % rorder},
                         {'command': 'type %s' % rtype},
                         {'command': 'connector %s' % typed_cid},
                         {'command': 'rate %s' % rate},
                         {'command': 'filters %s' % fid}]
        yield self.add_mtroute('jcli : ', extraCommands)

        # Make asserts
        expectedList = ['%s' % _str_]
        yield self._test('jcli : ', [{'command': 'mtrouter -s %s' % rorder, 'expect': expectedList}])
        expectedList = ['#Order Type                    Rate       Connector ID\(s\)                                  Filter\(s\)',
                        '#%s %s %s %s   <T>' % (rorder.ljust(5), rtype.ljust(23), '0 \(\!\)'.ljust(13), re.escape(typed_cid).ljust(48)),
                        'Total MT Routes: 1']
        commands = [{'command': 'mtrouter -l', 'expect': expectedList}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_add_RandomRoundrobinMTRoute(self):
        rorder = '10'
        rtype = 'RandomRoundrobinMTRoute'
        cid1 = 'smpp1'
        typed_cid1 = 'smppc(%s)' % cid1
        cid2 = 'smpp2'
        typed_cid2 = 'smppc(%s)' % cid2
        rate = '0'
        fid = 'f1'
        _str_ = ['%s to 2 connectors:' % rtype, '\t- %s' % re.escape(typed_cid1), '\t- %s' % re.escape(typed_cid2), 'NOT RATED']

        # Add MTRoute
        extraCommands = [{'command': 'order %s' % rorder},
                         {'command': 'type %s' % rtype},
                         {'command': 'connectors %s;%s' % (typed_cid1, typed_cid2)},
                         {'command': 'rate %s' % rate},
                         {'command': 'filters %s' % fid}]
        yield self.add_mtroute('jcli : ', extraCommands)

        # Make asserts
        expectedList = _str_
        yield self._test('jcli : ', [{'command': 'mtrouter -s %s' % rorder, 'expect': expectedList}])
        expectedList = ['#Order Type                    Rate       Connector ID\(s\)                                  Filter\(s\)',
                        '#%s %s %s %s     <T>' % (rorder.ljust(5), rtype.ljust(23), '0 \(\!\)'.ljust(13), (re.escape(typed_cid1)+', '+re.escape(typed_cid2)).ljust(48)),
                        'Total MT Routes: 1']
        commands = [{'command': 'mtrouter -l', 'expect': expectedList}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_add_FailoverMTRoute(self):
        rorder = '10'
        rtype = 'FailoverMTRoute'
        cid1 = 'smpp1'
        typed_cid1 = 'smppc(%s)' % cid1
        cid2 = 'smpp2'
        typed_cid2 = 'smppc(%s)' % cid2
        rate = '0'
        fid = 'f1'
        _str_ = ['%s to 2 connectors:' % rtype, '\t- %s' % re.escape(typed_cid1), '\t- %s' % re.escape(typed_cid2),
                 'NOT RATED']

        # Add MTRoute
        extraCommands = [{'command': 'order %s' % rorder},
                         {'command': 'type %s' % rtype},
                         {'command': 'connectors %s;%s' % (typed_cid1, typed_cid2)},
                         {'command': 'rate %s' % rate},
                         {'command': 'filters %s' % fid}]
        yield self.add_mtroute('jcli : ', extraCommands)

        # Make asserts
        expectedList = _str_
        yield self._test('jcli : ', [{'command': 'mtrouter -s %s' % rorder, 'expect': expectedList}])
        expectedList = [
            '#Order Type                    Rate       Connector ID\(s\)                                  Filter\(s\)',
            '#%s %s %s %s     <T>' % (rorder.ljust(5), rtype.ljust(23), '0 \(\!\)'.ljust(13),
                                      (re.escape(typed_cid1) + ', ' + re.escape(typed_cid2)).ljust(48)),
            'Total MT Routes: 1']
        commands = [{'command': 'mtrouter -l', 'expect': expectedList}]
        yield self._test(r'jcli : ', commands)


class MtRouteArgsTestCases(MxRouterTestCases):

    @defer.inlineCallbacks
    def test_add_defaultroute_with_nonzero_order(self):
        """The route order will be forced to 0 when route type is set to
        DefaultRoute just after indicating the order
        """
        extraCommands = [{'command': 'order 20'},
                         {'command': 'type DefaultRoute'},
                         {'command': 'rate 0.0'},
                         {'command': 'connector smppc(smpp1)'}]
        yield self.add_mtroute(r'jcli : ', extraCommands)

        expectedList = ['#Order Type                    Rate       Connector ID\(s\)                                  Filter\(s\)',
                        '#0     DefaultRoute            0 \(!\)      smppc\(smpp1\)',
                        'Total MT Routes: 1']
        commands = [{'command': 'mtrouter -l', 'expect': expectedList}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_add_defaultroute_without_indicating_order(self):
        """The route order will be set to 0 when route type is set to
        DefaultRoute without the need to indicate the order
        """
        extraCommands = [{'command': 'type DefaultRoute'},
                         {'command': 'rate 0.0'},
                         {'command': 'connector smppc(smpp1)'}]
        yield self.add_mtroute(r'jcli : ', extraCommands)

        expectedList = ['#Order Type                    Rate       Connector ID\(s\)                                  Filter\(s\)',
                        '#0     DefaultRoute            0 \(!\)      smppc\(smpp1\)',
                        'Total MT Routes: 1']
        commands = [{'command': 'mtrouter -l', 'expect': expectedList}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_add_nondefaultroute_with_zero_order(self):
        """The only route that can have a Zero as it's order is the DefaultRoute
        """
        commands = [{'command': 'mtrouter -a'},
                    {'command': 'order 0'},
                    {'command': 'type StaticMTRoute'},
                    {'command': 'rate 0.0'},
                    {'command': 'connector smppc(smpp1)'},
                    {'command': 'filters f1'},
                    {'command': 'ok', 'expect': 'Failed adding MTRoute, check log for details'}]
        yield self._test(r'> ', commands)

    @defer.inlineCallbacks
    def test_add_incompatible_connector(self):
        commands = [{'command': 'mtrouter -a'},
                    {'command': 'type DefaultRoute'},
                    {'command': 'connector smppc(http1)', 'expect': 'Unknown smppc cid: http1'},
                    {'command': 'ok', 'expect': 'You must set these options before saving: type, order, connector'}]
        yield self._test(r'> ', commands)

    @defer.inlineCallbacks
    def test_add_incompatible_filter(self):
        commands = [{'command': 'mtrouter -a'},
                    {'command': 'order 20'},
                    {'command': 'type StaticMTRoute'},
                    {'command': 'connector smppc(smpp1)'},
                    {'command': 'filters cf1', 'expect': 'ConnectorFilter#cf1 is not a valid filter for MTRoute'},
                    {'command': 'ok', 'expect': 'You must set these options before saving: type, order, filters, connector'}]
        yield self._test(r'> ', commands)

    @defer.inlineCallbacks
    def test_list_with_filter(self):
        extraCommands = [{'command': 'order 20'},
                         {'command': 'type StaticMTRoute'},
                         {'command': 'connector smppc(smpp1)'},
                         {'command': 'rate 0.0'},
                         {'command': 'filters uf1'}]
        yield self.add_mtroute(r'jcli : ', extraCommands)

        expectedList = ['#Order Type                    Rate       Connector ID\(s\)                                  Filter\(s\)',
                        '#20    StaticMTRoute           0 \(!\)      smppc\(smpp1\)                                     <U \(uid=Any\)>',
                        'Total MT Routes: 1']
        commands = [{'command': 'mtrouter -l', 'expect': expectedList}]
        yield self._test(r'jcli : ', commands)
