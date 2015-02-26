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
"""
Updated code parts are marked with "Jasmin update" comment
"""
from jasmin.vendor.enum import Enum
from jasmin.vendor.smpp.pdu.namedtuple import namedtuple
from jasmin.vendor.smpp.pdu import constants

CommandId = Enum(*constants.command_id_name_map.keys())

CommandStatus = Enum(*constants.command_status_name_map.keys())

Tag = Enum(*constants.tag_name_map.keys())

Option = namedtuple('Option', 'tag, value')

EsmClassMode = Enum(*constants.esm_class_mode_name_map.keys())
EsmClassType = Enum(*constants.esm_class_type_name_map.keys())
EsmClassGsmFeatures = Enum(*constants.esm_class_gsm_features_name_map.keys())

EsmClassBase = namedtuple('EsmClass', 'mode, type, gsmFeatures')

class EsmClass(EsmClassBase):
    
    def __new__(cls, mode, type, gsmFeatures=[]):
        return EsmClassBase.__new__(cls, mode, type, set(gsmFeatures))
        
    def __repr__(self):
        return 'EsmClass[mode: %s, type: %s, gsmFeatures: %s]' % (self.mode, self.type, self.gsmFeatures)

RegisteredDeliveryReceipt = Enum(*constants.registered_delivery_receipt_name_map.keys())
RegisteredDeliverySmeOriginatedAcks = Enum(*constants.registered_delivery_sme_originated_acks_name_map.keys())

RegisteredDeliveryBase = namedtuple('RegisteredDelivery', 'receipt, smeOriginatedAcks, intermediateNotification')

class RegisteredDelivery(RegisteredDeliveryBase):
    
    def __new__(cls, receipt, smeOriginatedAcks=[], intermediateNotification=False):
        return RegisteredDeliveryBase.__new__(cls, receipt, set(smeOriginatedAcks), intermediateNotification)
        
    def __repr__(self):
        return 'RegisteredDelivery[receipt: %s, smeOriginatedAcks: %s, intermediateNotification: %s]' % (self.receipt, self.smeOriginatedAcks, self.intermediateNotification)

AddrTon = Enum(*constants.addr_ton_name_map.keys())
AddrNpi = Enum(*constants.addr_npi_name_map.keys())
PriorityFlag = Enum(*constants.priority_flag_name_map.keys())
ReplaceIfPresentFlag = Enum(*constants.replace_if_present_flap_name_map.keys())

DataCodingScheme = Enum('RAW', 'DEFAULT', *constants.data_coding_scheme_name_map.keys())
DataCodingDefault = Enum(*constants.data_coding_default_name_map.keys())
DataCodingGsmMsgCoding = Enum(*constants.data_coding_gsm_message_coding_name_map.keys())
DataCodingGsmMsgClass = Enum(*constants.data_coding_gsm_message_class_name_map.keys())

DataCodingGsmMsgBase = namedtuple('DataCodingGsmMsg', 'msgCoding, msgClass')

class DataCodingGsmMsg(DataCodingGsmMsgBase):
    
    def __new__(cls, msgCoding, msgClass):
        return DataCodingGsmMsgBase.__new__(cls, msgCoding, msgClass)
        
    def __repr__(self):
        return 'DataCodingGsmMsg[msgCoding: %s, msgClass: %s]' % (self.msgCoding, self.msgClass)


class DataCoding(object):
    
    def __init__(self, scheme=DataCodingScheme.DEFAULT, schemeData=DataCodingDefault.SMSC_DEFAULT_ALPHABET):
        self.scheme = scheme
        self.schemeData = schemeData

    def __repr__(self):
        return 'DataCoding[scheme: %s, schemeData: %s]' % (self.scheme, self.schemeData)
        
    def __eq__(self, other):
        if self.scheme != other.scheme:
            return False
        if self.schemeData != other.schemeData:
            return False
        return True
    
    def __ne__(self, other):
        return not self.__eq__(other)

DestFlag = Enum(*constants.dest_flag_name_map.keys())
MessageState = Enum(*constants.message_state_name_map.keys())
CallbackNumDigitModeIndicator = Enum(*constants.callback_num_digit_mode_indicator_name_map.keys())
SubaddressTypeTag = Enum(*constants.subaddress_type_tag_name_map.keys())

CallbackNumBase = namedtuple('CallbackNum', 'digitModeIndicator, ton, npi, digits')
class CallbackNum(CallbackNumBase):
    
    def __new__(cls, digitModeIndicator, ton=AddrTon.UNKNOWN, npi=AddrNpi.UNKNOWN, digits=None):
        return CallbackNumBase.__new__(cls, digitModeIndicator, ton, npi, digits)
    
    def __repr__(self):
        return 'CallbackNum[digitModeIndicator: %s, ton: %s, npi: %s, digits: %s]' % (self.digitModeIndicator, self.ton, self.npi, self.digits)

SubaddressBase = namedtuple('Subaddress', 'typeTag, value')
class Subaddress(SubaddressBase):
    
    def __new__(cls, typeTag, value):
        return SubaddressBase.__new__(cls, typeTag, value)
    
    def __repr__(self):
        return 'Subaddress[typeTag: %s, value: %s]' % (self.typeTag, self.value)

AddrSubunit = Enum(*constants.addr_subunit_name_map.keys())
NetworkType = Enum(*constants.network_type_name_map.keys())
BearerType = Enum(*constants.bearer_type_name_map.keys())
PayloadType = Enum(*constants.payload_type_name_map.keys())
PrivacyIndicator = Enum(*constants.privacy_indicator_name_map.keys())
LanguageIndicator = Enum(*constants.language_indicator_name_map.keys())
DisplayTime = Enum(*constants.display_time_name_map.keys())
MsAvailabilityStatus = Enum(*constants.ms_availability_status_name_map.keys())
NetworkErrorCode = Enum(*constants.network_error_code_name_map.keys())
DeliveryFailureReason = Enum(*constants.delivery_failure_reason_name_map.keys())
MoreMessagesToSend = Enum(*constants.more_messages_to_send_name_map.keys())

class PDU(object):
    commandId = None
    mandatoryParams = []
    optionalParams = []
    
    def __init__(self, seqNum=None, status=CommandStatus.ESME_ROK, **kwargs):
        self.id = self.commandId
        self.seqNum = seqNum
        self.status = status
        self.params = kwargs
        for mParam in self.mandatoryParams:
            if mParam not in self.params:
                self.params[mParam] = None
    
    def __repr__(self):
        # Jasmin update:
        # Displaying values with %r converter since %s doesnt work with unicode
        r = "PDU [command: %s, sequence_number: %s, command_status: %s" % (self.id, self.seqNum, self.status)
        for mParam in self.mandatoryParams:
            if mParam in self.params:
                r += "\n%s: %r" % (mParam, self.params[mParam])
        for oParam in self.params.keys():
            if oParam not in self.mandatoryParams:
                r += "\n%s: %r" % (oParam, self.params[oParam])                
        r += '\n]'
        return r
        
    def __eq__(self, pdu):
        if self.id != pdu.id:
            return False
        if self.seqNum != pdu.seqNum:
            return False
        if self.status != pdu.status:
            return False
        if self.params != pdu.params:
            return False
        return True
        
    def __ne__(self, other):
        return not self.__eq__(other)
        
class PDURequest(PDU):
    requireAck = None

class PDUResponse(PDU):
    noBodyOnError = False

    def __init__(self, seqNum=None, status=CommandStatus.ESME_ROK, **kwargs):
        """Some PDU responses have no defined body when the status is not 0
            c.f. 4.1.4. "BIND_RECEIVER_RESP"
            c.f. 4.4.2. SMPP PDU Definition "SUBMIT_SM_RESP"
        """
        PDU.__init__(self, seqNum, status, **kwargs)
            
        if self.noBodyOnError:
            if status != CommandStatus.ESME_ROK:
                self.params = {}
        

class PDUDataRequest(PDURequest):
    pass
