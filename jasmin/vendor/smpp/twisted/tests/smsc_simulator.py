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
import logging, struct, StringIO, binascii
from twisted.internet.protocol import Protocol, Factory
from twisted.internet import reactor, protocol
from jasmin.vendor.smpp.pdu.operations import *
from jasmin.vendor.smpp.pdu.pdu_encoding import PDUEncoder
from jasmin.vendor.smpp.pdu.pdu_types import *

LOG_CATEGORY="smpp.twisted.tests.smsc_simulator"

class BlackHoleSMSC( protocol.Protocol ):

    responseMap = {}

    def __init__( self ):
        self.log = logging.getLogger(LOG_CATEGORY)
        self.recvBuffer = ""
        self.lastSeqNum = 0
        self.encoder = PDUEncoder()

    def dataReceived( self, data ):
        self.recvBuffer = self.recvBuffer + data

        while len( self.recvBuffer ) > 3:
            ( length, ) = struct.unpack( '!L', self.recvBuffer[:4] )
            if len( self.recvBuffer ) < length:
                break
            message = self.recvBuffer[:length]
            self.recvBuffer = self.recvBuffer[length:]
            self.rawMessageReceived( message )

    def rawMessageReceived( self, message ):
        return self.PDUReceived( self.encoder.decode( StringIO.StringIO(message) ) )

    def PDUReceived( self, pdu ):
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
        self.transport.write( encoded )

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

    def __init__( self ):
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

    def __init__( self ):
        HappySMSC.__init__(self)
        del self.responseMap[Unbind]

class BindErrorSMSC(BlackHoleSMSC):

    def __init__( self ):
        BlackHoleSMSC.__init__(self)
        self.responseMap = {
            BindTransmitter: self.bindError,
            BindReceiver: self.bindError,
            BindTransceiver: self.bindError,
        }

    def bindError(self, reqPDU):
        self.sendResponse(reqPDU, CommandStatus.ESME_RBINDFAIL)

class BindErrorGenericNackSMSC(BlackHoleSMSC):

    def __init__( self ):
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

    def __init__( self ):
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
        #Overwrite the command length (first octet)
        badCmdLenHex = '0000000f'
        badHexEncoded = badCmdLenHex + hexEncoded[len(badCmdLenHex):]
        self.log.debug("Sending PDU with cmd len too small [%s]" % badHexEncoded)
        badEncoded = binascii.a2b_hex(badHexEncoded)
        self.transport.write(badEncoded)

class CommandLengthTooLongSMSC(BlackHoleSMSC):

    def __init__( self ):
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
        #Overwrite the command length (first octet)
        badCmdLenHex = '0000ffff'
        badHexEncoded = badCmdLenHex + hexEncoded[len(badCmdLenHex):]
        self.log.debug("Sending PDU with cmd len too large [%s]" % badHexEncoded)
        badEncoded = binascii.a2b_hex(badHexEncoded)
        self.transport.write(badEncoded)

class InvalidCommandIdSMSC(BlackHoleSMSC):

    def __init__( self ):
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
        #Overwrite the command id (second octet)
        badCmdIdHex = 'f0000009'
        badHexEncoded = hexEncoded[:8] + badCmdIdHex + hexEncoded[8 + len(badCmdIdHex):]
        self.log.debug("Sending PDU with invalid cmd id [%s]" % badHexEncoded)
        badEncoded = binascii.a2b_hex(badHexEncoded)
        self.transport.write(badEncoded)

class NonFatalParseErrorSMSC(BlackHoleSMSC):
    seqNum = 2654

    def __init__( self ):
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
        #Overwrite the source_addr_ton param (18th octet)
        badSrcAddrTonHex = '07'
        badIdx = 17*2
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

    def __init__( self ):
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

    def __init__( self ):
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

if __name__ == '__main__':
    logging.basicConfig(level=logging.DEBUG)
    factory          = Factory()
    factory.protocol = BlackHoleSMSC
    reactor.listenTCP(8007, factory)
    reactor.run()
