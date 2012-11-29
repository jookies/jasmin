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
from jasmin.routing.configs import RouterPBConfig

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
        Router = internet.TCPServer(RouterPBConfigInstance.port, pb.PBServerFactory(RouterPB_f))
        Router.setServiceParent(top_service)
        # AMQP Broker is used to listen to deliver_sm/dlr queues
        RouterPB_f.addAmqpBroker(amqpBroker_f)

        # Start SMPP Client connector manager
        SMPPClientPBConfigInstance = SMPPClientPBConfig(options['config'])
        clientManager_f = SMPPClientManagerPB()
        clientManager_f.setConfig(SMPPClientPBConfigInstance)
        clientManager = internet.TCPServer(SMPPClientPBConfigInstance.port, pb.PBServerFactory(clientManager_f))
        clientManager.setServiceParent(top_service)
        # AMQP Broker is used to listen to submit_sm queues and publish to deliver_sm/dlr queues
        clientManager_f.addAmqpBroker(amqpBroker_f)
        
        # Start HTTP Api
        httpApiConfigInstance = HTTPApiConfig(options['config'])
        httpApi = HTTPApi(RouterPB_f, clientManager_f, httpApiConfigInstance)
        httpApi = internet.TCPServer(httpApiConfigInstance.port, 
                                     server.Site(httpApi, 
                                                 logPath=httpApiConfigInstance.access_log
                                                 ), 
                                     interface=httpApiConfigInstance.bind
                                     )
        httpApi.setServiceParent(top_service)
        
        return top_service

service_maker = JasminServiceMaker()