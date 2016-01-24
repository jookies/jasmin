import jasmin
from twisted.internet import reactor, defer
from test_cmdprotocol import ProtocolTestCases
from jasmin.protocols.cli.factory import JCliFactory
from jasmin.protocols.cli.configs import JCliConfig
from jasmin.routing.router import RouterPB
from jasmin.routing.configs import RouterPBConfig
from jasmin.managers.clients import SMPPClientManagerPB
from jasmin.managers.configs import SMPPClientPBConfig
from jasmin.queues.factory import AmqpFactory
from jasmin.queues.configs import AmqpConfig
from jasmin.protocols.smpp.configs import SMPPServerConfig
from jasmin.protocols.smpp.factory import SMPPServerFactory
from twisted.test import proto_helpers
from twisted.cred import portal
from jasmin.tools.cred.portal import SmppsRealm
from jasmin.tools.cred.checkers import RouterAuthChecker

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
        self.RouterPBConfigInstance = RouterPBConfig()
        self.RouterPBConfigInstance.authentication = False
        self.RouterPB_f = RouterPB()
        self.RouterPB_f.setConfig(self.RouterPBConfigInstance)

        # Instanciate a SMPPClientManagerPB (a requirement for JCliFactory)
        SMPPClientPBConfigInstance = SMPPClientPBConfig()
        SMPPClientPBConfigInstance.authentication = False
        self.clientManager_f = SMPPClientManagerPB()
        self.clientManager_f.setConfig(SMPPClientPBConfigInstance)
        yield self.clientManager_f.addAmqpBroker(self.amqpBroker)

        # Instanciate a SMPPServerFactory (a requirement for JCliFactory)
        SMPPServerConfigInstance = SMPPServerConfig()

        # Portal init
        _portal = portal.Portal(SmppsRealm(SMPPServerConfigInstance.id, self.RouterPB_f))
        _portal.registerChecker(RouterAuthChecker(self.RouterPB_f))

        self.SMPPSFactory = SMPPServerFactory(
            config = SMPPServerConfigInstance,
            auth_portal = _portal,
            RouterPB = self.RouterPB_f,
            SMPPClientManagerPB = self.clientManager_f)

    def tearDown(self):
        self.amqpClient.disconnect()
        self.RouterPB_f.cancelPersistenceTimer()

class jCliWithAuthTestCases(jCliTestCases):
    @defer.inlineCallbacks
    def setUp(self):
        yield jCliTestCases.setUp(self)

        # Connect to jCli server through a fake network transport
        self.JCliConfigInstance = JCliConfig()
        self.JCli_f = JCliFactory(
            self.JCliConfigInstance,
            self.clientManager_f,
            self.RouterPB_f,
            self.SMPPSFactory)
        self.proto = self.JCli_f.buildProtocol(('127.0.0.1', 0))
        self.tr = proto_helpers.StringTransport()
        self.proto.makeConnection(self.tr)
        # Test for greeting
        receivedLines = self.getBuffer(True)
        self.assertRegexpMatches(receivedLines[0], r'Authentication required.')

class jCliWithoutAuthTestCases(jCliTestCases):
    @defer.inlineCallbacks
    def setUp(self):
        yield jCliTestCases.setUp(self)

        # Connect to jCli server through a fake network transport
        self.JCliConfigInstance = JCliConfig()
        self.JCliConfigInstance.authentication = False
        self.JCli_f = JCliFactory(
            self.JCliConfigInstance,
            self.clientManager_f,
            self.RouterPB_f,
            self.SMPPSFactory)
        self.proto = self.JCli_f.buildProtocol(('127.0.0.1', 0))
        self.tr = proto_helpers.StringTransport()
        self.proto.makeConnection(self.tr)
        # Test for greeting
        receivedLines = self.getBuffer(True)
        self.assertRegexpMatches(receivedLines[0], r'Welcome to Jasmin %s console' % jasmin.get_release())
        self.assertRegexpMatches(receivedLines[3], r'Type help or \? to list commands\.')
        self.assertRegexpMatches(receivedLines[9], r'Session ref: ')

class BasicTestCases(jCliWithoutAuthTestCases):
    def test_quit(self):
        commands = [{'command': 'quit'}]
        return self._test(None, commands)

    def test_help(self):
        expectedList = ['Available commands:',
                        '===================',
                        'persist             Persist current configuration profile to disk in PROFILE',
                        'load                Load configuration PROFILE profile from disk',
                        'user                User management',
                        'group               Group management',
                        'filter              Filter management',
                        'mointerceptor       MO Interceptor management',
                        'mtinterceptor       MT Interceptor management',
                        'morouter            MO Router management',
                        'mtrouter            MT Router management',
                        'smppccm             SMPP connector management',
                        'httpccm             HTTP client connector management',
                        'stats               Stats management',
                        '',
                        'Control commands:',
                        '=================',
                        'quit                Disconnect from console',
                        'help                List available commands with "help" or detailed help with "help cmd".']
        commands = [{'command': 'help', 'expect': expectedList}]
        return self._test('jcli : ', commands)

class PersistanceTestCases(jCliWithoutAuthTestCases):

    @defer.inlineCallbacks
    def test_persist(self):
        expectedList = [r'mtrouter configuration persisted \(profile:jcli-prod\)',
                        r'smppcc configuration persisted \(profile\:jcli-prod\)',
                        r'group configuration persisted \(profile\:jcli-prod\)',
                        r'user configuration persisted \(profile\:jcli-prod\)',
                        r'httpcc configuration persisted \(profile\:jcli-prod\)',
                        r'mointerceptor configuration persisted \(profile\:jcli-prod\)',
                        r'filter configuration persisted \(profile\:jcli-prod\)',
                        r'mtinterceptor configuration persisted \(profile\:jcli-prod\)',
                        r'morouter configuration persisted \(profile\:jcli-prod\)',
                        ]
        commands = [{'command': 'persist', 'expect': expectedList}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_persist_profile(self):
        expectedList = [r'mtrouter configuration persisted \(profile:testprofile\)',
                        r'smppcc configuration persisted \(profile\:testprofile\)',
                        r'group configuration persisted \(profile\:testprofile\)',
                        r'user configuration persisted \(profile\:testprofile\)',
                        r'httpcc configuration persisted \(profile\:testprofile\)',
                        r'mointerceptor configuration persisted \(profile\:testprofile\)',
                        r'filter configuration persisted \(profile\:testprofile\)',
                        r'mtinterceptor configuration persisted \(profile\:testprofile\)',
                        r'morouter configuration persisted \(profile\:testprofile\)',
                        ]
        commands = [{'command': 'persist -p testprofile', 'expect': expectedList}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_load(self):
        # Persist before load to avoid getting a failure
        commands = [{'command': 'persist'}]
        yield self._test(r'jcli : ', commands)

        expectedList = [r'mtrouter configuration loaded \(profile\:jcli-prod\)',
                        r'smppcc configuration loaded \(profile\:jcli-prod\)',
                        r'group configuration loaded \(profile\:jcli-prod\)',
                        r'user configuration loaded \(profile\:jcli-prod\)',
                        r'httpcc configuration loaded \(profile\:jcli-prod\)',
                        r'mointerceptor configuration loaded \(profile\:jcli-prod\)',
                        r'filter configuration loaded \(profile\:jcli-prod\)',
                        r'mtinterceptor configuration loaded \(profile\:jcli-prod\)',
                        r'morouter configuration loaded \(profile\:jcli-prod\)',
                        ]
        commands = [{'command': 'load', 'expect': expectedList}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_load_profile(self):
        # Persist before load to avoid getting a failure
        commands = [{'command': 'persist -p testprofile'}]
        yield self._test(r'jcli : ', commands)

        expectedList = [r'mtrouter configuration loaded \(profile\:testprofile\)',
                        r'smppcc configuration loaded \(profile\:testprofile\)',
                        r'group configuration loaded \(profile\:testprofile\)',
                        r'user configuration loaded \(profile\:testprofile\)',
                        r'httpcc configuration loaded \(profile\:testprofile\)',
                        r'mointerceptor configuration loaded \(profile\:testprofile\)',
                        r'filter configuration loaded \(profile\:testprofile\)',
                        r'mtinterceptor configuration loaded \(profile\:testprofile\)',
                        r'morouter configuration loaded \(profile\:testprofile\)',
                        ]
        commands = [{'command': 'load -p testprofile', 'expect': expectedList}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_load_unknown_profile(self):
        expectedList = [r'Failed to load mtrouter configuration \(profile\:any_profile\)',
                        r'Failed to load smppcc configuration \(profile\:any_profile\)',
                        r'Failed to load group configuration \(profile\:any_profile\)',
                        r'Failed to load user configuration \(profile\:any_profile\)',
                        r'Failed to load httpcc configuration \(profile\:any_profile\)',
                        r'Failed to load mointerceptor configuration \(profile\:any_profile\)',
                        r'Failed to load filter configuration \(profile\:any_profile\)',
                        r'Failed to load mtinterceptor configuration \(profile\:any_profile\)',
                        r'Failed to load morouter configuration \(profile\:any_profile\)',
                        ]
        commands = [{'command': 'load -p any_profile', 'expect': expectedList}]
        yield self._test(r'jcli : ', commands)

class LoadingTestCases(jCliWithoutAuthTestCases):
    """The 2 test cases below will ensure that persisted configurations to the default profile
    will be automatically loaded if jCli restarts
    """

    @defer.inlineCallbacks
    def setUp(self):
        yield jCliWithoutAuthTestCases.setUp(self)

    @defer.inlineCallbacks
    def test_01_persist_all_configurations(self):
        # Add Group
        commands = [{'command': 'group -a'},
                    {'command': 'gid g1'},
                    {'command': 'ok', 'expect': 'Successfully added Group'}]
        yield self._test(r'jcli : ', commands)
        # Add User
        commands = [{'command': 'user -a'},
                    {'command': 'gid g1'},
                    {'command': 'uid u1'},
                    {'command': 'username nathalie'},
                    {'command': 'password fpwd'},
                    {'command': 'ok', 'expect': 'Successfully added User'}]
        yield self._test(r'jcli : ', commands)
        # Add HTTP Connector
        commands = [{'command': 'httpccm -a'},
                    {'command': 'url http://127.0.0.1'},
                    {'command': 'method POST'},
                    {'command': 'cid http1'},
                    {'command': 'ok', 'expect': 'Successfully added Httpcc'}]
        yield self._test(r'jcli : ', commands)
        # Add SMPP Connector
        commands = [{'command': 'smppccm -a'},
                    {'command': 'cid smpp1'},
                    {'command': 'ok', 'expect': 'Successfully added connector', 'wait': 0.2}]
        yield self._test(r'jcli : ', commands)
        # Add Filter for MT route
        commands = [{'command': 'filter -a'},
                    {'command': 'type UserFilter'},
                    {'command': 'uid u1'},
                    {'command': 'fid fMT1'},
                    {'command': 'ok', 'expect': 'Successfully added Filter'}]
        yield self._test(r'jcli : ', commands)
        # Add Filter for MO route
        commands = [{'command': 'filter -a'},
                    {'command': 'type ConnectorFilter'},
                    {'command': 'cid smpp1'},
                    {'command': 'fid fMO1'},
                    {'command': 'ok', 'expect': 'Successfully added Filter'}]
        yield self._test(r'jcli : ', commands)
        # Add Default MO route
        commands = [{'command': 'morouter -a'},
                    {'command': 'type defaultroute'},
                    {'command': 'connector http(http1)'},
                    {'command': 'ok', 'expect': 'Successfully added MORoute'}]
        yield self._test(r'jcli : ', commands)
        # Add static MO route
        commands = [{'command': 'morouter -a'},
                    {'command': 'type staticmoroute'},
                    {'command': 'filters fMO1'},
                    {'command': 'order 100'},
                    {'command': 'connector http(http1)'},
                    {'command': 'rate 0.0'},
                    {'command': 'ok', 'expect': 'Successfully added MORoute'}]
        yield self._test(r'jcli : ', commands)
        # Add static MO route
        commands = [{'command': 'morouter -a'},
                    {'command': 'type staticmoroute'},
                    {'command': 'filters fMO1'},
                    {'command': 'order 200'},
                    {'command': 'connector smpps(smppuser)'},
                    {'command': 'rate 0.0'},
                    {'command': 'ok', 'expect': 'Successfully added MORoute'}]
        yield self._test(r'jcli : ', commands)
        # Add Default MT route
        commands = [{'command': 'mtrouter -a'},
                    {'command': 'type defaultroute'},
                    {'command': 'connector smppc(smpp1)'},
                    {'command': 'rate 0.0'},
                    {'command': 'ok', 'expect': 'Successfully added MTRoute'}]
        yield self._test(r'jcli : ', commands)
        # Add static MT route
        commands = [{'command': 'mtrouter -a'},
                    {'command': 'type staticmtroute'},
                    {'command': 'filters fMT1'},
                    {'command': 'order 100'},
                    {'command': 'connector smppc(smpp1)'},
                    {'command': 'rate 0.0'},
                    {'command': 'ok', 'expect': 'Successfully added MTRoute'}]
        yield self._test(r'jcli : ', commands)

        # Finally persist to disk
        commands = [{'command': 'persist'}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_02_check_automatic_load_after_jcli_reboot(self):
        # The conf loading on startup is made through JCliFactory.doStart() method
        # and is emulated below:
        yield self.sendCommand('load', 0.2)
        # Clear buffer before beginning asserts
        self.tr.clear()

        # Assert Group
        yield self.sendCommand('group -l')
        self.assertEqual(self.getBuffer(True)[9], 'Total Groups: 1')
        # Assert User
        yield self.sendCommand('user -l')
        self.assertEqual(self.getBuffer(True)[9], 'Total Users: 1')
        # Assert HTTP Connector
        yield self.sendCommand('httpccm -l')
        self.assertEqual(self.getBuffer(True)[9], 'Total Httpccs: 1')
        # Assert SMPP Connector
        yield self.sendCommand('smppccm -l')
        self.assertEqual(self.getBuffer(True)[9], 'Total connectors: 1')
        # Assert Filters
        yield self.sendCommand('filter -l')
        self.assertEqual(self.getBuffer(True)[12], 'Total Filters: 2')
        # Assert MO Routes
        yield self.sendCommand('morouter -l')
        self.assertEqual(self.getBuffer(True)[15], 'Total MO Routes: 3')
        # Assert MT Routes
        yield self.sendCommand('mtrouter -l')
        self.assertEqual(self.getBuffer(True)[12], 'Total MT Routes: 2')
