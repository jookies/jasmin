# Gist from https://gist.github.com/zoufou/922e1cb2c4782660c257
"""
An example of scenario with the following actions:
 1. Add and start a SMPP Client connector
 2. Provision a DefaultRoute to that connector
 3. Provision a User
 
This is a demonstration of using PB (PerspectiveBroker) API to gain control Jasmin.

The jasmin SMS gateway shall be already running and having
a pb listening on 8989.
"""

import pickle
from twisted.internet import reactor, defer
from jasmin.managers.proxies import SMPPClientManagerPBProxy
from jasmin.routing.proxies import RouterPBProxy
from jasmin.routing.Routes import DefaultRoute
from jasmin.routing.jasminApi import SmppClientConnector, User, Group
from jasmin.protocols.smpp.configs import SMPPClientConfig
from twisted.web.client import getPage

@defer.inlineCallbacks
def runScenario():
    try:
        ## First part, SMPP Client connector management
        ###############################################
        # Connect to SMPP Client management PB proxy
        proxy_smpp = SMPPClientManagerPBProxy()
        yield proxy_smpp.connect('127.0.0.1', 8989, 'cmadmin', 'cmpwd')

        # Provision SMPPClientManagerPBProxy with a connector and start it
        connector1 = {'id':'abc', 'username':'smppclient1', 'reconnectOnConnectionFailure':True}
        config1 = SMPPClientConfig(**connector1)
        yield proxy_smpp.add(config1)
        yield proxy_smpp.start('abc')
        
        ## Second part, User and Routing management
        ###########################################
        # Connect to Router PB proxy
        proxy_router = RouterPBProxy()
        yield proxy_router.connect('127.0.0.1', 8988, 'radmin', 'rpwd')
        
        # Provision RouterPBProxy with MT routes
        yield proxy_router.mtroute_add(DefaultRoute(SmppClientConnector('abc')), 0)
        routes = yield proxy_router.mtroute_get_all()
        print "Configured routes: \n\t%s" % pickle.loads(routes)
        
        # Provisiong router with users
        g1 = Group(1)
        u1 = User(uid = 1, group = g1, username = 'fourat', password = 'anypassword')
        yield proxy_router.group_add(g1)
        yield proxy_router.user_add(u1)
        users = yield proxy_router.user_get_all()
        print "Users: \n\t%s" % pickle.loads(users)
        
        ## Last, tear down
        ##################
        # Stop connector
        yield proxy_smpp.stop('abc')
    except Exception, e:
        print "ERROR RUNNING SCENARIO: %s" % str(e)
    finally:
        reactor.stop()

runScenario()
reactor.run()