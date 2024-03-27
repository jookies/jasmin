#!/usr/bin/python3

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
from jasmin.routing.configs import deliverSmThrowerConfig
from jasmin.routing.throwers import deliverSmThrower
from jasmin.config import ROOT_PATH
from jasmin.bin import BaseDaemon

CONFIG_PATH = os.getenv('CONFIG_PATH', '%s/etc/jasmin/' % ROOT_PATH)


class Options(usage.Options):
    optParameters = [
        ['config', 'c', '%s/deliversm.cfg' % CONFIG_PATH,
         'Jasmin deliversmd configuration file'],
        ['id', 'i', 'master',
         'Daemon id, need to be different for each deliversmd daemon'],
    ]

    optFlags = [
    ]


class DeliverSmDaemon(BaseDaemon):
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

    def startdeliverSmThrowerService(self):
        """Start deliverSmThrower"""

        deliverThrowerConfigInstance = deliverSmThrowerConfig(self.options['config'])
        self.components['deliversm-thrower'] = deliverSmThrower(deliverThrowerConfigInstance)
        self.components['deliversm-thrower'].addSmpps(self.components['smpps-pb-client'])

        # AMQP Broker is used to listen to deliver_sm queue
        return self.components['deliversm-thrower'].addAmqpBroker(self.components['amqp-broker-factory'])

    def stopdeliverSmThrowerService(self):
        """Stop deliverSmThrower"""
        return self.components['deliversm-thrower'].stopService()

    @defer.inlineCallbacks
    def start(self):
        """Start Deliver-Sm Daemon"""
        syslog.syslog(syslog.LOG_INFO, "Starting Deliver-Sm Daemon ...")

        ########################################################
        # Start AMQP Broker
        try:
            self.startAMQPBrokerService()
            yield self.components['amqp-broker-factory'].getChannelReadyDeferred()
        except Exception as e:
            syslog.syslog(syslog.LOG_ERR, "  Cannot start AMQP Broker: %s" % e)
        else:
            syslog.syslog(syslog.LOG_INFO, "  AMQP Broker Started.")

        ########################################################
        try:
            # Start SMPPServerPB Client
            yield self.startSMPPServerPBClient()
        except Exception as e:
            syslog.syslog(syslog.LOG_ERR, "  Cannot start SMPPServerPBClient: %s" % e)
        else:
            syslog.syslog(syslog.LOG_INFO, "  SMPPServerPBClient Started.")

        ########################################################
        try:
            # Start deliverSmThrower
            yield self.startdeliverSmThrowerService()
        except Exception as e:
            syslog.syslog(syslog.LOG_ERR, "  Cannot start deliverSmThrower: %s" % e)
        else:
            syslog.syslog(syslog.LOG_INFO, "  deliverSmThrower Started.")

    @defer.inlineCallbacks
    def stop(self):
        """Stop Deliver-Sm daemon"""
        syslog.syslog(syslog.LOG_INFO, "Stopping Deliver-Sm Daemon ...")

        if 'smpps-pb-client' in self.components:
            yield self.stopSMPPServerPBClient()
            syslog.syslog(syslog.LOG_INFO, "  SMPPServerPBClient stopped.")

        if 'deliversm-thrower' in self.components:
            yield self.stopdeliverSmThrowerService()
            syslog.syslog(syslog.LOG_INFO, "  deliverSmThrower stopped.")

        if 'amqp-broker-client' in self.components:
            yield self.stopAMQPBrokerService()
            syslog.syslog(syslog.LOG_INFO, "  AMQP Broker disconnected.")

        reactor.stop()

    def sighandler_stop(self, signum, frame):
        """Handle stop signal cleanly"""
        syslog.syslog(syslog.LOG_INFO, "Received signal to stop Deliver-Sm Daemon")

        return self.stop()


if __name__ == '__main__':
    lock = None
    try:
        options = Options()
        options.parseOptions()

        # Must not be executed simultaneously (c.f. #265)
        lock = FileLock("/tmp/deliversmd-%s" % options['id'])

        # Ensure there are no paralell runs of this script
        lock.acquire(timeout=2)

        # Prepare to start
        deliversm_d = DeliverSmDaemon(options)
        # Setup signal handlers
        signal.signal(signal.SIGINT, deliversm_d.sighandler_stop)
        signal.signal(signal.SIGTERM, deliversm_d.sighandler_stop)
        # Start DeliverSmDaemon
        deliversm_d.start()

        reactor.run()
    except usage.UsageError as errortext:
        print('%s: %s' % (sys.argv[0], errortext))
        print('%s: Try --help for usage details.' % (sys.argv[0]))
    except LockTimeout:
        print("Lock not acquired ! exiting")
    except AlreadyLocked:
        print("There's another instance on deliversmd running, exiting.")
    finally:
        # Release the lock
        if lock is not None and lock.i_am_locking():
            lock.release()
