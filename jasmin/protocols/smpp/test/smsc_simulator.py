from datetime import datetime, timedelta
from jasmin.vendor.smpp.twisted.tests.smsc_simulator import *
from jasmin.vendor.smpp.pdu.pdu_types import *
import random

LOG_CATEGORY="jasmin.smpp.tests.smsc_simulator"

message_state_map = {
    'ACCEPTD':                  MessageState.ACCEPTED,
    'UNDELIV':                  MessageState.UNDELIVERABLE,
    'REJECTD':                  MessageState.REJECTED,
    'DELIVRD':                  MessageState.DELIVERED,
    'EXPIRED':                  MessageState.EXPIRED,
    'DELETED':                  MessageState.DELETED,
    'ACCEPTD':                  MessageState.ACCEPTED,
    'UNKNOWN':                  MessageState.UNKNOWN,
}

class NoSubmitSmWhenReceiverIsBoundSMSC(HappySMSC):

    def handleSubmit(self, reqPDU):
        self.sendResponse(reqPDU, CommandStatus.ESME_RINVBNDSTS)

class NoResponseOnSubmitSMSCRecorder(HappySMSC):
    submitRecords = []

    def handleSubmit(self, reqPDU):
        self.submitRecords.append(reqPDU)
        pass

class HappySMSCRecorder(HappySMSC):
    def __init__(self):
        HappySMSC.__init__(self)

        self.pduRecords = []
        self.submitRecords = []

    def PDUReceived( self, pdu ):
        HappySMSC.PDUReceived( self, pdu )
        self.pduRecords.append(pdu)

    def handleSubmit(self, reqPDU):
        self.submitRecords.append(reqPDU)

        self.sendSubmitSmResponse(reqPDU)

    def sendSubmitSmResponse(self, reqPDU):
        if reqPDU.params['short_message'] == 'test_error: ESME_RTHROTTLED':
            status = CommandStatus.ESME_RTHROTTLED
        elif reqPDU.params['short_message'] == 'test_error: ESME_RSYSERR':
            status = CommandStatus.ESME_RSYSERR
        elif reqPDU.params['short_message'] == 'test_error: ESME_RREPLACEFAIL':
            status = CommandStatus.ESME_RREPLACEFAIL
        else:
            status = CommandStatus.ESME_ROK

        # Return back a pdu
        self.lastSubmitSmRestPDU = reqPDU.requireAck(reqPDU.seqNum, status=status, message_id = str(random.randint(10000000, 9999999999)))
        self.sendPDU(self.lastSubmitSmRestPDU)

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

        self.nextResponseMsgId = None
        self.pduRecords = []

    def PDUReceived( self, pdu ):
        HappySMSC.PDUReceived(self, pdu)
        self.pduRecords.append(pdu)

    def sendSuccessResponse(self, reqPDU):
        if str(reqPDU.commandId)[:5] == 'bind_':
            self.submitRecords = []

        HappySMSC.sendSuccessResponse(self, reqPDU)

    def sendSubmitSmResponse(self, reqPDU):
        if self.nextResponseMsgId is None:
            msgid = str(random.randint(10000000, 9999999999))
        else:
            msgid = str(self.nextResponseMsgId)
            self.nextResponseMsgId = None

        self.lastSubmitSmRestPDU = reqPDU.requireAck(reqPDU.seqNum,
            status=CommandStatus.ESME_ROK,
            message_id=msgid,
            )
        self.sendPDU(self.lastSubmitSmRestPDU)

    def handleSubmit(self, reqPDU):
        # Send back a submit_sm_resp
        self.sendSubmitSmResponse(reqPDU)

        self.lastSubmitSmPDU = reqPDU
        self.submitRecords.append(reqPDU)

    def trigger_deliver_sm(self, pdu):
        self.sendPDU(pdu)

    def trigger_data_sm(self, pdu):
        self.sendPDU(pdu)

    def trigger_DLR(self, _id = None, pdu_type = 'deliver_sm', stat = 'DELIVRD'):
        if self.lastSubmitSmRestPDU is None:
            raise Exception('A submit_sm must be sent to this SMSC before requesting sendDeliverSM !')

        if _id is None:
            _id = self.lastSubmitSmRestPDU.params['message_id']

        if pdu_type == 'deliver_sm':
            # Send back a deliver_sm with containing a DLR
            pdu = DeliverSM(
                source_addr=self.lastSubmitSmPDU.params['source_addr'],
                destination_addr=self.lastSubmitSmPDU.params['destination_addr'],
                short_message='id:%s sub:001 dlvrd:001 submit date:1305050826 done date:1305050826 stat:%s err:000 text:%s' % (
                            str(_id),
                            stat,
                            self.lastSubmitSmPDU.params['short_message'][:20]
                            ),
                message_state=message_state_map[stat],
                receipted_message_id=str(_id),
            )
            self.trigger_deliver_sm(pdu)
        elif pdu_type == 'data_sm':
            # Send back a data_sm with containing a DLR
            pdu = DataSM(
                source_addr=self.lastSubmitSmPDU.params['source_addr'],
                destination_addr=self.lastSubmitSmPDU.params['destination_addr'],
                message_state=message_state_map[stat],
                receipted_message_id=str(_id),
            )
            self.trigger_data_sm(pdu)
        else:
            raise Exception('Unknown pdu_type (%s) when calling trigger_DLR()' % pdu_type)

class QoSSMSC_2MPS(HappySMSC):
    "A throttled SMSC that only accept 2 Messages per second"
    last_submit_at = None

    def handleSubmit(self, reqPDU):
        # Calculate MPS
        permitted_throughput = 1 / 2.0
        permitted_delay = timedelta( microseconds = permitted_throughput * 1000000)
        if self.last_submit_at is not None:
            delay = datetime.now() - self.last_submit_at

        if self.last_submit_at is not None and delay < permitted_delay:
            self.sendResponse(reqPDU, CommandStatus.ESME_RTHROTTLED)
        else:
            self.last_submit_at = datetime.now()
            self.sendResponse(reqPDU, CommandStatus.ESME_ROK)

if __name__ == '__main__':
    logging.basicConfig(level=logging.DEBUG)
    factory          = Factory()
    factory.protocol = BlackHoleSMSC
    reactor.listenTCP(8007, factory)
    reactor.run()
