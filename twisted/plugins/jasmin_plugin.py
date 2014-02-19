# Copyright 2012 Fourat Zouari <fourat@gmail.com>
# See LICENSE for details.

from zope.interface import implements
from twisted.python import usage, log
from twisted.plugin import IPlugin
from twisted.application import internet, service
from twisted.web import server
from twisted.spread import pb
from jasmin.managers.clients import SMPPClientManagerPB
from jasmin.managers.configs import SMPPClientPBConfig
from jasmin.queues.configs import AmqpConfig
from jasmin.queues.factory import AmqpFactory
from jasmin.protocols.http.configs import HTTPApiConfig
from jasmin.protocols.http.server import HTTPApi
from jasmin.routing.router import RouterPB
from jasmin.routing.configs import RouterPBConfig, deliverSmThrowerConfig, DLRThrowerConfig
from jasmin.routing.throwers import deliverSmThrower, DLRThrower
from jasmin.redis.configs import RedisForJasminConfig
from jasmin.redis.client import ConnectionWithConfiguration
from jasmin.protocols.cli.factory import JCliFactory
from jasmin.protocols.cli.configs import JCliConfig

class Options(usage.Options):

    optParameters = [
        ['config', 'c', '/etc/jasmin/jasmin.cfg', 'Jasmin configuration file'],
        ]
    
class JasminServiceMaker:

    implements(service.IServiceMaker, IPlugin)

    tapname = "jasmin"
    description = "Open source SMS gateway"
    options = Options

    def makeService(self, options):
        top_service = service.MultiService()

        # Use standard python logger with twistd:
        # c.f: http://twistedmatrix.com/documents/current/core/howto/logging.htl#auto3
        log.PythonLoggingObserver().start()

        # Start AMQP Broker
        AMQPServiceConfigInstance = AmqpConfig(options['config'])
        amqpBroker_f = AmqpFactory(AMQPServiceConfigInstance)
        amqpBroker_f.preConnect()
        amqpBroker = internet.TCPClient(AMQPServiceConfigInstance.host, AMQPServiceConfigInstance.port, amqpBroker_f)
        amqpBroker.setServiceParent(top_service)

        # Start Router PB server
        RouterPBConfigInstance = RouterPBConfig(options['config'])
        RouterPB_f = RouterPB()
        RouterPB_f.setConfig(RouterPBConfigInstance)
        Router = internet.TCPServer(RouterPBConfigInstance.port, pb.PBServerFactory(RouterPB_f), 
                                    interface=RouterPBConfigInstance.bind)
        Router.setServiceParent(top_service)
        # AMQP Broker is used to listen to deliver_sm/dlr queues
        RouterPB_f.addAmqpBroker(amqpBroker_f)

        # Start SMPP Client connector manager
        SMPPClientPBConfigInstance = SMPPClientPBConfig(options['config'])
        clientManager_f = SMPPClientManagerPB()
        clientManager_f.setConfig(SMPPClientPBConfigInstance)
        clientManager = internet.TCPServer(SMPPClientPBConfigInstance.port, pb.PBServerFactory(clientManager_f), 
                                           interface=SMPPClientPBConfigInstance.bind)
        clientManager.setServiceParent(top_service)
        # AMQP Broker is used to listen to submit_sm queues and publish to deliver_sm/dlr queues
        clientManager_f.addAmqpBroker(amqpBroker_f)
        
        # Connect to redis server
        RedisForJasminConfigInstance = RedisForJasminConfig(options['config'])
        rc = ConnectionWithConfiguration(RedisForJasminConfigInstance)
        # Authenticate and select db
        if RedisForJasminConfigInstance.password is not None:
            rc.auth(RedisForJasminConfigInstance.password)
            rc.select(RedisForJasminConfigInstance.dbid)
        clientManager_f.addRedisClient(rc)

        # Start deliverSmThrower
        deliverThrowerConfigInstance = deliverSmThrowerConfig(options['config'])
        deliverThrower = deliverSmThrower()
        deliverThrower.setConfig(deliverThrowerConfigInstance)
        deliverThrower.setServiceParent(top_service)
        # AMQP Broker is used to listen to deliver_sm queue
        deliverThrower.addAmqpBroker(amqpBroker_f)
        
        # Start DLRThrower
        DLRThrowerConfigInstance = DLRThrowerConfig(options['config'])
        _DLRThrower = DLRThrower()
        _DLRThrower.setConfig(DLRThrowerConfigInstance)
        _DLRThrower.setServiceParent(top_service)
        # AMQP Broker is used to listen to DLRThrower queue
        _DLRThrower.addAmqpBroker(amqpBroker_f)
        
        # Start HTTP Api
        httpApiConfigInstance = HTTPApiConfig(options['config'])
        httpApi_f = HTTPApi(RouterPB_f, clientManager_f, httpApiConfigInstance)
        httpApi = internet.TCPServer(httpApiConfigInstance.port, 
                                     server.Site(httpApi_f, 
                                                 logPath=httpApiConfigInstance.access_log
                                                 ), 
                                     interface=httpApiConfigInstance.bind
                                     )
        httpApi.setServiceParent(top_service)
        
        # Start JCli server
        JCliConfigInstance = JCliConfig(options['config'])
        JCli_f = JCliFactory(JCliConfigInstance, clientManager_f, RouterPB_f)
        jcli = internet.TCPServer(JCliConfigInstance.port, JCli_f, interface=JCliConfigInstance.bind)
        jcli.setServiceParent(top_service)
        
        return top_service

service_maker = JasminServiceMaker()