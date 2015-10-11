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
from jasmin.vendor.smpp.pdu.pdu_types import CommandId, PDU, PDURequest, PDUResponse, PDUDataRequest

class BindTransmitterResp(PDUResponse):
    noBodyOnError = True
    commandId = CommandId.bind_transmitter_resp
    mandatoryParams = ['system_id']
    optionalParams = ['sc_interface_version']

class BindTransmitter(PDURequest):
    requireAck = BindTransmitterResp
    commandId = CommandId.bind_transmitter
    mandatoryParams = [
        'system_id',
        'password',
        'system_type',
        'interface_version',
        'addr_ton',
        'addr_npi',
        'address_range',
    ]

class BindReceiverResp(PDUResponse):
    noBodyOnError = True
    commandId = CommandId.bind_receiver_resp
    mandatoryParams = ['system_id']
    optionalParams = ['sc_interface_version']

class BindReceiver(PDURequest):
    requireAck = BindReceiverResp
    commandId = CommandId.bind_receiver
    mandatoryParams = [
        'system_id',
        'password',
        'system_type',
        'interface_version',
        'addr_ton',
        'addr_npi',
        'address_range',
    ]

class BindTransceiverResp(PDUResponse):
    noBodyOnError = True
    commandId = CommandId.bind_transceiver_resp
    mandatoryParams = ['system_id']
    optionalParams = ['sc_interface_version']

class BindTransceiver(PDURequest):
    requireAck = BindTransceiverResp
    commandId = CommandId.bind_transceiver
    mandatoryParams = [
        'system_id',
        'password',
        'system_type',
        'interface_version',
        'addr_ton',
        'addr_npi',
        'address_range',
    ]

class Outbind(PDU):
    commandId = CommandId.outbind
    mandatoryParams = [
        'system_id',
        'password',
    ]

class UnbindResp(PDUResponse):
    commandId = CommandId.unbind_resp

class Unbind(PDURequest):
    requireAck = UnbindResp
    commandId = CommandId.unbind

class GenericNack(PDUResponse):
    commandId = CommandId.generic_nack

class SubmitSMResp(PDUResponse):
    noBodyOnError = True
    commandId = CommandId.submit_sm_resp
    mandatoryParams = ['message_id']

class SubmitSM(PDUDataRequest):
    requireAck = SubmitSMResp
    commandId = CommandId.submit_sm
    mandatoryParams = [
        'service_type',
        'source_addr_ton',
        'source_addr_npi',
        'source_addr',
        'dest_addr_ton',
        'dest_addr_npi',
        'destination_addr',
        'esm_class',
        'protocol_id',
        'priority_flag',
        'schedule_delivery_time',
        'validity_period',
        'registered_delivery',
        'replace_if_present_flag',
        'data_coding',
        'sm_default_msg_id',
        #The sm_length parameter is handled by ShortMessageEncoder
        'short_message',
    ]
    optionalParams = [
        'user_message_reference',
        'source_port',
        'source_addr_subunit',
        'destination_port',
        'dest_addr_subunit',
        'sar_msg_ref_num',
        'sar_total_segments',
        'sar_segment_seqnum',
        'more_messages_to_send',
        'payload_type',
        'message_payload',
        'privacy_indicator',
        'callback_num',
        'callback_num_pres_ind',
        'callback_num_atag',
        'source_subaddress',
        'dest_subaddress',
        'user_response_code',
        'display_time',
        'sms_signal',
        'ms_validity',
        'ms_msg_wait_facilities',
        'number_of_messages',
        'alert_on_msg_delivery',
        'language_indicator',
        'its_reply_type',
        'its_session_info',
        'ussd_service_op',
    ]

class SubmitMultiResp(PDUResponse):
    commandId = CommandId.submit_multi_resp
    mandatoryParams = [
        'message_id',
        'no_unsuccess',
        'no_unsuccess_sme',
    ]

class SubmitMulti(PDUDataRequest):
    requireAck = SubmitMultiResp
    commandId = CommandId.submit_multi
    mandatoryParams = [
        'service_type',
        'source_addr_ton',
        'source_addr_npi',
        'source_addr',
        'number_of_dests',
        'dest_address',
        'esm_class',
        'protocol_id',
        'priority_flag',
        'schedule_delivery_time',
        'validity_period',
        'registered_delivery',
        'replace_if_present_flag',
        'data_coding',
        'sm_default_msg_id',
        #The sm_length parameter is handled by ShortMessageEncoder
        'short_message',
    ]
    optionalParams = [
        'user_message_reference',
        'source_port',
        'source_addr_subunit',
        'destination_port',
        'dest_addr_subunit',
        'sar_msg_ref_num',
        'sar_total_segments',
        'sar_segment_seqnum',
        'more_messages_to_send',
        'payload_type',
        'message_payload',
        'privacy_indicator',
        'callback_num',
        'callback_num_pres_ind',
        'callback_num_atag',
        'source_subaddress',
        'dest_subaddress',
        'display_time',
        'sms_signal',
        'ms_validity',
        'ms_msg_wait_facilities',
        'number_of_messages',
        'alert_on_msg_delivery',
        'language_indicator',
    ]

class DeliverSMResp(PDUResponse):
    commandId = CommandId.deliver_sm_resp
    mandatoryParams = ['message_id']

class DeliverSM(PDUDataRequest):
    requireAck = DeliverSMResp
    commandId = CommandId.deliver_sm
    mandatoryParams = [
        'service_type',
        'source_addr_ton',
        'source_addr_npi',
        'source_addr',
        'dest_addr_ton',
        'dest_addr_npi',
        'destination_addr',
        'esm_class',
        'protocol_id',
        'priority_flag',
        'schedule_delivery_time',
        'validity_period',
        'registered_delivery',
        'replace_if_present_flag',
        'data_coding',
        'sm_default_msg_id',
        #The sm_length parameter is handled by ShortMessageEncoder
        'short_message',
    ]
    optionalParams = [
        # Jasmin update:
        # *_network_type are not optional parameters in standard SMPP
        # it is added for compatibility with some providers (c.f. #120)
        'source_network_type',
        'dest_network_type',
        # Avoid raising exceptions when having vendor specific tags, just
        # bypass them
        'vendor_specific_bypass',

        'user_message_reference',
        'source_port',
        'destination_port',
        'sar_msg_ref_num',
        'sar_total_segments',
        'sar_segment_seqnum',
        'user_response_code',
        'privacy_indicator',
        'payload_type',
        'message_payload',
        'callback_num',
        'source_subaddress',
        'dest_subaddress',
        'language_indicator',
        'its_session_info',
        'network_error_code',
        'message_state',
        'receipted_message_id',
    ]

class DataSMResp(PDUResponse):
    commandId = CommandId.data_sm_resp
    mandatoryParams = ['message_id']
    optionalParams = [
        'delivery_failure_reason',
        'network_error_code',
        'additional_status_info_text',
        'dpf_result',
    ]

class DataSM(PDUDataRequest):
    requireAck = DataSMResp
    commandId = CommandId.data_sm
    mandatoryParams = [
        'service_type',
        'source_addr_ton',
        'source_addr_npi',
        'source_addr',
        'dest_addr_ton',
        'dest_addr_npi',
        'destination_addr',
        'esm_class',
        'registered_delivery',
        'data_coding',
    ]
    optionalParams = [
        'source_port',
        'source_addr_subunit',
        'source_network_type',
        'source_bearer_type',
        'source_telematics_id',
        'destination_port',
        'dest_addr_subunit',
        'dest_network_type',
        'dest_bearer_type',
        'dest_telematics_id',
        'sar_msg_ref_num',
        'sar_total_segments',
        'sar_segment_seqnum',
        'more_messages_to_send',
        'qos_time_to_live',
        'payload_type',
        'message_payload',
        'set_dpf',
        'receipted_message_id',
        'message_state',
        'network_error_code',
        'user_message_reference',
        'privacy_indicator',
        'callback_num',
        'callback_num_pres_ind',
        'callback_num_atag',
        'source_subaddress',
        'dest_subaddress',
        'user_response_code',
        'display_time',
        'sms_signal',
        'ms_validity',
        'ms_msg_wait_facilities',
        'number_of_messages',
        'alert_on_msg_delivery',
        'language_indicator',
        'its_reply_type',
        'its_session_info',
    ]

class QuerySMResp(PDUResponse):
    commandId = CommandId.query_sm_resp
    mandatoryParams = [
        'message_id',
        'final_date',
        'message_state',
        'error_code',
    ]

class QuerySM(PDUDataRequest):
    requireAck = QuerySMResp
    commandId = CommandId.query_sm
    mandatoryParams = [
        'message_id',
        'source_addr_ton',
        'source_addr_npi',
        'source_addr',
    ]

class CancelSMResp(PDUResponse):
    commandId = CommandId.cancel_sm_resp

class CancelSM(PDUDataRequest):
    requireAck = CancelSMResp
    commandId = CommandId.cancel_sm
    mandatoryParams = [
        'service_type',
        'message_id',
        'source_addr_ton',
        'source_addr_npi',
        'source_addr',
        'dest_addr_ton',
        'dest_addr_npi',
        'destination_addr',
    ]

class ReplaceSMResp(PDUResponse):
    commandId = CommandId.replace_sm_resp

class ReplaceSM(PDUDataRequest):
    requireAck = ReplaceSMResp
    commandId = CommandId.replace_sm
    mandatoryParams = [
        'message_id',
        'source_addr_ton',
        'source_addr_npi',
        'source_addr',
        'schedule_delivery_time',
        'validity_period',
        'registered_delivery',
        'sm_default_msg_id',
        'sm_length',
        'short_message',
    ]

class EnquireLinkResp(PDUResponse):
    commandId = CommandId.enquire_link_resp

class EnquireLink(PDURequest):
    requireAck = EnquireLinkResp
    commandId = CommandId.enquire_link

class AlertNotification(PDU):
    commandId = CommandId.alert_notification
    mandatoryParams = [
        'source_addr_ton',
        'source_addr_npi',
        'source_addr',
        'esme_addr_ton',
        'esme_addr_npi',
        'esme_addr',
    ]
    optionalParams = [
        'ms_availability_status',
    ]

PDUS = {}

def _register():
  for pduKlass in globals().values():
      try:
          if issubclass(pduKlass, PDU):
              PDUS[pduKlass.commandId] = pduKlass
      except TypeError:
          pass

_register()

def getPDUClass(commandId):
    return PDUS[commandId]
