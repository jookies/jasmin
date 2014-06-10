# Copyright 2012 Fourat Zouari <fourat@gmail.com>
# See LICENSE for details.

"""
An example of scenario with the following actions:
 1. Add two connectors
 2. start them
 3. list connectors
 4. remove one connector
 5. relist connectors
 
The jasmin SMS gateway shall be already running and having
a pb listening on 8989
"""

from twisted.spread import pb
from twisted.internet import reactor, defer
from jasmin.managers.proxies import SMPPClientManagerPBProxy
from jasmin.protocols.smpp.configs import SMPPClientConfig

@defer.inlineCallbacks
def runScenario():
    try:
        connector1 = {'id':'abc', 'username':'smppclient1', 'reconnectOnConnectionFailure':False}
        connector2 = {'id':'abc2', 'username':'smppclient2', 'reconnectOnConnectionFailure':False}
        
        proxy = SMPPClientManagerPBProxy()

        # Anonymous connection used
        yield proxy.connect('127.0.0.1', 8989)
        yield proxy.add(SMPPClientConfig(**connector1))
        yield proxy.add(SMPPClientConfig(**connector2))
        yield proxy.start('abc')
        yield proxy.start('abc2')
        clist = yield proxy.connector_list()
        print clist

        yield proxy.remove('abc')
        
        clist = yield proxy.connector_list()
        print clist
    except Exception, e:
        print "ERROR RUNNING SCENARIO: %s" % str(e)
    finally:
        reactor.stop()

runScenario()
reactor.run()
