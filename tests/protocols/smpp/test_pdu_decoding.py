from io import BytesIO
import binascii
from twisted.trial.unittest import TestCase

from smpp.pdu.pdu_encoding import PDUEncoder
from smpp.pdu.sm_encoding import SMStringEncoder
from smpp.pdu.pdu_types import CommandId, CommandStatus, NetworkType


class PDUDecoderTest(TestCase):
    def getPDU(self, hexStr):
        return PDUEncoder().decode(BytesIO(binascii.a2b_hex(hexStr)))

    def test_deliver_sm_unknown_param_network_error_code(self):
        pduHex = '000000e40000000500000000542e0f21312e303030000101323434393335353535300005005a4150000400000000000000008069643a3661336564393861363634343465386162616333616364396262613836353666207375623a30303120646c7672643a303030207375626d697420646174653a31343130313630303336353020646f6e6520646174653a31343130313630303338303020737461743a554e44454c4956206572723a30303020746578743a042300033030300427000105001e0021366133656439386136363434346538616261633361636439626261383635366600'
        pdu = self.getPDU(pduHex)
        SMStringEncoder().decodeSM(pdu)

        # Asserts
        self.assertEqual(b'000', pdu.params['network_error_code'])

    def test_any_network_type(self):
        """Related to #120"""

        pduHex = '0000004500000005000000000000000100020135393232393631383600040933373435320000000000000000000000000e00010100060001010424000848692066696b7279'
        pdu = self.getPDU(pduHex)
        SMStringEncoder().decodeSM(pdu)

        # Asserts
        self.assertEqual(NetworkType.GSM, pdu.params['source_network_type'])
        self.assertEqual(NetworkType.GSM, pdu.params['dest_network_type'])

    def test_any_network_error_code(self):
        """Related to #117"""

        pduHex = '000000f3000000050000000000000001000101343931353235363739343838370001013034303531333036393939000400000000000000008569643a62633539623861612d326664322d343033352d383131332d313933303165303530303739207375623a30303120646c7672643a303031207375626d697420646174653a31353035303831343430353820646f6e6520646174653a31353035303831343430353820737461743a44454c49565244206572723a30303020746578743a2d042300030300000427000102001e002562633539623861612d326664322d343033352d383131332d31393330316530353030373900'
        pdu = self.getPDU(pduHex)
        SMStringEncoder().decodeSM(pdu)

        # Asserts
        self.assertEqual(b'\x03\x00\x00', pdu.params['network_error_code'])

    def test_deliver_sm_with_message_payload(self):
        pduHex = '0000009200000005000000000001693c00000032313635333532303730330000003737383800040000000001000000000424004f69643a30303030343336393439207375626d697420646174653a3135303432313135303820646f6e6520646174653a3135303432313135303820737461743a44454c49565244206572723a30303000001e00063661616435000427000102'
        pdu = self.getPDU(pduHex)
        SMStringEncoder().decodeSM(pdu)

        # Asserts
        self.assertEqual(b'id:0000436949 submit date:1504211508 done date:1504211508 stat:DELIVRD err:000\x00',
                          pdu.params['message_payload'])
        self.assertEqual(b'6aad5', pdu.params['receipted_message_id'])

    def test_invalid_command_length(self):
        """Related to #124"""

        pduHex = '0000001180000009000000530000000100'
        pdu = self.getPDU(pduHex)

        # Asserts
        self.assertEqual(CommandId.bind_transceiver_resp, pdu.id)
        self.assertEqual('1', str(pdu.seqNum))
        self.assertEqual(CommandStatus.ESME_RINVSYSTYP, pdu.status)

    def test_invalid_command_length_2(self):
        """Related to #128"""

        pduHex = '00000019800000040000000a00000002303030303030303000'
        pdu = self.getPDU(pduHex)

        # Asserts
        self.assertEqual(CommandId.submit_sm_resp, pdu.id)
        self.assertEqual('2', str(pdu.seqNum))
        self.assertEqual(CommandStatus.ESME_RINVSRCADR, pdu.status)

    def test_unknown_tag_value_0x1454(self):
        """Related to #322"""

        pduHex = '000000c9000000050000000003088101000101393731353634313034303934000500534b5954454c45434f4d000400000000000000007b69643a313435313030333135333230323234393537207375623a30303120646c7672643a303031207375626d697420646174653a3135313030333137333220646f6e6520646174653a3135313030333137333220737461743a44454c49565244206572723a3030303020746578743a5465737420286d65686469290423000303000014540004000000011418000400000030'
        pdu = self.getPDU(pduHex)

        # Asserts
        self.assertEqual(CommandId.deliver_sm, pdu.id)
        self.assertEqual('50888961', str(pdu.seqNum))
        self.assertEqual(CommandStatus.ESME_ROK, pdu.status)

    def test_unknown_tag_value_0x60D(self):
        """Related to #325 (deliver_sm)"""

        pduHex = '000000de0000000500000000569d3e3600000033353236393136303034303300000030000400000000000000008569643a3444313943383343207375623a30303120646c7672643a303031207375626d697420646174653a3135313030393031303520646f6e6520646174653a3135313030393031303520737461743a44454c49565244206572723a3030303020746578743a4757323530204d43433d323730204d4e433d312042696c6c696e674d4e433d31001e0009344431394338334300000e000101060d000731323730303100060f000532373030310427000102'
        pdu = self.getPDU(pduHex)

        # Asserts
        self.assertEqual(CommandId.deliver_sm, pdu.id)
        self.assertEqual('1453145654', str(pdu.seqNum))
        self.assertEqual(CommandStatus.ESME_ROK, pdu.status)

    def test_unknown_tag_value_0x2B(self):
        """Related to #325 (deliver_sm) and discussed in
        https://groups.google.com/forum/#!topic/jasmin-sms-gateway/o9iLEfqu8Rs"""

        pduHex = '0000004600000005000000001f368007000101393839333437363930383738000201363937300080000000000000010004546573740202000d2b393839333437363930303030'
        pdu = self.getPDU(pduHex)

        # Asserts
        self.assertEqual(CommandId.deliver_sm, pdu.id)
        self.assertEqual('523665415', str(pdu.seqNum))
        self.assertEqual(CommandStatus.ESME_ROK, pdu.status)

    def test_unknown_tag_value_38636(self):
        """Related to #325 (submit_sm)"""

        pduHex = '000000650000000400000000000005860005004a6f6a6f7300010135373331383833303831323900030000003135313131303134353630373030302b000100f1001a504c454153452049474e4f52452054484953204d455353414745140300053338363336'
        pdu = self.getPDU(pduHex)

        # Asserts
        self.assertEqual(CommandId.submit_sm, pdu.id)
        self.assertEqual('1414', str(pdu.seqNum))
        self.assertEqual(CommandStatus.ESME_ROK, pdu.status)

    def test_unknown_command_status_34(self):
        """Related to #563"""

        pduHex = '00000010800000040000002200000002'
        pdu = self.getPDU(pduHex)

        # Asserts
        self.assertEqual(CommandId.submit_sm_resp, pdu.id)
        self.assertEqual(CommandStatus.RESERVEDSTATUS_UNKNOWN_STATUS, pdu.status)

    def test_deliver_sm_resp_unknown_error(self):
        """Raises a 'builtins.UnicodeDecodeError: 'ascii' codec can't decode byte 0x84 in position 102: ordinal not in range(128)'
        ref: #937
        """
        pduHex = '000000c600000005000000000000000a000201363930373137373035380005003532303030000400000000000000007a69643a30343136333432303734207375623a30303120646c7672643a303031207375626d697420646174653a3231303130343135323220646f6e6520646174653a3231303130343135323220737461743a44454c49565244206572723a30303020746578743a8441894f8820454e544f84482e208c4150414b41000e0001010006000101001e00093138643065303361000427000102'
        pdu = self.getPDU(pduHex)
        SMStringEncoder().decodeSM(pdu)

        # Asserts
        self.assertEqual(b'id:0416342074 sub:001 dlvrd:001 submit date:2101041522 done date:2101041522 stat:DELIVRD err:000 text:\x84A\x89O\x88 ENTO\x84H. \x8cAPAKA',
                          pdu.params['short_message'])
        self.assertEqual(b'18d0e03a', pdu.params['receipted_message_id'])

    def test_deliver_sm_resp_opt_param_not_allow_more_messages_to_send(self):
        """Raises a 'Optional Parameter not allowed: Invalid option more_messages_to_send'
        ref: #938
        """
        pduHex = '000000cb00000005000000000000000b000201363930373137373035380005003532303030000400000000000000007a69643a30343136333432303734207375623a30303120646c7672643a303031207375626d697420646174653a3231303130343135323220646f6e6520646174653a3231303130343135323220737461743a44454c49565244206572723a30303020746578743a8441894f8820454e544f84482e208c4150414b41000e0001010006000101001e000931386430653033610004270001020426000101'
        pdu = self.getPDU(pduHex)
        SMStringEncoder().decodeSM(pdu)

        # Asserts
        self.assertEqual(b'id:0416342074 sub:001 dlvrd:001 submit date:2101041522 done date:2101041522 stat:DELIVRD err:000 text:\x84A\x89O\x88 ENTO\x84H. \x8cAPAKA',
                          pdu.params['short_message'])
        self.assertEqual(b'18d0e03a', pdu.params['receipted_message_id'])

    def test_invalid_option_vendor_specific_bypass(self):
        """Related to #577"""

        pduHex = '0000001c80000009000000000000000159656e746131001002000134'
        pdu = self.getPDU(pduHex)

        # Asserts
        self.assertEqual(CommandId.bind_transceiver_resp, pdu.id)
        self.assertEqual(CommandStatus.ESME_ROK, pdu.status)
