#!/usr/bin/python

import sys
import signal
import syslog
from twisted.python import usage
from jasmin.managers.clients import SMPPClientManagerPB
from jasmin.managers.configs import SMPPClientPBConfig
from jasmin.queues.configs import AmqpConfig
from jasmin.queues.factory import AmqpFactory
from jasmin.protocols.smpp.configs import SMPPServerConfig
from jasmin.protocols.smpp.factory import SMPPServerFactory
from jasmin.protocols.http.configs import HTTPApiConfig
from jasmin.protocols.http.server import HTTPApi
from jasmin.tools.cred.portal import SmppsRealm
from jasmin.tools.cred.checkers import RouterAuthChecker
from jasmin.routing.router import RouterPB
from jasmin.routing.configs import RouterPBConfig, deliverSmThrowerConfig, DLRThrowerConfig
from jasmin.routing.throwers import deliverSmThrower, DLRThrower
from jasmin.redis.configs import RedisForJasminConfig
from jasmin.redis.client import ConnectionWithConfiguration
from jasmin.protocols.cli.factory import JCliFactory
from jasmin.protocols.cli.configs import JCliConfig
from twisted.cred import portal
from twisted.cred.checkers import AllowAnonymousAccess, InMemoryUsernamePasswordDatabaseDontUse
from jasmin.tools.cred.portal import JasminPBRealm
from jasmin.tools.spread.pb import JasminPBPortalRoot
from twisted.web import server
from twisted.spread import pb
from twisted.internet import reactor, defer

class Options(usage.Options):

    optParameters = [
        ['config',                  'c', '/etc/jasmin/jasmin.cfg', 
         'Jasmin configuration file'],
        ['username',                'u', None, 
         'jCli username used to load configuration profile on startup'],
        ['password',                'p', None, 
         'jCli password used to load configuration profile on startup'],
        ['enable-smpp-server',      None, True, 
         'Start SMPP Server service'],
        ['enable-dlr-thrower',      None, True, 
         'Start DLR Thrower service'],
        ['enable-deliver-thrower',  None, True, 
         'Start DeliverSm Thrower service'],
        ['enable-http-api',         None, True, 
         'Start HTTP API'],
        ['enable-jcli',             None, True, 
         'Start jCli console'],
        ]

class JasminDaemon:
    
    def __init__(self, options):
        self.options = options
        self.components = {}
    
    @defer.inlineCallbacks
    def startRedisClient(self):
        "Start AMQP Broker"
        RedisForJasminConfigInstance = RedisForJasminConfig(self.options['config'])
        self.components['rc'] = yield ConnectionWithConfiguration(RedisForJasminConfigInstance)
        # Authenticate and select db
        if RedisForJasminConfigInstance.password is not None:
            yield self.components['rc'].auth(RedisForJasminConfigInstance.password)
            yield self.components['rc'].select(RedisForJasminConfigInstance.dbid)

    def stopRedisClient(self):
        "Stop AMQP Broker"
        return self.components['rc'].disconnect()

    def startAMQPBrokerService(self):
        "Start AMQP Broker"
        
        AMQPServiceConfigInstance = AmqpConfig(self.options['config'])
        self.components['amqp-broker-factory'] = AmqpFactory(AMQPServiceConfigInstance)
        self.components['amqp-broker-factory'].preConnect()
        
        # Add service
        self.components['amqp-broker-client'] = reactor.connectTCP(AMQPServiceConfigInstance.host, 
                                        AMQPServiceConfigInstance.port, 
                                        self.components['amqp-broker-factory'])
    
    def stopAMQPBrokerService(self):
        "Stop AMQP Broker"
        
        return self.components['amqp-broker-client'].disconnect()

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
        self.components['router-pb-server'] = reactor.listenTCP(RouterPBConfigInstance.port, 
                                    pb.PBServerFactory(jPBPortalRoot), 
                                    interface=RouterPBConfigInstance.bind)

        # AMQP Broker is used to listen to deliver_sm/dlr queues
        return self.components['router-pb-factory'].addAmqpBroker(self.components['amqp-broker-factory'])

    def stopRouterPBService(self):
        "Stop Router PB server"
        return self.components['router-pb-server'].stopListening()

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
        self.components['smppcm-pb-server'] = reactor.listenTCP(SMPPClientPBConfigInstance.port, pb.PBServerFactory(jPBPortalRoot), 
                                           interface=SMPPClientPBConfigInstance.bind)

        # AMQP Broker is used to listen to submit_sm queues and publish to deliver_sm/dlr queues
        self.components['smppcm-pb-factory'].addAmqpBroker(self.components['amqp-broker-factory'])
        self.components['smppcm-pb-factory'].addRedisClient(self.components['rc'])
    
    def stopSMPPClientManagerPBService(self):
        "Stop SMPP Client Manager PB server"
        return self.components['smppcm-pb-server'].stopListening()
    
    def startSMPPServerService(self):
        "Start SMPP Server"

        SMPPServerConfigInstance = SMPPServerConfig(self.options['config'])

        # Set authentication portal
        p = portal.Portal(
            SmppsRealm(
                SMPPServerConfigInstance.id, 
                self.components['router-pb-factory'],
                )
            )
        p.registerChecker(RouterAuthChecker(self.components['router-pb-factory']))

        # SMPPServerFactory init
        self.components['smpp-server-factory'] = SMPPServerFactory(
            SMPPServerConfigInstance,
            auth_portal = p,
            RouterPB = self.components['router-pb-factory'],
            SMPPClientManagerPB = self.components['smppcm-pb-factory'],
            )

        # Start server
        self.components['smpp-server'] = reactor.listenTCP(SMPPServerConfigInstance.port, 
            self.components['smpp-server-factory'],
            interface = SMPPServerConfigInstance.bind
            )

    def stopSMPPServerService(self):
        "Stop SMPP Server"
        return self.components['smpp-server'].stopListening()

    def startdeliverSmThrowerService(self):
        "Start deliverSmThrower"
        
        deliverThrowerConfigInstance = deliverSmThrowerConfig(self.options['config'])
        self.components['deliversm-thrower'] = deliverSmThrower()
        self.components['deliversm-thrower'].setConfig(deliverThrowerConfigInstance)
        self.components['deliversm-thrower'].addSmpps(self.components['smpp-server-factory'])

        # AMQP Broker is used to listen to deliver_sm queue
        return self.components['deliversm-thrower'].addAmqpBroker(self.components['amqp-broker-factory'])

    def stopdeliverSmThrowerService(self):
        "Stop deliverSmThrower"
        return self.components['deliversm-thrower'].stopService()

    def startDLRThrowerService(self):
        "Start DLRThrower"
        
        DLRThrowerConfigInstance = DLRThrowerConfig(self.options['config'])
        self.components['dlr-thrower'] = DLRThrower()
        self.components['dlr-thrower'].setConfig(DLRThrowerConfigInstance)
        self.components['dlr-thrower'].addSmpps(self.components['smpp-server-factory'])

        # AMQP Broker is used to listen to DLRThrower queue
        return self.components['dlr-thrower'].addAmqpBroker(self.components['amqp-broker-factory'])

    def stopDLRThrowerService(self):
        "Stop DLRThrower"
        return self.components['dlr-thrower'].stopService()

    def startHTTPApiService(self):
        "Start HTTP Api"
        
        httpApiConfigInstance = HTTPApiConfig(self.options['config'])
        httpApi_f = HTTPApi(self.components['router-pb-factory'], self.components['smppcm-pb-factory'], httpApiConfigInstance)
        
        self.components['http-api-server'] = reactor.listenTCP(httpApiConfigInstance.port, 
                                     server.Site(httpApi_f, 
                                                 logPath = httpApiConfigInstance.access_log
                                                 ), 
                                     interface = httpApiConfigInstance.bind
                                     )

    def stopHTTPApiService(self):
        "Stop HTTP Api"
        return self.components['http-api-server'].stopListening()

    def startJCliService(self):
        "Start jCli console server"
        loadConfigProfileWithCreds = {'username': self.options['username'], 'password': self.options['password']}
        JCliConfigInstance = JCliConfig(self.options['config'])
        JCli_f = JCliFactory(JCliConfigInstance, self.components['smppcm-pb-factory'], self.components['router-pb-factory'], loadConfigProfileWithCreds)
        
        self.components['jcli-server'] = reactor.listenTCP(JCliConfigInstance.port, JCli_f, interface=JCliConfigInstance.bind)

    def stopJCliService(self):
        "Stop jCli console server"
        return self.components['jcli-server'].stopListening()

    @defer.inlineCallbacks
    def start(self):
        syslog.syslog(syslog.LOG_LOCAL0, "Starting Jasmin Daemon ...")
        
        ########################################################
        # Connect to redis server
        yield self.startRedisClient()
        syslog.syslog(syslog.LOG_LOCAL0, "  RedisClient started.")

        ########################################################
        # Start AMQP Broker
        self.startAMQPBrokerService()
        yield self.components['amqp-broker-factory'].getChannelReadyDeferred()
        syslog.syslog(syslog.LOG_LOCAL0, "  AMQP Broker connected.")

        ########################################################
        # Start SMPP Client connector manager and add rc
        self.startSMPPClientManagerPBService()
        syslog.syslog(syslog.LOG_LOCAL0, "  SMPPClientManagerPB Started.")

        ########################################################
        # Start Router PB server
        yield self.startRouterPBService()
        syslog.syslog(syslog.LOG_LOCAL0, "  RouterPB Started.")

        ########################################################
        # [optional] Start SMPP Server
        if (self.options['enable-smpp-server'] == True or 
            (type(self.options['enable-smpp-server']) == str and 
             self.options['enable-smpp-server'].lower() == 'true')):
            self.startSMPPServerService()
            syslog.syslog(syslog.LOG_LOCAL0, "  SMPPServer Started.")
        
        ########################################################
        # [optional] Start deliverSmThrower
        if (self.options['enable-deliver-thrower'] == True or 
            (type(self.options['enable-deliver-thrower']) == str and 
             self.options['enable-deliver-thrower'].lower() == 'true')):
            yield self.startdeliverSmThrowerService()
            syslog.syslog(syslog.LOG_LOCAL0, "  deliverSmThrower Started.")
        
        ########################################################
        # [optional] Start DLRThrower
        if (self.options['enable-dlr-thrower'] == True or 
            (type(self.options['enable-dlr-thrower']) == str and 
             self.options['enable-dlr-thrower'].lower() == 'true')):
            yield self.startDLRThrowerService()
            syslog.syslog(syslog.LOG_LOCAL0, "  DLRThrower Started.")
        
        ########################################################
        # [optional] Start HTTP Api
        if (self.options['enable-http-api'] == True or 
            (type(self.options['enable-http-api']) == str and 
             self.options['enable-http-api'].lower() == 'true')):
            self.startHTTPApiService()
            syslog.syslog(syslog.LOG_LOCAL0, "  HTTPApi Started.")
        
        ########################################################
        # [optional] Start JCli server
        if (self.options['enable-jcli'] == True or 
            (type(self.options['enable-jcli']) == str and 
             self.options['enable-jcli'].lower() == 'true')):
            self.startJCliService()
            syslog.syslog(syslog.LOG_LOCAL0, "  jCli Started.")
    
    @defer.inlineCallbacks
    def stop(self):
        syslog.syslog(syslog.LOG_LOCAL0, "Stopping Jasmin Daemon ...")
        
        if 'jcli-server' in self.components:
            yield self.stopJCliService()
            syslog.syslog(syslog.LOG_LOCAL0, "  jCli stopped.")
        
        if 'http-api-server' in self.components:
            yield self.stopHTTPApiService()
            syslog.syslog(syslog.LOG_LOCAL0, "  HTTPApi stopped.")

        if 'dlr-thrower' in self.components:
            yield self.stopDLRThrowerService()
            syslog.syslog(syslog.LOG_LOCAL0, "  DLRThrower stopped.")

        if 'deliversm-thrower' in self.components:
            yield self.stopdeliverSmThrowerService()
            syslog.syslog(syslog.LOG_LOCAL0, "  deliverSmThrower stopped.")
        
        if 'smpp-server' in self.components:
            yield self.stopSMPPServerService()
            syslog.syslog(syslog.LOG_LOCAL0, "  SMPPServer stopped.")

        if 'router-pb-server' in self.components:
            yield self.stopRouterPBService()
            syslog.syslog(syslog.LOG_LOCAL0, "  RouterPB stopped.")

        if 'smppcm-pb-server' in self.components:
            yield self.stopSMPPClientManagerPBService()
            syslog.syslog(syslog.LOG_LOCAL0, "  SMPPClientManagerPB stopped.")

        if 'amqp-broker-client' in self.components:
            yield self.stopAMQPBrokerService()
            syslog.syslog(syslog.LOG_LOCAL0, "  AMQP Broker disconnected.")

        if 'rc' in self.components:
            yield self.stopRedisClient()
            syslog.syslog(syslog.LOG_LOCAL0, "  RedisClient stopped.")

        reactor.stop()
    
    def sighandler_stop(self, signum, frame):
        syslog.syslog(syslog.LOG_LOCAL0, "Received signal to stop Jasmin Daemon")
        
        return self.stop()

if __name__ == '__main__':
    options = Options()
    try:
        options.parseOptions()
    except usage.UsageError, errortext:
        print '%s: %s' % (sys.argv[0], errortext)
        print '%s: Try --help for usage details.' % (sys.argv[0])
    else:
        jd = JasminDaemon(options)
        # Setup signal handlers
        signal.signal(signal.SIGINT, jd.sighandler_stop)
        # Start JasminDaemon
        jd.start()
        
        reactor.run()