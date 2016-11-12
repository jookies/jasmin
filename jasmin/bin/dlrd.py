#!/usr/bin/python

import os
import signal
import sys
import syslog

from lockfile import FileLock, LockTimeout, AlreadyLocked
from twisted.internet import reactor, defer
from twisted.python import usage

from jasmin.protocols.smpp.configs import SMPPServerPBClientConfig
from jasmin.protocols.smpp.proxies import SMPPServerPBProxy
from jasmin.queues.configs import AmqpConfig
from jasmin.queues.factory import AmqpFactory
from jasmin.routing.configs import DLRThrowerConfig
from jasmin.routing.throwers import DLRThrower

# Related to travis-ci builds
ROOT_PATH = os.getenv('ROOT_PATH', '/')


class Options(usage.Options):
    optParameters = [
        ['config', 'c', '%s/etc/jasmin/dlr.cfg' % ROOT_PATH,
         'Jasmin dlrd configuration file'],
        ['id', 'i', 'master',
         'Daemon id, need to be different for each dlrd daemon'],
    ]

    optFlags = [
    ]


class DlrDaemon(object):
    def __init__(self, opt):
        self.options = opt
        self.components = {}

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

    def startSMPPServerPBClient(self):
        """Start SMPPServerPB client"""

        SMPPServerPBClientConfigInstance = SMPPServerPBClientConfig(self.options['config'])
        self.components['smpps-pb-client'] = SMPPServerPBProxy()

        return self.components['smpps-pb-client'].connect(
            SMPPServerPBClientConfigInstance.host,
            SMPPServerPBClientConfigInstance.port,
            SMPPServerPBClientConfigInstance.username,
            SMPPServerPBClientConfigInstance.password,
            retry=True)

    def stopSMPPServerPBClient(self):
        """Stop SMPPServerPB client"""

        if self.components['smpps-pb-client'].isConnected:
            return self.components['smpps-pb-client'].disconnect()

    def startDLRThrowerService(self):
        """Start DLRThrower"""

        DLRThrowerConfigInstance = DLRThrowerConfig(self.options['config'])
        self.components['dlr-thrower'] = DLRThrower(DLRThrowerConfigInstance)
        self.components['dlr-thrower'].addSmpps(self.components['smpps-pb-client'])

        # AMQP Broker is used to listen to DLRThrower queue
        return self.components['dlr-thrower'].addAmqpBroker(self.components['amqp-broker-factory'])

    def stopDLRThrowerService(self):
        """Stop DLRThrower"""
        return self.components['dlr-thrower'].stopService()

    @defer.inlineCallbacks
    def start(self):
        """Start Dlrd daemon"""
        syslog.syslog(syslog.LOG_INFO, "Starting Dlr Daemon ...")

        ########################################################
        # Start AMQP Broker
        try:
            self.startAMQPBrokerService()
            yield self.components['amqp-broker-factory'].getChannelReadyDeferred()
        except Exception, e:
            syslog.syslog(syslog.LOG_ERR, "  Cannot start AMQP Broker: %s" % e)
        else:
            syslog.syslog(syslog.LOG_INFO, "  AMQP Broker Started.")

        ########################################################
        try:
            # Start SMPPServerPB Client
            yield self.startSMPPServerPBClient()
        except Exception, e:
            syslog.syslog(syslog.LOG_ERR, "  Cannot start SMPPServerPBClient: %s" % e)
        else:
            syslog.syslog(syslog.LOG_INFO, "  SMPPServerPBClientStarted.")

        ########################################################
        try:
            # Start DLRThrower
            yield self.startDLRThrowerService()
        except Exception, e:
            syslog.syslog(syslog.LOG_ERR, "  Cannot start DLRThrower: %s" % e)
        else:
            syslog.syslog(syslog.LOG_INFO, "  DLRThrower Started.")

    @defer.inlineCallbacks
    def stop(self):
        """Stop Dlrd daemon"""
        syslog.syslog(syslog.LOG_INFO, "Stopping Dlr Daemon ...")

        if 'smpps-pb-client' in self.components:
            yield self.stopSMPPServerPBClient()
            syslog.syslog(syslog.LOG_INFO, "  SMPPServerPBClient stopped.")

        if 'dlr-thrower' in self.components:
            yield self.stopDLRThrowerService()
            syslog.syslog(syslog.LOG_INFO, "  DLRThrower stopped.")

        if 'amqp-broker-client' in self.components:
            yield self.stopAMQPBrokerService()
            syslog.syslog(syslog.LOG_INFO, "  AMQP Broker disconnected.")

        reactor.stop()

    def sighandler_stop(self, signum, frame):
        """Handle stop signal cleanly"""
        syslog.syslog(syslog.LOG_INFO, "Received signal to stop Dlr Daemon")

        return self.stop()


if __name__ == '__main__':
    lock = None
    try:
        options = Options()
        options.parseOptions()

        # Must not be executed simultaneously (c.f. #265)
        lock = FileLock("/tmp/dlrd-%s" % options['id'])

        # Ensure there are no paralell runs of this script
        lock.acquire(timeout=2)

        # Prepare to start
        dlr_d = DlrDaemon(options)
        # Setup signal handlers
        signal.signal(signal.SIGINT, dlr_d.sighandler_stop)
        # Start DlrDaemon
        dlr_d.start()

        reactor.run()
    except usage.UsageError, errortext:
        print '%s: %s' % (sys.argv[0], errortext)
        print '%s: Try --help for usage details.' % (sys.argv[0])
    except LockTimeout:
        print "Lock not acquired ! exiting"
    except AlreadyLocked:
        print "There's another instance on dlrd running, exiting."
    finally:
        # Release the lock
        if lock is not None and lock.i_am_locking():
            lock.release()
