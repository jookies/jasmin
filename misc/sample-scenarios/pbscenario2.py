"""
An example of scenario with the following actions:
 1. Add and start a connectors
 2. send an sms with validy time set to 10s through PB client proxy
 
The jasmin SMS gateway shall be already running and having
a pb listening on 8989
"""

from twisted.internet import reactor, defer
from jasmin.managers.proxies import SMPPClientManagerPBProxy
from jasmin.protocols.smpp.operations import SMPPOperationFactory
from jasmin.protocols.smpp.configs import SMPPClientConfig
import datetime

@defer.inlineCallbacks
def runScenario():
    try:
        connector1 = {'id':'abc', 'username':'smppclient1', 'reconnectOnConnectionFailure':True}
        
        config1 = SMPPClientConfig(**connector1)
        opFactory = SMPPOperationFactory(config1)
        SubmitSmPDU = opFactory.SubmitSM(
            source_addr='1423',
            destination_addr='98700177',
            short_message='Hello world !',
        )
        
        proxy = SMPPClientManagerPBProxy()

        yield proxy.connect('127.0.0.1', 8989)
        yield proxy.add(config1)
        yield proxy.start('abc')
        SubmitSmPDU.params['short_message'] = 'val 10 seconds'
        delta = datetime.timedelta(seconds=10)
        SubmitSmPDU.params['validity_period'] = datetime.datetime.today() + delta
        c = yield proxy.submit_sm(config1.id, SubmitSmPDU)
        print c
                
        yield proxy.stop('abc')
    except Exception, e:
        print "ERROR RUNNING SCENARIO: %s" % str(e)
    finally:
        reactor.stop()

runScenario()
reactor.run()