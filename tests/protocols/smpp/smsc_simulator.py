"""
Copyright 2009-2010 Mozes, Inc.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either expressed or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
"""
from io import BytesIO
import logging
import struct
import binascii
import random
from datetime import datetime, timedelta

from twisted.internet.protocol import Protocol
from smpp.pdu.operations import (
    GenericNack,
    EnquireLink,
    BindTransmitter,
    BindTransceiver,
    BindReceiver,
    Unbind,
    SubmitSM,
    DataSM,
    AlertNotification,
    DeliverSM,
    QuerySM,
    Outbind
)
from smpp.pdu.pdu_encoding import PDUEncoder
from smpp.pdu.pdu_types import CommandId, CommandStatus, PDURequest, AddrTon, MessageState

LOG_CATEGORY = "jasmin.smpp.tests.smsc_simulator"

message_state_map = {
    'ACCEPTD': MessageState.ACCEPTED,
    'UNDELIV': MessageState.UNDELIVERABLE,
    'REJECTD': MessageState.REJECTED,
    'DELIVRD': MessageState.DELIVERED,
    'EXPIRED': MessageState.EXPIRED,
    'DELETED': MessageState.DELETED,
    'UNKNOWN': MessageState.UNKNOWN,
}


class BlackHoleSMSC(Protocol):
    responseMap = {}

    def __init__(self):
        self.log = logging.getLogger(LOG_CATEGORY)
        self.recvBuffer = b""
        self.lastSeqNum = 0
        self.encoder = PDUEncoder()

    def dataReceived(self, data):
        self.recvBuffer = self.recvBuffer + data

        while len(self.recvBuffer) > 3:
            (length,) = struct.unpack('!L', self.recvBuffer[:4])
            if len(self.recvBuffer) < length:
                break
            message = self.recvBuffer[:length]
            self.recvBuffer = self.recvBuffer[length:]
            self.rawMessageReceived(message)

    def rawMessageReceived(self, message):
        return self.PDUReceived(
            self.encoder.decode(
                BytesIO(message)))

    def PDUReceived(self, pdu):
        if pdu.__class__ in self.responseMap:
            self.responseMap[pdu.__class__](pdu)

    def sendSuccessResponse(self, reqPDU):
        self.sendResponse(reqPDU, CommandStatus.ESME_ROK)

    def sendResponse(self, reqPDU, status):
        respPDU = reqPDU.requireAck(reqPDU.seqNum, status=status)
        self.sendPDU(respPDU)

    def sendPDU(self, pdu):
        if isinstance(pdu, PDURequest) and pdu.seqNum is None:
            self.lastSeqNum += 1
            pdu.seqNum = self.lastSeqNum
        # self.log.debug("Sending PDU: %s" % pdu)
        encoded = self.encoder.encode(pdu)
        # self.log.debug("Sending data [%s]" % binascii.b2a_hex(encoded))
        self.transport.write(encoded)


class HappySMSC(BlackHoleSMSC):

    def __init__(self):
        BlackHoleSMSC.__init__(self)
        self.responseMap = {
            BindTransmitter: self.sendSuccessResponse,
            BindReceiver: self.sendSuccessResponse,
            BindTransceiver: self.sendSuccessResponse,
            EnquireLink: self.sendSuccessResponse,
            Unbind: self.sendSuccessResponse,
            SubmitSM: self.handleSubmit,
            DataSM: self.handleData,
        }

    def handleSubmit(self, reqPDU):
        self.sendSuccessResponse(reqPDU)

    def handleData(self, reqPDU):
        self.sendSuccessResponse(reqPDU)


class AlertNotificationSMSC(HappySMSC):

    def handleData(self, reqPDU):
        HappySMSC.handleData(self, reqPDU)
        self.sendPDU(AlertNotification())


class EnquireLinkEchoSMSC(HappySMSC):

    def __init__(self):
        HappySMSC.__init__(self)
        self.responseMap[EnquireLink] = self.echoEnquireLink

    def echoEnquireLink(self, reqPDU):
        self.sendSuccessResponse(reqPDU)
        self.sendPDU(EnquireLink())


class NoResponseOnSubmitSMSC(HappySMSC):

    def handleSubmit(self, reqPDU):
        pass


class GenericNackNoSeqNumOnSubmitSMSC(HappySMSC):

    def handleSubmit(self, reqPDU):
        respPDU = GenericNack(status=CommandStatus.ESME_RINVCMDLEN)
        self.sendPDU(respPDU)


class GenericNackWithSeqNumOnSubmitSMSC(HappySMSC):

    def handleSubmit(self, reqPDU):
        respPDU = GenericNack(seqNum=reqPDU.seqNum, status=CommandStatus.ESME_RINVCMDID)
        self.sendPDU(respPDU)


class ErrorOnSubmitSMSC(HappySMSC):

    def handleSubmit(self, reqPDU):
        self.sendResponse(reqPDU, CommandStatus.ESME_RINVESMCLASS)


class UnbindOnSubmitSMSC(HappySMSC):

    def handleSubmit(self, reqPDU):
        self.sendPDU(Unbind())


class UnbindNoResponseSMSC(HappySMSC):

    def __init__(self):
        HappySMSC.__init__(self)
        del self.responseMap[Unbind]


class BindErrorSMSC(BlackHoleSMSC):

    def __init__(self):
        BlackHoleSMSC.__init__(self)
        self.responseMap = {
            BindTransmitter: self.bindError,
            BindReceiver: self.bindError,
            BindTransceiver: self.bindError,
        }

    def bindError(self, reqPDU):
        self.sendResponse(reqPDU, CommandStatus.ESME_RBINDFAIL)


class BindErrorGenericNackSMSC(BlackHoleSMSC):

    def __init__(self):
        BlackHoleSMSC.__init__(self)
        self.responseMap = {
            BindTransmitter: self.bindError,
            BindReceiver: self.bindError,
            BindTransceiver: self.bindError,
        }

    def bindError(self, reqPDU):
        respPDU = GenericNack(reqPDU.seqNum)
        respPDU.status = CommandStatus.ESME_RINVCMDID
        self.sendPDU(respPDU)


class CommandLengthTooShortSMSC(BlackHoleSMSC):

    def __init__(self):
        BlackHoleSMSC.__init__(self)
        self.responseMap = {
            BindTransmitter: self.sendInvalidCommandLengthPDUAfterBind,
            BindReceiver: self.sendInvalidCommandLengthPDUAfterBind,
            BindTransceiver: self.sendInvalidCommandLengthPDUAfterBind,
        }

    def sendInvalidCommandLengthPDUAfterBind(self, reqPDU):
        self.sendSuccessResponse(reqPDU)
        unbind = Unbind()
        encoded = self.encoder.encode(unbind)
        hexEncoded = binascii.b2a_hex(encoded)
        # Overwrite the command length (first octet)
        badCmdLenHex = b'0000000f'
        badHexEncoded = badCmdLenHex + hexEncoded[len(badCmdLenHex):]
        self.log.debug("Sending PDU with cmd len too small [%s]" % badHexEncoded)
        badEncoded = binascii.a2b_hex(badHexEncoded)
        self.transport.write(badEncoded)


class CommandLengthTooLongSMSC(BlackHoleSMSC):

    def __init__(self):
        BlackHoleSMSC.__init__(self)
        self.responseMap = {
            BindTransmitter: self.sendInvalidCommandLengthPDUAfterBind,
            BindReceiver: self.sendInvalidCommandLengthPDUAfterBind,
            BindTransceiver: self.sendInvalidCommandLengthPDUAfterBind,
        }

    def sendInvalidCommandLengthPDUAfterBind(self, reqPDU):
        self.sendSuccessResponse(reqPDU)
        unbind = Unbind()
        encoded = self.encoder.encode(unbind)
        hexEncoded = binascii.b2a_hex(encoded)
        # Overwrite the command length (first octet)
        badCmdLenHex = b'0000ffff'
        badHexEncoded = badCmdLenHex + hexEncoded[len(badCmdLenHex):]
        self.log.debug("Sending PDU with cmd len too large [%s]" % badHexEncoded)
        badEncoded = binascii.a2b_hex(badHexEncoded)
        self.transport.write(badEncoded)


class InvalidCommandIdSMSC(BlackHoleSMSC):

    def __init__(self):
        BlackHoleSMSC.__init__(self)
        self.responseMap = {
            BindTransmitter: self.sendInvalidCommandIdAfterBind,
            BindReceiver: self.sendInvalidCommandIdAfterBind,
            BindTransceiver: self.sendInvalidCommandIdAfterBind,
        }

    def sendInvalidCommandIdAfterBind(self, reqPDU):
        self.sendSuccessResponse(reqPDU)
        unbind = Unbind()
        encoded = self.encoder.encode(unbind)
        hexEncoded = binascii.b2a_hex(encoded)
        # Overwrite the command id (second octet)
        badCmdIdHex = b'f0000009'
        badHexEncoded = hexEncoded[:8] + badCmdIdHex + hexEncoded[8 + len(badCmdIdHex):]
        self.log.debug("Sending PDU with invalid cmd id [%s]" % badHexEncoded)
        badEncoded = binascii.a2b_hex(badHexEncoded)
        self.transport.write(badEncoded)


class NonFatalParseErrorSMSC(BlackHoleSMSC):
    seqNum = 2654

    def __init__(self):
        BlackHoleSMSC.__init__(self)
        self.responseMap = {
            BindTransmitter: self.sendInvalidMessageAfterBind,
            BindReceiver: self.sendInvalidMessageAfterBind,
            BindTransceiver: self.sendInvalidMessageAfterBind,
        }

    def sendInvalidMessageAfterBind(self, reqPDU):
        self.sendSuccessResponse(reqPDU)

        pdu = QuerySM(seqNum=self.seqNum,
                      source_addr_ton=AddrTon.ABBREVIATED,
                      source_addr='1234'
                      )
        encoded = self.encoder.encode(pdu)
        hexEncoded = binascii.b2a_hex(encoded)
        # Overwrite the source_addr_ton param (18th octet)
        badSrcAddrTonHex = b'07'
        badIdx = 17 * 2
        badHexEncoded = hexEncoded[:badIdx] + badSrcAddrTonHex + hexEncoded[(badIdx + len(badSrcAddrTonHex)):]
        self.log.debug("Sending PDU with invalid source_addr_ton [%s]" % badHexEncoded)
        badEncoded = binascii.a2b_hex(badHexEncoded)
        self.transport.write(badEncoded)


class DeliverSMBeforeBoundSMSC(BlackHoleSMSC):

    def connectionMade(self):
        pdu = DeliverSM(
            source_addr='1234',
            destination_addr='4567',
            short_message='test',
        )
        self.sendPDU(pdu)


class OutbindSMSC(HappySMSC):

    def __init__(self):
        HappySMSC.__init__(self)
        self.responseMap[BindReceiver] = self.sendDeliverSM

    def connectionMade(self):
        self.sendPDU(Outbind())

    def sendDeliverSM(self, reqPDU):
        self.sendSuccessResponse(reqPDU)

        pdu = DeliverSM(
            source_addr='1234',
            destination_addr='4567',
            short_message='test',
        )
        self.sendPDU(pdu)


class DeliverSMSMSC(HappySMSC):

    def __init__(self):
        HappySMSC.__init__(self)
        self.responseMap[BindReceiver] = self.sendDeliverSM
        self.responseMap[BindTransceiver] = self.sendDeliverSM

    def sendDeliverSM(self, reqPDU):
        self.sendSuccessResponse(reqPDU)

        pdu = DeliverSM(
            source_addr='1234',
            destination_addr='4567',
            short_message='test',
        )
        self.sendPDU(pdu)


class DeliverSMAndUnbindSMSC(DeliverSMSMSC):

    def sendDeliverSM(self, reqPDU):
        DeliverSMSMSC.sendDeliverSM(self, reqPDU)
        self.sendPDU(Unbind())


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

    def PDUReceived(self, pdu):
        HappySMSC.PDUReceived(self, pdu)
        self.pduRecords.append(pdu)

    def handleSubmit(self, reqPDU):
        self.submitRecords.append(reqPDU)

        self.sendSubmitSmResponse(reqPDU)

    def sendSubmitSmResponse(self, reqPDU):
        short_message = reqPDU.params['short_message']
        if isinstance(short_message, bytes):
            short_message = short_message.decode()

        if short_message == 'test_error: ESME_RTHROTTLED':
            status = CommandStatus.ESME_RTHROTTLED
        elif short_message == 'test_error: ESME_RSYSERR':
            status = CommandStatus.ESME_RSYSERR
        elif short_message == 'test_error: ESME_RREPLACEFAIL':
            status = CommandStatus.ESME_RREPLACEFAIL
        else:
            status = CommandStatus.ESME_ROK

        # Return back a pdu
        self.lastSubmitSmRestPDU = reqPDU.requireAck(reqPDU.seqNum, status=status,
                                                     message_id=str(random.randint(10000000, 9999999999)))
        self.sendPDU(self.lastSubmitSmRestPDU)

    def handleData(self, reqPDU):
        self.sendSuccessResponse(reqPDU)


class DeliverSmSMSC(HappySMSC):
    def trigger_deliver_sm(self, pdu):
        self.sendPDU(pdu)


class DeliveryReceiptSMSC(HappySMSC):
    """Will send a deliver_sm on bind request
    """

    def __init__(self):
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

    def PDUReceived(self, pdu):
        HappySMSC.PDUReceived(self, pdu)
        self.pduRecords.append(pdu)

    def sendSuccessResponse(self, reqPDU):
        if reqPDU.commandId in (CommandId.bind_receiver, CommandId.bind_transmitter, CommandId.bind_transceiver):
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
        return self.sendPDU(self.lastSubmitSmRestPDU)

    def handleSubmit(self, reqPDU):
        # Send back a submit_sm_resp
        self.sendSubmitSmResponse(reqPDU)

        self.lastSubmitSmPDU = reqPDU
        self.submitRecords.append(reqPDU)

    def trigger_deliver_sm(self, pdu):
        return self.sendPDU(pdu)

    def trigger_data_sm(self, pdu):
        return self.sendPDU(pdu)

    def trigger_DLR(self, _id=None, pdu_type='deliver_sm', stat='DELIVRD'):
        if self.lastSubmitSmRestPDU is None:
            raise Exception('A submit_sm must be sent to this SMSC before requesting sendDeliverSM !')

        # Pick the last submit_sm
        submitsm_pdu = self.lastSubmitSmPDU

        # Pick the last submit_sm_resp
        submitsm_resp_pdu = self.lastSubmitSmRestPDU
        if _id is None:
            _id = submitsm_resp_pdu.params['message_id']
        
        if isinstance(_id, bytes):
            _id = _id.decode()
        if isinstance(pdu_type, bytes):
            pdu_type = pdu_type.decode()

        if pdu_type == 'deliver_sm':
            # Send back a deliver_sm with containing a DLR
            
            pdu = DeliverSM(
                source_addr=submitsm_pdu.params['source_addr'],
                destination_addr=submitsm_pdu.params['destination_addr'],
                short_message='id:%s sub:001 dlvrd:001 submit date:1305050826 done date:1305050826 stat:%s err:000 text:%s' % (
                    str(_id),
                    stat,
                    submitsm_pdu.params['short_message'][:20]
                ),
                message_state=message_state_map[stat],
                receipted_message_id=str(_id),
            )
            return self.trigger_deliver_sm(pdu)
        elif pdu_type == 'data_sm':
            # Send back a data_sm with containing a DLR
            pdu = DataSM(
                source_addr=submitsm_pdu.params['source_addr'],
                destination_addr=submitsm_pdu.params['destination_addr'],
                message_state=message_state_map[stat],
                receipted_message_id=str(_id),
            )
            return self.trigger_data_sm(pdu)
        else:
            raise Exception('Unknown pdu_type (%s) when calling trigger_DLR()' % pdu_type)


class QoSSMSC_2MPS(HappySMSC):
    """A throttled SMSC that only accept 2 Messages per second"""
    last_submit_at = None

    def handleSubmit(self, reqPDU):
        # Calculate MPS
        permitted_throughput = 1 / 2.0
        permitted_delay = timedelta(microseconds=permitted_throughput * 1000000)
        if self.last_submit_at is not None:
            delay = datetime.now() - self.last_submit_at

        if self.last_submit_at is not None and delay < permitted_delay:
            self.sendResponse(reqPDU, CommandStatus.ESME_RTHROTTLED)
        else:
            self.last_submit_at = datetime.now()
            self.sendResponse(reqPDU, CommandStatus.ESME_ROK)
