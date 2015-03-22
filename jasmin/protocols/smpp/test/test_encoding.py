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