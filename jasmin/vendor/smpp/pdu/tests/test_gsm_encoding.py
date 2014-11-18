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
import unittest
import StringIO
import binascii
from jasmin.vendor.smpp.pdu.gsm_encoding import *
from jasmin.vendor.smpp.pdu.gsm_types import *

class EncoderTest(unittest.TestCase):
    
    def do_conversion_test(self, encoder, value, hexdumpValue):
        encoded = encoder.encode(value)
        hexEncoded = binascii.b2a_hex(encoded)
        if hexdumpValue != hexEncoded:
            print "\nHex Value:\n%s" % hexdumpValue
            print "Hex Encoded:\n%s" % hexEncoded
            chars1 = list(hexdumpValue)
            chars2 = list(hexEncoded)
            for i in range(0, len(hexEncoded)):
                if chars1[i] != chars2[i]:
                    print "Letter %d diff [%s] [%s]" % (i, chars1[i], chars2[i])
            
        self.assertEquals(hexdumpValue, hexEncoded)
        file = StringIO.StringIO(encoded)
        decoded = encoder.decode(file)
        self.assertEquals(value, decoded)
        
    def do_null_encode_test(self, encoder, nullDecodeVal, hexdumpValue):
        encoded = encoder.encode(None)
        self.assertEquals(hexdumpValue, binascii.b2a_hex(encoded))
        file = StringIO.StringIO(encoded)
        decoded = encoder.decode(file)
        self.assertEquals(nullDecodeVal, decoded)
        
    def decode(self, decodeFunc, hexdumpValue):
        bytes = binascii.a2b_hex(hexdumpValue)
        # print "hex: %s, num bytes %s" % (hexdumpValue, len(bytes))
        file = StringIO.StringIO(bytes)
        error = None
        decoded = None
        try:
            decoded = decodeFunc(file)
        except Exception, e:
            error = e
        # print "file index: %s" % file.tell()
        self.assertEquals(len(bytes), file.tell())
        if error:
            raise error
        return decoded
                    
    def do_decode_udh_parse_error_test(self, decodeFunc, hexdumpValue):
        try:
            decoded = self.decode(decodeFunc, hexdumpValue)
            self.assertTrue(False, 'Decode did not throw exception. Result was: %s' % str(decoded))
        except UDHParseError:
            pass

class InformationElementIdentifierEncoderTest(EncoderTest):
    
    def test_conversion(self):
        self.do_conversion_test(InformationElementIdentifierEncoder(), InformationElementIdentifier.CONCATENATED_SM_16BIT_REF_NUM, '08')
        
class IEConcatenatedSMEncoderTest(EncoderTest):

    def test_conversion(self):
        self.do_conversion_test(IEConcatenatedSMEncoder(False), IEConcatenatedSM(0xFA, 0x03, 0x02), 'fa0302')
        self.do_conversion_test(IEConcatenatedSMEncoder(True), IEConcatenatedSM(0x9CFA, 0x03, 0x02), '9cfa0302')

class InformationElementEncoderTest(EncoderTest):

    def test_conversion(self):
        self.do_conversion_test(InformationElementEncoder(), InformationElement(InformationElementIdentifier.CONCATENATED_SM_8BIT_REF_NUM, IEConcatenatedSM(0xFA, 0x03, 0x02)), '0003fa0302')
        self.do_conversion_test(InformationElementEncoder(), InformationElement(InformationElementIdentifier.CONCATENATED_SM_16BIT_REF_NUM, IEConcatenatedSM(0x9CFA, 0x03, 0x02)), '08049cfa0302')
        self.do_conversion_test(InformationElementEncoder(), InformationElement(InformationElementIdentifier.HYPERLINK_FORMAT_ELEMENT, binascii.a2b_hex('9cfa0302')), '21049cfa0302')
        
    def test_decode_unknown_identifier(self):
        decoded = self.decode(InformationElementEncoder().decode, '02049cfa0302')
        self.assertEquals(None, decoded)
        decoded = self.decode(InformationElementEncoder().decode, '0200')
        self.assertEquals(None, decoded)
        
    def test_invalid_length(self):
        self.do_decode_udh_parse_error_test(InformationElementEncoder().decode, '0002fa0302')  
        self.do_decode_udh_parse_error_test(InformationElementEncoder().decode, '0004fa0302')
        
class UserDataHeaderEncoderTest(EncoderTest):

    def test_conversion(self):
        udh = [InformationElement(InformationElementIdentifier.CONCATENATED_SM_8BIT_REF_NUM, IEConcatenatedSM(0x24, 0x03, 0x01))]
        self.do_conversion_test(UserDataHeaderEncoder(), udh, '050003240301')
        
    def test_decode_repeated_non_repeatable_element(self):
        udh = self.decode(UserDataHeaderEncoder().decode, '0c0804abcd030208049cfa0302')
        udhExpected = [InformationElement(InformationElementIdentifier.CONCATENATED_SM_16BIT_REF_NUM, IEConcatenatedSM(0x9CFA, 0x03, 0x02))]
        self.assertEquals(udhExpected, udh)
        
    def test_decode_with_unknown_elements(self):
        udh = self.decode(UserDataHeaderEncoder().decode, '0f0203ffffff0201ff00032403010200')
        udhExpected = [InformationElement(InformationElementIdentifier.CONCATENATED_SM_8BIT_REF_NUM, IEConcatenatedSM(0x24, 0x03, 0x01))]
        self.assertEquals(udhExpected, udh)

    def test_encode_repeated_non_repeatable_element(self):
        ie1 = InformationElement(InformationElementIdentifier.CONCATENATED_SM_16BIT_REF_NUM, IEConcatenatedSM(0x9CFA, 0x03, 0x02))
        ie2 = InformationElement(InformationElementIdentifier.CONCATENATED_SM_16BIT_REF_NUM, IEConcatenatedSM(0xABCD, 0x04, 0x01))
        udh = [ie1, ie2]
        self.assertRaises(ValueError, UserDataHeaderEncoder().encode, udh)

    def test_decode_mutually_exclusive_elements(self):
        udh = self.decode(UserDataHeaderEncoder().decode, '0b000324030108049cfa0302')
        udhExpected = [InformationElement(InformationElementIdentifier.CONCATENATED_SM_16BIT_REF_NUM, IEConcatenatedSM(0x9CFA, 0x03, 0x02))]
        self.assertEquals(udhExpected, udh)

    def test_encode_mutually_exclusive_elements(self):
        ie1 = InformationElement(InformationElementIdentifier.CONCATENATED_SM_8BIT_REF_NUM, IEConcatenatedSM(0x24, 0x03, 0x01))
        ie2 = InformationElement(InformationElementIdentifier.CONCATENATED_SM_16BIT_REF_NUM, IEConcatenatedSM(0xABCD, 0x04, 0x01))
        udh = [ie1, ie2]
        self.assertRaises(ValueError, UserDataHeaderEncoder().encode, udh)    
    

if __name__ == '__main__':
    unittest.main()