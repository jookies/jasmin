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
import struct
from jasmin.vendor.smpp.pdu import gsm_constants, gsm_types
from jasmin.vendor.smpp.pdu.encoding import IEncoder
from jasmin.vendor.smpp.pdu.error import PDUParseError

class UDHParseError(Exception):
    pass

class UDHInformationElementIdentifierUnknownError(UDHParseError):
    pass

class Int8Encoder(IEncoder):
    
    def encode(self, value):
        return struct.pack('!B', value)

    def decode(self, file):
        byte = self.read(file, 1)
        return struct.unpack('!B', byte)[0]

class Int16Encoder(IEncoder):
    
    def encode(self, value):
        return struct.pack('!H', value)

    def decode(self, file):
        bytes = self.read(file, 2)
        return struct.unpack('!H', bytes)[0]

class InformationElementIdentifierEncoder(IEncoder):
    int8Encoder = Int8Encoder()
    nameMap = gsm_constants.information_element_identifier_name_map
    valueMap = gsm_constants.information_element_identifier_value_map
    
    def encode(self, value):
        name = str(value)
        if name not in self.nameMap:
            raise ValueError("Unknown InformationElementIdentifier name %s" % name)
        return self.int8Encoder.encode(self.nameMap[name])        

    def decode(self, file):
        intVal = self.int8Encoder.decode(file)
        if intVal not in self.valueMap:
            errStr = "Unknown InformationElementIdentifier value %s" % intVal
            raise UDHInformationElementIdentifierUnknownError(errStr)
        name = self.valueMap[intVal]
        return getattr(gsm_types.InformationElementIdentifier, name)    

class IEConcatenatedSMEncoder(IEncoder):
    int8Encoder = Int8Encoder()
    int16Encoder = Int16Encoder()
    
    def __init__(self, is16bitRefNum):
        self.is16bitRefNum = is16bitRefNum
    
    def encode(self, cms):
        bytes = ''
        if self.is16bitRefNum:
            bytes += self.int16Encoder.encode(cms.referenceNum)
        else:
            bytes += self.int8Encoder.encode(cms.referenceNum)
        bytes += self.int8Encoder.encode(cms.maximumNum)
        bytes += self.int8Encoder.encode(cms.sequenceNum)
        return bytes

    def decode(self, file):
        refNum = None
        if self.is16bitRefNum:
            refNum = self.int16Encoder.decode(file)
        else:
            refNum = self.int8Encoder.decode(file)
        maxNum = self.int8Encoder.decode(file)
        seqNum = self.int8Encoder.decode(file)
        return gsm_types.IEConcatenatedSM(refNum, maxNum, seqNum)

class InformationElementEncoder(IEncoder):
    int8Encoder = Int8Encoder()
    iEIEncoder = InformationElementIdentifierEncoder()
    dataEncoders = {
        gsm_types.InformationElementIdentifier.CONCATENATED_SM_8BIT_REF_NUM: IEConcatenatedSMEncoder(False),
        gsm_types.InformationElementIdentifier.CONCATENATED_SM_16BIT_REF_NUM: IEConcatenatedSMEncoder(True),
    }
    
    def encode(self, iElement):
        dataBytes = None
        if iElement.identifier in self.dataEncoders:
            dataBytes = self.dataEncoders[iElement.identifier].encode(iElement.data)
        else:
            dataBytes = iElement.data
        length = len(dataBytes)
            
        bytes = ''
        bytes += self.iEIEncoder.encode(iElement.identifier)
        bytes += self.int8Encoder.encode(length)
        bytes += dataBytes
        return bytes

    def decode(self, file):
        fStart = file.tell()
        
        identifier = None
        try:
            identifier = self.iEIEncoder.decode(file)
        except UDHInformationElementIdentifierUnknownError:
            #Continue parsing after this so that these can be ignored
            pass
        
        length = self.int8Encoder.decode(file)
        data = None
        if identifier in self.dataEncoders:
            data = self.dataEncoders[identifier].decode(file)
        elif length > 0:
            data = self.read(file, length)
            
        parsed = file.tell() - fStart
        if parsed != length + 2:
            raise UDHParseError("Invalid length: expected %d, parsed %d" % (length + 2, parsed))
        
        if identifier is None:
            return None
        
        return gsm_types.InformationElement(identifier, data)
        
class UserDataHeaderEncoder(IEncoder):
    iEEncoder = InformationElementEncoder()
    int8Encoder = Int8Encoder()
        
    def encode(self, udh):
        nonRepeatable = {}
        iEBytes = ''
        for iElement in udh:
            if not self.isIdentifierRepeatable(iElement.identifier):
                if iElement.identifier in nonRepeatable:
                    raise ValueError("Cannot repeat element %s" % str(iElement.identifier))
                for identifier in self.getIdentifierExclusionList(iElement.identifier):
                    if identifier in nonRepeatable:
                        raise ValueError("%s and %s are mutually exclusive elements" % (str(iElement.identifier), str(identifier)))
                nonRepeatable[iElement.identifier] = None
            iEBytes += self.iEEncoder.encode(iElement)   
        headerLen = len(iEBytes)
        return self.int8Encoder.encode(headerLen) + iEBytes

    #http://www.3gpp.org/ftp/Specs/archive/23_series/23.040/23040-100.zip
    #GSM spec says for non-repeatable and mutually exclusive elements that
    #get repeated we should use the last occurrance
    def decode(self, file):
        repeatable = []
        nonRepeatable = {}
        headerLen = self.int8Encoder.decode(file)
        while file.tell() < headerLen + 1:
            iStart = file.tell()
            iElement = self.iEEncoder.decode(file)
            if iElement is not None:
                if self.isIdentifierRepeatable(iElement.identifier):
                    repeatable.append(iElement)
                else:
                    nonRepeatable[iElement.identifier] = iElement
                    for identifier in self.getIdentifierExclusionList(iElement.identifier):
                        if identifier in nonRepeatable:
                            del nonRepeatable[identifier]
            bytesRead = file.tell() - iStart
        return repeatable + nonRepeatable.values()
        
    def isIdentifierRepeatable(self, identifier):
        return gsm_constants.information_element_identifier_full_value_map[gsm_constants.information_element_identifier_name_map[str(identifier)]]['repeatable']
        
    def getIdentifierExclusionList(self, identifier):
        nameList = gsm_constants.information_element_identifier_full_value_map[gsm_constants.information_element_identifier_name_map[str(identifier)]]['excludes']
        return [getattr(gsm_types.InformationElementIdentifier, name) for name in nameList]
