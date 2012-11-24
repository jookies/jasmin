# Copyright 2012 Fourat Zouari <fourat@gmail.com>
# See LICENSE for details.

"""
An example of scenario with the following actions:
 1. Add and start a connector
 2. Provision a DefaultRoute to that connector
 3. Provision a User
 4. send an sms through http api using that User
 
The jasmin SMS gateway shall be already running and having
a pb listening on 8989
"""

import pickle
from twisted.internet import reactor, defer
from jasmin.managers.proxies import SMPPClientManagerPBProxy
from jasmin.routing.proxies import RouterPBProxy
from jasmin.routing.Routes import DefaultRoute
from jasmin.routing.jasminApi import Connector, User, Group
from jasmin.protocols.smpp.configs import SMPPClientConfig
from twisted.web.client import getPage

@defer.inlineCallbacks
def runScenario():
    try:
        # Now connect to SMPP Client management PB proxy
        proxy_smpp = SMPPClientManagerPBProxy()

        yield proxy_smpp.connect('127.0.0.1', 8989)
        connector1 = {'id':'abc', 'username':'smppclient1', 'reconnectOnConnectionFailure':True}
        config1 = SMPPClientConfig(**connector1)
        yield proxy_smpp.add(config1)
        yield proxy_smpp.start('abc')
        
        # Now connect to SMPP Client management PB proxy
        proxy_router = RouterPBProxy()
        yield proxy_router.connect('127.0.0.1', 8988)
        
        # Provision router with routes
        yield proxy_router.mtroute_add(DefaultRoute(Connector('abc')), 0)
        routes = yield proxy_router.mtroute_get_all()
        print "Configured routes: \n\t%s" % pickle.loads(routes)
        
        # Provisiong router with users
        u1 = User(uid = 1, group = Group(1), username = 'fourat', password = 'anypassword')
        yield proxy_router.user_add(u1)
        users = yield proxy_router.user_get_all()
        print "Users: \n\t%s" % pickle.loads(users)
        
        c = yield getPage('http://127.0.0.1:1401/send?to=%2b21698700177&content=test&username=fourat&password=anypassword')
        print "SMS Id: \n\t%s" % c
                
        #yield proxy.stop('abc')
    except Exception, e:
        print "ERROR RUNNING SCENARIO: %s" % str(e)
    finally:
        reactor.stop()

runScenario()
reactor.run()