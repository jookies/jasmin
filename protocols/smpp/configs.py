# Copyright 2012 Fourat Zouari <fourat@gmail.com>
# See LICENSE for details.

import logging
import re
from jasmin.vendor.smpp.pdu.pdu_types import (EsmClass, EsmClassMode, EsmClassType, 
                                RegisteredDelivery, RegisteredDeliveryReceipt, 
                                AddrTon, AddrNpi, 
                                PriorityFlag, ReplaceIfPresentFlag, 
                                DataCoding, DataCodingDefault)
from jasmin.vendor.smpp.pdu.smpp_time import SMPPRelativeTime 
from jasmin.config.tools import ConfigFile

class ConfigUndefinedIdError(Exception):
    """Raised when a *Config class is initialized without ID
    """

class ConfigInvalidIdError(Exception):
    """Raised when a *Config class is initialized with an invalid ID syntax
    """
    
class TypeMismatch(Exception):
    """Raised when a *Config element has not a valid type
    """

class InvalidValue(Exception):
    """Raised when a *Config element is set with an invalid value
    """

class SMPPClientConfig():
    def __init__(self, **kwargs):
        if kwargs.get('id', None) == None:
            raise ConfigUndefinedIdError('SMPPClientConfig must have an id')
        
        idcheck = re.compile(r'^[A-Za-z0-9_-]{3,25}$')
        if idcheck.match(kwargs.get('id')) == None:
            raise ConfigInvalidIdError('SMPPClientConfig id syntax is invalid')
            
        self.id = kwargs.get('id')
        
        self.host = kwargs.get('host', '127.0.0.1')
        if not isinstance(self.host, str):
            raise TypeMismatch('host must be a string')
        self.port = kwargs.get('port', 2775)
        if not isinstance(self.port, int):
            raise TypeMismatch('port must be an integer')
        self.username = kwargs.get('username', 'smppclient')
        self.password = kwargs.get('password', 'password')
        self.systemType = kwargs.get('systemType', '')
        self.log_file = kwargs.get('log_file', '/var/log/jasmin/default-%s.log' % self.id)
        self.log_level = kwargs.get('log_level', logging.INFO)
        self.log_format = kwargs.get('log_format', '%(asctime)s %(levelname)-8s %(process)d %(message)s')
        self.log_date_format = kwargs.get('log_dateformat', '%Y-%m-%d %H:%M:%S')
        
        # Timeout for response to bind request
        self.sessionInitTimerSecs = kwargs.get('sessionInitTimerSecs', 30)
        
        # Enquire link interval
        self.enquireLinkTimerSecs = kwargs.get('enquireLinkTimerSecs', 10)
        
        # Maximum time lapse allowed between transactions, after which period
        # of inactivity, the connection is considered as inactive and will reconnect 
        self.inactivityTimerSecs = kwargs.get('inactivityTimerSecs', 300)
        
        # Timeout for responses to any request PDU
        self.responseTimerSecs = kwargs.get('responseTimerSecs', 60)
        
        # Reconnection
        self.reconnectOnConnectionLoss = kwargs.get('reconnectOnConnectionLoss', True)
        self.reconnectOnConnectionFailure = kwargs.get('reconnectOnConnectionFailure', True)
        self.reconnectOnConnectionLossDelay = kwargs.get('reconnectOnConnectionLossDelay', 10)        
        self.reconnectOnConnectionFailureDelay = kwargs.get('reconnectOnConnectionFailureDelay', 10)        
        
        # Timeout for reading a single PDU, this is the maximum lapse of time between
        # receiving PDU's header and its complete read, if the PDU reading timed out,
        # the connection is considered as 'corrupt' and will reconnect
        self.pduReadTimerSecs = kwargs.get('pduReadTimerSecs', 10)
        
        self.useSSL = kwargs.get('useSSL', False)
        self.SSLCertificateFile = kwargs.get('SSLCertificateFile', None)
        
        # Type of bind operation, can be one of these:
        # - transceiver
        # - transmitter
        # - receiver
        self.bindOperation = kwargs.get('bindOperation', 'transceiver')
        
        # These are default parameters, c.f. _setConfigParamsInPDU method in SMPPOperationFactory
        self.service_type = kwargs.get('service_type', None)
        self.bind_addr_ton = kwargs.get('bind_addr_ton', AddrTon.UNKNOWN)
        self.bind_addr_npi = kwargs.get('bind_addr_npi', AddrNpi.ISDN)
        self.source_addr_ton = kwargs.get('source_addr_ton', AddrTon.NATIONAL)
        self.source_addr_npi = kwargs.get('source_addr_npi', AddrNpi.ISDN)
        self.dest_addr_ton = kwargs.get('dest_addr_ton', AddrTon.INTERNATIONAL)
        self.dest_addr_npi = kwargs.get('dest_addr_npi', AddrNpi.ISDN)
        self.address_range = kwargs.get('address_range', None)
        self.source_addr = kwargs.get('source_addr', None)
        self.esm_class = kwargs.get('esm_class', EsmClass(EsmClassMode.STORE_AND_FORWARD, EsmClassType.DEFAULT))
        self.protocol_id = kwargs.get('protocol_id', None)
        self.priority_flag = kwargs.get('priority_flag', PriorityFlag.LEVEL_0)
        self.schedule_delivery_time = kwargs.get('schedule_delivery_time', None)
        self.validity_period = kwargs.get('validity_period', None)
        self.registered_delivery = kwargs.get('registered_delivery', RegisteredDelivery(RegisteredDeliveryReceipt.NO_SMSC_DELIVERY_RECEIPT_REQUESTED))
        self.replace_if_present_flag = kwargs.get('replace_if_present_flag', ReplaceIfPresentFlag.DO_NOT_REPLACE)
        self.sm_default_msg_id = kwargs.get('sm_default_msg_id', 0)

        # 5.2.19 data_coding / c. There is no default setting for the data_coding parameter.
        # Possible values:
        # SMSC_DEFAULT_ALPHABET:     0x00 / 0
        # IA5_ASCII:                 0x01 / 1
        # OCTET_UNSPECIFIED:         0x02 / 2
        # LATIN_1:                   0x03 / 3
        # OCTET_UNSPECIFIED_COMMON:  0x04 / 4
        # JIS:                       0x05 / 5
        # CYRILLIC:                  0x06 / 6
        # ISO_8859_8:                0x07 / 7
        # UCS2:                      0x08 / 8
        # PICTOGRAM:                 0x09 / 9
        # ISO_2022_JP:               0x0a / 10
        # EXTENDED_KANJI_JIS:        0x0d / 13
        # KS_C_5601:                 0x0e / 14
        self.data_coding = 0x0

        # These were added to preserve compatibility with smpp.twisted project
        self.addressTon = self.bind_addr_ton
        self.addressNpi = self.bind_addr_npi
        self.addressRange = self.address_range
        
        # QoS
        # Rejected messages are requeued with a fixed delay
        self.requeue_delay = kwargs.get('requeue_delay', 120)
        self.submit_sm_throughput = 1
        
        # DLR
        self.dlr_expiry = kwargs.get('dlr_expiry', 86400)
        
        # Long message splitting
        self.longContentMaxParts = kwargs.get('longContentMaxParts', 5)
        if not isinstance(self.longContentMaxParts, int):
            raise TypeMismatch('longContentMaxParts must be an integer')
        self.longContentSplit = kwargs.get('longContentSplit', 'sar')
        if self.longContentSplit not in ['sar', 'udh']:
            raise InvalidValue("longContentSplit must be 'sar' or 'udh'")
        
class SMPPClientServiceConfig(ConfigFile):
    def __init__(self, config_file):
        ConfigFile.__init__(self, config_file)
        
        self.log_level = logging.getLevelName(self._get('service-smppclient', 'log_level', 'INFO'))
        self.log_file = self._get('services-smppclient', 'log_file', '/var/log/jasmin/service-smppclient.log')
        self.log_format = self._get('services-smppclient', 'log_format', '%(asctime)s %(levelname)-8s %(process)d %(message)s')
        self.log_date_format = self._get('services-smppclient', 'log_date_format', '%Y-%m-%d %H:%M:%S')
