# -*- test-case-name: jasmin.test.test_operations -*-
# Copyright 2012 Fourat Zouari <fourat@gmail.com>
# See LICENSE for details.

import math
import re
from jasmin.vendor.smpp.pdu.operations import SubmitSM
from jasmin.protocols.smpp.configs import SMPPClientConfig

class SMPPOperationFactory():
    lastLongSmSeqNum = 0
    
    def __init__(self, config = None):
        if config is not None:
            self.config = config
        else:
            self.config = SMPPClientConfig(**{'id':'anyid'})
        
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
        pattern = r"^id:(?P<id>\d{1,20}) sub:(?P<sub>\d{3}) dlvrd:(?P<dlvrd>\d{3}) submit date:(?P<sdate>\d{10}) done date:(?P<ddate>\d{10}) stat:(?P<stat>\w{7}) err:(?P<err>\w{3}) text:(?P<text>.*)"
        m = re.search(pattern, DeliverSM.params['short_message'], flags=re.IGNORECASE)
        if m is not None:
            ret = m.groupdict()
        
        return ret
    
    def claimLongSmSeqNum(self):
        if self.lastLongSmSeqNum > 65535:
            self.lastLongSmSeqNum = 0

        self.lastLongSmSeqNum += 1
        
        return self.lastLongSmSeqNum

    def SubmitSM(self, **kwargs):
        maxSmLength = 254
        
        if kwargs['short_message']:
            longMessage = kwargs['short_message']
            smLength = len(kwargs['short_message'])
        else:
            smLength = 0
        
        """if SM is longer than maxSmLength, build multiple SubmitSMs
        and link them
        """
        if smLength > maxSmLength:
            sar_total_segments = int(math.ceil(smLength / float(maxSmLength)))
            sar_msg_ref_num = self.claimLongSmSeqNum()
            
            for i in range(sar_total_segments):
                sar_segment_seqnum = i+1
                
                # Keep in memory previous PDU in order to set nextPdu in it later
                try:
                    tmpPdu
                    previousPdu = tmpPdu
                except NameError:
                    previousPdu = None

                # Slice short_message and create the PDU
                kwargs['short_message'] = longMessage[maxSmLength*i:maxSmLength*(i+1)]
                tmpPdu = self._setConfigParamsInPDU(SubmitSM(**kwargs), kwargs)
                tmpPdu.params['sar_total_segments'] = sar_total_segments
                tmpPdu.params['sar_segment_seqnum'] = sar_segment_seqnum
                tmpPdu.params['sar_msg_ref_num'] = sar_msg_ref_num
                
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
