# Copyright 2012 Fourat Zouari <fourat@gmail.com>
# See LICENSE for details.

"""
An example of scenario with the following actions:
 1. Add and start a connector
 2. Provision a DefaultRoute to that connector
 3. Provision a User
 4. send an sms through http api using that User and requesting a level_1 DLR
 5. send an sms through http api using that User and requesting a level_2 DLR with GET method
 6. send an sms through http api using that User and requesting a level_3 DLR
 7. Shut down the connector
 
 For more details, c.f. jasmin.routing.test.test_router.DeliveryReceiptTestCases
 
The jasmin SMS gateway shall be already running and having
a pb listening on 8989
"""

import logging
import pickle
import urllib
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

        # Provision SMPPClientManagerPBProxy with a connector and start it
        connector1 = {'id':'SMPPCONNECTOR', 'username':'zfourat', 'password':'testpasswd', 
                      'host':'127.0.0.1', 'port':2775, 
                      'reconnectOnConnectionFailure':True, 'log_level':logging.DEBUG}
        config1 = SMPPClientConfig(**connector1)
        
        # Adds the connector if only it's not already added
        # This is to avoid a known bug:
        # https://github.com/fourat/jasmin/issues/21
        addedConnectors = yield proxy_smpp.connector_list()
        alreadyAdded = False
        for c in addedConnectors:
            if c['id'] == 'SMPPCONNECTOR':
                alreadyAdded = True
        if not alreadyAdded:
            rAdd = yield proxy_smpp.add(config1)
            if not rAdd:
                raise Exception('Cannot add the connector')
        
        cDetails = yield proxy_smpp.connector_details('SMPPCONNECTOR')
        if not cDetails:
            raise Exception('SMPP Connector is not available')
        
        cDetails = yield proxy_smpp.connector_details('SMPPCONNECTOR')
        if cDetails['session_state'] != 'BOUND_TRX':
            print "Starting connector ..."
            yield proxy_smpp.start('SMPPCONNECTOR')

            # Wait some seconds for getting bound
            print 'Waiting for 5s for getting bound ...'
            timer = defer.Deferred()
            reactor.callLater(5, timer.callback, None)
            yield timer
        
        # Now connect to Router PB proxy
        proxy_router = RouterPBProxy()
        yield proxy_router.connect('127.0.0.1', 8988)
        
        # Provision RouterPBProxy with MT routes
        yield proxy_router.mtroute_add(DefaultRoute(Connector('SMPPCONNECTOR')), 0)
        routes = yield proxy_router.mtroute_get_all()
        print "Configured routes: \n\t%s" % pickle.loads(routes)
        
        # Provision router with users
        u1 = User(uid = 1, group = Group(1), username = 'fourat', password = 'anypassword')
        yield proxy_router.user_add(u1)
        users = yield proxy_router.user_get_all()
        #print "Users: \n\t%s" % pickle.loads(users)
        
        # Send a SMS MT through http interface using DLR LEVEL = 1
        baseurl = 'http://127.0.0.1:1401/send'
        params = urllib.urlencode({'from': 'FirstSMS',
                                   'to': '+21698700177', 
                                   'username': 'fourat', 
                                   'password': 'anypassword', 
                                   'content': 'test dlr level 1', 
                                   'dlr-url': 'http://127.0.0.1/any/receive_dlr.php',
                                   'dlr-level': 1})
        baseurl += '?%s' % params        
        c = yield getPage(baseurl)
        print "SMS Id: \n\t%s" % c

        # Send a SMS MT through http interface using DLR LEVEL = 2 and DLR METHOD is GET
        baseurl = 'http://127.0.0.1:1401/send'
        params = urllib.urlencode({'from': 'FirstSMS',
                                   'to': '+21698700177', 
                                   'username': 'fourat', 
                                   'password': 'anypassword', 
                                   'content': 'test dlr level 2', 
                                   'dlr-url': 'http://127.0.0.1/any/receive_dlr.php',
                                   'dlr-method': 'get',
                                   'dlr-level': 2})
        baseurl += '?%s' % params        
        c = yield getPage(baseurl)
        print "SMS Id: \n\t%s" % c

        # Send a SMS MT through http interface using DLR LEVEL = 1
        baseurl = 'http://127.0.0.1:1401/send'
        params = urllib.urlencode({'from': 'FirstSMS',
                                   'to': '+21698700177', 
                                   'username': 'fourat', 
                                   'password': 'anypassword', 
                                   'content': 'test dlr level 3', 
                                   'dlr-url': 'http://127.0.0.1/any/receive_dlr.php',
                                   'dlr-level': 3})
        baseurl += '?%s' % params        
        c = yield getPage(baseurl)
        print "SMS Id: \n\t%s" % c

        # Wait some seconds before shutting down the smpp connector
        print 'Waiting for 20s before shutting down ...'
        timer = defer.Deferred()
        reactor.callLater(20, timer.callback, None)
        yield timer

        yield proxy_smpp.stop('SMPPCONNECTOR')
    except Exception, e:
        print "ERROR RUNNING SCENARIO: %s" % str(e)
    finally:
        reactor.stop()

runScenario()
reactor.run()