import re
from twisted.internet import defer
from test_jcli import jCliWithoutAuthTestCases

class MxInterceptorTestCases(jCliWithoutAuthTestCases):
    @defer.inlineCallbacks
    def setUp(self):
        """Will add filters in
        order to run tests for mointerceptorm and mtinterceptorm as well as
        some dummy script files
        """
        yield jCliWithoutAuthTestCases.setUp(self)

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

        # Create script files
        self.invalid_syntax = '/tmp/invalid_syntax.py'
        self.file_not_found = '/file/not/found'
        self.valid_script = '/tmp/valid_script.py'

        with open(self.invalid_syntax, 'w') as fh:
            fh.write('Something to throw a syntax error')
        with open(self.valid_script, 'w') as fh:
            fh.write('print "hello  world"')

    @defer.inlineCallbacks
    def add_mointerceptor(self, finalPrompt, extraCommands = []):
        sessionTerminated = False
        commands = []
        commands.append({'command': 'mointerceptor -a', 'expect': r'Adding a new MO Interceptor\: \(ok\: save, ko\: exit\)'})
        for extraCommand in extraCommands:
            commands.append(extraCommand)

            if extraCommand['command'] in ['ok', 'ko']:
                sessionTerminated = True

        if not sessionTerminated:
            commands.append({'command': 'ok', 'expect': r'Successfully added MOInterceptor \['})

        yield self._test(finalPrompt, commands)

    @defer.inlineCallbacks
    def add_mtinterceptor(self, finalPrompt, extraCommands = []):
        sessionTerminated = False
        commands = []
        commands.append({'command': 'mtinterceptor -a', 'expect': r'Adding a new MT Interceptor\: \(ok\: save, ko\: exit\)'})
        for extraCommand in extraCommands:
            commands.append(extraCommand)

            if extraCommand['command'] in ['ok', 'ko']:
                sessionTerminated = True

        if not sessionTerminated:
            commands.append({'command': 'ok',
                             'expect': r'Successfully added MTInterceptor \[',
                             'wait': 0.4})

        yield self._test(finalPrompt, commands)
