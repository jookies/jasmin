"""
Test cases for jasmin.protocols.smpp.operations module.
"""

import binascii
from twisted.trial.unittest import TestCase
from jasmin.protocols.smpp.configs import SMPPClientConfig
from jasmin.protocols.smpp.operations import SMPPOperationFactory, UnknownMessageStatusError
from smpp.pdu.pdu_types import CommandId, CommandStatus, MessageState
from smpp.pdu.operations import SubmitSM, DeliverSM, DataSM


class OperationsTest(TestCase):
    def setUp(self):
        self.opFactory = SMPPOperationFactory(SMPPClientConfig(id='test-id'))


class SubmitTest(OperationsTest):
    source_addr = b'20203060'
    destination_addr = b'06155423'
    latin1_sm = b'6162636465666768696a6b6c6d6e6f707172737475767778797a'
    latin1_long_sm = b'6162636465666768696a6b6c6d6e6f707172737475767778797a2e6162636465666768696a6b6c6d6e6f707172737475767778797a2e6162636465666768696a6b6c6d6e6f707172737475767778797a2e6162636465666768696a6b6c6d6e6f707172737475767778797a2e6162636465666768696a6b6c6d6e6f707172737475767778797a2e6162636465666768696a6b6c6d6e6f707172737475767778797a2e6162636465666768696a6b6c6d6e6f707172737475767778797a2e6162636465666768696a6b6c6d6e6f707172737475767778797a2e6162636465666768696a6b6c6d6e6f707172737475767778797a2e6162636465666768696a6b6c6d6e6f707172737475767778797a2e6162636465666768696a6b6c6d6e6f707172737475767778797a2e6162636465666768696a6b6c6d6e6f707172737475767778797a2e6162636465666768696a6b6c6d6e6f707172737475767778797a2e'

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

        self.assertEqual(pdu.id, CommandId.submit_sm)
        self.assertEqual(pdu.status, CommandStatus.ESME_ROK)
        self.assertEqual(pdu.params['destination_addr'], self.destination_addr)
        self.assertEqual(pdu.params['source_addr'], self.source_addr)

        return pdu

    def test_encode_latin1(self):
        """
        Test that a latin1 short message text remain the same after it's getting
        encoded in a PDU object.
        """

        sm = binascii.a2b_hex(self.latin1_sm)
        pdu = self.buildSubmitSmTest(sm)

        # SM shall not be altered since it is not sliced (not too long)
        self.assertEqual(pdu.params['short_message'], sm)

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
        assembledSm = b''
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
        self.assertEqual(assembledSm, sm)

        # The last seqNum shall be equal to total segments
        self.assertEqual(lastSeqNum, pdu.params['sar_total_segments'])


class DeliveryParsingTest(OperationsTest):
    def test_is_delivery_standard(self):
        pdu = DeliverSM(
            source_addr='1234',
            destination_addr='4567',
            short_message='id:1891273321 sub:001 dlvrd:001 submit date:1305050826 done date:1305050826 stat:DELIVRD err:000 text:DLVRD TO MOBILE',
        )

        isDlr = self.opFactory.isDeliveryReceipt(pdu)
        self.assertTrue(isDlr is not None)
        self.assertEqual(isDlr['id'], '1891273321')
        self.assertEqual(isDlr['sub'], '001')
        self.assertEqual(isDlr['dlvrd'], '001')
        self.assertEqual(isDlr['sdate'], '1305050826')
        self.assertEqual(isDlr['ddate'], '1305050826')
        self.assertEqual(isDlr['stat'], 'DELIVRD')
        self.assertEqual(isDlr['err'], '000')
        self.assertEqual(isDlr['text'], 'DLVRD TO MOBILE')

    def test_is_delivery_empty_text(self):
        pdu = DeliverSM(
            source_addr='1234',
            destination_addr='4567',
            short_message='id:1891273321 sub:001 dlvrd:001 submit date:1305050826 done date:1305050826 stat:DELIVRD err:000 text:',
        )

        isDlr = self.opFactory.isDeliveryReceipt(pdu)
        self.assertTrue(isDlr is not None)
        self.assertEqual(isDlr['id'], '1891273321')
        self.assertEqual(isDlr['sub'], '001')
        self.assertEqual(isDlr['dlvrd'], '001')
        self.assertEqual(isDlr['sdate'], '1305050826')
        self.assertEqual(isDlr['ddate'], '1305050826')
        self.assertEqual(isDlr['stat'], 'DELIVRD')
        self.assertEqual(isDlr['err'], '000')
        self.assertEqual(isDlr['text'], '')

    def test_is_delivery_clickatell_70(self):
        """Related to #70
        Parsing clickatell's DLRs
        """
        pdu = DeliverSM(
            source_addr='1234',
            destination_addr='4567',
            short_message='id:a29f6845555647139e5c8f3b817f2c9a sub:001 dlvrd:001 submit date:141023215253 done date:141023215259 stat:DELIVRD err:000 text:HOLA',
        )

        isDlr = self.opFactory.isDeliveryReceipt(pdu)
        self.assertTrue(isDlr is not None)
        self.assertEqual(isDlr['id'], 'a29f6845555647139e5c8f3b817f2c9a')
        self.assertEqual(isDlr['sub'], '001')
        self.assertEqual(isDlr['dlvrd'], '001')
        self.assertEqual(isDlr['sdate'], '141023215253')
        self.assertEqual(isDlr['ddate'], '141023215259')
        self.assertEqual(isDlr['stat'], 'DELIVRD')
        self.assertEqual(isDlr['err'], '000')
        self.assertEqual(isDlr['text'], 'HOLA')

    def test_is_delivery_jasmin_153(self):
        """Related to #153
        Parsing jasmin's DLRs
        """
        pdu = DeliverSM(
            source_addr='1234',
            destination_addr='4567',
            short_message='id:4a38dc46-5125-4969-90be-72104c340d5c sub:001 dlvrd:001 submit date:150519232657 done date:150519232657 stat:DELIVRD err:000 text:-',
        )

        isDlr = self.opFactory.isDeliveryReceipt(pdu)
        self.assertTrue(isDlr is not None)
        self.assertEqual(isDlr['id'], '4a38dc46-5125-4969-90be-72104c340d5c')
        self.assertEqual(isDlr['sub'], '001')
        self.assertEqual(isDlr['dlvrd'], '001')
        self.assertEqual(isDlr['sdate'], '150519232657')
        self.assertEqual(isDlr['ddate'], '150519232657')
        self.assertEqual(isDlr['stat'], 'DELIVRD')
        self.assertEqual(isDlr['err'], '000')
        self.assertEqual(isDlr['text'], '-')

    def test_is_delivery_jasmin_195(self):
        """Related to #195
        Mandatory fields in short_message are parsed and optional fields are set to defaults when
        they dont exist in short_message"""
        pdu = DeliverSM(
            source_addr='1234',
            destination_addr='4567',
            short_message='id:c87c2273-7edb-4bc7-8d3a-7f57f21b625e submit date:201506201641 done date:201506201641 stat:DELIVRD err:000',
        )

        isDlr = self.opFactory.isDeliveryReceipt(pdu)
        self.assertTrue(isDlr is not None)
        self.assertEqual(isDlr['id'], 'c87c2273-7edb-4bc7-8d3a-7f57f21b625e')
        self.assertEqual(isDlr['sub'], 'ND')
        self.assertEqual(isDlr['dlvrd'], 'ND')
        self.assertEqual(isDlr['sdate'], '201506201641')
        self.assertEqual(isDlr['ddate'], '201506201641')
        self.assertEqual(isDlr['stat'], 'DELIVRD')
        self.assertEqual(isDlr['err'], '000')
        self.assertEqual(isDlr['text'], '')

    def test_is_delivery_mmg_deliver_sm_224(self):
        """Related to #224, this is a Sicap's MMG deliver_sm receipt"""
        pdu = DeliverSM(
            source_addr='24206155423',
            destination_addr='JOOKIES',
            short_message='362d9701 2',
            message_state=MessageState.DELIVERED,
            receipted_message_id='362d9701',
        )

        isDlr = self.opFactory.isDeliveryReceipt(pdu)
        self.assertTrue(isDlr is not None)
        self.assertEqual(isDlr['id'], b'362d9701')
        self.assertEqual(isDlr['sub'], 'ND')
        self.assertEqual(isDlr['dlvrd'], 'ND')
        self.assertEqual(isDlr['sdate'], 'ND')
        self.assertEqual(isDlr['ddate'], 'ND')
        self.assertEqual(isDlr['stat'], 'DELIVRD')
        self.assertEqual(isDlr['err'], 'ND')
        self.assertEqual(isDlr['text'], '')

    def test_is_delivery_mmg_data_sm_92(self):
        """Related to #92, this is a Sicap's MMG data_sm receipt"""
        pdu = DataSM(
            source_addr='24206155423',
            destination_addr='JOOKIES',
            message_state=MessageState.DELIVERED,
            receipted_message_id='362d9701',
        )

        isDlr = self.opFactory.isDeliveryReceipt(pdu)
        self.assertTrue(isDlr is not None)
        self.assertEqual(isDlr['id'], b'362d9701')
        self.assertEqual(isDlr['sub'], 'ND')
        self.assertEqual(isDlr['dlvrd'], 'ND')
        self.assertEqual(isDlr['sdate'], 'ND')
        self.assertEqual(isDlr['ddate'], 'ND')
        self.assertEqual(isDlr['stat'], 'DELIVRD')
        self.assertEqual(isDlr['err'], 'ND')
        self.assertEqual(isDlr['text'], '')

    def test_take_msgid_from_tlv_first(self):
        """Related to #427
        When reciept_message_id is provided in short_message and in TLV param, consider the latter"""
        pdu = DataSM(
            source_addr='24206155423',
            destination_addr='JOOKIES',
            message_state=MessageState.DELIVERED,
            receipted_message_id='6000',
            short_message='id:5000 submit date:201506201641 done date:201506201641 stat:DELIVRD err:000',
        )

        isDlr = self.opFactory.isDeliveryReceipt(pdu)
        self.assertEqual(isDlr['id'], b'6000')

    def test_take_message_state_from_tlv_first(self):
        """Related to #427
        When message_state is provided in short_message and in TLV param, consider the latter"""
        pdu = DataSM(
            source_addr='24206155423',
            destination_addr='JOOKIES',
            message_state=MessageState.ACCEPTED,
            receipted_message_id='5000',
            short_message='id:5000 submit date:201506201641 done date:201506201641 stat:DELIVRD err:000',
        )

        isDlr = self.opFactory.isDeliveryReceipt(pdu)
        self.assertEqual(isDlr['stat'], 'ACCEPTD')


class ReceiptCreationTestCases(OperationsTest):
    message_state_map = {
        'ESME_ROK': {'sm': 'ACCEPTD', 'state': MessageState.ACCEPTED},
        'UNDELIV': {'sm': 'UNDELIV', 'state': MessageState.UNDELIVERABLE},
        'REJECTD': {'sm': 'REJECTD', 'state': MessageState.REJECTED},
        'DELIVRD': {'sm': 'DELIVRD', 'state': MessageState.DELIVERED},
        'EXPIRED': {'sm': 'EXPIRED', 'state': MessageState.EXPIRED},
        'DELETED': {'sm': 'DELETED', 'state': MessageState.DELETED},
        'ACCEPTD': {'sm': 'ACCEPTD', 'state': MessageState.ACCEPTED},
        'UNKNOWN': {'sm': 'UNKNOWN', 'state': MessageState.UNKNOWN},
    }

    def test_unknown_message_state(self):
        for dlr_pdu in ['deliver_sm', 'data_sm']:
            self.assertRaises(UnknownMessageStatusError, self.opFactory.getReceipt,
                              dlr_pdu,
                              'anyid',
                              'JASMIN',
                              '06155423',
                              'ANY_STATus',
                              1,
                              '2017-07-19 17:50:12',
                              'UNKNOWN',
                              'UNKNOWN',
                              'UNKNOWN',
                              'UNKNOWN')

    def test_deliver_sm(self):
        for message_state, _test in self.message_state_map.items():
            pdu = self.opFactory.getReceipt(
                'deliver_sm',
                'anyid',
                'JASMIN',
                '06155423',
                message_state,
                1,
                '2017-07-19 17:50:12',
                'UNKNOWN',
                'UNKNOWN',
                'UNKNOWN',
                'UNKNOWN')

            self.assertEqual(pdu.params['message_state'], _test['state'])
            self.assertTrue(('stat:%s' % _test['sm']).encode() in pdu.params['short_message'])

        # Test other ESME_* states:
        pdu = self.opFactory.getReceipt(
            'deliver_sm',
            'anyid',
            'JASMIN',
            '06155423',
            'ESME_RTHROTTLED',
            3,
            '2017-07-19 17:50:12',
            'UNKNOWN',
            'UNKNOWN',
            'UNKNOWN',
            'UNKNOWN')

        self.assertEqual(pdu.params['message_state'], MessageState.UNDELIVERABLE)
        self.assertTrue(b'stat:UNDELIV' in pdu.params['short_message'])

    def test_data_sm(self):
        for message_state, _test in self.message_state_map.items():
            pdu = self.opFactory.getReceipt(
                'data_sm',
                'anyid',
                'JASMIN',
                '06155423',
                message_state,
                2,
                '2017-07-19 17:50:12',
                'UNKNOWN',
                'UNKNOWN',
                'UNKNOWN',
                'UNKNOWN')

            self.assertEqual(pdu.params['message_state'], _test['state'])

        # Test other ESME_* states:
        pdu = self.opFactory.getReceipt(
            'data_sm',
            'anyid',
            'JASMIN',
            '06155423',
            'ESME_RTHROTTLED',
            2,
            '2017-07-19 17:50:12',
            'UNKNOWN',
            'UNKNOWN',
            'UNKNOWN',
            'UNKNOWN')

        self.assertEqual(pdu.params['message_state'], MessageState.UNDELIVERABLE)
