# -*- test-case-name: jasmin.test.test_operations -*-
import struct
import math
import re
import datetime
import dateutil.parser as parser
from jasmin.vendor.smpp.pdu.operations import SubmitSM, DataSM, DeliverSM
from jasmin.protocols.smpp.configs import SMPPClientConfig
from jasmin.vendor.smpp.pdu.pdu_types import (EsmClass, 
                                            EsmClassMode, 
                                            EsmClassType, 
                                            EsmClassGsmFeatures, 
                                            MoreMessagesToSend, 
                                            MessageState
                                            )

class UnknownMessageStatusError(Exception):
    """Raised when message_status is not recognized
    """

class SMPPOperationFactory():
    lastLongSmSeqNum = 0
    
    def __init__(self, config = None, long_content_max_parts = 5, long_content_split = 'sar'):
        if config is not None:
            self.config = config
        else:
            self.config = SMPPClientConfig(**{'id':'anyid'})

        self.long_content_max_parts = long_content_max_parts
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
    
    def isDeliveryReceipt(self, DeliverSM):
        """Check whether DeliverSM is a DLR or not, will return None if not
        or a dict with the DLR elements"""
        ret = None
        
        # Example of DLR content
        # id:IIIIIIIIII sub:SSS dlvrd:DDD submit date:YYMMDDhhmm done
        # date:YYMMDDhhmm stat:DDDDDDD err:E text: . . . . . . . . .
        pattern = r"^id:(?P<id>[\dA-Za-z-_]+) sub:(?P<sub>\d{3}) dlvrd:(?P<dlvrd>\d{3}) submit date:(?P<sdate>\d+) done date:(?P<ddate>\d+) stat:(?P<stat>\w{7}) err:(?P<err>\w{3}) text:(?P<text>.*)"
        m = re.search(pattern, DeliverSM.params['short_message'], flags=re.IGNORECASE)
        if m is not None:
            ret = m.groupdict()
        
        return ret
    
    def claimLongSmSeqNum(self):
        if self.lastLongSmSeqNum > 65535:
            self.lastLongSmSeqNum = 0

        self.lastLongSmSeqNum += 1
        
        return self.lastLongSmSeqNum

    def SubmitSM(self, short_message, data_coding = 0, **kwargs):
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
            maxSmLength = 140
            slicedMaxSmLength = maxSmLength - 6
        elif kwargs['data_coding'] in [2, 4, 5, 8, 9, 13, 14]:
            # 16 bit coding
            maxSmLength = 70
            slicedMaxSmLength = maxSmLength - 3
        else:
            # 7 bit coding is the default 
            # for data_coding in [0, 1] or any other invalid value
            maxSmLength = 160
            slicedMaxSmLength = 153

        longMessage = kwargs['short_message']
        smLength = len(kwargs['short_message'])
        
        # if SM is longer than maxSmLength, build multiple SubmitSMs
        # and link them
        if smLength > maxSmLength:
            total_segments = int(math.ceil(smLength / float(slicedMaxSmLength)))
            # Obey to configured longContentMaxParts
            if total_segments > self.long_content_max_parts:
                total_segments = self.long_content_max_parts
            
            msg_ref_num = self.claimLongSmSeqNum()
            
            for i in range(total_segments):
                segment_seqnum = i+1
                
                # Keep in memory previous PDU in order to set nextPdu in it later
                try:
                    tmpPdu
                    previousPdu = tmpPdu
                except NameError:
                    previousPdu = None

                kwargs['short_message'] = longMessage[slicedMaxSmLength*i:slicedMaxSmLength*(i+1)]
                tmpPdu = self._setConfigParamsInPDU(SubmitSM(**kwargs), kwargs)
                if self.long_content_split == 'sar':
                    # Slice short_message and create the PDU using SAR options
                    tmpPdu.params['sar_total_segments'] = total_segments
                    tmpPdu.params['sar_segment_seqnum'] = segment_seqnum
                    tmpPdu.params['sar_msg_ref_num'] = msg_ref_num
                elif self.long_content_split == 'udh':
                    # Slice short_message and create the PDU using UDH options
                    tmpPdu.params['esm_class'] = EsmClass(EsmClassMode.DEFAULT, EsmClassType.DEFAULT, [EsmClassGsmFeatures.UDHI_INDICATOR_SET])
                    if segment_seqnum < total_segments:
                        tmpPdu.params['more_messages_to_send'] = MoreMessagesToSend.MORE_MESSAGES
                    else:
                        tmpPdu.params['more_messages_to_send'] = MoreMessagesToSend.NO_MORE_MESSAGES
                    # UDH composition:
                    udh = []
                    udh.append(struct.pack('!B', 5)) # Length of User Data Header
                    udh.append(struct.pack('!B', 0)) # Information Element Identifier, equal to 00 (Concatenated short messages, 8-bit reference number)
                    udh.append(struct.pack('!B', 3)) # Length of the header, excluding the first two fields; equal to 03
                    udh.append(struct.pack('!B', msg_ref_num))
                    udh.append(struct.pack('!B', total_segments))
                    udh.append(struct.pack('!B', segment_seqnum))
                    tmpPdu.params['short_message'] = ''.join(udh) + kwargs['short_message']
                
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

    def getReceipt(self, dlr_pdu, msgid, source_addr, destination_addr, message_status, sub_date):
        "Will build a DataSm or a DeliverSm (depending on dlr_pdu) containing a receipt data"

        sm_message_stat = message_status
        # Prepare message_state
        if message_status[:5] == 'ESME_':
            if message_status == 'ESME_ROK':
                message_state = MessageState.ACCEPTED
                sm_message_stat = 'ACCEPTD'
                err = 0
            else:
                message_state = MessageState.UNDELIVERABLE
                sm_message_stat = 'UNDELIV'
                err = 10
        elif message_status == 'UNDELIV':
            message_state = MessageState.UNDELIVERABLE
            err = 10
        elif message_status == 'REJECTD':
            message_state = MessageState.REJECTED
            err = 20
        elif message_status == 'DELIVRD':
            err = 0
            message_state = MessageState.DELIVERED
        elif message_status == 'EXPIRED':
            err = 30
            message_state = MessageState.EXPIRED
        elif message_status == 'DELETED':
            err = 40
            message_state = MessageState.DELETED
        elif message_status == 'ACCEPTD':
            err = 0
            message_state = MessageState.ACCEPTED
        elif message_status == 'UNKNOWN':
            err = 50
            message_state = MessageState.UNKNOWN
        else:
            raise UnknownMessageStatusError('Unknow message_status: %s' % message_status)

        # Build pdu
        if dlr_pdu == 'deliver_sm':
            short_message = r"id:%s submit date:%s done date:%s stat:%s err:%03d" % (
                msgid,
                parser.parse(sub_date).strftime("%Y%m%d%H%M"),
                datetime.datetime.now().strftime("%Y%m%d%H%M"),
                sm_message_stat,
                err,
            )

            # Build DeliverSM pdu
            pdu = DeliverSM(
                source_addr = destination_addr,
                destination_addr = source_addr,
                esm_class = EsmClass(EsmClassMode.DEFAULT, EsmClassType.SMSC_DELIVERY_RECEIPT),
                receipted_message_id = msgid,
                short_message = short_message,
                message_state = message_state,
            )
        else:
            # Build DataSM pdu
            pdu = DataSM(
                source_addr = destination_addr,
                destination_addr = source_addr,
                esm_class = EsmClass(EsmClassMode.DEFAULT, EsmClassType.SMSC_DELIVERY_RECEIPT),
                receipted_message_id = msgid,
                message_state = message_state,
            )

        return pdu