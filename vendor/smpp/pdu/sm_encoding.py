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
import struct, StringIO
from jasmin.vendor.smpp.pdu.operations import DeliverSM, DataSM
from jasmin.vendor.smpp.pdu.pdu_types import *
from jasmin.vendor.smpp.pdu.namedtuple import namedtuple
from jasmin.vendor.smpp.pdu.gsm_types import InformationElementIdentifier
from jasmin.vendor.smpp.pdu.gsm_encoding import UserDataHeaderEncoder

ShortMessageString = namedtuple('ShortMessageString', 'bytes, unicode, udh')

class SMStringEncoder(object):
    userDataHeaderEncoder = UserDataHeaderEncoder()
        
    def decodeSM(self, pdu):
        data_coding = pdu.params['data_coding']
        #TODO - when to look for message_payload instead of short_message??
        (smBytes, udhBytes, smStrBytes) = self.splitSM(pdu)
        udh = self.decodeUDH(udhBytes)
        
        if data_coding.scheme == DataCodingScheme.DEFAULT:
            unicodeStr = None
            if data_coding.schemeData == DataCodingDefault.SMSC_DEFAULT_ALPHABET:
                unicodeStr = unicode(smStrBytes, 'ascii')
            elif data_coding.schemeData == DataCodingDefault.IA5_ASCII:
                unicodeStr = unicode(smStrBytes, 'ascii')
            elif data_coding.schemeData == DataCodingDefault.UCS2:
                unicodeStr = unicode(smStrBytes, 'UTF-16BE')
            elif data_coding.schemeData == DataCodingDefault.LATIN_1:
                unicodeStr = unicode(smStrBytes, 'latin_1')
            if unicodeStr is not None:
                return ShortMessageString(smBytes, unicodeStr, udh)
                
        raise NotImplementedError("I don't know what to do!!! Data coding %s" % str(data_coding))

    def containsUDH(self, pdu):
        if EsmClassGsmFeatures.UDHI_INDICATOR_SET in pdu.params['esm_class'].gsmFeatures:
            return True
        return False
        
    def isConcatenatedSM(self, pdu):
        return self.getConcatenatedSMInfoElement(pdu) != None
        
    def getConcatenatedSMInfoElement(self, pdu):
        (smBytes, udhBytes, smStrBytes) = self.splitSM(pdu)
        udh = self.decodeUDH(udhBytes)
        if udh is None:
            return None
        return self.findConcatenatedSMInfoElement(udh)
        
    def findConcatenatedSMInfoElement(self, udh):
        iElems = [iElem for iElem in udh if iElem.identifier in (InformationElementIdentifier.CONCATENATED_SM_8BIT_REF_NUM, InformationElementIdentifier.CONCATENATED_SM_16BIT_REF_NUM)]
        assert len(iElems) <= 1
        if len(iElems) == 1:
            return iElems[0]
        return None
                
    def decodeUDH(self, udhBytes):
        if udhBytes is not None:
            return self.userDataHeaderEncoder.decode(StringIO.StringIO(udhBytes))
        return None
            
    def splitSM(self, pdu):
        short_message = pdu.params['short_message']
        if self.containsUDH(pdu):
            if len(short_message) == 0:
                raise ValueError("Empty short message")
            headerLen = struct.unpack('!B', short_message[0])[0]
            if headerLen + 1 > len(short_message):
                raise ValueError("Invalid header len (%d). Longer than short_message len (%d) + 1" % (headerLen, len(short_message)))
            return (short_message, short_message[:headerLen+1], short_message[headerLen+1:])
        return (short_message, None, short_message)
    