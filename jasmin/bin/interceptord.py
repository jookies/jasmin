#!/usr/bin/python

import os
import sys
import signal
import syslog
from twisted.python import usage
from jasmin.tools.cred.checkers import RouterAuthChecker
from jasmin.interceptor.interceptor import InterceptorPB
from jasmin.interceptor.configs import InterceptorPBConfig
from twisted.cred import portal
from twisted.cred.checkers import AllowAnonymousAccess, InMemoryUsernamePasswordDatabaseDontUse
from jasmin.tools.cred.portal import JasminPBRealm
from jasmin.tools.spread.pb import JasminPBPortalRoot
from twisted.spread import pb
from twisted.internet import reactor, defer

# Related to travis-ci builds
root_path = os.getenv('ROOT_PATH', '/')

class Options(usage.Options):

    optParameters = [
        ['config',                  'c', '%s/etc/jasmin/interceptor.cfg' % root_path,
         'Jasmin interceptor configuration file'],
        ]

class InterceptorDaemon:

    def __init__(self, options):
        self.options = options
        self.components = {}

    def startInterceptorPBService(self):
        "Start Interceptor PB server"

        InterceptorPBConfigInstance = InterceptorPBConfig(self.options['config'])
        self.components['interceptor-pb-factory'] = InterceptorPB()
        self.components['interceptor-pb-factory'].setConfig(InterceptorPBConfigInstance)

        # Set authentication portal
        p = portal.Portal(JasminPBRealm(self.components['interceptor-pb-factory']))
        if InterceptorPBConfigInstance.authentication:
            c = InMemoryUsernamePasswordDatabaseDontUse()
            c.addUser(InterceptorPBConfigInstance.admin_username,
                      InterceptorPBConfigInstance.admin_password)
            p.registerChecker(c)
        else:
            p.registerChecker(AllowAnonymousAccess())
        jPBPortalRoot = JasminPBPortalRoot(p)

        # Add service
        self.components['interceptor-pb-server'] = reactor.listenTCP(InterceptorPBConfigInstance.port,
                                    pb.PBServerFactory(jPBPortalRoot),
                                    interface=InterceptorPBConfigInstance.bind)

    def stopInterceptorPBService(self):
        "Stop Interceptor PB server"
        return self.components['interceptor-pb-server'].stopListening()

    @defer.inlineCallbacks
    def start(self):
        syslog.syslog(syslog.LOG_INFO, "Starting InterceptorPB Daemon ...")

        ########################################################
        # Start Interceptor PB server
        yield self.startInterceptorPBService()
        syslog.syslog(syslog.LOG_INFO, "  Interceptor Started.")

    @defer.inlineCallbacks
    def stop(self):
        syslog.syslog(syslog.LOG_INFO, "Stopping Interceptor Daemon ...")

        if 'interceptor-pb-server' in self.components:
            yield self.stopInterceptorPBService()
            syslog.syslog(syslog.LOG_INFO, "  InterceptorPB stopped.")

        reactor.stop()

    def sighandler_stop(self, signum, frame):
        syslog.syslog(syslog.LOG_INFO, "Received signal to stop Interceptor Daemon")

        return self.stop()

if __name__ == '__main__':
    options = Options()
    try:
        options.parseOptions()
    except usage.UsageError, errortext:
        print '%s: %s' % (sys.argv[0], errortext)
        print '%s: Try --help for usage details.' % (sys.argv[0])
    else:
        id = InterceptorDaemon(options)
        # Setup signal handlers
        signal.signal(signal.SIGINT, id.sighandler_stop)
        # Start InterceptorDaemon
        id.start()

        reactor.run()
