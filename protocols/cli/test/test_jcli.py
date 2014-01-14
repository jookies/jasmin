from twisted.internet import reactor, defer
from test_protocol import ProtocolTestCases
from jasmin.protocols.cli.factory import JCliFactory
from jasmin.protocols.cli.configs import JCliConfig
from jasmin.routing.router import RouterPB
from jasmin.routing.configs import RouterPBConfig
from jasmin.managers.clients import SMPPClientManagerPB
from jasmin.managers.configs import SMPPClientPBConfig
from jasmin.queues.factory import AmqpFactory
from jasmin.queues.configs import AmqpConfig
from twisted.test import proto_helpers

class jCliTestCases(ProtocolTestCases):
    @defer.inlineCallbacks
    def setUp(self):
        # Launch AMQP Broker
        AMQPServiceConfigInstance = AmqpConfig()
        AMQPServiceConfigInstance.reconnectOnConnectionLoss = False
        self.amqpBroker = AmqpFactory(AMQPServiceConfigInstance)
        self.amqpBroker.preConnect()
        self.amqpClient = reactor.connectTCP(AMQPServiceConfigInstance.host, AMQPServiceConfigInstance.port, self.amqpBroker)
        
        # Wait for AMQP Broker connection to get ready
        yield self.amqpBroker.getChannelReadyDeferred()

        # Instanciate a RouterPB (a requirement for JCliFactory)
        RouterPBConfigInstance = RouterPBConfig()
        RouterPB_f = RouterPB()
        RouterPB_f.setConfig(RouterPBConfigInstance)

        # Instanciate a SMPPClientManagerPB (a requirement for JCliFactory)
        SMPPClientPBConfigInstance = SMPPClientPBConfig()
        clientManager_f = SMPPClientManagerPB()
        clientManager_f.setConfig(SMPPClientPBConfigInstance)
        clientManager_f.addAmqpBroker(self.amqpBroker)
        
        # Connect to jCli server through a fake network transport
        JCliConfigInstance = JCliConfig()
        self.JCli_f = JCliFactory(JCliConfigInstance, clientManager_f, RouterPB_f)
        self.proto = self.JCli_f.buildProtocol(('127.0.0.1', 0))
        self.tr = proto_helpers.StringTransport()
        self.proto.makeConnection(self.tr)
        # Test for greeting
        receivedLines = self.getBuffer(True)
        self.assertRegexpMatches(receivedLines[0], r'Welcome to Jasmin console')
        self.assertRegexpMatches(receivedLines[3], r'Type help or \? to list commands\.')
        self.assertRegexpMatches(receivedLines[9], r'Session ref: ')
        
    def tearDown(self):
        self.amqpClient.disconnect()
        
class BasicTestCase(jCliTestCases):
    def test_quit(self):
        commands = [{'command': 'quit'}]
        return self._test(None, commands)
    
    def test_help(self):
        expectedList = ['Available commands:', 
                        '===================', 
                        'user\t\tUser management', 
                        'group\t\tGroup management', 
                        'router\t\tRouter management', 
                        'smppccm\t\tSMPP connector management', 
                        '', 
                        'Control commands:', 
                        '=================', 
                        'quit\t\tDisconnect from console', 
                        'help\t\tList available commands with "help" or detailed help with "help cmd".']
        commands = [{'command': 'help', 'expect': expectedList}]
        return self._test('jcli : ', commands)