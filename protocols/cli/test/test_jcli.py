from test_protocol import ProtocolTestCases
from jasmin.protocols.cli.factory import JCliFactory
from jasmin.protocols.cli.configs import JCliConfig
from jasmin.routing.router import RouterPB
from jasmin.routing.configs import RouterPBConfig
from jasmin.managers.clients import SMPPClientManagerPB
from jasmin.managers.configs import SMPPClientPBConfig
from twisted.test import proto_helpers

class jCliTestCases(ProtocolTestCases):
    def setUp(self):
        # Instanciate a RouterPB (a requirement for JCliFactory)
        RouterPBConfigInstance = RouterPBConfig()
        RouterPB_f = RouterPB()
        RouterPB_f.setConfig(RouterPBConfigInstance)

        # Instanciate a SMPPClientManagerPB (a requirement for JCliFactory)
        SMPPClientPBConfigInstance = SMPPClientPBConfig()
        clientManager_f = SMPPClientManagerPB()
        clientManager_f.setConfig(SMPPClientPBConfigInstance)
        
        # Connect to jCli server through a fake network transport
        JCliConfigInstance = JCliConfig()
        self.JCli_f = JCliFactory(JCliConfigInstance, clientManager_f, RouterPB_f)
        self.proto = self.JCli_f.buildProtocol(('127.0.0.1', 0))
        self.tr = proto_helpers.StringTransport()
        self.proto.makeConnection(self.tr)
        # Test for greeting
        self.assertReceivedLines(['Welcome to Jasmin console', 
                             'Type help or ? to list commands.', 
                             'Session ref: '], self.getBuffer(True))
    
class SmppccmTestCase(jCliTestCases):
    def test_list(self):
        return self._test('smppccm -l', ['Total: 0', 'jcli :'])