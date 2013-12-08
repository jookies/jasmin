# Copyright 2012 Fourat Zouari <fourat@gmail.com>
# See LICENSE for details.

from jasmin.vendor.smpp.twisted.tests.smsc_simulator import *
from jasmin.vendor.smpp.pdu.pdu_types import *
import random

LOG_CATEGORY="jasmin.smpp.tests.smsc_simulator"

class NoSubmitSmWhenReceiverIsBoundSMSC(HappySMSC):
    
    def handleSubmit(self, reqPDU):
        self.sendResponse(reqPDU, CommandStatus.ESME_RINVBNDSTS)
        
class HappySMSCRecorder(HappySMSC):
    submitRecords = []
    
    def handleSubmit(self, reqPDU):
        self.submitRecords.append(reqPDU)
        self.sendSuccessResponse(reqPDU)
        
    def handleData(self, reqPDU):
        self.sendSuccessResponse(reqPDU)
                
class DeliverSmSMSC(HappySMSC):
    def trigger_deliver_sm(self, pdu):
        self.sendPDU(pdu)
        
class DeliveryReceiptSMSC(HappySMSC):
    """Will send a deliver_sm on bind request
    """
    
    def __init__( self ):
        HappySMSC.__init__(self)
        self.responseMap[BindReceiver] = self.sendDeliverSM
        self.responseMap[BindTransceiver] = self.sendDeliverSM
        
    def sendDeliverSM(self, reqPDU):
        self.sendSuccessResponse(reqPDU)
        
        message_id = '1891273321'
        pdu = DeliverSM(
            source_addr='1234',
            destination_addr='4567',
            short_message='id:%s sub:001 dlvrd:001 submit date:1305050826 done date:1305050826 stat:DELIVRD err:000 text:DLVRD TO MOBILE' % message_id,
            message_state=MessageState.DELIVERED,
            receipted_message_id=message_id,
        )
        self.sendPDU(pdu)

class ManualDeliveryReceiptHappySMSC(HappySMSC):
    """Will send a deliver_sm through trigger_DLR() method
    A submit_sm must be sent to this SMSC before requesting sendDeliverSM !
    """
    submitRecords = []
    lastSubmitSmRestPDU = None
    lastSubmitSmPDU = None

    def __init__(self):
        HappySMSC.__init__(self)
        
    def sendSuccessResponse(self, reqPDU):
        if str(reqPDU.commandId)[:5] == 'bind_':
            self.submitRecords = []

        HappySMSC.sendSuccessResponse(self, reqPDU)

    def sendSubmitSmResponse(self, reqPDU):
        self.lastSubmitSmRestPDU = reqPDU.requireAck(reqPDU.seqNum, status=CommandStatus.ESME_ROK, message_id = str(random.randint(10000000, 9999999999)))
        self.sendPDU(self.lastSubmitSmRestPDU)

    def handleSubmit(self, reqPDU):
        # Send back a submit_sm_resp
        self.sendSubmitSmResponse(reqPDU)
        
        self.lastSubmitSmPDU = reqPDU
        self.submitRecords.append(reqPDU)

    def trigger_DLR(self):
        if self.lastSubmitSmRestPDU is None:
            raise Exception('A submit_sm must be sent to this SMSC before requesting sendDeliverSM !')
        
        # Send back a deliver_sm with containing a DLR
        pdu = DeliverSM(
            source_addr=self.lastSubmitSmPDU.params['source_addr'],
            destination_addr=self.lastSubmitSmPDU.params['destination_addr'],
            short_message='id:%s sub:001 dlvrd:001 submit date:1305050826 done date:1305050826 stat:DELIVRD err:000 text:%s' % (
                        self.lastSubmitSmRestPDU.params['message_id'],
                        self.lastSubmitSmPDU.params['short_message'][:20]
                        ),
            message_state=MessageState.DELIVERED,
            receipted_message_id=self.lastSubmitSmRestPDU.params['message_id'],
        )
        self.sendPDU(pdu)

if __name__ == '__main__':
    logging.basicConfig(level=logging.DEBUG)
    factory          = Factory()
    factory.protocol = BlackHoleSMSC
    reactor.listenTCP(8007, factory) 
    reactor.run()
