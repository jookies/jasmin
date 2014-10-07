import re
from test_mxrouterm import MxRouterTestCases
    
class BasicTestCases(MxRouterTestCases):
    
    def test_add_with_minimum_args(self):
        extraCommands = [{'command': 'order 0'}, 
                         {'command': 'type DefaultRoute'},
                         {'command': 'connector http1'}]
        return self.add_moroute(r'jcli : ', extraCommands)
    
    def test_add_without_minimum_args(self):
        extraCommands = [{'command': 'ok', 'expect': r'You must set these options before saving: type, order'}]
        return self.add_moroute(r'> ', extraCommands)
    
    def test_add_invalid_key(self):
        extraCommands = [{'command': 'order 0'},
                         {'command': 'type DefaultRoute'},
                         {'command': 'connector http1'},
                         {'command': 'anykey anyvalue', 'expect': r'Unknown Route key: anykey'}]
        return self.add_moroute(r'jcli : ', extraCommands)
    
    def test_cancel_add(self):
        extraCommands = [{'command': 'order 1'},
                         {'command': 'ko'}]
        return self.add_moroute(r'jcli : ', extraCommands)

    def test_list(self):
        commands = [{'command': 'morouter -l', 'expect': r'Total MO Routes: 0'}]
        return self._test(r'jcli : ', commands)
    
    def test_add_and_list(self):
        extraCommands = [{'command': 'order 0'}, 
                         {'command': 'type DefaultRoute'},
                         {'command': 'connector http1'}]
        self.add_moroute('jcli : ', extraCommands)

        expectedList = ['#MO Route order   Type                    Connector ID\(s\)                  Filter\(s\)', 
                        '#0                DefaultRoute            http1', 
                        'Total MO Routes: 1']
        commands = [{'command': 'morouter -l', 'expect': expectedList}]
        return self._test(r'jcli : ', commands)
    
    def test_add_cancel_and_list(self):
        extraCommands = [{'command': 'type defaultroute'},
                         {'command': 'ko'}, ]
        self.add_moroute(r'jcli : ', extraCommands)

        commands = [{'command': 'morouter -l', 'expect': r'Total MO Routes: 0'}]
        return self._test(r'jcli : ', commands)

    def test_show(self):
        order = '0'
        cid = 'http1'
        extraCommands = [{'command': 'order %s' % order}, 
                         {'command': 'type DefaultRoute'},
                         {'command': 'connector %s' % cid}]
        self.add_moroute('jcli : ', extraCommands)

        commands = [{'command': 'morouter -s %s' % order, 'expect': 'DefaultRoute to cid:%s NOT RATED' % cid}]
        return self._test(r'jcli : ', commands)
    
    def test_update_not_available(self):
        order = '0'
        extraCommands = [{'command': 'order %s' % order}, 
                         {'command': 'type DefaultRoute'},
                         {'command': 'connector http1'}]
        self.add_moroute('jcli : ', extraCommands)

        commands = [{'command': 'morouter -u %s' % order, 'expect': 'no such option\: -u'}]
        return self._test(r'jcli : ', commands)

    def test_remove_invalid_order(self):
        commands = [{'command': 'morouter -r invalid_cid', 'expect': r'MO Route order must be a positive integer'}]
        self._test(r'jcli : ', commands)

        commands = [{'command': 'morouter -r 66', 'expect': r'Unknown MO Route: 66'}]
        return self._test(r'jcli : ', commands)
    
    def test_remove(self):
        order = '0'
        extraCommands = [{'command': 'order %s' % order}, 
                         {'command': 'type DefaultRoute'},
                         {'command': 'connector http1'}]
        self.add_moroute('jcli : ', extraCommands)
    
        commands = [{'command': 'morouter -r %s' % order, 'expect': r'Successfully removed MO Route with order\:%s' % order}]
        return self._test(r'jcli : ', commands)
    
    def test_flush(self):
        # Add 2 Routes:
        extraCommands = [{'command': 'order 0'}, 
                         {'command': 'type DefaultRoute'},
                         {'command': 'connector http1'}]
        self.add_moroute('jcli : ', extraCommands)

        extraCommands = [{'command': 'order 20'}, 
                         {'command': 'type StaticMoRoute'},
                         {'command': 'connector http1'},
                         {'command': 'filters f1'}]
        self.add_moroute('jcli : ', extraCommands)

        # List
        expectedList = ['#MO Route order   Type                    Connector ID\(s\)                  Filter\(s\)', 
                        '#20               StaticMORoute           http1                            \<TransparentFilter\>', 
                        '#0                DefaultRoute            http1', 
                        'Total MO Routes: 2']
        commands = [{'command': 'morouter -l', 'expect': expectedList}]
        self._test(r'jcli : ', commands)
        
        # Flush
        commands = [{'command': 'morouter -f', 'expect': 'Successfully flushed MO Route table \(2 flushed entries\)'}]
        self._test(r'jcli : ', commands)
        
        # Relist
        commands = [{'command': 'morouter -l', 'expect': r'Total MO Routes: 0'}]
        return self._test(r'jcli : ', commands)

    def test_remove_and_list(self):
        # Add
        order = '0'
        extraCommands = [{'command': 'order %s' % order}, 
                         {'command': 'type DefaultRoute'},
                         {'command': 'connector http1'}]
        self.add_moroute('jcli : ', extraCommands)
    
        expectedList = ['#MO Route order   Type                    Connector ID\(s\)                  Filter\(s\)', 
                        '#0                DefaultRoute            http1', 
                        'Total MO Routes: 1']
        commands = [{'command': 'morouter -l', 'expect': expectedList}]
        self._test(r'jcli : ', commands)
    
        # Remove
        commands = [{'command': 'morouter -r %s' % order, 'expect': r'Successfully removed MO Route with order\:%s' % order}]
        self._test(r'jcli : ', commands)

        # List again
        commands = [{'command': 'morouter -l', 'expect': r'Total MO Routes: 0'}]
        return self._test(r'jcli : ', commands)
    
class MoRouteTypingTestCases(MxRouterTestCases):
    
    def test_available_moroutes(self):
        # Go to MORoute adding invite
        commands = [{'command': 'morouter -a'}]
        self._test(r'>', commands)
        
        # Send 'type' command without any arg in order to get
        # the available moroutes from the error string
        self.sendCommand('type')
        receivedLines = self.getBuffer(True)
        
        filters = []
        results = re.findall(' (\w+)Route', receivedLines[3])
        filters.extend('%sRoute' % item for item in results[:])
        
        # Any new filter must be added here
        self.assertEqual(filters, ['DefaultRoute', 'StaticMORoute', 
                                   'RandomRoundrobinMORoute'])
        
        # Check if MoRouteTypingTestCases is covering all the moroutes
        for f in filters:
            self.assertIn('test_add_%s' % f, dir(self), '%s moroute is not tested !' % f)
            
    def test_add_DefaultRoute(self):
        rorder = '0'
        rtype = 'DefaultRoute'
        cid = 'http1'
        _str_ = '%s to cid:%s NOT RATED' % (rtype, cid)
        
        # Add MORoute
        extraCommands = [{'command': 'order %s' % rorder}, 
                         {'command': 'type %s' % rtype},
                         {'command': 'connector %s' % cid}]
        self.add_moroute('jcli : ', extraCommands)

        # Make asserts
        expectedList = ['%s' % _str_]
        self._test('jcli : ', [{'command': 'morouter -s %s' % rorder, 'expect': expectedList}])
        expectedList = ['#MO Route order   Type                    Connector ID\(s\)                  Filter\(s\)', 
                        '#%s %s %s ' % (rorder.ljust(16), rtype.ljust(23), cid.ljust(32)), 
                        'Total MO Routes: 1']
        commands = [{'command': 'morouter -l', 'expect': expectedList}]
        return self._test(r'jcli : ', commands)
    
    def test_add_StaticMORoute(self):
        rorder = '10'
        rtype = 'StaticMORoute'
        cid = 'http1'
        fid = 'f1'
        _str_ = '%s to cid:%s NOT RATED' % (rtype, cid)
        
        # Add MORoute
        extraCommands = [{'command': 'order %s' % rorder}, 
                         {'command': 'type %s' % rtype},
                         {'command': 'connector %s' % cid},
                         {'command': 'filters %s' % fid}]
        self.add_moroute('jcli : ', extraCommands)

        # Make asserts
        expectedList = ['%s' % _str_]
        self._test('jcli : ', [{'command': 'morouter -s %s' % rorder, 'expect': expectedList}])
        expectedList = ['#MO Route order   Type                    Connector ID\(s\)                  Filter\(s\)', 
                        '#%s %s %s <TransparentFilter>' % (rorder.ljust(16), rtype.ljust(23), cid.ljust(32)), 
                        'Total MO Routes: 1']
        commands = [{'command': 'morouter -l', 'expect': expectedList}]
        return self._test(r'jcli : ', commands)
        
    def test_add_RandomRoundrobinMORoute(self):
        rorder = '10'
        rtype = 'RandomRoundrobinMORoute'
        cid1 = 'http1'
        cid2 = 'http2'
        fid = 'f1'
        _str_ = ['%s to 2 connectors:' % rtype, '\t- %s' % cid1, '\t- %s' % cid2]
        
        # Add MORoute
        extraCommands = [{'command': 'order %s' % rorder}, 
                         {'command': 'type %s' % rtype},
                         {'command': 'connectors %s;%s' % (cid1, cid2)},
                         {'command': 'filters %s' % fid}]
        self.add_moroute('jcli : ', extraCommands)

        # Make asserts
        expectedList = _str_
        self._test('jcli : ', [{'command': 'morouter -s %s' % rorder, 'expect': expectedList}])
        expectedList = ['#MO Route order   Type                    Connector ID\(s\)                  Filter\(s\)', 
                        '#%s %s %s <TransparentFilter>' % (rorder.ljust(16), rtype.ljust(23), (cid1+', '+cid2).ljust(32)), 
                        'Total MO Routes: 1']
        commands = [{'command': 'morouter -l', 'expect': expectedList}]
        return self._test(r'jcli : ', commands)
        
class MoRouteArgsTestCases(MxRouterTestCases):
    
    def test_add_defaultroute_with_nonzero_order(self):
        """The route order will be forced to 0 when route type is set to
        DefaultRoute just after indicating the order
        """
        extraCommands = [{'command': 'order 20'}, 
                         {'command': 'type DefaultRoute'},
                         {'command': 'connector http1'}]
        self.add_moroute(r'jcli : ', extraCommands)

        expectedList = ['#MO Route order   Type                    Connector ID\(s\)                  Filter\(s\)', 
                        '#0                DefaultRoute            http1', 
                        'Total MO Routes: 1']
        commands = [{'command': 'morouter -l', 'expect': expectedList}]
        return self._test(r'jcli : ', commands)

    def test_add_defaultroute_without_indicating_order(self):
        """The route order will be set to 0 when route type is set to
        DefaultRoute without the need to indicate the order
        """
        extraCommands = [{'command': 'type DefaultRoute'},
                         {'command': 'connector http1'}]
        self.add_moroute(r'jcli : ', extraCommands)

        expectedList = ['#MO Route order   Type                    Connector ID\(s\)                  Filter\(s\)', 
                        '#0                DefaultRoute            http1', 
                        'Total MO Routes: 1']
        commands = [{'command': 'morouter -l', 'expect': expectedList}]
        return self._test(r'jcli : ', commands)

    def test_add_nondefaultroute_with_zero_order(self):
        """The only route that can have a Zero as it's order is the DefaultRoute
        """
        commands = [{'command': 'morouter -a'}, 
                    {'command': 'order 0'}, 
                    {'command': 'type StaticMORoute'},
                    {'command': 'connector http1'},
                    {'command': 'filters f1'},
                    {'command': 'ok', 'expect': 'Failed adding MORoute, check log for details'}]
        return self._test(r'> ', commands)

    def test_add_incompatible_connector(self):
        commands = [{'command': 'morouter -a'}, 
                    {'command': 'type DefaultRoute'},
                    {'command': 'connector smpp1', 'expect': 'Unknown cid: smpp1'},
                    {'command': 'ok', 'expect': 'You must set these options before saving: type, order, connector'}]
        return self._test(r'> ', commands)

    def test_add_incompatible_filter(self):
        commands = [{'command': 'morouter -a'}, 
                    {'command': 'order 20'}, 
                    {'command': 'type StaticMORoute'},
                    {'command': 'connector http1'},
                    {'command': 'filters uf1', 'expect': 'UserFilter#uf1 is not a valid filter for MORoute'},
                    {'command': 'ok', 'expect': 'You must set these options before saving: type, order, filters, connector'}]
        return self._test(r'> ', commands)
    
    def test_list_with_filter(self):
        extraCommands = [{'command': 'order 20'}, 
                         {'command': 'type StaticMORoute'},
                         {'command': 'connector http1'},
                         {'command': 'filters cf1'}]
        self.add_moroute(r'jcli : ', extraCommands)
        
        expectedList = ['#MO Route order   Type                    Connector ID\(s\)                  Filter\(s\)', 
                        '#20               StaticMORoute           http1                            <ConnectorFilter \(cid=Any\)>', 
                        'Total MO Routes: 1']
        commands = [{'command': 'morouter -l', 'expect': expectedList}]
        return self._test(r'jcli : ', commands)