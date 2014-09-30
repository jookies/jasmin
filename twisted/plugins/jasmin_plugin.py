import time
from zope.interface import implements
from twisted.python import usage, log
from twisted.plugin import IPlugin
from twisted.application import internet, service
from twisted.web import server
from twisted.spread import pb
from twisted.internet import defer
from jasmin.managers.clients import SMPPClientManagerPB
from jasmin.managers.configs import SMPPClientPBConfig
from jasmin.queues.configs import AmqpConfig
from jasmin.queues.factory import AmqpFactory
from jasmin.protocols.http.configs import HTTPApiConfig
from jasmin.protocols.http.server import HTTPApi
from jasmin.routing.router import RouterPB
from jasmin.routing.configs import RouterPBConfig, deliverSmHttpThrowerConfig, DLRThrowerConfig
from jasmin.routing.throwers import deliverSmHttpThrower, DLRThrower
from jasmin.redis.configs import RedisForJasminConfig
from jasmin.redis.client import ConnectionWithConfiguration
from jasmin.protocols.cli.factory import JCliFactory
from jasmin.protocols.cli.configs import JCliConfig
from twisted.cred import portal
from twisted.cred.checkers import AllowAnonymousAccess, InMemoryUsernamePasswordDatabaseDontUse
from jasmin.tools.cred.portal import JasminPBRealm
from jasmin.tools.spread.pb import JasminPBPortalRoot

class Options(usage.Options):

    optParameters = [
        ['config',                  'c', '/etc/jasmin/jasmin.cfg', 
         'Jasmin configuration file'],
        ['username',                'u', None, 
         'jCli username used to load configuration profile on startup'],
        ['password',                'p', None, 
         'jCli password used to load configuration profile on startup'],
        ['enable-dlr-thrower',      None, True, 
         'Start DLR Thrower service'],
        ['enable-deliver-thrower',  None, True, 
         'Start DeliverSm Thrower service'],
        ['enable-http-api',         None, True, 
         'Start HTTP API'],
        ['enable-jcli',             None, True, 
         'Start jCli console'],
        ]

class JasminServiceMaker:

    implements(service.IServiceMaker, IPlugin)

    tapname = "jasmin"
    description = "Open source SMS gateway"
    options = Options
    top_service = None
    components = {}
    
    def startAMQPBrokerService(self):
        "Start AMQP Broker"
        
        AMQPServiceConfigInstance = AmqpConfig(self.options['config'])
        self.components['amqp-broker-factory'] = AmqpFactory(AMQPServiceConfigInstance)
        self.components['amqp-broker-factory'].preConnect()
        
        # Add service
        AMQPBrokerService = internet.TCPClient(AMQPServiceConfigInstance.host, 
                                        AMQPServiceConfigInstance.port, 
                                        self.components['amqp-broker-factory'])
        AMQPBrokerService.setServiceParent(self.top_service)
    
    def startRouterPBService(self):
        "Start Router PB server"

        RouterPBConfigInstance = RouterPBConfig(self.options['config'])
        self.components['router-pb-factory'] = RouterPB()
        self.components['router-pb-factory'].setConfig(RouterPBConfigInstance)
        
        # Set authentication portal
        p = portal.Portal(JasminPBRealm(self.components['router-pb-factory']))
        if RouterPBConfigInstance.authentication:
            c = InMemoryUsernamePasswordDatabaseDontUse()
            c.addUser(RouterPBConfigInstance.admin_username, 
                      RouterPBConfigInstance.admin_password)
            p.registerChecker(c)
        else:
            p.registerChecker(AllowAnonymousAccess())
        jPBPortalRoot = JasminPBPortalRoot(p)
        
        # Add service
        RouterPBService = internet.TCPServer(RouterPBConfigInstance.port, 
                                    pb.PBServerFactory(jPBPortalRoot), 
                                    interface=RouterPBConfigInstance.bind)
        RouterPBService.setServiceParent(self.top_service)

        # AMQP Broker is used to listen to deliver_sm/dlr queues
        return self.components['router-pb-factory'].addAmqpBroker(self.components['amqp-broker-factory'])

    def startSMPPClientManagerPBService(self):
        "Start SMPP Client Manager PB server"

        SMPPClientPBConfigInstance = SMPPClientPBConfig(self.options['config'])
        self.components['smppcm-pb-factory'] = SMPPClientManagerPB()
        self.components['smppcm-pb-factory'].setConfig(SMPPClientPBConfigInstance)
        
        # Set authentication portal
        p = portal.Portal(JasminPBRealm(self.components['smppcm-pb-factory']))
        if SMPPClientPBConfigInstance.authentication:
            c = InMemoryUsernamePasswordDatabaseDontUse()
            c.addUser(SMPPClientPBConfigInstance.admin_username, SMPPClientPBConfigInstance.admin_password)
            p.registerChecker(c)
        else:
            p.registerChecker(AllowAnonymousAccess())
        jPBPortalRoot = JasminPBPortalRoot(p)

        # Add service
        clientManager = internet.TCPServer(SMPPClientPBConfigInstance.port, pb.PBServerFactory(jPBPortalRoot), 
                                           interface=SMPPClientPBConfigInstance.bind)
        clientManager.setServiceParent(self.top_service)

        # AMQP Broker is used to listen to submit_sm queues and publish to deliver_sm/dlr queues
        self.components['smppcm-pb-factory'].addAmqpBroker(self.components['amqp-broker-factory'])
        self.components['smppcm-pb-factory'].addRedisClient(self.components['rc'])
    
    def startDeliverSmHttpThrowerService(self):
        "Start deliverSmHttpThrower"
        
        deliverThrowerConfigInstance = deliverSmHttpThrowerConfig(self.options['config'])
        deliverThrower = deliverSmHttpThrower()
        deliverThrower.setConfig(deliverThrowerConfigInstance)

        deliverThrower.setServiceParent(self.top_service)

        # AMQP Broker is used to listen to deliver_sm queue
        return deliverThrower.addAmqpBroker(self.components['amqp-broker-factory'])

    def startDLRThrowerService(self):
        "Start DLRThrower"
        
        DLRThrowerConfigInstance = DLRThrowerConfig(self.options['config'])
        _DLRThrower = DLRThrower()
        _DLRThrower.setConfig(DLRThrowerConfigInstance)

        _DLRThrower.setServiceParent(self.top_service)

        # AMQP Broker is used to listen to DLRThrower queue
        return _DLRThrower.addAmqpBroker(self.components['amqp-broker-factory'])

    def startHTTPApiService(self):
        "Start HTTP Api"
        
        httpApiConfigInstance = HTTPApiConfig(self.options['config'])
        httpApi_f = HTTPApi(self.components['router-pb-factory'], self.components['smppcm-pb-factory'], httpApiConfigInstance)
        
        httpApi = internet.TCPServer(httpApiConfigInstance.port, 
                                     server.Site(httpApi_f, 
                                                 logPath=httpApiConfigInstance.access_log
                                                 ), 
                                     interface=httpApiConfigInstance.bind
                                     )
        httpApi.setServiceParent(self.top_service)

    def startJCliService(self):
        loadConfigProfileWithCreds = {'username': self.options['username'], 'password': self.options['password']}
        JCliConfigInstance = JCliConfig(self.options['config'])
        JCli_f = JCliFactory(JCliConfigInstance, self.components['smppcm-pb-factory'], self.components['router-pb-factory'], loadConfigProfileWithCreds)
        
        jcli = internet.TCPServer(JCliConfigInstance.port, JCli_f, interface=JCliConfigInstance.bind)
        jcli.setServiceParent(self.top_service)

    def makeService(self, options):
        self.top_service = service.MultiService()
        self.options = options

        # Use standard python logger with twistd:
        # c.f: http://twistedmatrix.com/documents/current/core/howto/logging.htl#auto3
        log.PythonLoggingObserver().start()

        ########################################################
        # Connect to redis server
        RedisForJasminConfigInstance = RedisForJasminConfig(self.options['config'])
        self.components['rc'] = ConnectionWithConfiguration(RedisForJasminConfigInstance)
        # Authenticate and select db
        if RedisForJasminConfigInstance.password is not None:
            self.components['rc'].auth(RedisForJasminConfigInstance.password)
            self.components['rc'].select(RedisForJasminConfigInstance.dbid)

        ########################################################
        # Start AMQP Broker
        self.startAMQPBrokerService()

        ########################################################
        # Start SMPP Client connector manager and add rc
        self.startSMPPClientManagerPBService()

        ########################################################
        # Start Router PB server
        self.startRouterPBService()

        ########################################################
        # [optional] Start deliverSmHttpThrower
        if (self.options['enable-deliver-thrower'] == True or 
            (type(self.options['enable-deliver-thrower']) == str and 
             self.options['enable-deliver-thrower'].lower() == 'true')):
            self.startDeliverSmHttpThrowerService()
        
        ########################################################
        # [optional] Start DLRThrower
        if (self.options['enable-dlr-thrower'] == True or 
            (type(self.options['enable-dlr-thrower']) == str and 
             self.options['enable-dlr-thrower'].lower() == 'true')):
            self.startDLRThrowerService()
        
        ########################################################
        # [optional] Start HTTP Api
        if (self.options['enable-http-api'] == True or 
            (type(self.options['enable-http-api']) == str and 
             self.options['enable-http-api'].lower() == 'true')):
            self.startHTTPApiService()
        
        ########################################################
        # [optional] Start JCli server
        if (self.options['enable-jcli'] == True or 
            (type(self.options['enable-jcli']) == str and 
             self.options['enable-jcli'].lower() == 'true')):
            self.startJCliService()
        
        return self.top_service

service_maker = JasminServiceMaker()
