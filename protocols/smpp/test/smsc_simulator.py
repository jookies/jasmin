# Copyright 2012 Fourat Zouari <fourat@gmail.com>
# See LICENSE for details.

from smpp.twisted.tests.smsc_simulator import *

LOG_CATEGORY="jasmin.smpp.tests.smsc_simulator"

class NoSubmitSmWhenReceiverIsBoundSMSC(HappySMSC):
    
    def handleSubmit(self, reqPDU):
        self.sendResponse(reqPDU, CommandStatus.ESME_RINVBNDSTS)
        self.transport.loseConnection()
        
class HappySMSCRecorder(HappySMSC):
    submitRecords = []
    
    def handleSubmit(self, reqPDU):
        self.submitRecords.append(reqPDU)
        self.sendSuccessResponse(reqPDU)
        
    def handleData(self, reqPDU):
        self.sendSuccessResponse(reqPDU)
        
class DeliveryReceiptSMSMSC(HappySMSC):
    
    def __init__( self ):
        HappySMSC.__init__(self)
        self.responseMap[BindReceiver] = self.sendDeliverSM
        self.responseMap[BindTransceiver] = self.sendDeliverSM
        
    def sendDeliverSM(self, reqPDU):
        self.sendSuccessResponse(reqPDU)
        
        pdu = DeliverSM(
            source_addr='1234',
            destination_addr='4567',
            short_message='id:1891273321 sub:001 dlvrd:001 submit date:1305050826 done date:1305050826 stat:DELIVRD err:000 text:DLVRD TO MOBILE',
        )
        self.sendPDU(pdu)


if __name__ == '__main__':
    logging.basicConfig(level=logging.DEBUG)
    factory          = Factory()
    factory.protocol = BlackHoleSMSC
    reactor.listenTCP(8007, factory) 
    reactor.run()