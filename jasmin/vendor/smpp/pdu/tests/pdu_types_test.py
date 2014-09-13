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
from jasmin.vendor.smpp.pdu.pdu_types import *

class EsmClassTest(unittest.TestCase):

    def test_equality_with_array_and_set(self):
        e1 = EsmClass(EsmClassMode.DATAGRAM, EsmClassType.DEFAULT, set([EsmClassGsmFeatures.SET_REPLY_PATH]))
        e2 = EsmClass(EsmClassMode.DATAGRAM, EsmClassType.DEFAULT, [EsmClassGsmFeatures.SET_REPLY_PATH])
        self.assertEquals(e1, e2)
    
    def test_equality_with_different_array_order(self):
        e1 = EsmClass(EsmClassMode.DATAGRAM, EsmClassType.DEFAULT, [EsmClassGsmFeatures.SET_REPLY_PATH, EsmClassGsmFeatures.UDHI_INDICATOR_SET])
        e2 = EsmClass(EsmClassMode.DATAGRAM, EsmClassType.DEFAULT, [EsmClassGsmFeatures.UDHI_INDICATOR_SET, EsmClassGsmFeatures.SET_REPLY_PATH])
        self.assertEquals(e1, e2)
        
    def test_equality_with_array_duplicates(self):
        e1 = EsmClass(EsmClassMode.DATAGRAM, EsmClassType.DEFAULT, [EsmClassGsmFeatures.SET_REPLY_PATH, EsmClassGsmFeatures.SET_REPLY_PATH])
        e2 = EsmClass(EsmClassMode.DATAGRAM, EsmClassType.DEFAULT, [EsmClassGsmFeatures.SET_REPLY_PATH])
        self.assertEquals(e1, e2)    

class RegisteredDeliveryTest(unittest.TestCase):

    def test_equality_with_array_and_set(self):
        r1 = RegisteredDelivery(RegisteredDeliveryReceipt.SMSC_DELIVERY_RECEIPT_REQUESTED, set([RegisteredDeliverySmeOriginatedAcks.SME_DELIVERY_ACK_REQUESTED]))
        r2 = RegisteredDelivery(RegisteredDeliveryReceipt.SMSC_DELIVERY_RECEIPT_REQUESTED, [RegisteredDeliverySmeOriginatedAcks.SME_DELIVERY_ACK_REQUESTED])
        self.assertEquals(r1, r2)
    
    def test_equality_with_different_array_order(self):
        r1 = RegisteredDelivery(RegisteredDeliveryReceipt.SMSC_DELIVERY_RECEIPT_REQUESTED, [RegisteredDeliverySmeOriginatedAcks.SME_MANUAL_ACK_REQUESTED, RegisteredDeliverySmeOriginatedAcks.SME_DELIVERY_ACK_REQUESTED])
        r2 = RegisteredDelivery(RegisteredDeliveryReceipt.SMSC_DELIVERY_RECEIPT_REQUESTED, [RegisteredDeliverySmeOriginatedAcks.SME_DELIVERY_ACK_REQUESTED, RegisteredDeliverySmeOriginatedAcks.SME_MANUAL_ACK_REQUESTED])
        self.assertEquals(r1, r2)
        
    def test_equality_with_array_duplicates(self):
        r1 = RegisteredDelivery(RegisteredDeliveryReceipt.SMSC_DELIVERY_RECEIPT_REQUESTED, [RegisteredDeliverySmeOriginatedAcks.SME_MANUAL_ACK_REQUESTED, RegisteredDeliverySmeOriginatedAcks.SME_MANUAL_ACK_REQUESTED])
        r2 = RegisteredDelivery(RegisteredDeliveryReceipt.SMSC_DELIVERY_RECEIPT_REQUESTED, [RegisteredDeliverySmeOriginatedAcks.SME_MANUAL_ACK_REQUESTED])
        self.assertEquals(r1, r2)    

        
if __name__ == '__main__':
    unittest.main()