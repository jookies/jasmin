import datetime
import math
import re
import struct
from enum import Enum

import dateutil.parser as parser

from jasmin.protocols.smpp.configs import SMPPClientConfig
from smpp.pdu.operations import SubmitSM, DataSM, DeliverSM
from smpp.pdu.pdu_types import (EsmClass, EsmClassMode, EsmClassType, EsmClassGsmFeatures,
                                              MoreMessagesToSend, MessageState, AddrTon, AddrNpi)

message_state_map = {
    MessageState.ACCEPTED: 'ACCEPTD',
    MessageState.UNDELIVERABLE: 'UNDELIV',
    MessageState.REJECTED: 'REJECTD',
    MessageState.DELIVERED: 'DELIVRD',
    MessageState.EXPIRED: 'EXPIRED',
    MessageState.DELETED: 'DELETED',
    MessageState.UNKNOWN: 'UNKNOWN',
}


class UnknownMessageStatusError(Exception):
    """Raised when message_status is not recognized
    """


class SMPPOperationFactory:
    lastLongMsgRefNum = 0

    def __init__(self, config=None, long_content_max_parts=5, long_content_split='sar'):
        if config is not None:
            self.config = config
        else:
            self.config = SMPPClientConfig(**{'id': 'anyid'})

        self.long_content_max_parts = int(long_content_max_parts)
        if isinstance(long_content_split, bytes):
            long_content_split = long_content_split.decode()
        self.long_content_split = long_content_split

    def _setConfigParamsInPDU(self, pdu, kwargs):
        """Check for PDU's mandatory parameters and try to set
        remaining unset params (in kwargs) from the config default
        values
        """

        for param in pdu.mandatoryParams:
            if param not in kwargs:
                try:
                    pdu.params[param] = getattr(self.config, param)
                except AttributeError:
                    pdu.params[param] = None

        return pdu

    def isDeliveryReceipt(self, pdu):
        """Check whether pdu is a DLR or not, will return None if not
        or a dict with the DLR elements.
        It'll proceed through 2 steps:
         1. looking for receipted_message_id and message_state
         2. then parsing the message content for extra fields
        """

        # Delivery receipt can be in form of DeliverSM or DataSM
        if not isinstance(pdu, DeliverSM) and not isinstance(pdu, DataSM):
            return None

        # Fill return object with default values
        # These values are not mandatory, this means the pdu will
        # be considered as a DLR even when they are not set !
        ret = {'dlvrd': 'ND', 'sub': 'ND', 'sdate': 'ND', 'ddate': 'ND', 'err': 'ND', 'text': ''}

        # 1.Looking for optional parameters
        ###################################
        if 'receipted_message_id' in pdu.params and 'message_state' in pdu.params:
            ret['id'] = pdu.params['receipted_message_id']

            if pdu.params['message_state'] in message_state_map:
                ret['stat'] = message_state_map[pdu.params['message_state']]
            else:
                ret['stat'] = 'UNKNOWN'

        # 2.Message content parsing if short_message exists:
        ####################################################
        # Example of DLR content
        # id:IIIIIIIIII sub:SSS dlvrd:DDD submit date:YYMMDDhhmm done
        # date:YYMMDDhhmm stat:DDDDDDD err:E text: . . . . . . . . .
        if 'short_message' in pdu.params:
            patterns = [
                r"id:(?P<id>[\dA-Za-z-_]+)",
                r"sub:(?P<sub>\d{1,3})",
                r"dlvrd:(?P<dlvrd>\d{1,3})",
                r"submit date:(?P<sdate>\d+)",
                r"done date:(?P<ddate>\d+)",
                r"stat:(?P<stat>\w{7})",
                r"err:(?P<err>\w{1,3})",
                r"[tT]ext:(?P<text>.*)",
            ]

            # Look for patterns and compose return object
            for pattern in patterns:
                if isinstance(pdu.params['short_message'], bytes):
                    m = re.search(pattern, pdu.params['short_message'].decode('utf-8', 'ignore'))
                else:
                    m = re.search(pattern, pdu.params['short_message'])
                if m:
                    key = list(m.groupdict())[0]
                    if (key not in ['id', 'stat']
                        or (key == 'id' and 'id' not in ret)
                        or (key == 'stat' and 'stat' not in ret)):
                        ret.update(m.groupdict())

        if ret['sub'] != 'ND' and len(ret['sub']) < 3:
            ret['sub'] = '{:0>3}'.format(ret['sub'])

        if ret['dlvrd'] != 'ND' and len(ret['dlvrd']) < 3:
            ret['dlvrd'] = '{:0>3}'.format(ret['dlvrd'])

        if ret['err'] != 'ND' and len(ret['err']) < 3:
            ret['err'] = '{:0>3}'.format(ret['err'])

        # Should we consider this as a DLR ?
        if 'id' in ret and 'stat' in ret:
            return ret
        else:
            return None

    def claimLongMsgRefNum(self):
        if self.lastLongMsgRefNum >= 255:
            self.lastLongMsgRefNum = 0

        self.lastLongMsgRefNum += 1

        return self.lastLongMsgRefNum

    def SubmitSM(self, short_message, data_coding=0, **kwargs):
        """Depending on the short_message length, this method will return a classical SubmitSM or
        a serie of linked SubmitSMs (parted message)
        """

        kwargs['short_message'] = short_message
        kwargs['data_coding'] = data_coding

        # Possible data_coding values : 0,1,2,3,4,5,6,7,8,9,10,13,14
        # Set the max short message length depending on the
        # coding (7, 8 or 16 bits)
        if kwargs['data_coding'] in [3, 6, 7, 10]:
            # 8 bit coding
            bits = 8
            maxSmLength = 140
            slicedMaxSmLength = maxSmLength - 6
        elif kwargs['data_coding'] in [2, 4, 5, 8, 9, 13, 14]:
            # 16 bit coding
            bits = 16
            maxSmLength = 70
            slicedMaxSmLength = maxSmLength - 3
        else:
            # 7 bit coding is the default
            # for data_coding in [0, 1] or any other invalid value
            bits = 7
            maxSmLength = 160
            slicedMaxSmLength = 153

        longMessage = kwargs['short_message']
        if bits == 16:
            smLength = len(kwargs['short_message']) / 2
        else:
            smLength = len(kwargs['short_message'])

        # if SM is longer than maxSmLength, build multiple SubmitSMs
        # and link them
        if smLength > maxSmLength:
            total_segments = int(math.ceil(smLength / float(slicedMaxSmLength)))
            # Obey to configured longContentMaxParts
            if total_segments > self.long_content_max_parts:
                total_segments = self.long_content_max_parts

            msg_ref_num = self.claimLongMsgRefNum()

            for i in range(total_segments):
                segment_seqnum = i + 1

                # Keep in memory previous PDU in order to set nextPdu in it later
                try:
                    tmpPdu
                    previousPdu = tmpPdu
                except NameError:
                    previousPdu = None

                if bits == 16:
                    kwargs['short_message'] = longMessage[slicedMaxSmLength * i * 2:slicedMaxSmLength * (i + 1) * 2]
                else:
                    kwargs['short_message'] = longMessage[slicedMaxSmLength * i:slicedMaxSmLength * (i + 1)]
                tmpPdu = self._setConfigParamsInPDU(SubmitSM(**kwargs), kwargs)
                if self.long_content_split == 'sar':
                    # Slice short_message and create the PDU using SAR options
                    tmpPdu.params['sar_total_segments'] = total_segments
                    tmpPdu.params['sar_segment_seqnum'] = segment_seqnum
                    tmpPdu.params['sar_msg_ref_num'] = msg_ref_num
                elif self.long_content_split == 'udh':
                    # Slice short_message and create the PDU using UDH options
                    tmpPdu.params['esm_class'] = EsmClass(
                        EsmClassMode.DEFAULT, EsmClassType.DEFAULT, [EsmClassGsmFeatures.UDHI_INDICATOR_SET])
                    if segment_seqnum < total_segments:
                        tmpPdu.params['more_messages_to_send'] = MoreMessagesToSend.MORE_MESSAGES
                    else:
                        tmpPdu.params['more_messages_to_send'] = MoreMessagesToSend.NO_MORE_MESSAGES
                    # UDH composition:
                    udh = []
                    # Length of User Data Header
                    udh.append(struct.pack('!B', 5))
                    # Information Element Identifier, equal to 00
                    # (Concatenated short messages, 8-bit reference number)
                    udh.append(struct.pack('!B', 0))
                    # Length of the header, excluding the first two fields; equal to 03
                    udh.append(struct.pack('!B', 3))
                    udh.append(struct.pack('!B', msg_ref_num))
                    udh.append(struct.pack('!B', total_segments))
                    udh.append(struct.pack('!B', segment_seqnum))
                    if isinstance(kwargs['short_message'], str):
                        tmpPdu.params['short_message'] = b''.join(udh) + kwargs['short_message'].encode()
                    else:
                        tmpPdu.params['short_message'] = b''.join(udh) + kwargs['short_message']

                # - The first PDU is the one we return back
                # - sar_msg_ref_num takes the seqnum of the initial submit_sm
                if i == 0:
                    pdu = tmpPdu

                # PDU chaining
                if previousPdu is not None:
                    previousPdu.nextPdu = tmpPdu
        else:
            pdu = self._setConfigParamsInPDU(SubmitSM(**kwargs), kwargs)

        return pdu

    def getReceipt(self, dlr_pdu, msgid, source_addr, destination_addr, message_status, err, sub_date,
                   source_addr_ton, source_addr_npi, dest_addr_ton, dest_addr_npi):
        """Will build a DataSm or a DeliverSm (depending on dlr_pdu) containing a receipt data"""

        if isinstance(message_status, bytes):
            message_status = message_status.decode()
        if isinstance(msgid, bytes):
            msgid = msgid.decode()
        if isinstance(err, bytes):
            err = err.decode()
        sm_message_stat = message_status
        # Prepare message_state
        if message_status[:5] == 'ESME_':
            if message_status == 'ESME_ROK':
                message_state = MessageState.ACCEPTED
                sm_message_stat = 'ACCEPTD'
            else:
                message_state = MessageState.UNDELIVERABLE
                sm_message_stat = 'UNDELIV'
        elif message_status == 'UNDELIV':
            message_state = MessageState.UNDELIVERABLE
        elif message_status == 'REJECTD':
            message_state = MessageState.REJECTED
        elif message_status == 'DELIVRD':
            message_state = MessageState.DELIVERED
        elif message_status == 'EXPIRED':
            message_state = MessageState.EXPIRED
        elif message_status == 'DELETED':
            message_state = MessageState.DELETED
        elif message_status == 'ACCEPTD':
            message_state = MessageState.ACCEPTED
        elif message_status == 'ENROUTE':
            message_state = MessageState.ENROUTE
        elif message_status == 'UNKNOWN':
            message_state = MessageState.UNKNOWN
        else:
            raise UnknownMessageStatusError('Unknown message_status: %s' % message_status)

        # Build pdu
        if dlr_pdu == 'deliver_sm':
            short_message = r"id:%s submit date:%s done date:%s stat:%s err:%s" % (
                msgid,
                parser.parse(sub_date).strftime("%y%m%d%H%M"),
                datetime.datetime.now().strftime("%y%m%d%H%M"),
                sm_message_stat,
                err,
            )

            # Build DeliverSM pdu
            pdu = DeliverSM(
                source_addr=destination_addr,
                destination_addr=source_addr,
                esm_class=EsmClass(EsmClassMode.DEFAULT, EsmClassType.SMSC_DELIVERY_RECEIPT),
                receipted_message_id=msgid,
                short_message=short_message,
                message_state=message_state,
                source_addr_ton=self.get_enum(AddrTon, dest_addr_ton),
                source_addr_npi=self.get_enum(AddrNpi, dest_addr_npi),
                dest_addr_ton=self.get_enum(AddrTon, source_addr_ton),
                dest_addr_npi=self.get_enum(AddrNpi, source_addr_npi),
            )
        else:
            # Build DataSM pdu
            pdu = DataSM(
                source_addr=destination_addr,
                destination_addr=source_addr,
                esm_class=EsmClass(EsmClassMode.DEFAULT, EsmClassType.SMSC_DELIVERY_RECEIPT),
                receipted_message_id=msgid,
                message_state=message_state,
                source_addr_ton=self.get_enum(AddrTon, dest_addr_ton),
                source_addr_npi=self.get_enum(AddrNpi, dest_addr_npi),
                dest_addr_ton=self.get_enum(AddrTon, source_addr_ton),
                dest_addr_npi=self.get_enum(AddrNpi, source_addr_npi),
            )

        return pdu

    def get_enum(self, enum_type, value):
        if isinstance(value, Enum):
            return value

        _value = value.split('.')

        if len(_value) == 2:
            return getattr(enum_type, _value[1])
        else:
            return getattr(enum_type, value)
