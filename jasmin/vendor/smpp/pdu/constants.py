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

"""
Updated code parts are marked with "Jasmin update" comment
"""
command_status_value_map = {
    0x00000000L : {
        'name' : 'ESME_ROK',
        'description' : 'No error',
    },
    0x00000001L : {
        'name' : 'ESME_RINVMSGLEN',
        'description': 'Message Length is invalid',
    },
    0x00000002L : {
        'name' : 'ESME_RINVCMDLEN',
        'description': 'Command Length is invalid',
    },
    0x00000003L : {
        'name' : 'ESME_RINVCMDID',
        'description': 'Invalid Command ID',
    },
    0x00000004L : {
        'name' : 'ESME_RINVBNDSTS',
        'description': 'Invalid BIND Status for given command',
    },
    0x00000005L : {
        'name' : 'ESME_RALYBND',
        'description': 'ESME Already in Bound State',
    },
    0x00000006L : {
        'name' : 'ESME_RINVPRTFLG',
        'description': 'Invalid Priority Flag',
    },
    0x00000007L : {
        'name' : 'ESME_RINVREGDLVFLG',
        'description': 'Invalid Registered Delivery Flag',
    },
    0x00000008L : {
        'name' : 'ESME_RSYSERR',
        'description': 'System Error',
    },
    0x0000000AL : {
        'name' : 'ESME_RINVSRCADR',
        'description': 'Invalid Source Address',
    },
    0x0000000BL : {
        'name' : 'ESME_RINVDSTADR',
        'description': 'Invalid Dest Addr',
    },
    0x0000000CL : {
        'name' : 'ESME_RINVMSGID',
        'description': 'Message ID is invalid',
    },
    0x0000000DL : {
        'name' : 'ESME_RBINDFAIL',
        'description': 'Bind Failed',
    },
    0x0000000EL : {
        'name' : 'ESME_RINVPASWD',
        'description': 'Invalid Password',
    },
    0x0000000FL : {
        'name' : 'ESME_RINVSYSID',
        'description': 'Invalid System ID',
    },
    0x00000011L : {
        'name' : 'ESME_RCANCELFAIL',
        'description': 'Cancel SM Failed',
    },
    0x00000013L : {
        'name' : 'ESME_RREPLACEFAIL',
        'description': 'Replace SM Failed',
    },
    0x00000014L : {
        'name' : 'ESME_RMSGQFUL',
        'description': 'Message Queue Full',
    },
    0x00000015L : {
        'name' : 'ESME_RINVSERTYP',
        'description': 'Invalid Service Type',
    },
    0x00000033L : {
        'name' : 'ESME_RINVNUMDESTS',
        'description': 'Invalid number of destinations',
    },
    0x00000034L : {
        'name' : 'ESME_RINVDLNAME',
        'description': 'Invalid Distribution List Name',
    },
    0x00000040L : {
        'name' : 'ESME_RINVDESTFLAG',
        'description': 'Destination flag is invalid (submit_multi)',
    },
    0x00000042L : {
        'name' : 'ESME_RINVSUBREP',
        'description': 'Invalid submit with replace request (i.e.  submit_sm with replace_if_present_flag set)',
    },
    0x00000043L : {
        'name' : 'ESME_RINVESMCLASS',
        'description': 'Invalid esm_class field data',
    },
    0x00000044L : {
        'name' : 'ESME_RCNTSUBDL',
        'description': 'Cannot Submit to Distribution List',
    },
    0x00000045L : {
        'name' : 'ESME_RSUBMITFAIL',
        'description': 'submit_sm or submit_multi failed',
    },
    0x00000048L : {
        'name' : 'ESME_RINVSRCTON',
        'description': 'Invalid Source address TON',
    },
    0x00000049L : {
        'name' : 'ESME_RINVSRCNPI',
        'description': 'Invalid Source address NPI',
    },
    0x00000050L : {
        'name' : 'ESME_RINVDSTTON',
        'description': 'Invalid Destination address TON',
    },
    0x00000051L : {
        'name' : 'ESME_RINVDSTNPI',
        'description': 'Invalid Destination address NPI',
    },
    0x00000053L : {
        'name' : 'ESME_RINVSYSTYP',
        'description': 'Invalid system_type field',
    },
    0x00000054L : {
        'name' : 'ESME_RINVREPFLAG',
        'description': 'Invalid replace_if_present flag',
    },
    0x00000055L : {
        'name' : 'ESME_RINVNUMMSGS',
        'description': 'Invalid number of messages',
    },
    0x00000058L : {
        'name' : 'ESME_RTHROTTLED',
        'description': 'Throttling error (ESME has exceeded allowed message limits',
    },
    0x00000061L : {
        'name' : 'ESME_RINVSCHED',
        'description': 'Invalid Scheduled Delivery Time',
    },
    0x00000062L : {
        'name' : 'ESME_RINVEXPIRY',
        'description': 'Invalid message validity period (Expiry time)',
    },
    0x00000063L : {
        'name' : 'ESME_RINVDFTMSGID',
        'description': 'Predefined Message Invalid or Not Found',
    },
    0x00000064L : {
        'name' : 'ESME_RX_T_APPN',
        'description': 'ESME Receiver Temporary App Error Code',
    },
    0x00000065L : {
        'name' : 'ESME_RX_P_APPN',
        'description': 'ESME Receiver Permanent App Error Code',
    },
    0x00000066L : {
        'name' : 'ESME_RX_R_APPN',
        'description': 'ESME Receiver Reject Message Error Code',
    },
    0x00000067L : {
        'name' : 'ESME_RQUERYFAIL',
        'description': 'query_sm request failed',
    },
    0x000000C0L : {
        'name' : 'ESME_RINVOPTPARSTREAM',
        'description': 'Error in the optional part of the PDU Body',
    },
    0x000000C1L : {
        'name' : 'ESME_ROPTPARNOTALLWD',
        'description': 'Optional Parameter not allowed',
    },
    0x000000C2L : {
        'name' : 'ESME_RINVPARLEN',
        'description': 'Invalid Parameter Length',
    },
    0x000000C3L : {
        'name' : 'ESME_RMISSINGOPTPARAM',
        'description': 'Expected Optional Parameter missing',
    },
    0x000000C4L : {
        'name' : 'ESME_RINVOPTPARAMVAL',
        'description': 'Invalid Optional Parameter Value',
    },
    0x000000FEL : {
        'name' : 'ESME_RDELIVERYFAILURE',
        'description': 'Delivery Failure (used for data_sm_resp)',
    },
    0x000000FFL : {
        'name' : 'ESME_RUNKNOWNERR',
        'description': 'Unknown Error',
    },
    # Jasmin update:
    0x00000100L : {
        'name' : 'ESME_RSERTYPUNAUTH',
        'description': 'ESME Not authorised to use specified service_type',
    },
    0x00000101L : {
        'name' : 'ESME_RPROHIBITED',
        'description': 'ESME Prohibited from using specified operation',
    },
    0x00000102L : {
        'name' : 'ESME_RSERTYPUNAVAIL',
        'description': 'Specified service_type is unavailable',
    },
    0x00000103L : {
        'name' : 'ESME_RSERTYPDENIED',
        'description': 'Specified service_type is denied',
    },
    0x00000104L : {
        'name' : 'ESME_RINVDCS',
        'description': 'Invalid Data Coding Scheme',
    },
    0x00000105L : {
        'name' : 'ESME_RINVSRCADDRSUBUNIT',
        'description': 'Source Address Sub unit is Invalid',
    },
    0x00000106L : {
        'name' : 'ESME_RINVDSTADDRSUBUNIT',
        'description': 'Destination Address Sub unit is Invalid',
    },
    0x00000107L : {
        'name' : 'ESME_RINVBCASTFREQINT',
        'description': 'Broadcast Frequency Interval is invalid',
    },
    0x00000108L : {
        'name' : 'ESME_RINVBCASTALIAS_NAME',
        'description': 'Broadcast Alias Name is invalid',
    },
    0x00000109L : {
        'name' : 'ESME_RINVBCASTAREAFMT',
        'description': 'Broadcast Area Format is invalid',
    },
    0x0000010aL : {
        'name' : 'ESME_RINVNUMBCAST_AREAS',
        'description': 'Number of Broadcast Areas is invalid',
    },
    0x0000010bL : {
        'name' : 'ESME_RINVBCASTCNTTYPE',
        'description': 'Broadcast Content Type is invalid',
    },
    0x0000010cL : {
        'name' : 'ESME_RINVBCASTMSGCLASS',
        'description': 'Broadcast Message Class is invalid',
    },
    0x0000010dL : {
        'name' : 'ESME_RBCASTFAIL',
        'description': 'broadcast_sm operation failed',
    },
    0x0000010eL : {
        'name' : 'ESME_RBCASTQUERYFAIL',
        'description': 'query_broadcast_sm operation failed',
    },
    0x0000010fL : {
        'name' : 'ESME_RBCASTCANCELFAIL',
        'description': 'cancel_broadcast_sm operation failed',
    },
    0x00000110L : {
        'name' : 'ESME_RINVBCAST_REP',
        'description': 'Number of Repeated Broadcasts is invalid',
    },
    0x00000111L : {
        'name' : 'ESME_RINVBCASTSRVGRP',
        'description': 'Broadcast Service Group is invalid',
    },
    0x00000112L : {
        'name' : 'ESME_RINVBCASTCHANIND',
        'description': 'Broadcast Channel Indicator is invalid',
    },
    # Jasmin update:
    -1 : {
        'name' : 'RESERVEDSTATUS_SMPP_EXTENSION',
        'description': 'Reserved for SMPP extension',
    },
    # Jasmin update:
    -2 : {
        'name' : 'RESERVEDSTATUS_VENDOR_SPECIFIC',
        'description': 'Reserved for SMSC vendor specific errors',
    },
    # Jasmin update:
    -3 : {
        'name' : 'RESERVEDSTATUS',
        'description': 'Reserved',
    },
}

command_status_name_map = dict([(val['name'], key) for (key, val) in command_status_value_map.items()])

command_id_name_map = {
    'generic_nack': 0x80000000,
    'bind_receiver': 0x00000001,
    'bind_receiver_resp': 0x80000001,
    'bind_transmitter': 0x00000002,
    'bind_transmitter_resp': 0x80000002,
    'query_sm': 0x00000003,
    'query_sm_resp': 0x80000003,
    'submit_sm': 0x00000004,
    'submit_sm_resp': 0x80000004,
    'deliver_sm': 0x00000005,
    'deliver_sm_resp': 0x80000005,
    'unbind': 0x00000006,
    'unbind_resp': 0x80000006,
    'replace_sm': 0x00000007,
    'replace_sm_resp': 0x80000007,
    'cancel_sm': 0x00000008,
    'cancel_sm_resp': 0x80000008,
    'bind_transceiver': 0x00000009,
    'bind_transceiver_resp': 0x80000009,
    'outbind': 0x0000000B,
    'enquire_link': 0x00000015,
    'enquire_link_resp': 0x80000015,
    'submit_multi': 0x00000021,
    'submit_multi_resp': 0x80000021,
    'alert_notification': 0x00000102,
    'data_sm': 0x00000103,
    'data_sm_resp': 0x80000103,
}

command_id_value_map = dict([(val, key) for (key, val) in command_id_name_map.items()])

tag_name_map = {
    'dest_addr_subunit': 0x0005,
    'dest_network_type': 0x0006,
    'dest_bearer_type': 0x0007,
    'dest_telematics_id': 0x0008,
    'source_addr_subunit': 0x000D,
    'source_network_type': 0x000E,
    'source_bearer_type': 0x000F,
    'source_telematics_id': 0x0010,
    'qos_time_to_live': 0x0017,
    'payload_type': 0x0019,
    'additional_status_info_text': 0x001D,
    'receipted_message_id': 0x001E,
    'ms_msg_wait_facilities': 0x0030,
    'privacy_indicator': 0x0201,
    'source_subaddress': 0x0202,
    'dest_subaddress': 0x0203,
    'user_message_reference': 0x0204,
    'user_response_code': 0x0205,
    'source_port': 0x020A,
    'destination_port': 0x020B,
    'sar_msg_ref_num': 0x020C,
    'language_indicator': 0x020D,
    'sar_total_segments': 0x020E,
    'sar_segment_seqnum': 0x020F,
    'sc_interface_version': 0x0210,
    'callback_num_pres_ind': 0x0302,
    'callback_num_atag': 0x0303,
    'number_of_messages': 0x0304,
    'callback_num': 0x0381,
    'dpf_result': 0x0420,
    'set_dpf': 0x0421,
    'ms_availability_status': 0x0422,
    'network_error_code': 0x0423,
    'message_payload': 0x0424,
    'delivery_failure_reason': 0x0425,
    'more_messages_to_send': 0x0426,
    'message_state': 0x0427,
    'ussd_service_op': 0x0501,
    'display_time': 0x1201,
    'sms_signal': 0x1203,
    'ms_validity': 0x1204,
    'alert_on_message_delivery': 0x130C,
    'its_reply_type': 0x1380,
    'its_session_info': 0x1383,
    # Jasmin update: bypass vendor specific tags
    'vendor_specific_bypass': -1,
}

tag_value_map = dict([(val, key) for (key, val) in tag_name_map.items()])

esm_class_mode_name_map = {
    'DEFAULT': 0x0,
    'DATAGRAM': 0x1,
    'FORWARD': 0x2,
    'STORE_AND_FORWARD': 0x3,
}
esm_class_mode_value_map = dict([(val, key) for (key, val) in esm_class_mode_name_map.items()])

esm_class_type_name_map = {
    'DEFAULT': 0x00,
    'SMSC_DELIVERY_RECEIPT': 0x04,
    'DELIVERY_ACKNOWLEDGEMENT': 0x08,
    'MANUAL_ACKNOWLEDGMENT': 0x10,
    'CONVERSATION_ABORT': 0x18,
    'INTERMEDIATE_DELIVERY_NOTIFICATION': 0x20,
}
esm_class_type_value_map = dict([(val, key) for (key, val) in esm_class_type_name_map.items()])

esm_class_gsm_features_name_map = {
    'UDHI_INDICATOR_SET': 0x40,
    'SET_REPLY_PATH': 0x80,
}
esm_class_gsm_features_value_map = dict([(val, key) for (key, val) in esm_class_gsm_features_name_map.items()])

registered_delivery_receipt_name_map = {
    'NO_SMSC_DELIVERY_RECEIPT_REQUESTED': 0x00,
    'SMSC_DELIVERY_RECEIPT_REQUESTED': 0x01,
    'SMSC_DELIVERY_RECEIPT_REQUESTED_FOR_FAILURE': 0x02,
}
registered_delivery_receipt_value_map = dict([(val, key) for (key, val) in registered_delivery_receipt_name_map.items()])

registered_delivery_sme_originated_acks_name_map = {
    'SME_DELIVERY_ACK_REQUESTED': 0x04,
    'SME_MANUAL_ACK_REQUESTED': 0x08,
}
registered_delivery_sme_originated_acks_value_map = dict([(val, key) for (key, val) in registered_delivery_sme_originated_acks_name_map.items()])

addr_subunit_name_map = {
    'UNKNOWN': 0x00,
    'MS_DISPLAY': 0x01,
    'MOBILE_EQUIPMENT': 0x2,
    'SMART_CARD_1': 0x3,
    'EXTERNAL_UNIT_1': 0x4,
}
addr_subunit_value_map = dict([(val, key) for (key, val) in addr_subunit_name_map.items()])

addr_ton_name_map = {
    'UNKNOWN': 0x00,
    'INTERNATIONAL': 0x01,
    'NATIONAL': 0x02,
    'NETWORK_SPECIFIC': 0x03,
    'SUBSCRIBER_NUMBER': 0x04,
    'ALPHANUMERIC': 0x05,
    'ABBREVIATED': 0x06,
}
addr_ton_value_map = dict([(val, key) for (key, val) in addr_ton_name_map.items()])

addr_npi_name_map = {
    'UNKNOWN': 0x00,
    'ISDN': 0x01,
    'DATA': 0x03,
    'TELEX': 0x04,
    'LAND_MOBILE': 0x06,
    'NATIONAL': 0x08,
    'PRIVATE': 0x09,
    'ERMES': 0x0a,
    'INTERNET': 0x0e,
    'WAP_CLIENT_ID': 0x12,
}
addr_npi_value_map = dict([(val, key) for (key, val) in addr_npi_name_map.items()])

priority_flag_name_map = {
    'LEVEL_0': 0x00,
    'LEVEL_1': 0x01,
    'LEVEL_2': 0x02,
    'LEVEL_3': 0x03,
}
priority_flag_value_map = dict([(val, key) for (key, val) in priority_flag_name_map.items()])

replace_if_present_flap_name_map = {
    'DO_NOT_REPLACE': 0x00,
    'REPLACE': 0x01,
}
replace_if_present_flap_value_map = dict([(val, key) for (key, val) in replace_if_present_flap_name_map.items()])

more_messages_to_send_name_map = {
    'NO_MORE_MESSAGES': 0x00,
    'MORE_MESSAGES': 0x01,
}
more_messages_to_send_value_map = dict([(val, key) for (key, val) in more_messages_to_send_name_map.items()])

data_coding_scheme_name_map = {
    'GSM_MESSAGE_CLASS': 0xf0,
}
data_coding_scheme_value_map = dict([(val, key) for (key, val) in data_coding_scheme_name_map.items()])

data_coding_default_name_map = {
    'SMSC_DEFAULT_ALPHABET': 0x00,
    'IA5_ASCII': 0x01,
    'OCTET_UNSPECIFIED': 0x02,
    'LATIN_1': 0x03,
    'OCTET_UNSPECIFIED_COMMON': 0x04,
    'JIS': 0x05,
    'CYRILLIC': 0x06,
    'ISO_8859_8': 0x07,
    'UCS2': 0x08,
    'PICTOGRAM': 0x09,
    'ISO_2022_JP': 0x0a,
    'EXTENDED_KANJI_JIS': 0x0d,
    'KS_C_5601': 0x0e,
}
data_coding_default_value_map = dict([(val, key) for (key, val) in data_coding_default_name_map.items()])

data_coding_gsm_message_coding_name_map = {
    'DEFAULT_ALPHABET': 0x00,
    'DATA_8BIT': 0x04,
}
data_coding_gsm_message_coding_value_map = dict([(val, key) for (key, val) in data_coding_gsm_message_coding_name_map.items()])

data_coding_gsm_message_class_name_map = {
    'NO_MESSAGE_CLASS': 0x00,
    'CLASS_1': 0x01,
    'CLASS_2': 0x02,
    'CLASS_3': 0x03,
}
data_coding_gsm_message_class_value_map = dict([(val, key) for (key, val) in data_coding_gsm_message_class_name_map.items()])

dest_flag_name_map = {
    'SME_ADDRESS': 0x01,
    'DISTRIBUTION_LIST_NAME': 0x02,
}
dest_flag_value_map = dict([(val, key) for (key, val) in dest_flag_name_map.items()])

message_state_name_map = {
    'ENROUTE': 0x01,
    'DELIVERED': 0x02,
    'EXPIRED': 0x03,
    'DELETED': 0x04,
    'UNDELIVERABLE': 0x05,
    'ACCEPTED': 0x06,
    'UNKNOWN': 0x07,
    'REJECTED': 0x08,
}
message_state_value_map = dict([(val, key) for (key, val) in message_state_name_map.items()])

callback_num_digit_mode_indicator_name_map = {
    'TBCD': 0x00,
    'ASCII': 0x01,
}
callback_num_digit_mode_indicator_value_map = dict([(val, key) for (key, val) in callback_num_digit_mode_indicator_name_map.items()])

subaddress_type_tag_name_map = {
    'NSAP_EVEN': 0x80,
    'NSAP_ODD': 0x88,
    'USER_SPECIFIED': 0xa0,
    # Jasmin update: (#325)
    'RESERVED': 0x00,
}
subaddress_type_tag_value_map = dict([(val, key) for (key, val) in subaddress_type_tag_name_map.items()])

ms_availability_status_name_map = {
    'AVAILABLE': 0x00,
    'DENIED': 0x01,
    'UNAVAILABLE': 0x02,
}
ms_availability_status_value_map = dict([(val, key) for (key, val) in ms_availability_status_name_map.items()])

# Jasmin update:
network_error_code_name_map = {
    'ANSI-136': 0x01,
    'IS-95': 0x02,
    'GSM': 0x03,
    'RESERVED': 0x04,
}
network_error_code_value_map = dict([(val, key) for (key, val) in network_error_code_name_map.items()])

network_type_name_map = {
    'UNKNOWN': 0x00,
    'GSM': 0x01,
    'TDMA': 0x02,
    'CDMA': 0x03,
    'PDC': 0x04,
    'PHS': 0x05,
    'IDEN': 0x06,
    'AMPS': 0x07,
    'PAGING_NETWORK': 0x08,
}
network_type_value_map = dict([(val, key) for (key, val) in network_type_name_map.items()])

bearer_type_name_map = {
    'UNKNOWN': 0x00,
    'SMS': 0x01,
    'CSD': 0x02,
    'PACKET_DATA': 0x03,
    'USSD': 0x04,
    'CDPD': 0x05,
    'DATATAC': 0x06,
    'FLEX_REFLEX': 0x07,
    'CELL_BROADCAST': 0x08,
}
bearer_type_value_map = dict([(val, key) for (key, val) in bearer_type_name_map.items()])

payload_type_name_map = {
    'DEFAULT': 0x00,
    'WCMP': 0x01,
}
payload_type_value_map = dict([(val, key) for (key, val) in payload_type_name_map.items()])

privacy_indicator_name_map = {
    'NOT_RESTRICTED': 0x00,
    'RESTRICTED': 0x01,
    'CONFIDENTIAL': 0x02,
    'SECRET': 0x03,
}
privacy_indicator_value_map = dict([(val, key) for (key, val) in privacy_indicator_name_map.items()])

language_indicator_name_map = {
    'UNSPECIFIED': 0x00,
    'ENGLISH': 0x01,
    'FRENCH': 0x02,
    'SPANISH': 0x03,
    'GERMAN': 0x04,
    'PORTUGUESE': 0x05,
}
language_indicator_value_map = dict([(val, key) for (key, val) in language_indicator_name_map.items()])

display_time_name_map = {
    'TEMPORARY': 0x00,
    'DEFAULT': 0x01,
    'INVOKE': 0x02,
}
display_time_value_map = dict([(val, key) for (key, val) in display_time_name_map.items()])

delivery_failure_reason_name_map = {
    'DESTINATION_UNAVAILABLE': 0x00,
    'DESTINATION_ADDRESS_INVALID': 0x01,
    'PERMANENT_NETWORK_ERROR': 0x02,
    'TEMPORARY_NETWORK_ERROR': 0x03,
}
delivery_failure_reason_value_map = dict([(val, key) for (key, val) in delivery_failure_reason_name_map.items()])
