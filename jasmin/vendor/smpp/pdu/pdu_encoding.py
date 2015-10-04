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
import struct, string, binascii
from jasmin.vendor.smpp.pdu import smpp_time
from jasmin.vendor.smpp.pdu import constants, pdu_types, operations
from jasmin.vendor.smpp.pdu.error import PDUParseError, PDUCorruptError
from jasmin.vendor.smpp.pdu.pdu_types import CommandId
from jasmin.vendor.smpp.pdu.pdu_types import DataCodingDefault
# Jasmin update:
from jasmin.vendor.messaging.sms.gsm0338 import encode

class IEncoder(object):

    def encode(self, value):
        """Takes an object representing the type and returns a byte string"""
        raise NotImplementedError()

    def decode(self, file):
        """Takes file stream in and returns an object representing the type"""
        raise NotImplementedError()

    def read(self, file, size):
        bytesRead = file.read(size)
        length = len(bytesRead)
        if length == 0:
            raise PDUCorruptError("Unexpected EOF", pdu_types.CommandStatus.ESME_RINVMSGLEN)
        if length != size:
            raise PDUCorruptError("Length mismatch. Expecting %d bytes. Read %d" % (size, length), pdu_types.CommandStatus.ESME_RINVMSGLEN)
        return bytesRead

class EmptyEncoder(IEncoder):

    def encode(self, value):
        return ''

    def decode(self, file):
        return None

class PDUNullableFieldEncoder(IEncoder):
    nullHex = None
    nullable = True
    decodeNull = False
    requireNull = False

    def __init__(self, **kwargs):
        self.nullable = kwargs.get('nullable', self.nullable)
        self.decodeNull = kwargs.get('decodeNull', self.decodeNull)
        self.requireNull = kwargs.get('requireNull', self.requireNull)
        self._validateParams()

    def _validateParams(self):
        if self.decodeNull:
            if not self.nullable:
                raise ValueError("nullable must be set if decodeNull is set")
        if self.requireNull:
            if not self.decodeNull:
                raise ValueError("decodeNull must be set if requireNull is set")

    def encode(self, value):
        if value is None:
            if not self.nullable:
                raise ValueError("Field is not nullable")
            if self.nullHex is None:
                raise NotImplementedError("No value for null")
            return binascii.a2b_hex(self.nullHex)
        if self.requireNull:
            raise ValueError("Field must be null")
        return self._encode(value)

    def decode(self, file):
        bytes = self._read(file)
        if self.decodeNull:
            if self.nullHex is None:
                raise NotImplementedError("No value for null")
            if self.nullHex == binascii.b2a_hex(bytes):
                return None
            if self.requireNull:
                raise PDUParseError("Field must be null", pdu_types.CommandStatus.ESME_RUNKNOWNERR)
        return self._decode(bytes)

    def _encode(self, value):
        """Takes an object representing the type and returns a byte string"""
        raise NotImplementedError()

    def _read(self, file):
        """Takes file stream in and returns raw bytes"""
        raise NotImplementedError()

    def _decode(self, bytes):
        """Takes bytes in and returns an object representing the type"""
        raise NotImplementedError()

class IntegerBaseEncoder(PDUNullableFieldEncoder):
    size = None
    sizeFmtMap = {
        1: '!B',
        2: '!H',
        4: '!L',
    }

    #pylint: disable-msg=E0213
    def assertFmtSizes(sizeFmtMap):
        for (size, fmt) in sizeFmtMap.items():
            assert struct.calcsize(fmt) == size

    #Verify platform sizes match protocol
    assertFmtSizes(sizeFmtMap)

    def __init__(self, **kwargs):
        PDUNullableFieldEncoder.__init__(self, **kwargs)

        self.nullHex = '00' * self.size

        self.max = 2 ** (8 * self.size) - 1
        self.min = 0
        if 'max' in kwargs:
            if kwargs['max'] > self.max:
                raise ValueError("Illegal value for max %d" % kwargs['max'])
            self.max = kwargs['max']
        if 'min' in kwargs:
            if kwargs['min'] < self.min:
                raise ValueError("Illegal value for min %d" % kwargs['min'])
            self.min = kwargs['min']
        if self.nullable and self.min > 0:
            self.decodeNull = True

    def _encode(self, value):
        if value > self.max:
            raise ValueError("Value %d exceeds max %d" % (value, self.max))
        if value < self.min:
            raise ValueError("Value %d is less than min %d" % (value, self.min))
        return struct.pack(self.sizeFmtMap[self.size], value)

    def _read(self, file):
        return self.read(file, self.size)

    def _decode(self, bytes):
        return struct.unpack(self.sizeFmtMap[self.size], bytes)[0]

class Int4Encoder(IntegerBaseEncoder):
    size = 4

class Int1Encoder(IntegerBaseEncoder):
    size = 1

class Int2Encoder(IntegerBaseEncoder):
    size = 2

class OctetStringEncoder(PDUNullableFieldEncoder):
    nullable = False

    def __init__(self, size=None, **kwargs):
        PDUNullableFieldEncoder.__init__(self, **kwargs)
        self.size = size

    def getSize(self):
        if callable(self.size):
            return self.size()
        return self.size

    def _encode(self, value):
        length = len(value)
        if self.getSize() is not None:
            if length != self.getSize():
                raise ValueError("Value (%s) size %d does not match expected %d" % (value, length, self.getSize()))

        return value

    def _read(self, file):
        if self.getSize() is None:
            raise AssertionError("Missing size to decode")
        if self.getSize() == 0:
            return ''
        return self.read(file, self.getSize())

    def _decode(self, bytes):
        return bytes

class COctetStringEncoder(PDUNullableFieldEncoder):
    nullHex = '00'
    decodeErrorClass = PDUParseError
    decodeErrorStatus = pdu_types.CommandStatus.ESME_RUNKNOWNERR

    def __init__(self, maxSize=None, **kwargs):
        PDUNullableFieldEncoder.__init__(self, **kwargs)
        if maxSize is not None and maxSize < 1:
            raise ValueError("maxSize must be > 0")
        self.maxSize = maxSize
        self.decodeErrorClass = kwargs.get('decodeErrorClass', self.decodeErrorClass)
        self.decodeErrorStatus = kwargs.get('decodeErrorStatus', self.decodeErrorStatus)

    def _encode(self, value):
        asciiVal = value.encode('ascii')
        length = len(asciiVal)
        if self.maxSize is not None:
            if length + 1 > self.maxSize:
                raise ValueError("COctetString is longer than allowed maximum size (%d): %s" % (self.maxSize, asciiVal))
        encoded =  struct.pack("%ds" % length, asciiVal) + '\0'
        assert len(encoded) == length + 1
        return encoded

    def _read(self, file):
        result = ''
        while True:
            c = self.read(file, 1)
            result += c
            if c == '\0':
                break
        return result

    def _decode(self, bytes):
        if self.maxSize is not None:
            if len(bytes) > self.maxSize:
                errStr = "COctetString is longer than allowed maximum size (%d)" % (self.maxSize)
                raise self.decodeErrorClass(errStr, self.decodeErrorStatus)
        return bytes[:-1]

class IntegerWrapperEncoder(PDUNullableFieldEncoder):
    fieldName = None
    nameMap = None
    valueMap = None
    encoder = None
    pduType = None
    decodeErrorClass = PDUParseError
    decodeErrorStatus = pdu_types.CommandStatus.ESME_RUNKNOWNERR

    def __init__(self, **kwargs):
        PDUNullableFieldEncoder.__init__(self, **kwargs)
        self.nullHex = self.encoder.nullHex
        self.fieldName = kwargs.get('fieldName', self.fieldName)
        self.decodeErrorClass = kwargs.get('decodeErrorClass', self.decodeErrorClass)
        self.decodeErrorStatus = kwargs.get('decodeErrorStatus', self.decodeErrorStatus)

    def _encode(self, value):
        name = str(value)
        if name not in self.nameMap:
            raise ValueError("Unknown %s name %s" % (self.fieldName, name))
        intVal = self.nameMap[name]
        return self.encoder.encode(intVal)

    def _read(self, file):
        return self.encoder._read(file)

    def _decode(self, bytes):
        intVal = self.encoder._decode(bytes)

        # Jasmin update: bypass vendor specific tags
        if self.fieldName == 'tag' and intVal >= 5120 and intVal <= 16383:
            # Vendor specific tag is not supported by Jasmin but must
            # not raise an error
            return self.pduType.vendor_specific_bypass
        elif intVal not in self.valueMap:
            errStr = "Unknown %s value %s" % (self.fieldName, hex(intVal))
            raise self.decodeErrorClass(errStr, self.decodeErrorStatus)

        name = self.valueMap[intVal]
        return getattr(self.pduType, name)

class CommandIdEncoder(IntegerWrapperEncoder):
    fieldName = 'command_id'
    nameMap = constants.command_id_name_map
    valueMap = constants.command_id_value_map
    encoder = Int4Encoder()
    pduType = pdu_types.CommandId
    decodeErrorClass = PDUCorruptError
    decodeErrorStatus = pdu_types.CommandStatus.ESME_RINVCMDID

class CommandStatusEncoder(Int4Encoder):
    nullable = False

    def _encode(self, value):
        name = str(value)
        if name not in constants.command_status_name_map:
            raise ValueError("Unknown command_status name %s" % name)
        intval = constants.command_status_name_map[name]
        return Int4Encoder().encode(intval)

    def _decode(self, bytes):
        intval = Int4Encoder()._decode(bytes)
        if intval not in constants.command_status_value_map:
            # Jasmin update:
            # as of Table 5-2: SMPP Error Codes
            # (256  .. 1023)   0x00000100 .. 0x000003FF = Reserved for SMPP extension
            # (1024 .. 1279)   0x00000400 .. 0x000004FF = Reserved for SMSC vendor specific errors
            # (1280 ...)       0x00000500 ...           = Reserved
            #
            # In order to avoid raising a PDUParseError on one of these reserved error codes,
            # jasmin will return a general status indicating a reserved field
            if 256 <= intval:
                if 256 <= intval <= 1023:
                    name = constants.command_status_value_map[-1]['name']
                elif 1024 <= intval <= 1279:
                    name = constants.command_status_value_map[-2]['name']
                elif 1280 <= intval:
                    name = constants.command_status_value_map[-3]['name']
            else:
                raise PDUParseError("Unknown command_status %s" % intval, pdu_types.CommandStatus.ESME_RUNKNOWNERR)
        else:
            name = constants.command_status_value_map[intval]['name']

        return getattr(pdu_types.CommandStatus, name)

class TagEncoder(IntegerWrapperEncoder):
    fieldName = 'tag'
    nameMap = constants.tag_name_map
    valueMap = constants.tag_value_map
    encoder = Int2Encoder()
    pduType = pdu_types.Tag
    decodeErrorStatus = pdu_types.CommandStatus.ESME_RINVOPTPARSTREAM

class EsmClassEncoder(Int1Encoder):
    modeMask = 0x03
    typeMask = 0x3c
    gsmFeaturesMask = 0xc0

    def _encode(self, esmClass):
        modeName = str(esmClass.mode)
        typeName = str(esmClass.type)
        gsmFeatureNames = [str(f) for f in esmClass.gsmFeatures]

        if modeName not in constants.esm_class_mode_name_map:
            raise ValueError("Unknown esm_class mode name %s" % modeName)
        if typeName not in constants.esm_class_type_name_map:
            raise ValueError("Unknown esm_class type name %s" % typeName)
        for featureName in gsmFeatureNames:
            if featureName not in constants.esm_class_gsm_features_name_map:
                raise ValueError("Unknown esm_class GSM feature name %s" % featureName)

        modeVal = constants.esm_class_mode_name_map[modeName]
        typeVal = constants.esm_class_type_name_map[typeName]
        gsmFeatureVals = [constants.esm_class_gsm_features_name_map[fName] for fName in gsmFeatureNames]

        intVal = modeVal | typeVal
        for fVal in gsmFeatureVals:
            intVal |= fVal

        return Int1Encoder().encode(intVal)

    def _decode(self, bytes):
        intVal = Int1Encoder()._decode(bytes)
        modeVal = intVal & self.modeMask
        typeVal = intVal & self.typeMask
        gsmFeaturesVal = intVal & self.gsmFeaturesMask

        if modeVal not in constants.esm_class_mode_value_map:
            raise PDUParseError("Unknown esm_class mode %s" % modeVal, pdu_types.CommandStatus.ESME_RINVESMCLASS)
        if typeVal not in constants.esm_class_type_value_map:
            raise PDUParseError("Unknown esm_class type %s" % typeVal, pdu_types.CommandStatus.ESME_RINVESMCLASS)

        modeName = constants.esm_class_mode_value_map[modeVal]
        typeName = constants.esm_class_type_value_map[typeVal]
        gsmFeatureNames = [constants.esm_class_gsm_features_value_map[fVal] for fVal in constants.esm_class_gsm_features_value_map.keys() if fVal & gsmFeaturesVal]

        mode = getattr(pdu_types.EsmClassMode, modeName)
        type = getattr(pdu_types.EsmClassType, typeName)
        gsmFeatures = [getattr(pdu_types.EsmClassGsmFeatures, fName) for fName in gsmFeatureNames]

        return pdu_types.EsmClass(mode, type, gsmFeatures)

class RegisteredDeliveryEncoder(Int1Encoder):
    receiptMask = 0x03
    smeOriginatedAcksMask = 0x0c
    intermediateNotificationMask = 0x10

    def _encode(self, registeredDelivery):
        receiptName = str(registeredDelivery.receipt)
        smeOriginatedAckNames = [str(a) for a in registeredDelivery.smeOriginatedAcks]

        if receiptName not in constants.registered_delivery_receipt_name_map:
            raise ValueError("Unknown registered_delivery receipt name %s" % receiptName)
        for ackName in smeOriginatedAckNames:
            if ackName not in constants.registered_delivery_sme_originated_acks_name_map:
                raise ValueError("Unknown registered_delivery SME orginated ack name %s" % ackName)

        receiptVal = constants.registered_delivery_receipt_name_map[receiptName]
        smeOriginatedAckVals = [constants.registered_delivery_sme_originated_acks_name_map[ackName] for ackName in smeOriginatedAckNames]
        intermediateNotificationVal = 0
        if registeredDelivery.intermediateNotification:
            intermediateNotificationVal = self.intermediateNotificationMask

        intVal = receiptVal | intermediateNotificationVal
        for aVal in smeOriginatedAckVals:
            intVal |= aVal

        return Int1Encoder().encode(intVal)

    def _decode(self, bytes):
        intVal = Int1Encoder()._decode(bytes)
        receiptVal = intVal & self.receiptMask
        smeOriginatedAcksVal = intVal & self.smeOriginatedAcksMask
        intermediateNotificationVal = intVal & self.intermediateNotificationMask

        if receiptVal not in constants.registered_delivery_receipt_value_map:
            raise PDUParseError("Unknown registered_delivery receipt %s" % receiptVal, pdu_types.CommandStatus.ESME_RINVREGDLVFLG)

        receiptName = constants.registered_delivery_receipt_value_map[receiptVal]
        smeOriginatedAckNames = [constants.registered_delivery_sme_originated_acks_value_map[aVal] for aVal in constants.registered_delivery_sme_originated_acks_value_map.keys() if aVal & smeOriginatedAcksVal]

        receipt = getattr(pdu_types.RegisteredDeliveryReceipt, receiptName)
        smeOriginatedAcks = [getattr(pdu_types.RegisteredDeliverySmeOriginatedAcks, aName) for aName in smeOriginatedAckNames]
        intermediateNotification = False
        if intermediateNotificationVal:
            intermediateNotification = True

        return pdu_types.RegisteredDelivery(receipt, smeOriginatedAcks, intermediateNotification)

class DataCodingEncoder(Int1Encoder):
    schemeMask = 0xf0
    schemeDataMask = 0x0f
    gsmMsgCodingMask = 0x04
    gsmMsgClassMask = 0x03

    def _encode(self, dataCoding):
        return Int1Encoder().encode(self._encodeAsInt(dataCoding))

    def _encodeAsInt(self, dataCoding):
        # Jasmin update:
        # Comparing dataCoding.scheme to pdu_types.DataCodingScheme.RAW would result
        # to False even if the values are the same, this is because Enum object have
        # no right __eq__ to compare values
        # Fix: compare Enum indexes (.index)
        if dataCoding.scheme.index == pdu_types.DataCodingScheme.RAW.index:
            return dataCoding.schemeData
        if dataCoding.scheme.index == pdu_types.DataCodingScheme.DEFAULT.index:
            return self._encodeDefaultSchemeAsInt(dataCoding)
        return self._encodeSchemeAsInt(dataCoding)

    def _encodeDefaultSchemeAsInt(self, dataCoding):
        defaultName = str(dataCoding.schemeData)
        if defaultName not in constants.data_coding_default_name_map:
            raise ValueError("Unknown data_coding default name %s" % defaultName)
        return constants.data_coding_default_name_map[defaultName]

    def _encodeSchemeAsInt(self, dataCoding):
        schemeVal = self._encodeSchemeNameAsInt(dataCoding)
        schemeDataVal = self._encodeSchemeDataAsInt(dataCoding)
        return schemeVal | schemeDataVal

    def _encodeSchemeNameAsInt(self, dataCoding):
        schemeName = str(dataCoding.scheme)
        if schemeName not in constants.data_coding_scheme_name_map:
            raise ValueError("Unknown data_coding scheme name %s" % schemeName)
        return constants.data_coding_scheme_name_map[schemeName]

    def _encodeSchemeDataAsInt(self, dataCoding):
        # Jasmin update:
        # Related to #182
        # When pdu is unpickled (from smpps or http api), the comparison below will always
        # be False since memory addresses of both objects are different.
        # Using str() will get the comparison on the 'GSM_MESSAGE_CLASS' string value
        if str(dataCoding.scheme) == str(pdu_types.DataCodingScheme.GSM_MESSAGE_CLASS):
            return self._encodeGsmMsgSchemeDataAsInt(dataCoding)
        # Jasmin update:
        # As reported in https://github.com/mozes/smpp.pdu/issues/12
        # raise ValueError("Unknown data coding scheme %s" % dataCoding.scheme)
        #                                                    ~~~~~~~~~~~
        raise ValueError("Unknown data coding scheme %s" % dataCoding.scheme)

    def _encodeGsmMsgSchemeDataAsInt(self, dataCoding):
        msgCodingName = str(dataCoding.schemeData.msgCoding)
        msgClassName = str(dataCoding.schemeData.msgClass)

        if msgCodingName not in constants.data_coding_gsm_message_coding_name_map:
            raise ValueError("Unknown data_coding gsm msg coding name %s" % msgCodingName)
        if msgClassName not in constants.data_coding_gsm_message_class_name_map:
            raise ValueError("Unknown data_coding gsm msg class name %s" % msgClassName)

        msgCodingVal = constants.data_coding_gsm_message_coding_name_map[msgCodingName]
        msgClassVal = constants.data_coding_gsm_message_class_name_map[msgClassName]
        return msgCodingVal | msgClassVal

    def _decode(self, bytes):
        intVal = Int1Encoder()._decode(bytes)
        scheme = self._decodeScheme(intVal)
        schemeData = self._decodeSchemeData(scheme, intVal)
        return pdu_types.DataCoding(scheme, schemeData)

    def _decodeScheme(self, intVal):
        schemeVal = intVal & self.schemeMask
        if schemeVal in constants.data_coding_scheme_value_map:
            schemeName = constants.data_coding_scheme_value_map[schemeVal]
            return getattr(pdu_types.DataCodingScheme, schemeName)

        if intVal in constants.data_coding_default_value_map:
            return pdu_types.DataCodingScheme.DEFAULT

        return pdu_types.DataCodingScheme.RAW

    def _decodeSchemeData(self, scheme, intVal):
        if scheme == pdu_types.DataCodingScheme.RAW:
            return intVal
        if scheme == pdu_types.DataCodingScheme.DEFAULT:
            return self._decodeDefaultSchemeData(intVal)
        if scheme == pdu_types.DataCodingScheme.GSM_MESSAGE_CLASS:
            schemeDataVal = intVal & self.schemeDataMask
            return self._decodeGsmMsgSchemeData(schemeDataVal)
        raise ValueError("Unexpected data coding scheme %s" % scheme)

    def _decodeDefaultSchemeData(self, intVal):
        if intVal not in constants.data_coding_default_value_map:
            raise ValueError("Unknown data_coding default value %s" % intVal)
        defaultName = constants.data_coding_default_value_map[intVal]
        return getattr(pdu_types.DataCodingDefault, defaultName)

    def _decodeGsmMsgSchemeData(self, schemeDataVal):
        msgCodingVal = schemeDataVal & self.gsmMsgCodingMask
        msgClassVal = schemeDataVal & self.gsmMsgClassMask

        if msgCodingVal not in constants.data_coding_gsm_message_coding_value_map:
            raise ValueError("Unknown data_coding gsm msg coding value %s" % msgCodingVal)
        if msgClassVal not in constants.data_coding_gsm_message_class_value_map:
            raise ValueError("Unknown data_coding gsm msg class value %s" % msgClassVal)

        msgCodingName = constants.data_coding_gsm_message_coding_value_map[msgCodingVal]
        msgClassName = constants.data_coding_gsm_message_class_value_map[msgClassVal]

        msgCoding = getattr(pdu_types.DataCodingGsmMsgCoding, msgCodingName)
        msgClass = getattr(pdu_types.DataCodingGsmMsgClass, msgClassName)
        return pdu_types.DataCodingGsmMsg(msgCoding, msgClass)

class AddrTonEncoder(IntegerWrapperEncoder):
    fieldName = 'addr_ton'
    nameMap = constants.addr_ton_name_map
    valueMap = constants.addr_ton_value_map
    encoder = Int1Encoder()
    pduType = pdu_types.AddrTon

class AddrNpiEncoder(IntegerWrapperEncoder):
    fieldName = 'addr_npi'
    nameMap = constants.addr_npi_name_map
    valueMap = constants.addr_npi_value_map
    encoder = Int1Encoder()
    pduType = pdu_types.AddrNpi

class PriorityFlagEncoder(IntegerWrapperEncoder):
    fieldName = 'priority_flag'
    nameMap = constants.priority_flag_name_map
    valueMap = constants.priority_flag_value_map
    encoder = Int1Encoder()
    pduType = pdu_types.PriorityFlag
    decodeErrorStatus = pdu_types.CommandStatus.ESME_RINVPRTFLG

class ReplaceIfPresentFlagEncoder(IntegerWrapperEncoder):
    fieldName = 'replace_if_present_flag'
    nameMap = constants.replace_if_present_flap_name_map
    valueMap = constants.replace_if_present_flap_value_map
    encoder = Int1Encoder()
    pduType = pdu_types.ReplaceIfPresentFlag

class DestFlagEncoder(IntegerWrapperEncoder):
    nullable = False
    fieldName = 'dest_flag'
    nameMap = constants.dest_flag_name_map
    valueMap = constants.dest_flag_value_map
    encoder = Int1Encoder()
    pduType = pdu_types.DestFlag

class MessageStateEncoder(IntegerWrapperEncoder):
    nullable = False
    fieldName = 'message_state'
    nameMap = constants.message_state_name_map
    valueMap = constants.message_state_value_map
    encoder = Int1Encoder()
    pduType = pdu_types.MessageState

class CallbackNumDigitModeIndicatorEncoder(IntegerWrapperEncoder):
    nullable = False
    fieldName = 'callback_num_digit_mode_indicator'
    nameMap = constants.callback_num_digit_mode_indicator_name_map
    valueMap = constants.callback_num_digit_mode_indicator_value_map
    encoder = Int1Encoder()
    pduType = pdu_types.CallbackNumDigitModeIndicator
    decodeErrorStatus = pdu_types.CommandStatus.ESME_RINVOPTPARAMVAL

class CallbackNumEncoder(OctetStringEncoder):
    digitModeIndicatorEncoder = CallbackNumDigitModeIndicatorEncoder()
    tonEncoder = AddrTonEncoder()
    npiEncoder = AddrNpiEncoder()

    def _encode(self, callbackNum):
        encoded = ''
        encoded += self.digitModeIndicatorEncoder._encode(callbackNum.digitModeIndicator)
        encoded += self.tonEncoder._encode(callbackNum.ton)
        encoded += self.npiEncoder._encode(callbackNum.npi)
        encoded += callbackNum.digits
        return encoded

    def _decode(self, bytes):
        if len(bytes) < 3:
            raise PDUParseError("Invalid callback_num size %s" % len(bytes), pdu_types.CommandStatus.ESME_RINVOPTPARAMVAL)

        digitModeIndicator = self.digitModeIndicatorEncoder._decode(bytes[0])
        ton = self.tonEncoder._decode(bytes[1])
        npi = self.npiEncoder._decode(bytes[2])
        digits = bytes[3:]
        return pdu_types.CallbackNum(digitModeIndicator, ton, npi, digits)

class SubaddressTypeTagEncoder(IntegerWrapperEncoder):
    nullable = False
    fieldName = 'subaddress_type_tag'
    nameMap = constants.subaddress_type_tag_name_map
    valueMap = constants.subaddress_type_tag_value_map
    encoder = Int1Encoder()
    pduType = pdu_types.SubaddressTypeTag
    decodeErrorStatus = pdu_types.CommandStatus.ESME_RINVOPTPARAMVAL

class SubaddressEncoder(OctetStringEncoder):
    typeTagEncoder = SubaddressTypeTagEncoder()

    def _encode(self, subaddress):
        encoded = ''
        encoded += self.typeTagEncoder._encode(subaddress.typeTag)
        valSize = self.getSize() - 1 if self.getSize() is not None else None
        encoded += OctetStringEncoder(valSize)._encode(subaddress.value)
        return encoded

    def _decode(self, bytes):
        if len(bytes) < 2:
            raise PDUParseError("Invalid subaddress size %s" % len(bytes), pdu_types.CommandStatus.ESME_RINVOPTPARAMVAL)

        typeTag = self.typeTagEncoder._decode(bytes[0])
        value = OctetStringEncoder(self.getSize() - 1)._decode(bytes[1:])
        return pdu_types.Subaddress(typeTag, value)

class AddrSubunitEncoder(IntegerWrapperEncoder):
    fieldName = 'addr_subunit'
    nameMap = constants.addr_subunit_name_map
    valueMap = constants.addr_subunit_value_map
    encoder = Int1Encoder()
    pduType = pdu_types.AddrSubunit

class NetworkTypeEncoder(IntegerWrapperEncoder):
    fieldName = 'network_type'
    nameMap = constants.network_type_name_map
    valueMap = constants.network_type_value_map
    encoder = Int1Encoder()
    pduType = pdu_types.NetworkType

class BearerTypeEncoder(IntegerWrapperEncoder):
    fieldName = 'bearer_type'
    nameMap = constants.bearer_type_name_map
    valueMap = constants.bearer_type_value_map
    encoder = Int1Encoder()
    pduType = pdu_types.BearerType

class PayloadTypeEncoder(IntegerWrapperEncoder):
    fieldName = 'payload_type'
    nameMap = constants.payload_type_name_map
    valueMap = constants.payload_type_value_map
    encoder = Int1Encoder()
    pduType = pdu_types.PayloadType

class PrivacyIndicatorEncoder(IntegerWrapperEncoder):
    fieldName = 'privacy_indicator'
    nameMap = constants.privacy_indicator_name_map
    valueMap = constants.privacy_indicator_value_map
    encoder = Int1Encoder()
    pduType = pdu_types.PrivacyIndicator

class LanguageIndicatorEncoder(IntegerWrapperEncoder):
    fieldName = 'language_indicator'
    nameMap = constants.language_indicator_name_map
    valueMap = constants.language_indicator_value_map
    encoder = Int1Encoder()
    pduType = pdu_types.LanguageIndicator

class DisplayTimeEncoder(IntegerWrapperEncoder):
    fieldName = 'display_time'
    nameMap = constants.display_time_name_map
    valueMap = constants.display_time_value_map
    encoder = Int1Encoder()
    pduType = pdu_types.DisplayTime

class MsAvailabilityStatusEncoder(IntegerWrapperEncoder):
    fieldName = 'ms_availability_status'
    nameMap = constants.ms_availability_status_name_map
    valueMap = constants.ms_availability_status_value_map
    encoder = Int1Encoder()
    pduType = pdu_types.MsAvailabilityStatus

# Jasmin update:
class NetworkErrorCodeEncoder(OctetStringEncoder):
    fieldName = 'network_error_code'
    nameMap = constants.network_error_code_name_map
    valueMap = constants.network_error_code_value_map
    pduType = pdu_types.NetworkErrorCode

class DeliveryFailureReasonEncoder(IntegerWrapperEncoder):
    fieldName = 'delivery_failure_reason'
    nameMap = constants.delivery_failure_reason_name_map
    valueMap = constants.delivery_failure_reason_value_map
    encoder = Int1Encoder()
    pduType = pdu_types.DeliveryFailureReason

class MoreMessagesToSendEncoder(IntegerWrapperEncoder):
    fieldName = 'more_messages_to_send'
    nameMap = constants.more_messages_to_send_name_map
    valueMap = constants.more_messages_to_send_value_map
    encoder = Int1Encoder()
    pduType = pdu_types.MoreMessagesToSend

class TimeEncoder(PDUNullableFieldEncoder):
    nullHex = '00'
    decodeNull = True
    encoder = COctetStringEncoder(17)
    decodeErrorClass = PDUParseError
    decodeErrorStatus = pdu_types.CommandStatus.ESME_RUNKNOWNERR

    def __init__(self, **kwargs):
        PDUNullableFieldEncoder.__init__(self, **kwargs)
        self.decodeErrorClass = kwargs.get('decodeErrorClass', self.decodeErrorClass)
        self.decodeErrorStatus = kwargs.get('decodeErrorStatus', self.decodeErrorStatus)
        self.encoder.decodeErrorStatus = self.decodeErrorStatus

    def _encode(self, time):
        str = smpp_time.unparse(time)
        return self.encoder._encode(str)

    def _read(self, file):
        return self.encoder._read(file)

    def _decode(self, bytes):
        timeStr = self.encoder._decode(bytes)
        try:
            return smpp_time.parse(timeStr)
        except Exception, e:
            errStr = str(e)
            raise self.decodeErrorClass(errStr, self.decodeErrorStatus)

class ShortMessageEncoder(IEncoder):
    smLengthEncoder = Int1Encoder(max=254)

    def encode(self, shortMessage):
        if shortMessage is None:
            shortMessage = ''
        smLength = len(shortMessage)

        return self.smLengthEncoder.encode(smLength) + OctetStringEncoder(smLength).encode(shortMessage)

    def decode(self, file):
        smLength = self.smLengthEncoder.decode(file)
        return OctetStringEncoder(smLength).decode(file)

class MessagePayloadEncoder(OctetStringEncoder):
    pass

class OptionEncoder(IEncoder):

    def __init__(self):
        from jasmin.vendor.smpp.pdu.pdu_types import Tag as T
        self.length = None
        self.options = {
            T.dest_addr_subunit: AddrSubunitEncoder(),
            T.source_addr_subunit: AddrSubunitEncoder(),
            T.dest_network_type: NetworkTypeEncoder(),
            T.source_network_type: NetworkTypeEncoder(),
            T.dest_bearer_type: BearerTypeEncoder(),
            T.source_bearer_type: BearerTypeEncoder(),
            T.dest_telematics_id: Int2Encoder(),
            T.source_telematics_id: Int2Encoder(),
            T.qos_time_to_live: Int4Encoder(),
            T.payload_type: PayloadTypeEncoder(),
            T.additional_status_info_text: COctetStringEncoder(256),
            T.receipted_message_id: COctetStringEncoder(65),
            # T.ms_msg_wait_facilities: TODO(),
            T.privacy_indicator: PrivacyIndicatorEncoder(),
            T.source_subaddress: SubaddressEncoder(self.getLength),
            T.dest_subaddress: SubaddressEncoder(self.getLength),
            T.user_message_reference: Int2Encoder(),
            T.user_response_code: Int1Encoder(),
            T.language_indicator: LanguageIndicatorEncoder(),
            T.source_port: Int2Encoder(),
            T.destination_port: Int2Encoder(),
            T.sar_msg_ref_num: Int2Encoder(),
            T.sar_total_segments: Int1Encoder(),
            T.sar_segment_seqnum: Int1Encoder(),
            T.sc_interface_version: Int1Encoder(),
            T.display_time: DisplayTimeEncoder(),
            #T.ms_validity: MsValidityEncoder(),
            #T.dpf_result: DpfResultEncoder(),
            #T.set_dpf: SetDpfEncoder(),
            T.ms_availability_status: MsAvailabilityStatusEncoder(),
            # Jasmin update:
            T.network_error_code: NetworkErrorCodeEncoder(self.getLength),
            T.message_payload: MessagePayloadEncoder(self.getLength),
            T.delivery_failure_reason: DeliveryFailureReasonEncoder(),
            T.more_messages_to_send: MoreMessagesToSendEncoder(),
            T.message_state: MessageStateEncoder(),
            T.callback_num: CallbackNumEncoder(self.getLength),
            #T.callback_num_pres_ind: CallbackNumPresIndEncoder(),
            # T.callback_num_atag: CallbackNumAtag(),
            T.number_of_messages: Int1Encoder(max=99),
            T.sms_signal: OctetStringEncoder(self.getLength),
            T.alert_on_message_delivery: EmptyEncoder(),
            #T.its_reply_type: ItsReplyTypeEncoder(),
            # T.its_session_info: ItsSessionInfoEncoder(),
            # T.ussd_service_op: UssdServiceOpEncoder(),
            # Jasmin update: bypass vendor specific tags
            T.vendor_specific_bypass: OctetStringEncoder(self.getLength),
        }

    def getLength(self):
        return self.length

    def encode(self, option):
        if option.tag not in self.options:
            raise ValueError("Unknown option %s" % str(option))
        encoder = self.options[option.tag]
        encodedValue = encoder.encode(option.value)
        length = len(encodedValue)
        return string.join([
            TagEncoder().encode(option.tag),
            Int2Encoder().encode(length),
            encodedValue,
        ], '')

    def decode(self, file):
        # Jasmin update: bypass vendor specific tags
        tag = TagEncoder().decode(file)
        self.length = Int2Encoder().decode(file)
        if tag not in self.options:
            raise PDUParseError("Optional param %s unknown" % tag, pdu_types.CommandStatus.ESME_ROPTPARNOTALLWD)
        encoder = self.options[tag]
        iBeforeDecode = file.tell()
        value = None
        try:
            value = encoder.decode(file)
        except PDUParseError, e:
            e.status = pdu_types.CommandStatus.ESME_RINVOPTPARAMVAL
            raise e

        iAfterDecode = file.tell()
        parseLen = iAfterDecode - iBeforeDecode
        if parseLen != self.length:
            raise PDUParseError("Invalid option length: labeled [%d] but parsed [%d]" % (self.length, parseLen), pdu_types.CommandStatus.ESME_RINVPARLEN)
        return pdu_types.Option(tag, value)

class PDUEncoder(IEncoder):
    HEADER_LEN = 16

    HeaderEncoders = {
        'command_length': Int4Encoder(),
        'command_id': CommandIdEncoder(),
        'command_status': CommandStatusEncoder(),
        #the spec says max=0x7FFFFFFF but vendors don't respect this
        'sequence_number': Int4Encoder(min=0x00000001),
    }
    HeaderParams = [
        'command_length',
        'command_id',
        'command_status',
        'sequence_number',
    ]

    DefaultRequiredParamEncoders = {
        'system_id': COctetStringEncoder(16, decodeErrorStatus=pdu_types.CommandStatus.ESME_RINVSYSID),
        'password': COctetStringEncoder(9, decodeErrorStatus=pdu_types.CommandStatus.ESME_RINVPASWD),
        'system_type': COctetStringEncoder(13),
        'interface_version': Int1Encoder(),
        'addr_ton': AddrTonEncoder(),
        'addr_npi': AddrNpiEncoder(),
        'address_range': COctetStringEncoder(41),
        'service_type': COctetStringEncoder(6, decodeErrorStatus=pdu_types.CommandStatus.ESME_RINVSERTYP),
        'source_addr_ton': AddrTonEncoder(fieldName='source_addr_ton', decodeErrorStatus=pdu_types.CommandStatus.ESME_RINVSRCTON),
        'source_addr_npi': AddrNpiEncoder(fieldName='source_addr_npi', decodeErrorStatus=pdu_types.CommandStatus.ESME_RINVSRCNPI),
        'source_addr': COctetStringEncoder(21, decodeErrorStatus=pdu_types.CommandStatus.ESME_RINVSRCADR),
        'dest_addr_ton': AddrTonEncoder(fieldName='dest_addr_ton', decodeErrorStatus=pdu_types.CommandStatus.ESME_RINVDSTTON),
        'dest_addr_npi': AddrNpiEncoder(fieldName='dest_addr_npi', decodeErrorStatus=pdu_types.CommandStatus.ESME_RINVDSTNPI),
        'destination_addr': COctetStringEncoder(21, decodeErrorStatus=pdu_types.CommandStatus.ESME_RINVDSTADR),
        'esm_class': EsmClassEncoder(),
        'esme_addr_ton': AddrTonEncoder(fieldName='esme_addr_ton'),
        'esme_addr_npi': AddrNpiEncoder(fieldName='esme_addr_npi'),
        'esme_addr': COctetStringEncoder(65),
        'protocol_id': Int1Encoder(),
        'priority_flag': PriorityFlagEncoder(),
        'schedule_delivery_time': TimeEncoder(decodeErrorStatus=pdu_types.CommandStatus.ESME_RINVSCHED),
        'validity_period': TimeEncoder(decodeErrorStatus=pdu_types.CommandStatus.ESME_RINVEXPIRY),
        'registered_delivery': RegisteredDeliveryEncoder(),
        'replace_if_present_flag': ReplaceIfPresentFlagEncoder(),
        'data_coding': DataCodingEncoder(),
        # Jasmin update:
        # Minimum for sm_default_msg_id can be 0 (reserved value)
        'sm_default_msg_id': Int1Encoder(min=0, max=254, decodeErrorStatus=pdu_types.CommandStatus.ESME_RINVDFTMSGID),
        'short_message': ShortMessageEncoder(),
        'message_id': COctetStringEncoder(65, decodeErrorStatus=pdu_types.CommandStatus.ESME_RINVMSGID),
        # 'number_of_dests': Int1Encoder(max=254),
        # 'no_unsuccess': Int1Encoder(),
        # 'dl_name': COctetStringEncoder(21),
        'message_state': MessageStateEncoder(),
        'final_date': TimeEncoder(),
        'error_code':Int1Encoder(decodeNull=True),
    }

    CustomRequiredParamEncoders = {
        pdu_types.CommandId.alert_notification: {
            'source_addr': COctetStringEncoder(65, decodeErrorStatus=pdu_types.CommandStatus.ESME_RINVSRCADR),
        },
        pdu_types.CommandId.data_sm: {
            'source_addr': COctetStringEncoder(65, decodeErrorStatus=pdu_types.CommandStatus.ESME_RINVSRCADR),
            'destination_addr': COctetStringEncoder(65, decodeErrorStatus=pdu_types.CommandStatus.ESME_RINVDSTADR),
        },
        pdu_types.CommandId.deliver_sm: {
            'schedule_delivery_time': TimeEncoder(requireNull=True, decodeErrorStatus=pdu_types.CommandStatus.ESME_RINVSCHED),
            'validity_period': TimeEncoder(requireNull=True, decodeErrorStatus=pdu_types.CommandStatus.ESME_RINVEXPIRY),
        },
        pdu_types.CommandId.deliver_sm_resp: {
            'message_id': COctetStringEncoder(decodeNull=True, requireNull=True, decodeErrorStatus=pdu_types.CommandStatus.ESME_RINVMSGID),
        }
    }

    def __init__(self):
        self.optionEncoder = OptionEncoder()

    def getRequiredParamEncoders(self, pdu):
        if pdu.id in self.CustomRequiredParamEncoders:
            return dict(self.DefaultRequiredParamEncoders.items() + self.CustomRequiredParamEncoders[pdu.id].items())
        return self.DefaultRequiredParamEncoders

    def encode(self, pdu):
        body = self.encodeBody(pdu)
        return self.encodeHeader(pdu, body) + body

    def decode(self, file):
        iBeforeDecode = file.tell()
        headerParams = self.decodeHeader(file)
        pduKlass = operations.getPDUClass(headerParams['command_id'])
        pdu = pduKlass(headerParams['sequence_number'], headerParams['command_status'])
        self.decodeBody(file, pdu, headerParams['command_length'] - self.HEADER_LEN)

        iAfterDecode = file.tell()
        parsedLen = iAfterDecode - iBeforeDecode
        # Jasmin update:
        # Related to #124, don't error if parsedLen is greater than command_length,
        # there can be some padding in PDUs, this is a fix to be confirmed for stability
        if headerParams['command_length'] > parsedLen:
            padBytes = file.read(headerParams['command_length'] - parsedLen)
            if len(padBytes) != headerParams['command_length'] - parsedLen:
                raise PDUCorruptError("Invalid command length: expected %d, parsed %d, padding bytes not found" % (headerParams['command_length'], parsedLen), pdu_types.CommandStatus.ESME_RINVCMDLEN)
        elif parsedLen < headerParams['command_length']:
            raise PDUCorruptError("Invalid command length: expected %d, parsed %d" % (headerParams['command_length'], parsedLen), pdu_types.CommandStatus.ESME_RINVCMDLEN)

        return pdu

    def decodeHeader(self, file):
        headerParams = self.decodeRequiredParams(self.HeaderParams, self.HeaderEncoders, file)
        if headerParams['command_length'] < self.HEADER_LEN:
            raise PDUCorruptError("Invalid command_length %d" % headerParams['command_length'], pdu_types.CommandStatus.ESME_RINVCMDLEN)
        return headerParams

    def decodeBody(self, file, pdu, bodyLength):
        mandatoryParams = {}
        optionalParams = {}

        #Some PDU responses have no defined body when the status is not 0
        #    c.f. 4.1.2. "BIND_TRANSMITTER_RESP"
        #    c.f. 4.1.4. "BIND_RECEIVER_RESP"
        #    c.f. 4.4.2. SMPP PDU Definition "SUBMIT_SM_RESP"
        if pdu.commandId in (CommandId.bind_receiver_resp, CommandId.bind_transmitter_resp, CommandId.bind_transceiver_resp, CommandId.submit_sm_resp):
            if pdu.status != pdu_types.CommandStatus.ESME_ROK and pdu.noBodyOnError:
                return

        iBeforeMParams = file.tell()
        if len(pdu.mandatoryParams) > 0:
            mandatoryParams = self.decodeRequiredParams(pdu.mandatoryParams, self.getRequiredParamEncoders(pdu), file)
        iAfterMParams = file.tell()
        mParamsLen = iAfterMParams - iBeforeMParams
        if len(pdu.optionalParams) > 0:
            optionalParams = self.decodeOptionalParams(pdu.optionalParams, file, bodyLength - mParamsLen)
        pdu.params = dict(mandatoryParams.items() + optionalParams.items())

    def encodeBody(self, pdu):
        body = ''

        #Some PDU responses have no defined body when the status is not 0
        #    c.f. 4.1.2. "BIND_TRANSMITTER_RESP"
        #    c.f. 4.1.4. "BIND_RECEIVER_RESP"
        #    c.f. 4.4.2. SMPP PDU Definition "SUBMIT_SM_RESP"
        if pdu.commandId in (CommandId.bind_receiver_resp, CommandId.bind_transmitter_resp, CommandId.bind_transceiver_resp, CommandId.submit_sm_resp):
            if pdu.status != pdu_types.CommandStatus.ESME_ROK and pdu.noBodyOnError:
                return body

        for paramName in pdu.mandatoryParams:
            if paramName not in pdu.params:
                raise ValueError("Missing required parameter: %s" % paramName)

        body += self.encodeRequiredParams(pdu.mandatoryParams, self.getRequiredParamEncoders(pdu), pdu.params)
        body += self.encodeOptionalParams(pdu.optionalParams, pdu.params)
        return body

    def encodeHeader(self, pdu, body):
        cmdLength = len(body) + self.HEADER_LEN
        headerParams = {
            'command_length': cmdLength,
            'command_id': pdu.id,
            'command_status': pdu.status,
            'sequence_number': pdu.seqNum,
        }
        header = self.encodeRequiredParams(self.HeaderParams, self.HeaderEncoders, headerParams)
        assert len(header) == self.HEADER_LEN
        return header

    def encodeOptionalParams(self, optionalParams, params):
        result = ''
        for paramName in optionalParams:
            if paramName in params:
                tag = getattr(pdu_types.Tag, paramName)
                value = params[paramName]
                result += self.optionEncoder.encode(pdu_types.Option(tag, value))
        return result

    def decodeOptionalParams(self, paramList, file, optionsLength):
        optionalParams = {}
        iBefore = file.tell()
        while file.tell() - iBefore < optionsLength:
            option = self.optionEncoder.decode(file)
            optionName = str(option.tag)
            if optionName not in paramList:
                raise PDUParseError("Invalid option %s" % optionName, pdu_types.CommandStatus.ESME_ROPTPARNOTALLWD)
            optionalParams[optionName] = option.value
        return optionalParams

    def encodeRequiredParams(self, paramList, encoderMap, params):
        return string.join([encoderMap[paramName].encode(params[paramName]) for paramName in paramList], '')

    def decodeRequiredParams(self, paramList, encoderMap, file):
        params = {}
        for paramName in paramList:
            params[paramName] = encoderMap[paramName].decode(file)
        return params
