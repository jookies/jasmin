import unittest, binascii, StringIO
from jasmin.vendor.smpp.pdu.sm_encoding import SMStringEncoder
from jasmin.vendor.smpp.pdu.pdu_types import *
from jasmin.vendor.smpp.pdu.gsm_types import *
from jasmin.vendor.smpp.pdu.pdu_encoding import PDUEncoder

class PDUDecoderTest(unittest.TestCase):

    def getPDU(self, hexStr):
        return PDUEncoder().decode(StringIO.StringIO(binascii.a2b_hex(hexStr)))

    def test_deliver_sm_unknown_param_network_error_code(self):
        pduHex = '000000e40000000500000000542e0f21312e303030000101323434393335353535300005005a4150000400000000000000008069643a3661336564393861363634343465386162616333616364396262613836353666207375623a30303120646c7672643a303030207375626d697420646174653a31343130313630303336353020646f6e6520646174653a31343130313630303338303020737461743a554e44454c4956206572723a30303020746578743a042300033030300427000105001e0021366133656439386136363434346538616261633361636439626261383635366600'
        pdu = self.getPDU(pduHex)
        SMStringEncoder().decodeSM(pdu)

        # Asserts
        self.assertEquals('000', pdu.params['network_error_code'])

    def test_any_network_type(self):
        "Related to #120"

        pduHex = '0000004500000005000000000000000100020135393232393631383600040933373435320000000000000000000000000e00010100060001010424000848692066696b7279'
        pdu = self.getPDU(pduHex)
        SMStringEncoder().decodeSM(pdu)

        # Asserts
        self.assertEquals('GSM', str(pdu.params['source_network_type']))
        self.assertEquals('GSM', str(pdu.params['dest_network_type']))

    def test_any_network_error_code(self):
        "Related to #117"

        pduHex = '000000f3000000050000000000000001000101343931353235363739343838370001013034303531333036393939000400000000000000008569643a62633539623861612d326664322d343033352d383131332d313933303165303530303739207375623a30303120646c7672643a303031207375626d697420646174653a31353035303831343430353820646f6e6520646174653a31353035303831343430353820737461743a44454c49565244206572723a30303020746578743a2d042300030300000427000102001e002562633539623861612d326664322d343033352d383131332d31393330316530353030373900'
        pdu = self.getPDU(pduHex)
        SMStringEncoder().decodeSM(pdu)

        # Asserts
        self.assertEquals('\x03\x00\x00', str(pdu.params['network_error_code']))

    def test_deliver_sm_with_message_payload(self):
        pduHex = '0000009200000005000000000001693c00000032313635333532303730330000003737383800040000000001000000000424004f69643a30303030343336393439207375626d697420646174653a3135303432313135303820646f6e6520646174653a3135303432313135303820737461743a44454c49565244206572723a30303000001e00063661616435000427000102'
        pdu = self.getPDU(pduHex)
        SMStringEncoder().decodeSM(pdu)

        # Asserts
        self.assertEquals('id:0000436949 submit date:1504211508 done date:1504211508 stat:DELIVRD err:000\x00', str(pdu.params['message_payload']))
        self.assertEquals('6aad5', str(pdu.params['receipted_message_id']))

    def test_invalid_command_length(self):
        "Related to #124"

        pduHex = '0000001180000009000000530000000100'
        pdu = self.getPDU(pduHex)

        # Asserts
        self.assertEquals('bind_transceiver_resp', str(pdu.id))
        self.assertEquals('1', str(pdu.seqNum))
        self.assertEquals('ESME_RINVSYSTYP', str(pdu.status))

    def test_invalid_command_length_2(self):
        "Related to #128"

        pduHex = '00000019800000040000000a00000002303030303030303000'
        pdu = self.getPDU(pduHex)

        # Asserts
        self.assertEquals('submit_sm_resp', str(pdu.id))
        self.assertEquals('2', str(pdu.seqNum))
        self.assertEquals('ESME_RINVSRCADR', str(pdu.status))

    def test_unknown_tag_value_0x1454(self):
        "Related to #322"

        pduHex = '000000c9000000050000000003088101000101393731353634313034303934000500534b5954454c45434f4d000400000000000000007b69643a313435313030333135333230323234393537207375623a30303120646c7672643a303031207375626d697420646174653a3135313030333137333220646f6e6520646174653a3135313030333137333220737461743a44454c49565244206572723a3030303020746578743a5465737420286d65686469290423000303000014540004000000011418000400000030'
        pdu = self.getPDU(pduHex)

        # Asserts
        self.assertEquals('deliver_sm', str(pdu.id))
        self.assertEquals('50888961', str(pdu.seqNum))
        self.assertEquals('ESME_ROK', str(pdu.status))

    def test_unknown_tag_value_0x60D(self):
        "Related to #325 (deliver_sm)"

        pduHex = '000000de0000000500000000569d3e3600000033353236393136303034303300000030000400000000000000008569643a3444313943383343207375623a30303120646c7672643a303031207375626d697420646174653a3135313030393031303520646f6e6520646174653a3135313030393031303520737461743a44454c49565244206572723a3030303020746578743a4757323530204d43433d323730204d4e433d312042696c6c696e674d4e433d31001e0009344431394338334300000e000101060d000731323730303100060f000532373030310427000102'
        pdu = self.getPDU(pduHex)

        # Asserts
        self.assertEquals('deliver_sm', str(pdu.id))
        self.assertEquals('1453145654', str(pdu.seqNum))
        self.assertEquals('ESME_ROK', str(pdu.status))

    def test_unknown_tag_value_0x2B(self):
        """Related to #325 (deliver_sm) and discussed in
        https://groups.google.com/forum/#!topic/jasmin-sms-gateway/o9iLEfqu8Rs"""

        pduHex = '0000004600000005000000001f368007000101393839333437363930383738000201363937300080000000000000010004546573740202000d2b393839333437363930303030'
        pdu = self.getPDU(pduHex)

        # Asserts
        self.assertEquals('deliver_sm', str(pdu.id))
        self.assertEquals('523665415', str(pdu.seqNum))
        self.assertEquals('ESME_ROK', str(pdu.status))

    def test_unknown_tag_value_38636(self):
        "Related to #325 (submit_sm)"

        pduHex = '000000650000000400000000000005860005004a6f6a6f7300010135373331383833303831323900030000003135313131303134353630373030302b000100f1001a504c454153452049474e4f52452054484953204d455353414745140300053338363336'
        pdu = self.getPDU(pduHex)

        # Asserts
        self.assertEquals('submit_sm', str(pdu.id))
        self.assertEquals('1414', str(pdu.seqNum))
        self.assertEquals('ESME_ROK', str(pdu.status))
