import re
from twisted.internet import defer
from test_jcli import jCliWithoutAuthTestCases
    
class MxRouterTestCases(jCliWithoutAuthTestCases):
    @defer.inlineCallbacks
    def setUp(self):
        """Will add http and smpp connectors as well as some filters in
        order to run tests for morouterm and mtrouterm
        """
        yield jCliWithoutAuthTestCases.setUp(self)
        
        # Add an httpcc (cid = http1)
        commands = [{'command': 'httpccm -a'},
                    {'command': 'url http://127.0.0.1/Correct/Url'},
                    {'command': 'method post'},
                    {'command': 'cid http1'},
                    {'command': 'ok', 'expect': r'Successfully added'},
                    ]
        yield self._test(r'jcli : ', commands)
        
        # Add an httpcc (cid = http2)
        commands = [{'command': 'httpccm -a'},
                    {'command': 'url http://127.0.0.1/Correct/Url'},
                    {'command': 'method post'},
                    {'command': 'cid http2'},
                    {'command': 'ok', 'expect': r'Successfully added'},
                    ]
        yield self._test(r'jcli : ', commands)
        
        # Add an smppcc (cid = smpp1)
        commands = [{'command': 'smppccm -a'},
                    {'command': 'cid smpp1'},
                    {'command': 'ok', 'expect': r'Successfully added', 'wait': 0.4},
                    ]
        yield self._test(r'jcli : ', commands)
    
        # Add an smppcc (cid = smpp2)
        commands = [{'command': 'smppccm -a'},
                    {'command': 'cid smpp2'},
                    {'command': 'ok', 'expect': r'Successfully added', 'wait': 0.4},
                    ]
        yield self._test(r'jcli : ', commands)
    
        # Add a TransparentFilter (fid = f1)
        commands = [{'command': 'filter -a'},
                    {'command': 'fid f1'},
                    {'command': 'type TransparentFilter'},
                    {'command': 'ok', 'expect': r'Successfully added'},
                    ]
        yield self._test(r'jcli : ', commands)
    
        # Add a ConnectorFilter (fid = cf1)
        commands = [{'command': 'filter -a'},
                    {'command': 'fid cf1'},
                    {'command': 'type ConnectorFilter'},
                    {'command': 'cid Any'},
                    {'command': 'ok', 'expect': r'Successfully added'},
                    ]
        yield self._test(r'jcli : ', commands)
    
        # Add a UserFilter (fid = uf1)
        commands = [{'command': 'filter -a'},
                    {'command': 'fid uf1'},
                    {'command': 'type UserFilter'},
                    {'command': 'uid Any'},
                    {'command': 'ok', 'expect': r'Successfully added'},
                    ]
        yield self._test(r'jcli : ', commands)
    
    @defer.inlineCallbacks
    def add_moroute(self, finalPrompt, extraCommands = []):
        sessionTerminated = False
        commands = []
        commands.append({'command': 'morouter -a', 'expect': r'Adding a new MO Route\: \(ok\: save, ko\: exit\)'})
        for extraCommand in extraCommands:
            commands.append(extraCommand)
            
            if extraCommand['command'] in ['ok', 'ko']:
                sessionTerminated = True
        
        if not sessionTerminated:
            commands.append({'command': 'ok', 'expect': r'Successfully added MORoute \['})

        yield self._test(finalPrompt, commands)
    
    @defer.inlineCallbacks
    def add_mtroute(self, finalPrompt, extraCommands = []):
        sessionTerminated = False
        commands = []
        commands.append({'command': 'mtrouter -a', 'expect': r'Adding a new MT Route\: \(ok\: save, ko\: exit\)'})
        for extraCommand in extraCommands:
            commands.append(extraCommand)
            
            if extraCommand['command'] in ['ok', 'ko']:
                sessionTerminated = True
        
        if not sessionTerminated:
            commands.append({'command': 'ok', 
                             'expect': r'Successfully added MTRoute \[',
                             'wait': 0.4})

        yield self._test(finalPrompt, commands)