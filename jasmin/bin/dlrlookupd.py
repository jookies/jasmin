#!/usr/bin/python

import os
import signal
import sys
import syslog

from lockfile import FileLock, LockTimeout, AlreadyLocked
from twisted.internet import reactor, defer
from twisted.python import usage

from jasmin.managers.configs import DLRLookupConfig
from jasmin.managers.dlr import DLRLookup
from jasmin.queues.configs import AmqpConfig
from jasmin.queues.factory import AmqpFactory
from jasmin.redis.client import ConnectionWithConfiguration
from jasmin.redis.configs import RedisForJasminConfig

# Related to travis-ci builds
ROOT_PATH = os.getenv('ROOT_PATH', '/')


class Options(usage.Options):
    optParameters = [
        ['config', 'c', '%s/etc/jasmin/dlrlookupd.cfg' % ROOT_PATH,
         'Jasmin dlrlookupd configuration file'],
        ['id', 'i', 'master',
         'Daemon id, need to be different for each dlrlookupd daemon'],
    ]

    optFlags = [
    ]


class DlrlookupDaemon(object):
    def __init__(self, opt):
        self.options = opt
        self.components = {}

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

    def startDLRLookupService(self):
        """Start DLRLookup"""

        DLRLookupConfigInstance = DLRLookupConfig(self.options['config'])
        self.components['dlrlookup'] = DLRLookup(DLRLookupConfigInstance, self.components['amqp-broker-factory'],
                                                 self.components['rc'])

    @defer.inlineCallbacks
    def start(self):
        """Start Dlrlookupd daemon"""
        syslog.syslog(syslog.LOG_INFO, "Starting Dlrlookup Daemon ...")

        ########################################################
        # Connect to redis server
        try:
            yield self.startRedisClient()
        except Exception, e:
            syslog.syslog(syslog.LOG_ERR, "  Cannot start RedisClient: %s" % e)
        else:
            syslog.syslog(syslog.LOG_INFO, "  RedisClient Started.")

        ########################################################
        # Start AMQP Broker
        try:
            self.startAMQPBrokerService()
            yield self.components['amqp-broker-factory'].getChannelReadyDeferred()
        except Exception, e:
            syslog.syslog(syslog.LOG_ERR, "  Cannot start AMQP Broker: %s" % e)
        else:
            syslog.syslog(syslog.LOG_INFO, "  AMQP Broker Started.")

        try:
            # [optional] Start DLR Lookup
            self.startDLRLookupService()
        except Exception, e:
            syslog.syslog(syslog.LOG_ERR, "  Cannot start DLRLookup: %s" % e)
        else:
            syslog.syslog(syslog.LOG_INFO, "  DLRLookup Started.")

    @defer.inlineCallbacks
    def stop(self):
        """Stop Dlrlookup daemon"""
        syslog.syslog(syslog.LOG_INFO, "Stopping Dlrlookup Daemon ...")

        if 'amqp-broker-client' in self.components:
            yield self.stopAMQPBrokerService()
            syslog.syslog(syslog.LOG_INFO, "  AMQP Broker disconnected.")

        if 'rc' in self.components:
            yield self.stopRedisClient()
            syslog.syslog(syslog.LOG_INFO, "  RedisClient stopped.")

        reactor.stop()

    def sighandler_stop(self, signum, frame):
        """Handle stop signal cleanly"""
        syslog.syslog(syslog.LOG_INFO, "Received signal to stop Jasmin DlrlookupDaemon")

        return self.stop()


if __name__ == '__main__':
    lock = None
    try:
        options = Options()
        options.parseOptions()

        # Must not be executed simultaneously (c.f. #265)
        lock = FileLock("/tmp/dlrlookupd-%s" % options['id'])

        # Ensure there are no paralell runs of this script
        lock.acquire(timeout=2)

        # Prepare to start
        ja_d = DlrlookupDaemon(options)
        # Setup signal handlers
        signal.signal(signal.SIGINT, ja_d.sighandler_stop)
        # Start DlrlookupDaemon
        ja_d.start()

        reactor.run()
    except usage.UsageError, errortext:
        print '%s: %s' % (sys.argv[0], errortext)
        print '%s: Try --help for usage details.' % (sys.argv[0])
    except LockTimeout:
        print "Lock not acquired ! exiting"
    except AlreadyLocked:
        print "There's another instance on dlrlookupd running, exiting."
    finally:
        # Release the lock
        if lock is not None and lock.i_am_locking():
            lock.release()
