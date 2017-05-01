# -*- coding: utf-8 -*-

import unittest
from datetime import datetime

from jasmin.vendor.smpp.pdu.operations import *
from jasmin.vendor.smpp.pdu.pdu_encoding import *
from jasmin.vendor.smpp.pdu.pdu_types import *
from jasmin.vendor.smpp.pdu.tests.test_pdu_encoding import EncoderTest


class IntegerEncoderTest(EncoderTest):
    def test_int4(self):
        self.do_conversion_test(Int4Encoder(), 0x800001FF, '800001ff')

    def test_int1(self):
        encoder = Int1Encoder()
        self.do_conversion_test(encoder, 255, 'ff')
        self.assertRaises(ValueError, encoder.encode, 256)
        self.do_null_encode_test(encoder, 0, '00')

    def test_int1_max(self):
        self.assertRaises(ValueError, Int1Encoder, max=256)
        encoder = Int1Encoder(max=254)
        self.do_conversion_test(encoder, 254, 'fe')
        self.assertRaises(ValueError, encoder.encode, 255)

    def test_int1_min(self):
        self.assertRaises(ValueError, Int1Encoder, min=-1)
        encoder = Int1Encoder(min=1)
        self.do_conversion_test(encoder, 1, '01')
        self.do_conversion_test(encoder, None, '00')
        self.assertRaises(ValueError, encoder.encode, 0)

    def test_int2(self):
        self.do_conversion_test(Int2Encoder(), 0x41AC, '41ac')


class COctetStringEncoderTest(EncoderTest):
    def test_conversion(self):
        self.do_conversion_test(COctetStringEncoder(), 'hello', '68656c6c6f00')
        self.do_conversion_test(COctetStringEncoder(6), 'hello', '68656c6c6f00')
        self.do_conversion_test(COctetStringEncoder(1), '', '00')
        self.do_null_encode_test(COctetStringEncoder(), '', '00')
        self.assertRaises(ValueError, COctetStringEncoder, 0)

    def test_maxLength_exceeded(self):
        encoder = COctetStringEncoder(5, decodeErrorStatus=CommandStatus.ESME_RINVSRCADR)
        self.assertRaises(ValueError, encoder.encode, 'hello')
        self.do_decode_parse_error_test(encoder.decode, CommandStatus.ESME_RINVSRCADR, '68656c6c6f00')

    def test_ascii_required(self):
        encoder = COctetStringEncoder()
        self.assertRaises(ValueError, encoder.encode, u'\x9b\xa2\x7c')

    def test_requireNull(self):
        encoder = COctetStringEncoder(decodeNull=True, requireNull=True)
        self.do_conversion_test(encoder, None, '00')
        self.assertRaises(ValueError, encoder.encode, 'test')
        self.do_decode_parse_error_test(encoder.decode, CommandStatus.ESME_RUNKNOWNERR, '68656c6c6f00')


class OctetStringEncoderTest(EncoderTest):
    def test_conversion(self):
        hex = '68656c6c6f'
        self.do_conversion_test(OctetStringEncoder(len(hex) / 2), binascii.a2b_hex(hex), hex)
        self.do_conversion_test(OctetStringEncoder(0), '', '')

    def test_maxLength_exceeded(self):
        encoder = OctetStringEncoder(1)
        self.assertRaises(ValueError, encoder.encode, binascii.a2b_hex('ffaa'))


class CommandIdEncoderTest(EncoderTest):
    def test_conversion(self):
        self.do_conversion_test(CommandIdEncoder(), CommandId.enquire_link_resp, '80000015')

    def test_decode_invalid_command_id(self):
        self.do_decode_corrupt_data_error_test(CommandIdEncoder().decode, CommandStatus.ESME_RINVCMDID, 'f0000009')


class CommandStatusEncoderTest(EncoderTest):
    def test_conversion(self):
        self.do_conversion_test(CommandStatusEncoder(), CommandStatus.ESME_RUNKNOWNERR, '000000ff')


class TagEncoderTest(EncoderTest):
    def test_conversion(self):
        self.do_conversion_test(TagEncoder(), Tag.language_indicator, '020d')


class EsmClassEncoderTest(EncoderTest):
    def test_conversion(self):
        self.do_conversion_test(EsmClassEncoder(),
                                EsmClass(EsmClassMode.DATAGRAM, EsmClassType.INTERMEDIATE_DELIVERY_NOTIFICATION,
                                         [EsmClassGsmFeatures.SET_REPLY_PATH]), 'a1')
        self.do_null_encode_test(EsmClassEncoder(), EsmClass(EsmClassMode.DEFAULT, EsmClassType.DEFAULT, []), '00')

    def test_decode_invalid_type(self):
        self.do_decode_parse_error_test(EsmClassEncoder().decode, CommandStatus.ESME_RINVESMCLASS, '30')


class RegisteredDeliveryEncoderTest(EncoderTest):
    def test_conversion(self):
        value = RegisteredDelivery(RegisteredDeliveryReceipt.SMSC_DELIVERY_RECEIPT_REQUESTED,
                                   [RegisteredDeliverySmeOriginatedAcks.SME_DELIVERY_ACK_REQUESTED,
                                    RegisteredDeliverySmeOriginatedAcks.SME_MANUAL_ACK_REQUESTED], True)
        self.do_conversion_test(RegisteredDeliveryEncoder(), value, '1d')
        self.do_null_encode_test(RegisteredDeliveryEncoder(),
                                 RegisteredDelivery(RegisteredDeliveryReceipt.NO_SMSC_DELIVERY_RECEIPT_REQUESTED, [],
                                                    False), '00')

    def test_decode_invalid_receipt(self):
        self.do_decode_parse_error_test(RegisteredDeliveryEncoder().decode, CommandStatus.ESME_RINVREGDLVFLG, '03')


class AddrTonEncoderTest(EncoderTest):
    def test_conversion(self):
        self.do_conversion_test(AddrTonEncoder(fieldName='source_addr_ton'), AddrTon.ALPHANUMERIC, '05')


class PriorityFlagEncoderTest(EncoderTest):
    def test_conversion(self):
        self.do_conversion_test(PriorityFlagEncoder(), PriorityFlag.LEVEL_2, '02')

    def test_decode_invalid(self):
        self.do_decode_parse_error_test(PriorityFlagEncoder().decode, CommandStatus.ESME_RINVPRTFLG, '0f')


class AddrNpiEncoderTest(EncoderTest):
    def test_conversion(self):
        self.do_conversion_test(AddrNpiEncoder(fieldName='source_addr_npi'), AddrNpi.LAND_MOBILE, '06')


class AddrSubunitEncoderTest(EncoderTest):
    def test_conversion(self):
        self.do_conversion_test(AddrSubunitEncoder(), AddrSubunit.MOBILE_EQUIPMENT, '02')


class NetworkTypeEncoderTest(EncoderTest):
    def test_conversion(self):
        self.do_conversion_test(NetworkTypeEncoder(), NetworkType.GSM, '01')


class BearerTypeEncoderTest(EncoderTest):
    def test_conversion(self):
        self.do_conversion_test(BearerTypeEncoder(), BearerType.USSD, '04')


class PayloadTypeEncoderTest(EncoderTest):
    def test_conversion(self):
        self.do_conversion_test(PayloadTypeEncoder(), PayloadType.WCMP, '01')


class PrivacyIndicatorEncoderTest(EncoderTest):
    def test_conversion(self):
        self.do_conversion_test(PrivacyIndicatorEncoder(), PrivacyIndicator.CONFIDENTIAL, '02')


class LanguageIndicatorEncoderTest(EncoderTest):
    def test_conversion(self):
        self.do_conversion_test(LanguageIndicatorEncoder(), LanguageIndicator.SPANISH, '03')


class DisplayTimeEncoderTest(EncoderTest):
    def test_conversion(self):
        self.do_conversion_test(DisplayTimeEncoder(), DisplayTime.INVOKE, '02')


class MsAvailabilityStatusEncoderTest(EncoderTest):
    def test_conversion(self):
        self.do_conversion_test(MsAvailabilityStatusEncoder(), MsAvailabilityStatus.DENIED, '01')


class ReplaceIfPresentFlagEncoderTest(EncoderTest):
    def test_conversion(self):
        self.do_conversion_test(ReplaceIfPresentFlagEncoder(), ReplaceIfPresentFlag.REPLACE, '01')
        self.do_null_encode_test(ReplaceIfPresentFlagEncoder(), ReplaceIfPresentFlag.DO_NOT_REPLACE, '00')


class DataCodingEncoderTest(EncoderTest):
    def test_conversion(self):
        self.do_conversion_test(DataCodingEncoder(), DataCoding(schemeData=DataCodingDefault.LATIN_1), '03')
        self.do_null_encode_test(DataCodingEncoder(), DataCoding(schemeData=DataCodingDefault.SMSC_DEFAULT_ALPHABET),
                                 '00')
        self.do_conversion_test(DataCodingEncoder(), DataCoding(DataCodingScheme.RAW, 48), '30')
        self.do_conversion_test(DataCodingEncoder(), DataCoding(DataCodingScheme.RAW, 11), '0b')
        self.do_conversion_test(DataCodingEncoder(), DataCoding(DataCodingScheme.GSM_MESSAGE_CLASS, DataCodingGsmMsg(
            DataCodingGsmMsgCoding.DEFAULT_ALPHABET, DataCodingGsmMsgClass.CLASS_1)), 'f1')


class DestFlagEncoderTest(EncoderTest):
    def test_conversion(self):
        self.do_conversion_test(DestFlagEncoder(), DestFlag.DISTRIBUTION_LIST_NAME, '02')
        self.assertRaises(ValueError, DestFlagEncoder().encode, None)
        self.do_decode_parse_error_test(DestFlagEncoder().decode, CommandStatus.ESME_RUNKNOWNERR, '00')


class MessageStateEncoderTest(EncoderTest):
    def test_conversion(self):
        self.do_conversion_test(MessageStateEncoder(), MessageState.REJECTED, '08')
        self.assertRaises(ValueError, MessageStateEncoder().encode, None)
        self.do_decode_parse_error_test(MessageStateEncoder().decode, CommandStatus.ESME_RUNKNOWNERR, '00')


class CallbackNumDigitModeIndicatorEncoderTest(EncoderTest):
    def test_conversion(self):
        self.do_conversion_test(CallbackNumDigitModeIndicatorEncoder(), CallbackNumDigitModeIndicator.ASCII, '01')
        self.assertRaises(ValueError, CallbackNumDigitModeIndicatorEncoder().encode, None)


class CallbackNumEncoderTest(EncoderTest):
    def test_conversion(self):
        self.do_conversion_test(CallbackNumEncoder(13),
                                CallbackNum(CallbackNumDigitModeIndicator.ASCII, digits='8033237457'),
                                '01000038303333323337343537')

    def test_decode_invalid_type(self):
        self.do_decode_parse_error_test(CallbackNumEncoder(13).decode, CommandStatus.ESME_RINVOPTPARAMVAL,
                                        '02000038303333323337343537')

    def test_decode_invalid_size(self):
        self.do_decode_parse_error_test(CallbackNumEncoder(2).decode, CommandStatus.ESME_RINVOPTPARAMVAL, '0100')


class SubaddressTypeTagEncoderTest(EncoderTest):
    def test_conversion(self):
        self.do_conversion_test(SubaddressTypeTagEncoder(), SubaddressTypeTag.USER_SPECIFIED, 'a0')
        self.assertRaises(ValueError, SubaddressTypeTagEncoder().encode, None)


class SubaddressEncoderTest(EncoderTest):
    def test_conversion(self):
        self.do_conversion_test(SubaddressEncoder(4), Subaddress(SubaddressTypeTag.USER_SPECIFIED, value='742'),
                                'a0373432')

    def test_decode_invalid_type(self):
        "#325: any invalid type will be marked as RESERVED"
        self.do_conversion_test(SubaddressEncoder(4), Subaddress(SubaddressTypeTag.RESERVED, value='742'), '00373432')

    def test_decode_invalid_size(self):
        self.do_decode_parse_error_test(SubaddressEncoder(1).decode, CommandStatus.ESME_RINVOPTPARAMVAL, 'a0373432')


class TimeEncoderEncoderTest(EncoderTest):
    def test_conversion(self):
        self.do_conversion_test(TimeEncoder(), datetime(2007, 9, 27, 23, 34, 29, 800000),
                                binascii.b2a_hex('070927233429800+' + '\0'))
        self.do_conversion_test(TimeEncoder(), None, '00')

    def test_requireNull(self):
        encoder = TimeEncoder(requireNull=True)
        self.do_conversion_test(encoder, None, '00')
        self.assertRaises(ValueError, encoder.encode, datetime.now())
        self.do_decode_parse_error_test(encoder.decode, CommandStatus.ESME_RUNKNOWNERR,
                                        binascii.b2a_hex('070927233429800+' + '\0'))

    def test_decode_invalid(self):
        self.do_decode_parse_error_test(TimeEncoder(decodeErrorStatus=CommandStatus.ESME_RINVSRCADR).decode,
                                        CommandStatus.ESME_RINVSRCADR, binascii.b2a_hex('070927233429800' + '\0'))


class ShortMessageEncoderTest(EncoderTest):
    def test_conversion(self):
        self.do_conversion_test(ShortMessageEncoder(), 'hello', '0568656c6c6f')
        self.do_null_encode_test(ShortMessageEncoder(), '', '00')


class OptionEncoderTest(EncoderTest):
    def test_dest_addr_subunit(self):
        self.do_conversion_test(OptionEncoder(), Option(Tag.dest_addr_subunit, AddrSubunit.MOBILE_EQUIPMENT),
                                '0005000102')

    def test_decode_invalid_dest_addr_subunit(self):
        self.do_decode_parse_error_test(OptionEncoder().decode, CommandStatus.ESME_RINVOPTPARAMVAL, '00050001ff')

    def test_message_payload(self):
        hexVal = 'ffaa01ce'
        self.do_conversion_test(OptionEncoder(), Option(Tag.message_payload, binascii.a2b_hex(hexVal)),
                                '04240004' + hexVal)

    def test_alert_on_message_delivery(self):
        self.do_conversion_test(OptionEncoder(), Option(Tag.alert_on_message_delivery, None), '130c0000')


class PDUEncoderTest(EncoderTest):
    def do_bind_conversion_test(self, pduBindKlass, reqCommandIdHex, respCommandIdHex):
        reqPdu = pduBindKlass(2, CommandStatus.ESME_ROK,
                              system_id='test',
                              password='secret',
                              system_type='OTA',
                              interface_version=0x34,
                              addr_ton=AddrTon.NATIONAL,
                              addr_npi=AddrNpi.LAND_MOBILE,
                              address_range='127.0.0.*',
                              )
        self.do_conversion_test(PDUEncoder(), reqPdu,
                                '0000002d%s00000000000000027465737400736563726574004f5441003402063132372e302e302e2a00' % reqCommandIdHex)
        respPdu = reqPdu.requireAck(1, CommandStatus.ESME_ROK, system_id='TSI7588', sc_interface_version=0x34)
        self.do_conversion_test(PDUEncoder(), respPdu,
                                '0000001d%s000000000000000154534937353838000210000134' % respCommandIdHex)

    def test_BindTransmitter_conversion(self):
        self.do_bind_conversion_test(BindTransmitter, '00000002', '80000002')

    def test_BindReceiver_conversion(self):
        self.do_bind_conversion_test(BindReceiver, '00000001', '80000001')

    def test_BindTransceiver_conversion(self):
        self.do_bind_conversion_test(BindTransceiver, '00000009', '80000009')

    def test_Unbind_conversion(self):
        pdu = Unbind(4)
        self.do_conversion_test(PDUEncoder(), pdu, '00000010000000060000000000000004')

    def test_UnbindResp_conversion(self):
        pdu = UnbindResp(5, CommandStatus.ESME_ROK)
        self.do_conversion_test(PDUEncoder(), pdu, '00000010800000060000000000000005')

    def test_GenericNack_conversion(self):
        pdu = GenericNack(None, CommandStatus.ESME_RSYSERR)
        self.do_conversion_test(PDUEncoder(), pdu, '00000010800000000000000800000000')

    def test_DeliverSM_syniverse_MO_conversion(self):
        pdu = DeliverSM(2676551972,
                        service_type='AWSBD',
                        source_addr_ton=AddrTon.INTERNATIONAL,
                        source_addr_npi=AddrNpi.ISDN,
                        source_addr='16505551234',
                        dest_addr_ton=AddrTon.INTERNATIONAL,
                        dest_addr_npi=AddrNpi.ISDN,
                        destination_addr='17735554070',
                        esm_class=EsmClass(EsmClassMode.DEFAULT, EsmClassType.DEFAULT),
                        protocol_id=0,
                        priority_flag=PriorityFlag.LEVEL_0,
                        registered_delivery=RegisteredDelivery(
                            RegisteredDeliveryReceipt.NO_SMSC_DELIVERY_RECEIPT_REQUESTED),
                        replace_if_present_flag=ReplaceIfPresentFlag.DO_NOT_REPLACE,
                        data_coding=DataCoding(schemeData=DataCodingDefault.LATIN_1),
                        short_message='there is no spoon',
                        sm_default_msg_id=0,
                        )
        self.do_conversion_test(PDUEncoder(), pdu,
                                '0000004d00000005000000009f88f12441575342440001013136353035353531323334000101313737333535353430373000000000000000000300117468657265206973206e6f2073706f6f6e')

    def test_DeliverSM_handset_ack_conversion(self):
        pdu = DeliverSM(10,
                        service_type='CMT',
                        source_addr_ton=AddrTon.INTERNATIONAL,
                        source_addr_npi=AddrNpi.UNKNOWN,
                        source_addr='6515555678',
                        dest_addr_ton=AddrTon.INTERNATIONAL,
                        dest_addr_npi=AddrNpi.UNKNOWN,
                        destination_addr='123',
                        esm_class=EsmClass(EsmClassMode.DEFAULT, EsmClassType.SMSC_DELIVERY_RECEIPT),
                        protocol_id=0,
                        priority_flag=PriorityFlag.LEVEL_0,
                        registered_delivery=RegisteredDelivery(
                            RegisteredDeliveryReceipt.NO_SMSC_DELIVERY_RECEIPT_REQUESTED),
                        replace_if_present_flag=ReplaceIfPresentFlag.DO_NOT_REPLACE,
                        data_coding=DataCoding(schemeData=DataCodingDefault.SMSC_DEFAULT_ALPHABET),
                        short_message='id:1891273321 sub:001 dlvrd:001 submit date:1305050826 done date:1305050826 stat:DELIVRD err:000 Text:DLVRD TO MOBILE\x00',
                        message_state=MessageState.DELIVERED,
                        receipted_message_id='70BA8A69',
                        sm_default_msg_id=0,
                        )
        self.do_conversion_test(PDUEncoder(), pdu,
                                '000000b900000005000000000000000a434d5400010036353135353535363738000100313233000400000000000000007669643a31383931323733333231207375623a30303120646c7672643a303031207375626d697420646174653a3133303530353038323620646f6e6520646174653a3133303530353038323620737461743a44454c49565244206572723a30303020546578743a444c56524420544f204d4f42494c45000427000102001e0009373042413841363900')

    def test_DeliverSM_sybase_MO_conversion(self):
        pdu = DeliverSM(1,
                        service_type='CMT',
                        source_addr_ton=AddrTon.INTERNATIONAL,
                        source_addr_npi=AddrNpi.UNKNOWN,
                        source_addr='3411149500001',
                        dest_addr_ton=AddrTon.INTERNATIONAL,
                        dest_addr_npi=AddrNpi.UNKNOWN,
                        destination_addr='12345455',
                        esm_class=EsmClass(EsmClassMode.DEFAULT, EsmClassType.DEFAULT),
                        protocol_id=0,
                        priority_flag=PriorityFlag.LEVEL_0,
                        registered_delivery=RegisteredDelivery(
                            RegisteredDeliveryReceipt.NO_SMSC_DELIVERY_RECEIPT_REQUESTED),
                        replace_if_present_flag=ReplaceIfPresentFlag.DO_NOT_REPLACE,
                        data_coding=DataCoding(DataCodingScheme.GSM_MESSAGE_CLASS,
                                               DataCodingGsmMsg(DataCodingGsmMsgCoding.DEFAULT_ALPHABET,
                                                                DataCodingGsmMsgClass.CLASS_2)),
                        short_message='HELLO\x00',
                        sm_default_msg_id=0,
                        )
        self.do_conversion_test(PDUEncoder(), pdu,
                                '0000003f000000050000000000000001434d540001003334313131343935303030303100010031323334353435350000000000000000f2000648454c4c4f00')

    def test_DeliverSM_with_subaddress(self):
        pdu = DeliverSM(1,
                        service_type='BM8',
                        source_addr_ton=AddrTon.INTERNATIONAL,
                        source_addr_npi=AddrNpi.ISDN,
                        source_addr='46123456789',
                        dest_addr_ton=AddrTon.INTERNATIONAL,
                        dest_addr_npi=AddrNpi.ISDN,
                        destination_addr='14046653410',
                        esm_class=EsmClass(EsmClassMode.DEFAULT, EsmClassType.DEFAULT),
                        protocol_id=0,
                        priority_flag=PriorityFlag.LEVEL_0,
                        registered_delivery=RegisteredDelivery(
                            RegisteredDeliveryReceipt.NO_SMSC_DELIVERY_RECEIPT_REQUESTED),
                        replace_if_present_flag=ReplaceIfPresentFlag.DO_NOT_REPLACE,
                        data_coding=DataCoding(DataCodingScheme.GSM_MESSAGE_CLASS,
                                               DataCodingGsmMsg(DataCodingGsmMsgCoding.DEFAULT_ALPHABET,
                                                                DataCodingGsmMsgClass.CLASS_2)),
                        short_message="Hello I'm a bigg fan of you",
                        source_subaddress=Subaddress(SubaddressTypeTag.USER_SPECIFIED, '742'),
                        dest_subaddress=Subaddress(SubaddressTypeTag.USER_SPECIFIED, '4131'),
                        sm_default_msg_id=0,
                        )
        self.do_conversion_test(PDUEncoder(), pdu,
                                '00000066000000050000000000000001424d38000101343631323334353637383900010131343034363635333431300000000000000000f2001b48656c6c6f2049276d206120626967672066616e206f6620796f7502020004a037343202030005a034313331')

    def test_DeliverSM_with_network_error_code(self):
        """Related to #117"""

        pdu = DeliverSM(1,
                        service_type='',
                        source_addr_ton=AddrTon.INTERNATIONAL,
                        source_addr_npi=AddrNpi.ISDN,
                        source_addr='4915256794887',
                        dest_addr_ton=AddrTon.INTERNATIONAL,
                        dest_addr_npi=AddrNpi.ISDN,
                        destination_addr='04051306999',
                        esm_class=EsmClass(EsmClassMode.DEFAULT, EsmClassType.DEFAULT),
                        protocol_id=0,
                        priority_flag=PriorityFlag.LEVEL_0,
                        registered_delivery=RegisteredDelivery(
                            RegisteredDeliveryReceipt.NO_SMSC_DELIVERY_RECEIPT_REQUESTED),
                        replace_if_present_flag=ReplaceIfPresentFlag.DO_NOT_REPLACE,
                        data_coding=DataCoding(DataCodingScheme.GSM_MESSAGE_CLASS,
                                               DataCodingGsmMsg(DataCodingGsmMsgCoding.DEFAULT_ALPHABET,
                                                                DataCodingGsmMsgClass.CLASS_2)),
                        short_message='id:bc59b8aa-2fd2-4035-8113-19301e050079 sub:001 dlvrd:001 submit date:150508144058 done date:150508144058 stat:DELIVRD err:000 text:-',
                        sm_default_msg_id=0,
                        message_state=MessageState.DELIVERED,
                        receipted_message_id='bc59b8aa-2fd2-4035-8113-19301e050079',
                        network_error_code='\x03\x00\x00',
                        )
        self.do_conversion_test(PDUEncoder(), pdu,
                                '000000f30000000500000000000000010001013439313532353637393438383700010130343035313330363939390000000000000000f2008569643a62633539623861612d326664322d343033352d383131332d313933303165303530303739207375623a30303120646c7672643a303031207375626d697420646174653a31353035303831343430353820646f6e6520646174653a31353035303831343430353820737461743a44454c49565244206572723a30303020746578743a2d042300030300000427000102001e002562633539623861612d326664322d343033352d383131332d31393330316530353030373900')

    def test_DeliverSM_with_vendor_specific_bypass(self):
        """#449: fixing the 'Value -1 is less than min 0' error caused by the vendor_specific_bypass parameter"""
        pdu = DeliverSM(1141690945,
                        service_type='',
                        source_addr_ton=AddrTon.INTERNATIONAL,
                        source_addr_npi=AddrNpi.ISDN,
                        source_addr='27727331834',
                        dest_addr_ton=AddrTon.INTERNATIONAL,
                        dest_addr_npi=AddrNpi.ISDN,
                        destination_addr='27600701040',
                        esm_class=EsmClass(EsmClassMode.DEFAULT, EsmClassType.DEFAULT),
                        protocol_id=0,
                        priority_flag=PriorityFlag.LEVEL_0,
                        registered_delivery=RegisteredDelivery(
                            RegisteredDeliveryReceipt.SMSC_DELIVERY_RECEIPT_REQUESTED),
                        replace_if_present_flag=ReplaceIfPresentFlag.DO_NOT_REPLACE,
                        data_coding=DataCoding(schemeData=DataCodingDefault.SMSC_DEFAULT_ALPHABET),
                        short_message='Replied tue 16 Aug 10h11',
                        sm_default_msg_id=0,
                        vendor_specific_bypass='2782913594\x00',
                        )
        self.do_conversion_test(PDUEncoder(), pdu,
                                '0000004f0000000500000000440cd2410001013237373237333331383334000101323736303037303130343000000000000001000000185265706c6965642074756520313620417567203130683131')

    def test_EnquireLink_conversion(self):
        pdu = EnquireLink(6, CommandStatus.ESME_ROK)
        self.do_conversion_test(PDUEncoder(), pdu, '00000010000000150000000000000006')

    def test_EnquireLinkResp_conversion(self):
        pdu = EnquireLinkResp(7)
        self.do_conversion_test(PDUEncoder(), pdu, '00000010800000150000000000000007')

    def test_AlertNotification_conversion(self):
        pdu = AlertNotification(
            source_addr_ton=AddrTon.NATIONAL,
            source_addr_npi=AddrNpi.ISDN,
            source_addr='XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX',
            esme_addr_ton=AddrTon.INTERNATIONAL,
            esme_addr_npi=AddrNpi.LAND_MOBILE,
            esme_addr='YYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYY',
            ms_availability_status=MsAvailabilityStatus.DENIED,
        )
        self.do_conversion_test(PDUEncoder(), pdu,
                                '0000008900000102000000000000000002015858585858585858585858585858585858585858585858585858585858585858585858585858585858585858585858585858585858580001065959595959595959595959595959595959595959595959595959595959595959595959595959595959595959595959595959595959595959000422000101')

    def test_QuerySMResp_conversion(self):
        pdu = QuerySMResp(
            message_id='Smsc2003',
            final_date=None,
            message_state=MessageState.UNKNOWN,
            error_code=None,
        )
        self.do_conversion_test(PDUEncoder(), pdu, '0000001c800000030000000000000000536d73633230303300000700')

    def test_SubmitSM_conversion(self):
        pdu = SubmitSM(9284,
                       service_type='',
                       source_addr_ton=AddrTon.ALPHANUMERIC,
                       source_addr_npi=AddrNpi.UNKNOWN,
                       source_addr='mobileway',
                       dest_addr_ton=AddrTon.INTERNATIONAL,
                       dest_addr_npi=AddrNpi.ISDN,
                       destination_addr='1208230',
                       esm_class=EsmClass(EsmClassMode.DEFAULT, EsmClassType.DEFAULT),
                       protocol_id=0,
                       priority_flag=PriorityFlag.LEVEL_0,
                       registered_delivery=RegisteredDelivery(
                           RegisteredDeliveryReceipt.SMSC_DELIVERY_RECEIPT_REQUESTED),
                       replace_if_present_flag=ReplaceIfPresentFlag.DO_NOT_REPLACE,
                       data_coding=DataCoding(DataCodingScheme.GSM_MESSAGE_CLASS,
                                              DataCodingGsmMsg(DataCodingGsmMsgCoding.DEFAULT_ALPHABET,
                                                               DataCodingGsmMsgClass.CLASS_2)),
                       short_message='HELLO',
                       sm_default_msg_id=0,
                       )
        self.do_conversion_test(PDUEncoder(), pdu,
                                '000000360000000400000000000024440005006d6f62696c65776179000101313230383233300000000000000100f2000548454c4c4f')

    def test_SubmitSM_ringtone_conversion(self):
        pdu = SubmitSM(455569,
                       service_type='',
                       source_addr_ton=AddrTon.ALPHANUMERIC,
                       source_addr_npi=AddrNpi.UNKNOWN,
                       source_addr='mobileway',
                       dest_addr_ton=AddrTon.INTERNATIONAL,
                       dest_addr_npi=AddrNpi.ISDN,
                       destination_addr='3369809342',
                       esm_class=EsmClass(EsmClassMode.DEFAULT, EsmClassType.DEFAULT,
                                          [EsmClassGsmFeatures.UDHI_INDICATOR_SET]),
                       protocol_id=0,
                       priority_flag=PriorityFlag.LEVEL_0,
                       registered_delivery=RegisteredDelivery(
                           RegisteredDeliveryReceipt.SMSC_DELIVERY_RECEIPT_REQUESTED),
                       replace_if_present_flag=ReplaceIfPresentFlag.DO_NOT_REPLACE,
                       data_coding=DataCoding(DataCodingScheme.GSM_MESSAGE_CLASS,
                                              DataCodingGsmMsg(DataCodingGsmMsgCoding.DATA_8BIT,
                                                               DataCodingGsmMsgClass.CLASS_1)),
                       short_message=binascii.a2b_hex(
                           '06050415811581024a3a5db5a5cdcda5bdb8040084d8c51381481381481381481381481381381481581681781881881061881061b81081181081881061881061681081781081881061881061b81081181081881061881061681081781081b81881321081b81881221081b818811210824dc1446000'),
                       sm_default_msg_id=0,
                       )
        self.do_conversion_test(PDUEncoder(), pdu,
                                '000000a900000004000000000006f3910005006d6f62696c65776179000101333336393830393334320040000000000100f5007506050415811581024a3a5db5a5cdcda5bdb8040084d8c51381481381481381481381481381381481581681781881881061881061b81081181081881061881061681081781081881061881061b81081181081881061881061681081781081b81881321081b81881221081b818811210824dc1446000')

    def test_SubmitSM_with_data_coding_mclass_1(self):
        pdu = SubmitSM(2,
                       source_addr='385915222656',
                       destination_addr='385953926992',
                       short_message='jsmtest2 dc f1',
                       data_coding=DataCoding(
                           DataCodingScheme.GSM_MESSAGE_CLASS,
                           DataCodingGsmMsg(
                               DataCodingGsmMsgCoding.DEFAULT_ALPHABET,
                               DataCodingGsmMsgClass.CLASS_1
                           )
                       ),
                       service_type='',
                       esm_class=EsmClass(EsmClassMode.DEFAULT, EsmClassType.DEFAULT, []),
                       protocol_id=0,
                       priority_flag=PriorityFlag.LEVEL_0,
                       registered_delivery=RegisteredDelivery(
                           RegisteredDeliveryReceipt.SMSC_DELIVERY_RECEIPT_REQUESTED),
                       replace_if_present_flag=ReplaceIfPresentFlag.DO_NOT_REPLACE,
                       sm_default_msg_id=0,
                       source_addr_ton=AddrTon.INTERNATIONAL,
                       source_addr_npi=AddrNpi.ISDN,
                       dest_addr_ton=AddrTon.INTERNATIONAL,
                       dest_addr_npi=AddrNpi.ISDN,
                       )
        self.do_conversion_test(PDUEncoder(), pdu,
                                '000000470000000400000000000000020001013338353931353232323635360001013338353935333932363939320000000000000100f1000e6a736d7465737432206463206631')

    def test_decode_command_length_too_short(self):
        self.do_decode_corrupt_data_error_test(PDUEncoder().decode, CommandStatus.ESME_RINVCMDLEN,
                                               '0000000f000000060000000000000000')

    @unittest.skip("c.f. #124 and PDUEncoder's decode() method for more details")
    def test_decode_command_length_too_long(self):
        self.do_decode_corrupt_data_error_test(PDUEncoder().decode, CommandStatus.ESME_RINVCMDLEN,
                                               '00000011000000060000000000000000ff')

    def test_decodeHeader_command_length_too_short(self):
        self.do_decode_corrupt_data_error_test(PDUEncoder().decodeHeader, CommandStatus.ESME_RINVCMDLEN,
                                               '0000000f000000060000000000000000')

    def test_decode_bad_message_length_msg_too_short(self):
        self.do_decode_corrupt_data_error_test(PDUEncoder().decode, CommandStatus.ESME_RINVMSGLEN,
                                               '000000fd80000009000000000000000154534937353838000210000134')

    @unittest.skip("c.f. #124 and PDUEncoder's decode() method for more details")
    def test_decode_bad_message_length_msg_too_long(self):
        self.do_decode_corrupt_data_error_test(PDUEncoder().decode, CommandStatus.ESME_RINVCMDLEN,
                                               '0000001c80000009000000000000000154534937353838000210000134')

    def test_decode_bad_message_ends_in_middle_of_option(self):
        self.do_decode_corrupt_data_error_test(PDUEncoder().decode, CommandStatus.ESME_RINVMSGLEN,
                                               '0000001b8000000900000000000000015453493735383800021000')

    def test_SubmitSMResp_error_has_no_body(self):
        pdu = SubmitSMResp(1234, status=CommandStatus.ESME_RMSGQFUL)
        self.assertTrue(len(SubmitSMResp.mandatoryParams) > 0)
        self.assertEquals(0, len(pdu.params))
        self.do_conversion_test(PDUEncoder(), pdu, '000000108000000400000014000004d2')

    def test_BindReceiverResp_error_has_no_body(self):
        pdu = BindReceiverResp(3456, status=CommandStatus.ESME_RINVPASWD)
        self.assertTrue(len(BindReceiverResp.mandatoryParams) > 0)
        self.assertEquals(0, len(pdu.params))
        self.do_conversion_test(PDUEncoder(), pdu, '00000010800000010000000e00000d80')

    def test_BindTransmitterResp_error_has_no_body(self):
        pdu = BindTransmitterResp(3456, status=CommandStatus.ESME_RINVPASWD)
        self.assertTrue(len(BindTransmitterResp.mandatoryParams) > 0)
        self.assertEquals(0, len(pdu.params))
        self.do_conversion_test(PDUEncoder(), pdu, '00000010800000020000000e00000d80')

    def test_BindTransceiverResp_error_has_no_body(self):
        pdu = BindTransceiverResp(3456, status=CommandStatus.ESME_RINVPASWD)
        self.assertEquals(0, len(pdu.params))
        self.do_conversion_test(PDUEncoder(), pdu, '00000010800000090000000e00000d80')

    def test_BindTransceiverResp_error_has_no_body_status_set_later(self):
        hex = '00000010800000090000000e00000d80'
        pdu = BindTransceiverResp(3456, system_id="XYZ")
        pdu.status = CommandStatus.ESME_RINVPASWD
        # Even though the system_id param was set, it will not be encoded
        self.do_encode_test(PDUEncoder(), pdu, hex)
        # It will decode with no params set
        pduExpected = BindTransceiverResp(3456, status=CommandStatus.ESME_RINVPASWD)
        self.assertEquals(0, len(pduExpected.params))
        self.do_decode_test(PDUEncoder(), pduExpected, hex)


if __name__ == '__main__':
    unittest.main()
