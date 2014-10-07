import re
from test_mxrouterm import MxRouterTestCases
    
class BasicTestCases(MxRouterTestCases):
    
    def test_add_with_minimum_args(self):
        extraCommands = [{'command': 'order 0'}, 
                         {'command': 'type DefaultRoute'},
                         {'command': 'rate 0.0'},
                         {'command': 'connector smpp1'}]
        return self.add_mtroute(r'jcli : ', extraCommands)
    
    def test_add_without_minimum_args(self):
        extraCommands = [{'command': 'ok', 'expect': r'You must set these options before saving: type, order'}]
        return self.add_mtroute(r'> ', extraCommands)
    
    def test_add_invalid_key(self):
        extraCommands = [{'command': 'order 0'},
                         {'command': 'type DefaultRoute'},
                         {'command': 'connector smpp1'},
                         {'command': 'rate 0.0'},
                         {'command': 'anykey anyvalue', 'expect': r'Unknown Route key: anykey'}]
        return self.add_mtroute(r'jcli : ', extraCommands)
    
    def test_cancel_add(self):
        extraCommands = [{'command': 'order 1'},
                         {'command': 'ko'}]
        return self.add_mtroute(r'jcli : ', extraCommands)

    def test_list(self):
        commands = [{'command': 'mtrouter -l', 'expect': r'Total MT Routes: 0'}]
        return self._test(r'jcli : ', commands)
    
    def test_add_and_list(self):
        extraCommands = [{'command': 'order 0'}, 
                         {'command': 'type DefaultRoute'},
                         {'command': 'rate 0.0'},
                         {'command': 'connector smpp1'}]
        self.add_mtroute('jcli : ', extraCommands)

        expectedList = ['#MT Route order   Type                    Rate    Connector ID\(s\)                  Filter\(s\)', 
                        '#0                DefaultRoute            0.00    smpp1', 
                        'Total MT Routes: 1']
        commands = [{'command': 'mtrouter -l', 'expect': expectedList}]
        return self._test(r'jcli : ', commands)
    
    def test_add_cancel_and_list(self):
        extraCommands = [{'command': 'type defaultroute'},
                         {'command': 'ko'}, ]
        self.add_mtroute(r'jcli : ', extraCommands)

        commands = [{'command': 'mtrouter -l', 'expect': r'Total MT Routes: 0'}]
        return self._test(r'jcli : ', commands)

    def test_show(self):
        order = '0'
        cid = 'smpp1'
        extraCommands = [{'command': 'order %s' % order}, 
                         {'command': 'type DefaultRoute'},
                         {'command': 'rate 0.0'},
                         {'command': 'connector %s' % cid}]
        self.add_mtroute('jcli : ', extraCommands)

        commands = [{'command': 'mtrouter -s %s' % order, 'expect': 'DefaultRoute to cid:%s' % cid}]
        return self._test(r'jcli : ', commands)
    
    def test_update_not_available(self):
        order = '0'
        extraCommands = [{'command': 'order %s' % order}, 
                         {'command': 'type DefaultRoute'},
                         {'command': 'rate 0.0'},
                         {'command': 'connector smpp1'}]
        self.add_mtroute('jcli : ', extraCommands)

        commands = [{'command': 'mtrouter -u %s' % order, 'expect': 'no such option\: -u'}]
        return self._test(r'jcli : ', commands)

    def test_remove_invalid_order(self):
        commands = [{'command': 'mtrouter -r invalid_cid', 'expect': r'MT Route order must be a positive integer'}]
        self._test(r'jcli : ', commands)

        commands = [{'command': 'mtrouter -r 66', 'expect': r'Unknown MT Route: 66'}]
        return self._test(r'jcli : ', commands)
    
    def test_remove(self):
        order = '0'
        extraCommands = [{'command': 'order %s' % order}, 
                         {'command': 'type DefaultRoute'},
                         {'command': 'rate 0.0'},
                         {'command': 'connector smpp1'}]
        self.add_mtroute('jcli : ', extraCommands)
    
        commands = [{'command': 'mtrouter -r %s' % order, 'expect': r'Successfully removed MT Route with order\:%s' % order}]
        return self._test(r'jcli : ', commands)
    
    def test_flush(self):
        # Add 2 Routes:
        extraCommands = [{'command': 'order 0'}, 
                         {'command': 'type DefaultRoute'},
                         {'command': 'rate 0.0'},
                         {'command': 'connector smpp1'}]
        self.add_mtroute('jcli : ', extraCommands)

        extraCommands = [{'command': 'order 20'}, 
                         {'command': 'type StaticMtRoute'},
                         {'command': 'connector smpp1'},
                         {'command': 'rate 0.0'},
                         {'command': 'filters f1'}]
        self.add_mtroute('jcli : ', extraCommands)

        # List
        expectedList = ['#MT Route order   Type                    Rate    Connector ID\(s\)                  Filter\(s\)', 
                        '#20               StaticMTRoute           0.00    smpp1                            \<TransparentFilter\>', 
                        '#0                DefaultRoute            0.00    smpp1', 
                        'Total MT Routes: 2']
        commands = [{'command': 'mtrouter -l', 'expect': expectedList}]
        self._test(r'jcli : ', commands)
        
        # Flush
        commands = [{'command': 'mtrouter -f', 'expect': 'Successfully flushed MT Route table \(2 flushed entries\)'}]
        self._test(r'jcli : ', commands)
        
        # Relist
        commands = [{'command': 'mtrouter -l', 'expect': r'Total MT Routes: 0'}]
        return self._test(r'jcli : ', commands)

    def test_remove_and_list(self):
        # Add
        order = '0'
        extraCommands = [{'command': 'order %s' % order}, 
                         {'command': 'type DefaultRoute'},
                         {'command': 'rate 0.0'},
                         {'command': 'connector smpp1'}]
        self.add_mtroute('jcli : ', extraCommands)
    
        expectedList = ['#MT Route order   Type                    Rate    Connector ID\(s\)                  Filter\(s\)', 
                        '#0                DefaultRoute            0.00    smpp1', 
                        'Total MT Routes: 1']
        commands = [{'command': 'mtrouter -l', 'expect': expectedList}]
        self._test(r'jcli : ', commands)
    
        # Remove
        commands = [{'command': 'mtrouter -r %s' % order, 'expect': r'Successfully removed MT Route with order\:%s' % order}]
        self._test(r'jcli : ', commands)

        # List again
        commands = [{'command': 'mtrouter -l', 'expect': r'Total MT Routes: 0'}]
        return self._test(r'jcli : ', commands)
    
class MtRouteTypingTestCases(MxRouterTestCases):
    
    def test_available_mtroutes(self):
        # Go to MTRoute adding invite
        commands = [{'command': 'mtrouter -a'}]
        self._test(r'>', commands)
        
        # Send 'type' command without any arg in order to get
        # the available mtroutes from the error string
        self.sendCommand('type')
        receivedLines = self.getBuffer(True)
        
        filters = []
        results = re.findall(' (\w+)Route', receivedLines[3])
        filters.extend('%sRoute' % item for item in results[:])
        
        # Any new filter must be added here
        self.assertEqual(filters, ['DefaultRoute', 'StaticMTRoute', 
                                   'RandomRoundrobinMTRoute'])
        
        # Check if MtRouteTypingTestCases is covering all the mtroutes
        for f in filters:
            self.assertIn('test_add_%s' % f, dir(self), '%s mtroute is not tested !' % f)
            
    def test_add_DefaultRoute(self):
        rorder = '0'
        rtype = 'DefaultRoute'
        cid = 'smpp1'
        rate = '0.00'
        _str_ = '%s to cid:%s NOT RATED' % (rtype, cid)
        
        # Add MTRoute
        extraCommands = [{'command': 'order %s' % rorder}, 
                         {'command': 'type %s' % rtype},
                         {'command': 'rate %s' % rate},
                         {'command': 'connector %s' % cid}]
        self.add_mtroute('jcli : ', extraCommands)

        # Make asserts
        expectedList = ['%s' % _str_]
        self._test('jcli : ', [{'command': 'mtrouter -s %s' % rorder, 'expect': expectedList}])
        expectedList = ['#MT Route order   Type                    Rate    Connector ID\(s\)                  Filter\(s\)', 
                        '#%s %s %s %s ' % (rorder.ljust(16), rtype.ljust(23), rate.ljust(7), cid.ljust(32)), 
                        'Total MT Routes: 1']
        commands = [{'command': 'mtrouter -l', 'expect': expectedList}]
        return self._test(r'jcli : ', commands)
    
    def test_add_StaticMTRoute(self):
        rorder = '10'
        rtype = 'StaticMTRoute'
        cid = 'smpp1'
        rate = '0.00'
        fid = 'f1'
        _str_ = '%s to cid:%s NOT RATED' % (rtype, cid)
        
        # Add MTRoute
        extraCommands = [{'command': 'order %s' % rorder}, 
                         {'command': 'type %s' % rtype},
                         {'command': 'connector %s' % cid},
                         {'command': 'rate %s' % rate},
                         {'command': 'filters %s' % fid}]
        self.add_mtroute('jcli : ', extraCommands)

        # Make asserts
        expectedList = ['%s' % _str_]
        self._test('jcli : ', [{'command': 'mtrouter -s %s' % rorder, 'expect': expectedList}])
        expectedList = ['#MT Route order   Type                    Rate    Connector ID\(s\)                  Filter\(s\)', 
                        '#%s %s %s %s <TransparentFilter>' % (rorder.ljust(16), rtype.ljust(23), rate.ljust(7), cid.ljust(32)), 
                        'Total MT Routes: 1']
        commands = [{'command': 'mtrouter -l', 'expect': expectedList}]
        return self._test(r'jcli : ', commands)
        
    def test_add_RandomRoundrobinMTRoute(self):
        rorder = '10'
        rtype = 'RandomRoundrobinMTRoute'
        cid1 = 'smpp1'
        cid2 = 'smpp2'
        rate = '0.00'
        fid = 'f1'
        _str_ = ['%s to 2 connectors:' % rtype, '\t- %s' % cid1, '\t- %s' % cid2, 'NOT RATED']
        
        # Add MTRoute
        extraCommands = [{'command': 'order %s' % rorder}, 
                         {'command': 'type %s' % rtype},
                         {'command': 'connectors %s;%s' % (cid1, cid2)},
                         {'command': 'rate %s' % rate},
                         {'command': 'filters %s' % fid}]
        self.add_mtroute('jcli : ', extraCommands)

        # Make asserts
        expectedList = _str_
        self._test('jcli : ', [{'command': 'mtrouter -s %s' % rorder, 'expect': expectedList}])
        expectedList = ['#MT Route order   Type                    Rate    Connector ID\(s\)                  Filter\(s\)', 
                        '#%s %s %s %s <TransparentFilter>' % (rorder.ljust(16), rtype.ljust(23), rate.ljust(7), (cid1+', '+cid2).ljust(32)), 
                        'Total MT Routes: 1']
        commands = [{'command': 'mtrouter -l', 'expect': expectedList}]
        return self._test(r'jcli : ', commands)
        
class MtRouteArgsTestCases(MxRouterTestCases):
    
    def test_add_defaultroute_with_nonzero_order(self):
        """The route order will be forced to 0 when route type is set to
        DefaultRoute just after indicating the order
        """
        extraCommands = [{'command': 'order 20'}, 
                         {'command': 'type DefaultRoute'},
                         {'command': 'rate 0.0'},
                         {'command': 'connector smpp1'}]
        self.add_mtroute(r'jcli : ', extraCommands)

        expectedList = ['#MT Route order   Type                    Rate    Connector ID\(s\)                  Filter\(s\)', 
                        '#0                DefaultRoute            0.00    smpp1', 
                        'Total MT Routes: 1']
        commands = [{'command': 'mtrouter -l', 'expect': expectedList}]
        return self._test(r'jcli : ', commands)

    def test_add_defaultroute_without_indicating_order(self):
        """The route order will be set to 0 when route type is set to
        DefaultRoute without the need to indicate the order
        """
        extraCommands = [{'command': 'type DefaultRoute'},
                         {'command': 'rate 0.0'},
                         {'command': 'connector smpp1'}]
        self.add_mtroute(r'jcli : ', extraCommands)

        expectedList = ['#MT Route order   Type                    Rate    Connector ID\(s\)                  Filter\(s\)', 
                        '#0                DefaultRoute            0.00    smpp1', 
                        'Total MT Routes: 1']
        commands = [{'command': 'mtrouter -l', 'expect': expectedList}]
        return self._test(r'jcli : ', commands)

    def test_add_nondefaultroute_with_zero_order(self):
        """The only route that can have a Zero as it's order is the DefaultRoute
        """
        commands = [{'command': 'mtrouter -a'}, 
                    {'command': 'order 0'}, 
                    {'command': 'type StaticMTRoute'},
                    {'command': 'rate 0.0'},
                    {'command': 'connector smpp1'},
                    {'command': 'filters f1'},
                    {'command': 'ok', 'expect': 'Failed adding MTRoute, check log for details'}]
        return self._test(r'> ', commands)

    def test_add_incompatible_connector(self):
        commands = [{'command': 'mtrouter -a'}, 
                    {'command': 'type DefaultRoute'},
                    {'command': 'connector http1', 'expect': 'Unknown cid: http1'},
                    {'command': 'ok', 'expect': 'You must set these options before saving: type, order, connector'}]
        return self._test(r'> ', commands)

    def test_add_incompatible_filter(self):
        commands = [{'command': 'mtrouter -a'}, 
                    {'command': 'order 20'}, 
                    {'command': 'type StaticMTRoute'},
                    {'command': 'connector smpp1'},
                    {'command': 'filters cf1', 'expect': 'ConnectorFilter#cf1 is not a valid filter for MTRoute'},
                    {'command': 'ok', 'expect': 'You must set these options before saving: type, order, filters, connector'}]
        return self._test(r'> ', commands)
    
    def test_list_with_filter(self):
        extraCommands = [{'command': 'order 20'}, 
                         {'command': 'type StaticMTRoute'},
                         {'command': 'connector smpp1'},
                         {'command': 'rate 0.0'},
                         {'command': 'filters uf1'}]
        self.add_mtroute(r'jcli : ', extraCommands)
        
        expectedList = ['#MT Route order   Type                    Rate    Connector ID\(s\)                  Filter\(s\)', 
                        '#20               StaticMTRoute           0.00    smpp1                            <UserFilter \(uid=Any\)>', 
                        'Total MT Routes: 1']
        commands = [{'command': 'mtrouter -l', 'expect': expectedList}]
        return self._test(r'jcli : ', commands)