from jasmin.protocols.cli.factory import CmdFactory
from twisted.trial import unittest
from twisted.test import proto_helpers
from twisted.internet import reactor, defer

class ProtocolTestCases(unittest.TestCase):
    def getBuffer(self, clear = False, split = True):
        b = self.tr.value()

        if clear:
            self.tr.clear()
        if split:
            b = b.splitlines()

        return b

    @defer.inlineCallbacks
    def sendCommand(self, command, wait = None):
        self.proto.dataReceived('%s\r\n' % command)

        # Wait before getting recv buffer
        if wait is not None:
            exitDeferred = defer.Deferred()
            reactor.callLater(wait, exitDeferred.callback, None)
            yield exitDeferred

    @defer.inlineCallbacks
    def _test(self, finalPrompt, commands):
        receivedLines = None

        for cmd in commands:
            # Wait before getting recv buffer
            if 'wait' in cmd:
                yield self.sendCommand(cmd['command'], cmd['wait'])
            else:
                self.sendCommand(cmd['command'])

            # Get buffer and assert for `expect`
            receivedLines = self.getBuffer(True)
            #print receivedLines

            # First line is the command itself
            # 'noecho' is used when there's no echo back from the server while typing (e.g. password input)
            if 'noecho' not in cmd:
                self.assertEqual(receivedLines[0], cmd['command'])

            # Assert reply
            if 'expect' in cmd:
                if isinstance(cmd['expect'], str):
                    self.assertGreaterEqual(len(receivedLines), 4, 'Got no return from command %s: %s' % (cmd['command'], receivedLines))
                    receivedContent = ''
                    for line in range(len(receivedLines)):
                        if line % 3 == 0:
                            receivedContent+= receivedLines[line]
                    self.assertRegexpMatches(receivedContent, cmd['expect'])
                elif isinstance(cmd['expect'], list):
                    self.assertGreaterEqual(len(receivedLines), 3+(len(cmd['expect']) * 3), 'Got no return from command %s: %s' % (cmd['command'], receivedLines))

                    offset = 0
                    for e in cmd['expect']:
                        self.assertRegexpMatches(receivedLines[3 + offset], e)
                        offset += 3

        # Assert for final prompt
        if receivedLines is not None and finalPrompt is not None:
            self.assertRegexpMatches(receivedLines[len(receivedLines)-1], finalPrompt)

class CmdProtocolTestCases(ProtocolTestCases):
    def setUp(self):
        # Connect to CmdProtocol server through a fake network transport
        self.CmdCli_f = CmdFactory()
        self.proto = self.CmdCli_f.buildProtocol(('127.0.0.1', 0))
        self.tr = proto_helpers.StringTransport()
        self.proto.makeConnection(self.tr)
        # Test for greeting
        receivedLines = self.getBuffer(True)
        self.assertRegexpMatches(receivedLines[0], r'Welcome !')
        self.assertRegexpMatches(receivedLines[3], r'Session ref: ')

class BasicTestCase(CmdProtocolTestCases):
    def test_quit(self):
        commands = [{'command': 'quit'}]
        return self._test(None, commands)

    def test_help(self):
        expectedList = ['Available commands:',
                        '===================',
                        '',
                        'Control commands:',
                        '=================',
                        'quit                Disconnect from console',
                        'help                List available commands with "help" or detailed help with "help cmd".']
        commands = [{'command': 'help', 'expect': expectedList}]
        return self._test('>>> ', commands)
