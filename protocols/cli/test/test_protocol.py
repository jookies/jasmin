from jasmin.protocols.cli.factory import CmdFactory
from twisted.trial import unittest
from twisted.test import proto_helpers

class ProtocolTestCases(unittest.TestCase):
    def getBuffer(self, clear = False, split = True):
        b = self.tr.value()
        
        if clear:
            self.tr.clear()
        if split:
            b = b.splitlines()
        
        return b
    
    def assertReceivedLines(self, lines, receivedLines, msg=None):
        i = 0
        for receivedLine in receivedLines:
            self.assertRegexpMatches(receivedLine, lines[i], msg)
            i+= 1
        return

    def _test(self, command, expected = None):
        self.proto.dataReceived('%s\r\n' % command)
        receivedLines = self.getBuffer(True)

        print receivedLines
        if expected is None:
            return
        else:
            self.assertReceivedLines(expected, receivedLines)

class CmdProtocolTestCases(ProtocolTestCases):
    def setUp(self):
        # Connect to CmdProtocol server through a fake network transport
        self.CmdCli_f = CmdFactory()
        self.proto = self.CmdCli_f.buildProtocol(('127.0.0.1', 0))
        self.tr = proto_helpers.StringTransport()
        self.proto.makeConnection(self.tr)
        # Test for greeting
        self.assertReceivedLines(['Welcome !', '', '', 'Session ref:', '', ''], self.getBuffer(True))
    
class BasicTestCase(CmdProtocolTestCases):
    def test_quit(self):
        return self._test('quit')
    
    def test_help(self):
        return self._test('help', ['help', '', '', 
                                   'Available commands:', '', '', 
                                   '===================', '', '', '', '', '', 
                                   'Control commands:', '', '', 
                                   '=================', '', '',
                                   r'quit\t\tDisconnect from console', '', '', 
                                   r'help\t\tList available commands with "help" or detailed help with "help cmd".', '', '', 
                                   '>>> '])