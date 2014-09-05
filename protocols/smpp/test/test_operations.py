"""
Test cases for jasmin.protocols.smpp.operations module.
"""

import binascii
from twisted.trial.unittest import TestCase
from jasmin.protocols.smpp.configs import SMPPClientConfig
from jasmin.protocols.smpp.operations import SMPPOperationFactory
from jasmin.vendor.smpp.pdu.pdu_types import CommandId, CommandStatus
from jasmin.vendor.smpp.pdu.operations import SubmitSM, DeliverSM

class OperationsTest(TestCase):
    source_addr         = '20203060'
    destination_addr    = '98700177'
    latin1_sm           = '6162636465666768696a6b6c6d6e6f707172737475767778797a'
    latin1_long_sm      = '6162636465666768696a6b6c6d6e6f707172737475767778797a2e6162636465666768696a6b6c6d6e6f707172737475767778797a2e6162636465666768696a6b6c6d6e6f707172737475767778797a2e6162636465666768696a6b6c6d6e6f707172737475767778797a2e6162636465666768696a6b6c6d6e6f707172737475767778797a2e6162636465666768696a6b6c6d6e6f707172737475767778797a2e6162636465666768696a6b6c6d6e6f707172737475767778797a2e6162636465666768696a6b6c6d6e6f707172737475767778797a2e6162636465666768696a6b6c6d6e6f707172737475767778797a2e6162636465666768696a6b6c6d6e6f707172737475767778797a2e6162636465666768696a6b6c6d6e6f707172737475767778797a2e6162636465666768696a6b6c6d6e6f707172737475767778797a2e6162636465666768696a6b6c6d6e6f707172737475767778797a2e'

    def setUp(self):
        self.opFactory = SMPPOperationFactory(SMPPClientConfig(id='test-id'))
        
    def buildSubmitSmTest(self, sm):
        """
        Build a SubmitSm pdu and test if:
         - command_id is correct
         - command_status is ESME_ROCK (default value)
         - destination_addr is the same as self.destination_addr
         - source_addr is the same as self.source_addr
        """

        pdu = self.opFactory.SubmitSM(
            source_addr=self.source_addr,
            destination_addr=self.destination_addr,
            short_message=sm,
        )

        self.assertEquals(pdu.id, CommandId.submit_sm)
        self.assertEquals(pdu.status, CommandStatus.ESME_ROK)
        self.assertEquals(pdu.params['destination_addr'], self.destination_addr)
        self.assertEquals(pdu.params['source_addr'], self.source_addr)
        
        return pdu

    def test_encode_latin1(self):
        """
        Test that a latin1 short message text remain the same after it's getting
        encoded in a PDU object.
        """

        sm = binascii.a2b_hex(self.latin1_sm)
        pdu = self.buildSubmitSmTest(sm)        

        # SM shall not be altered since it is not sliced (not too long)
        self.assertEquals(pdu.params['short_message'], sm)

    def test_encode_latin1_long(self):
        """
        Test that a latin1 short message long text gets successfully sliced into
        multiple PDUs (parts)
        """

        sm = binascii.a2b_hex(self.latin1_long_sm)
        pdu = self.buildSubmitSmTest(sm)
        
        # The first PDU shall have a next one
        self.assertTrue(isinstance(pdu.nextPdu, SubmitSM))
        # These UDH parameters shall be present in all PDUs
        self.assertTrue(pdu.params['sar_msg_ref_num'] > 0)
        self.assertTrue(pdu.params['sar_total_segments'] > 0)
        self.assertTrue(pdu.params['sar_segment_seqnum'] > 0)
        
        # Iterating through sliced PDUs
        partedSmPdu = pdu
        assembledSm= ''
        lastSeqNum = 0
        while True:
            assembledSm += partedSmPdu.params['short_message']
            
            self.assertTrue(partedSmPdu.params['sar_msg_ref_num'] == pdu.params['sar_msg_ref_num'])
            self.assertTrue(partedSmPdu.params['sar_total_segments'] == pdu.params['sar_total_segments'])
            self.assertTrue(partedSmPdu.params['sar_segment_seqnum'] > lastSeqNum)
            lastSeqNum = partedSmPdu.params['sar_segment_seqnum']

            try:
                partedSmPdu = partedSmPdu.nextPdu
            except AttributeError:
                break
            
        # Assembled SM shall be equal to the original SM
        self.assertEquals(assembledSm, sm)
        
        # The last seqNum shall be equal to total segments
        self.assertEquals(lastSeqNum, pdu.params['sar_total_segments'])
        
    def test_is_delivery(self):
        pdu = DeliverSM(
            source_addr='1234',
            destination_addr='4567',
            short_message='id:1891273321 sub:001 dlvrd:001 submit date:1305050826 done date:1305050826 stat:DELIVRD err:000 text:DLVRD TO MOBILE',
        )
        
        isDlr = self.opFactory.isDeliveryReceipt(pdu)
        self.assertTrue(isDlr is not None)
        self.assertEquals(isDlr['id'], '1891273321')
        self.assertEquals(isDlr['sub'], '001')
        self.assertEquals(isDlr['dlvrd'], '001')
        self.assertEquals(isDlr['sdate'], '1305050826')
        self.assertEquals(isDlr['ddate'], '1305050826')
        self.assertEquals(isDlr['stat'], 'DELIVRD')
        self.assertEquals(isDlr['err'], '000')
        self.assertEquals(isDlr['text'], 'DLVRD TO MOBILE')
