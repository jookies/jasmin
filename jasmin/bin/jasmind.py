#!/usr/bin/python3

import os
import signal
import sys
import traceback
import logging

from lockfile import FileLock, LockTimeout, AlreadyLocked
from twisted.cred import portal
from twisted.cred.checkers import AllowAnonymousAccess, InMemoryUsernamePasswordDatabaseDontUse
from twisted.internet import reactor, defer
from twisted.python import usage
from twisted.spread import pb
from twisted.web import server

from jasmin.interceptor.configs import InterceptorPBClientConfig
from jasmin.interceptor.proxies import InterceptorPBProxy
from jasmin.managers.clients import SMPPClientManagerPB
from jasmin.managers.configs import SMPPClientPBConfig, DLRLookupConfig
from jasmin.managers.dlr import DLRLookup
from jasmin.protocols.cli.configs import JCliConfig
from jasmin.protocols.cli.factory import JCliFactory
from jasmin.protocols.http.configs import HTTPApiConfig
from jasmin.protocols.http.server import HTTPApi
from jasmin.protocols.smpp.configs import SMPPServerConfig, SMPPServerPBConfig
from jasmin.protocols.smpp.factory import SMPPServerFactory
from jasmin.protocols.smpp.pb import SMPPServerPB
from jasmin.queues.configs import AmqpConfig
from jasmin.queues.factory import AmqpFactory
from jasmin.redis.client import ConnectionWithConfiguration
from jasmin.redis.configs import RedisForJasminConfig
from jasmin.routing.configs import RouterPBConfig, deliverSmThrowerConfig, DLRThrowerConfig
from jasmin.routing.router import RouterPB
from jasmin.routing.throwers import deliverSmThrower, DLRThrower
from jasmin.tools.cred.checkers import RouterAuthChecker
from jasmin.tools.cred.portal import JasminPBRealm
from jasmin.tools.cred.portal import SmppsRealm
from jasmin.tools.spread.pb import JasminPBPortalRoot

ROOT_PATH = os.getenv('ROOT_PATH', '/')
CONFIG_PATH = os.getenv('CONFIG_PATH', '%s/etc/jasmin/' % ROOT_PATH)

LOG_CATEGORY = "jasmin-daemon"

class Options(usage.Options):
    optParameters = [
        ['config', 'c', '%s/jasmin.cfg' % CONFIG_PATH,
         'Jasmin configuration file'],
        ['username', 'u', None,
         'jCli username used to load configuration profile on startup'],
        ['password', 'p', None,
         'jCli password used to load configuration profile on startup'],
    ]

    optFlags = [
        ['disable-smpp-server', None, 'Do not start SMPP Server service'],
        ['enable-dlr-thrower', None, 'Enable DLR Thrower service (not recommended: start the dlrd daemon instead)'],
        ['enable-dlr-lookup', None, 'Enable DLR Lookup service (not recommended: start the dlrlookupd daemon instead)'],
        # @TODO: deliver-thrower must be executed as a standalone process, just like dlr-thrower
        ['disable-deliver-thrower', None, 'Do not DeliverSm Thrower service'],
        ['disable-http-api', None, 'Do not HTTP API'],
        ['disable-jcli', None, 'Do not jCli console'],
        ['enable-interceptor-client', None, 'Start Interceptor client'],
    ]


class JasminDaemon:
    def __init__(self, opt):
        self.options = opt
        self.components = {}
        self.log = logging.getLogger(LOG_CATEGORY)
        self.log.setLevel(logging.INFO)
        handler = logging.StreamHandler(sys.stdout)
        formatter = logging.Formatter('%(asctime)s %(levelname)-8s %(process)d %(message)s', '%Y-%m-%d %H:%M:%S')
        handler.setFormatter(formatter)
        self.log.addHandler(handler)
        self.log.propagate = False

    @defer.inlineCallbacks
    def startRedisClient(self):
        """Start AMQP Broker"""
        RedisForJasminConfigInstance = RedisForJasminConfig(self.options['config'])
        self.components['rc'] = yield ConnectionWithConfiguration(RedisForJasminConfigInstance)
        # Authenticate and select db
        if RedisForJasminConfigInstance.password is not None:
            yield self.components['rc'].auth(RedisForJasminConfigInstance.password)
            yield self.components['rc'].select(RedisForJasminConfigInstance.dbid)

    def stopRedisClient(self):
        """Stop AMQP Broker"""
        return self.components['rc'].disconnect()

    def startAMQPBrokerService(self):
        """Start AMQP Broker"""

        AMQPServiceConfigInstance = AmqpConfig(self.options['config'])
        self.components['amqp-broker-factory'] = AmqpFactory(AMQPServiceConfigInstance)
        self.components['amqp-broker-factory'].preConnect()

        # Add service
        self.components['amqp-broker-client'] = reactor.connectTCP(
            AMQPServiceConfigInstance.host,
            AMQPServiceConfigInstance.port,
            self.components['amqp-broker-factory'])

    def stopAMQPBrokerService(self):
        """Stop AMQP Broker"""

        return self.components['amqp-broker-client'].disconnect()

    def startRouterPBService(self):
        """Start Router PB server"""

        RouterPBConfigInstance = RouterPBConfig(self.options['config'])
        self.components['router-pb-factory'] = RouterPB(RouterPBConfigInstance)

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
        self.components['router-pb-server'] = reactor.listenTCP(
            RouterPBConfigInstance.port,
            pb.PBServerFactory(jPBPortalRoot),
            interface=RouterPBConfigInstance.bind)

        # AMQP Broker is used to listen to deliver_sm/dlr queues
        return self.components['router-pb-factory'].addAmqpBroker(self.components['amqp-broker-factory'])

    def stopRouterPBService(self):
        """Stop Router PB server"""
        return self.components['router-pb-server'].stopListening()

    def startSMPPClientManagerPBService(self):
        """Start SMPP Client Manager PB server"""

        SMPPClientPBConfigInstance = SMPPClientPBConfig(self.options['config'])
        self.components['smppcm-pb-factory'] = SMPPClientManagerPB(SMPPClientPBConfigInstance)

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
        self.components['smppcm-pb-server'] = reactor.listenTCP(
            SMPPClientPBConfigInstance.port,
            pb.PBServerFactory(jPBPortalRoot),
            interface=SMPPClientPBConfigInstance.bind)

        # AMQP Broker is used to listen to submit_sm queues and publish to deliver_sm/dlr queues
        self.components['smppcm-pb-factory'].addAmqpBroker(self.components['amqp-broker-factory'])
        self.components['smppcm-pb-factory'].addRedisClient(self.components['rc'])
        self.components['smppcm-pb-factory'].addRouterPB(self.components['router-pb-factory'])

        # Add interceptor if enabled:
        if 'interceptor-pb-client' in self.components:
            self.components['smppcm-pb-factory'].addInterceptorPBClient(
                self.components['interceptor-pb-client'])

    def stopSMPPClientManagerPBService(self):
        """Stop SMPP Client Manager PB server"""
        return self.components['smppcm-pb-server'].stopListening()

    @defer.inlineCallbacks
    def startDLRLookupService(self):
        """Start DLRLookup"""

        DLRLookupConfigInstance = DLRLookupConfig(self.options['config'])
        self.components['dlrlookup'] = DLRLookup(DLRLookupConfigInstance, self.components['amqp-broker-factory'],
                                                 self.components['rc'])
        yield self.components['dlrlookup'].subscribe()

    def startSMPPServerPBService(self):
        """Start SMPP Server PB server"""

        SMPPServerPBConfigInstance = SMPPServerPBConfig(self.options['config'])
        self.components['smpps-pb-factory'] = SMPPServerPB(SMPPServerPBConfigInstance)

        # Set authentication portal
        p = portal.Portal(JasminPBRealm(self.components['smpps-pb-factory']))
        if SMPPServerPBConfigInstance.authentication:
            c = InMemoryUsernamePasswordDatabaseDontUse()
            c.addUser(SMPPServerPBConfigInstance.admin_username, SMPPServerPBConfigInstance.admin_password)
            p.registerChecker(c)
        else:
            p.registerChecker(AllowAnonymousAccess())
        jPBPortalRoot = JasminPBPortalRoot(p)

        # Add service
        self.components['smpps-pb-server'] = reactor.listenTCP(
            SMPPServerPBConfigInstance.port,
            pb.PBServerFactory(jPBPortalRoot),
            interface=SMPPServerPBConfigInstance.bind)

    def stopSMPPServerPBService(self):
        """Stop SMPP Server PB"""
        return self.components['smpps-pb-server'].stopListening()

    def startSMPPServerService(self):
        """Start SMPP Server"""

        SMPPServerConfigInstance = SMPPServerConfig(self.options['config'])

        # Set authentication portal
        p = portal.Portal(
            SmppsRealm(
                SMPPServerConfigInstance.id,
                self.components['router-pb-factory']))
        p.registerChecker(RouterAuthChecker(self.components['router-pb-factory']))

        # SMPPServerFactory init
        self.components['smpp-server-factory'] = SMPPServerFactory(
            SMPPServerConfigInstance,
            auth_portal=p,
            RouterPB=self.components['router-pb-factory'],
            SMPPClientManagerPB=self.components['smppcm-pb-factory'])

        # Start server
        self.components['smpp-server'] = reactor.listenTCP(
            SMPPServerConfigInstance.port,
            self.components['smpp-server-factory'],
            interface=SMPPServerConfigInstance.bind)

        # Add interceptor if enabled:
        if 'interceptor-pb-client' in self.components:
            self.components['smpp-server-factory'].addInterceptorPBClient(
                self.components['interceptor-pb-client'])

    def stopSMPPServerService(self):
        """Stop SMPP Server"""
        return self.components['smpp-server'].stopListening()

    def startdeliverSmThrowerService(self):
        """Start deliverSmThrower"""

        deliverThrowerConfigInstance = deliverSmThrowerConfig(self.options['config'])
        self.components['deliversm-thrower'] = deliverSmThrower(deliverThrowerConfigInstance)
        self.components['deliversm-thrower'].addSmpps(self.components['smpp-server-factory'])

        # AMQP Broker is used to listen to deliver_sm queue
        return self.components['deliversm-thrower'].addAmqpBroker(self.components['amqp-broker-factory'])

    def stopdeliverSmThrowerService(self):
        """Stop deliverSmThrower"""
        return self.components['deliversm-thrower'].stopService()

    def startDLRThrowerService(self):
        """Start DLRThrower"""

        DLRThrowerConfigInstance = DLRThrowerConfig(self.options['config'])
        self.components['dlr-thrower'] = DLRThrower(DLRThrowerConfigInstance)
        self.components['dlr-thrower'].addSmpps(self.components['smpp-server-factory'])

        # AMQP Broker is used to listen to DLRThrower queue
        return self.components['dlr-thrower'].addAmqpBroker(self.components['amqp-broker-factory'])

    def stopDLRThrowerService(self):
        """Stop DLRThrower"""
        return self.components['dlr-thrower'].stopService()

    def startHTTPApiService(self):
        """Start HTTP Api"""

        httpApiConfigInstance = HTTPApiConfig(self.options['config'])

        # Add interceptor if enabled:
        if 'interceptor-pb-client' in self.components:
            interceptorpb_client = self.components['interceptor-pb-client']
        else:
            interceptorpb_client = None

        self.components['http-api-factory'] = HTTPApi(
            self.components['router-pb-factory'],
            self.components['smppcm-pb-factory'],
            httpApiConfigInstance,
            interceptorpb_client)

        self.components['http-api-server'] = reactor.listenTCP(
            httpApiConfigInstance.port,
            server.Site(self.components['http-api-factory'], logPath=httpApiConfigInstance.access_log),
            interface=httpApiConfigInstance.bind)

    def stopHTTPApiService(self):
        """Stop HTTP Api"""
        return self.components['http-api-server'].stopListening()

    def startJCliService(self):
        """Start jCli console server"""
        loadConfigProfileWithCreds = {
            'username': self.options['username'],
            'password': self.options['password']}
        JCliConfigInstance = JCliConfig(self.options['config'])
        JCli_f = JCliFactory(
            JCliConfigInstance,
            self.components['smppcm-pb-factory'],
            self.components['router-pb-factory'],
            self.components['smpp-server-factory'],
            loadConfigProfileWithCreds)

        self.components['jcli-server'] = reactor.listenTCP(
            JCliConfigInstance.port,
            JCli_f,
            interface=JCliConfigInstance.bind)

    def stopJCliService(self):
        """Stop jCli console server"""
        return self.components['jcli-server'].stopListening()

    def startInterceptorPBClient(self):
        """Start Interceptor client"""

        InterceptorPBClientConfigInstance = InterceptorPBClientConfig(self.options['config'])
        self.components['interceptor-pb-client'] = InterceptorPBProxy()

        return self.components['interceptor-pb-client'].connect(
            InterceptorPBClientConfigInstance.host,
            InterceptorPBClientConfigInstance.port,
            InterceptorPBClientConfigInstance.username,
            InterceptorPBClientConfigInstance.password,
            retry=True)

    def stopInterceptorPBClient(self):
        """Stop Interceptor client"""

        if self.components['interceptor-pb-client'].isConnected:
            return self.components['interceptor-pb-client'].disconnect()

    @defer.inlineCallbacks
    def start(self):
        """Start Jasmind daemon"""
        self.log.info("Starting Jasmin Daemon ...")

        # Requirements check begin:
        ########################################################
        if self.options['enable-interceptor-client']:
            try:
                # [optional] Start Interceptor client
                yield self.startInterceptorPBClient()
            except Exception as e:
                self.log.error("  Cannot connect to interceptor: %s\n%s" % (e, traceback.format_exc()))
            else:
                self.log.info("  Interceptor client Started.")
        # Requirements check end.

        ########################################################
        # Connect to redis server
        try:
            yield self.startRedisClient()
        except Exception as e:
            self.log.error("  Cannot start RedisClient: %s\n%s" % (e, traceback.format_exc()))
        else:
            self.log.info("  RedisClient Started.")

        ########################################################
        # Start AMQP Broker
        try:
            yield self.startAMQPBrokerService()
            yield self.components['amqp-broker-factory'].getChannelReadyDeferred()
        except Exception as e:
            self.log.error("  Cannot start AMQP Broker: %s\n%s" % (e, traceback.format_exc()))
        else:
            self.log.info("  AMQP Broker Started.")

        ########################################################
        # Start Router PB server
        try:
            yield self.startRouterPBService()
        except Exception as e:
            self.log.error("  Cannot start RouterPB: %s\n%s" % (e, traceback.format_exc()))
        else:
            self.log.info("  RouterPB Started.")

        ########################################################
        # Start SMPP Client connector manager and add rc
        try:
            self.startSMPPClientManagerPBService()
        except Exception as e:
            self.log.error("  Cannot start SMPPClientManagerPB: %s\n%s" % (e, traceback.format_exc()))
        else:
            self.log.info("  SMPPClientManagerPB Started.")

        ########################################################
        if self.options['enable-dlr-lookup']:
            try:
                # [optional] Start DLR Lookup
                self.startDLRLookupService()
            except Exception as e:
                self.log.error("  Cannot start DLRLookup: %s\n%s" % (e, traceback.format_exc()))
            else:
                self.log.info("  DLRLookup Started.")

        ########################################################
        if not self.options['disable-smpp-server']:
            try:
                # [optional] Start SMPP Server
                self.startSMPPServerService()
            except Exception as e:
                self.log.error("  Cannot start SMPPServer: %s\n%s" % (e, traceback.format_exc()))
            else:
                self.log.info("  SMPPServer Started.")

            try:
                # [optional] Start SMPP Server PB
                self.startSMPPServerPBService()
                self.components['smpps-pb-factory'].addSmpps(self.components['smpp-server-factory'])
            except Exception as e:
                self.log.error("  Cannot start SMPPServerPB: %s\n%s" % (e, traceback.format_exc()))
            else:
                self.log.info("  SMPPServer Started.")

        ########################################################
        if not self.options['disable-deliver-thrower']:
            try:
                # [optional] Start deliverSmThrower
                yield self.startdeliverSmThrowerService()
            except Exception as e:
                self.log.error("  Cannot start deliverSmThrower: %s\n%s" % (e, traceback.format_exc()))
            else:
                self.log.info("  deliverSmThrower Started.")

        ########################################################
        if self.options['enable-dlr-thrower']:
            try:
                # [optional] Start DLRThrower
                yield self.startDLRThrowerService()
            except Exception as e:
                self.log.error("  Cannot start DLRThrower: %s\n%s" % (e, traceback.format_exc()))
            else:
                self.log.info("  DLRThrower Started.")

        ########################################################
        if not self.options['disable-http-api']:
            try:
                # [optional] Start HTTP Api
                self.startHTTPApiService()
            except Exception as e:
                self.log.error("  Cannot start HTTPApi: %s\n%s" % (e, traceback.format_exc()))
            else:
                self.log.info("  HTTPApi Started.")

        ########################################################
        if not self.options['disable-jcli']:
            try:
                # [optional] Start JCli server
                self.startJCliService()
            except Exception as e:
                self.log.error("  Cannot start jCli: %s\n%s" % (e, traceback.format_exc()))
            else:
                self.log.info("  jCli Started.")

    @defer.inlineCallbacks
    def stop(self):
        """Stop Jasmind daemon"""
        self.log.info("Stopping Jasmin Daemon ...")

        if 'jcli-server' in self.components:
            yield self.stopJCliService()
            self.log.info("  jCli stopped.")

        if 'http-api-server' in self.components:
            yield self.stopHTTPApiService()
            self.log.info("  HTTPApi stopped.")

        if 'dlr-thrower' in self.components:
            yield self.stopDLRThrowerService()
            self.log.info("  DLRThrower stopped.")

        if 'deliversm-thrower' in self.components:
            yield self.stopdeliverSmThrowerService()
            self.log.info("  deliverSmThrower stopped.")

        if 'smpps-pb-server' in self.components:
            yield self.stopSMPPServerPBService()
            self.log.info("  SMPPServerPB stopped.")

        if 'smpp-server' in self.components:
            yield self.stopSMPPServerService()
            self.log.info("  SMPPServer stopped.")

        if 'smppcm-pb-server' in self.components:
            yield self.stopSMPPClientManagerPBService()
            self.log.info("  SMPPClientManagerPB stopped.")

        if 'router-pb-server' in self.components:
            yield self.stopRouterPBService()
            self.log.info("  RouterPB stopped.")

        if 'amqp-broker-client' in self.components:
            yield self.stopAMQPBrokerService()
            self.log.info("  AMQP Broker disconnected.")

        if 'rc' in self.components:
            yield self.stopRedisClient()
            self.log.info("  RedisClient stopped.")

        # Shutdown requirements:
        if 'interceptor-pb-client' in self.components:
            yield self.stopInterceptorPBClient()
            self.log.info("  Interceptor client stopped.")

        reactor.stop()

    def sighandler_stop(self, signum, frame):
        """Handle stop signal cleanly"""
        self.log.info("Received signal to stop Jasmin Daemon")

        return self.stop()


if __name__ == '__main__':
    lock = None
    try:
        options = Options()
        options.parseOptions()

        # Must not be executed simultaneously (c.f. #265)
        lock = FileLock("/tmp/jasmind")

        # Ensure there are no paralell runs of this script
        lock.acquire(timeout=2)

        # Prepare to start
        ja_d = JasminDaemon(options)
        # Setup signal handlers
        signal.signal(signal.SIGINT, ja_d.sighandler_stop)
        signal.signal(signal.SIGTERM, ja_d.sighandler_stop)
        # Start JasminDaemon
        ja_d.start()

        reactor.run()
    except usage.UsageError as errortext:
        print('%s: %s' % (sys.argv[0], errortext))
        print('%s: Try --help for usage details.' % (sys.argv[0]))
    except LockTimeout:
        print("Lock not acquired ! exiting")
    except AlreadyLocked:
        print("There's another instance on jasmind running, exiting.")
    finally:
        # Release the lock
        if lock is not None and lock.i_am_locking():
            lock.release()
