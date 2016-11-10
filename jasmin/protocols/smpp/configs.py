import logging
import os
import re

from jasmin.config.tools import ConfigFile
from jasmin.vendor.smpp.pdu.pdu_types import (EsmClass, EsmClassMode, EsmClassType,
                                              RegisteredDelivery, RegisteredDeliveryReceipt,
                                              AddrTon, AddrNpi,
                                              PriorityFlag, ReplaceIfPresentFlag)

# Related to travis-ci builds
ROOT_PATH = os.getenv('ROOT_PATH', '/')


class ConfigUndefinedIdError(Exception):
    """Raised when a *Config class is initialized without ID
    """


class ConfigInvalidIdError(Exception):
    """Raised when a *Config class is initialized with an invalid ID syntax
    """


class TypeMismatch(Exception):
    """Raised when a *Config element has not a valid type
    """


class UnknownValue(Exception):
    """Raised when a *Config element has a valid type and inappropriate value
    """


class SMPPClientConfig(object):

    def __init__(self, **kwargs):
        #####################
        # Generic configuration block

        # cid validation
        if kwargs.get('id', None) == None:
            raise ConfigUndefinedIdError('SMPPConfig must have an id')
        idcheck = re.compile(r'^[A-Za-z0-9_-]{3,25}$')
        if idcheck.match(str(kwargs.get('id'))) == None:
            raise ConfigInvalidIdError('SMPPConfig id syntax is invalid')

        self.id = str(kwargs.get('id'))

        self.port = kwargs.get('port', 2775)
        if not isinstance(self.port, int):
            raise TypeMismatch('port must be an integer')

        # Logging configuration
        self.log_file = kwargs.get('log_file', '%s/var/log/jasmin/default-%s.log' % (ROOT_PATH, self.id))
        self.log_rotate = kwargs.get('log_rotate', 'midnight')
        self.log_level = kwargs.get('log_level', logging.INFO)
        self.log_format = kwargs.get('log_format', '%(asctime)s %(levelname)-8s %(process)d %(message)s')
        self.log_date_format = kwargs.get('log_dateformat', '%Y-%m-%d %H:%M:%S')

        # Timeout for response to bind request
        self.sessionInitTimerSecs = kwargs.get('sessionInitTimerSecs', 30)
        if (not isinstance(self.sessionInitTimerSecs, int)
                and not isinstance(self.sessionInitTimerSecs, float)):
            raise TypeMismatch('sessionInitTimerSecs must be an integer or float')

        # Enquire link interval
        self.enquireLinkTimerSecs = kwargs.get('enquireLinkTimerSecs', 30)
        if (not isinstance(self.enquireLinkTimerSecs, int)
                and not isinstance(self.enquireLinkTimerSecs, float)):
            raise TypeMismatch('enquireLinkTimerSecs must be an integer or float')

        # Maximum time lapse allowed between transactions, after which,
        # the connection is considered as inactive and will reconnect
        self.inactivityTimerSecs = kwargs.get('inactivityTimerSecs', 300)
        if not isinstance(self.inactivityTimerSecs, int) and not isinstance(self.inactivityTimerSecs, float):
            raise TypeMismatch('inactivityTimerSecs must be an integer or float')

        # Timeout for responses to any request PDU
        self.responseTimerSecs = kwargs.get('responseTimerSecs', 120)
        if not isinstance(self.responseTimerSecs, int) and not isinstance(self.responseTimerSecs, float):
            raise TypeMismatch('responseTimerSecs must be an integer or float')

        # Timeout for reading a single PDU, this is the maximum lapse of time between
        # receiving PDU's header and its complete read, if the PDU reading timed out,
        # the connection is considered as 'corrupt' and will reconnect
        self.pduReadTimerSecs = kwargs.get('pduReadTimerSecs', 10)
        if not isinstance(self.pduReadTimerSecs, int) and not isinstance(self.pduReadTimerSecs, float):
            raise TypeMismatch('pduReadTimerSecs must be an integer or float')

        # DLR
        # How much time a message is kept in redis waiting for receipt
        self.dlr_expiry = kwargs.get('dlr_expiry', 86400)
        if not isinstance(self.dlr_expiry, int) and not isinstance(self.dlr_expiry, float):
            raise TypeMismatch('dlr_expiry must be an integer or float')

        #####################
        # SMPPClient Specific configuration block
        self.host = kwargs.get('host', '127.0.0.1')
        if not isinstance(self.host, str):
            raise TypeMismatch('host must be a string')
        self.username = kwargs.get('username', 'smppclient')
        if len(self.username) > 15:
            raise TypeMismatch('username is longer than allowed size (15)')
        self.password = kwargs.get('password', 'password')
        if len(self.password) > 8:
            raise TypeMismatch('password is longer than allowed size (8)')
        self.systemType = kwargs.get('systemType', '')

        # Reconnection
        self.reconnectOnConnectionLoss = kwargs.get('reconnectOnConnectionLoss', True)
        if not isinstance(self.reconnectOnConnectionLoss, bool):
            raise TypeMismatch('reconnectOnConnectionLoss must be a boolean')
        self.reconnectOnConnectionFailure = kwargs.get('reconnectOnConnectionFailure', True)
        if not isinstance(self.reconnectOnConnectionFailure, bool):
            raise TypeMismatch('reconnectOnConnectionFailure must be a boolean')
        self.reconnectOnConnectionLossDelay = kwargs.get('reconnectOnConnectionLossDelay', 10)
        if (not isinstance(self.reconnectOnConnectionLossDelay, int)
                and not isinstance(self.reconnectOnConnectionLossDelay, float)):
            raise TypeMismatch('reconnectOnConnectionLossDelay must be an integer or float')
        self.reconnectOnConnectionFailureDelay = kwargs.get('reconnectOnConnectionFailureDelay', 10)
        if (not isinstance(self.reconnectOnConnectionFailureDelay, int)
                and not isinstance(self.reconnectOnConnectionFailureDelay, float)):
            raise TypeMismatch('reconnectOnConnectionFailureDelay must be an integer or float')

        self.useSSL = kwargs.get('useSSL', False)
        self.SSLCertificateFile = kwargs.get('SSLCertificateFile', None)

        # Type of bind operation, can be one of these:
        # - transceiver
        # - transmitter
        # - receiver
        self.bindOperation = kwargs.get('bindOperation', 'transceiver')
        if self.bindOperation not in ['transceiver', 'transmitter', 'receiver']:
            raise UnknownValue('Invalid bindOperation: %s' % self.bindOperation)

        # These are default parameters, c.f. _setConfigParamsInPDU method in SMPPOperationFactory
        self.service_type = kwargs.get('service_type', None)
        self.addressTon = kwargs.get('addressTon', AddrTon.UNKNOWN)
        self.addressNpi = kwargs.get('addressNpi', AddrNpi.UNKNOWN)
        self.source_addr_ton = kwargs.get('source_addr_ton', AddrTon.NATIONAL)
        self.source_addr_npi = kwargs.get('source_addr_npi', AddrNpi.ISDN)
        self.dest_addr_ton = kwargs.get('dest_addr_ton', AddrTon.INTERNATIONAL)
        self.dest_addr_npi = kwargs.get('dest_addr_npi', AddrNpi.ISDN)
        self.addressRange = kwargs.get('addressRange', None)
        self.source_addr = kwargs.get('source_addr', None)
        self.esm_class = kwargs.get('esm_class',
                                    EsmClass(EsmClassMode.STORE_AND_FORWARD, EsmClassType.DEFAULT))
        self.protocol_id = kwargs.get('protocol_id', None)
        self.priority_flag = kwargs.get('priority_flag', PriorityFlag.LEVEL_0)
        self.schedule_delivery_time = kwargs.get('schedule_delivery_time', None)
        self.validity_period = kwargs.get('validity_period', None)
        self.registered_delivery = kwargs.get(
            'registered_delivery',
            RegisteredDelivery(RegisteredDeliveryReceipt.NO_SMSC_DELIVERY_RECEIPT_REQUESTED))
        self.replace_if_present_flag = kwargs.get(
            'replace_if_present_flag', ReplaceIfPresentFlag.DO_NOT_REPLACE)
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
        self.data_coding = kwargs.get('data_coding', 0)
        if self.data_coding not in [0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 13, 14]:
            raise UnknownValue('Invalid data_coding: %s' % self.data_coding)

        # QoS
        # Rejected messages are requeued with a fixed delay
        self.requeue_delay = kwargs.get('requeue_delay', 120)
        if not isinstance(self.requeue_delay, int) and not isinstance(self.requeue_delay, float):
            raise TypeMismatch('requeue_delay must be an integer or float')
        self.submit_sm_throughput = kwargs.get('submit_sm_throughput', 1)
        if (not isinstance(self.submit_sm_throughput, int)
                and not isinstance(self.submit_sm_throughput, float)):
            raise TypeMismatch('submit_sm_throughput must be an integer or float')

        # DLR Message id bases from submit_sm_resp to deliver_sm, possible values:
        # [0] (default) : submit_sm_resp and deliver_sm messages IDs are on the same base.
        # [1]           : submit_sm_resp msg-id is in hexadecimal base, deliver_sm msg-id is in
        #                 decimal base.
        # [2]           : submit_sm_resp msg-id is in decimal base, deliver_sm msg-id is in
        #                 hexadecimal base.
        self.dlr_msg_id_bases = kwargs.get('dlr_msg_id_bases', 0)
        if self.dlr_msg_id_bases not in [0, 1, 2]:
            raise UnknownValue('Invalid dlr_msg_id_bases: %s' % self.dlr_msg_id_bases)


class SMPPClientServiceConfig(ConfigFile):
    def __init__(self, config_file):
        ConfigFile.__init__(self, config_file)

        self.log_level = logging.getLevelName(self._get('service-smppclient', 'log_level', 'INFO'))
        self.log_file = self._get(
            'service-smppclient', 'log_file', '%s/var/log/jasmin/service-smppclient.log' % ROOT_PATH)
        self.log_rotate = self._get('service-smppclient', 'log_rotate', 'W6')
        self.log_format = self._get(
            'service-smppclient', 'log_format', '%(asctime)s %(levelname)-8s %(process)d %(message)s')
        self.log_date_format = self._get('service-smppclient', 'log_date_format', '%Y-%m-%d %H:%M:%S')


class SMPPServerConfig(ConfigFile):
    def __init__(self, config_file=None):
        ConfigFile.__init__(self, config_file)

        self.id = self._get('smpp-server', 'id', 'smpps_01')

        self.bind = self._get('smpp-server', 'bind', '0.0.0.0')
        self.port = self._getint('smpp-server', 'port', 2775)

        # Logging
        self.log_level = logging.getLevelName(self._get('smpp-server', 'log_level', 'INFO'))
        self.log_file = self._get(
            'smpp-server', 'log_file', '%s/var/log/jasmin/default-%s.log' % (ROOT_PATH, self.id))
        self.log_rotate = self._get('smpp-server', 'log_rotate', 'midnight')
        self.log_format = self._get(
            'smpp-server', 'log_format', '%(asctime)s %(levelname)-8s %(process)d %(message)s')
        self.log_date_format = self._get('smpp-server', 'log_date_format', '%Y-%m-%d %H:%M:%S')

        # Timeout for response to bind request
        self.sessionInitTimerSecs = self._getint('smpp-server', 'sessionInitTimerSecs', 30)

        # Enquire link interval
        self.enquireLinkTimerSecs = self._getint('smpp-server', 'enquireLinkTimerSecs', 30)

        # Maximum time lapse allowed between transactions, after which,
        # the connection is considered as inactive
        self.inactivityTimerSecs = self._getint('smpp-server', 'inactivityTimerSecs', 300)

        # Timeout for responses to any request PDU
        self.responseTimerSecs = self._getint('smpp-server', 'responseTimerSecs', 60)

        # Timeout for reading a single PDU, this is the maximum lapse of time between
        # receiving PDU's header and its complete read, if the PDU reading timed out,
        # the connection is considered as 'corrupt' and will reconnect
        self.pduReadTimerSecs = self._getint('smpp-server', 'pduReadTimerSecs', 10)

        # DLR
        # How much time a message is kept in redis waiting for receipt
        self.dlr_expiry = self._getint('smpp-server', 'dlr_expiry', 86400)


class SMPPServerPBConfig(ConfigFile):
    """Config handler for 'smpp-server-pb' section"""

    def __init__(self, config_file=None):
        ConfigFile.__init__(self, config_file)

        self.bind = self._get('smpp-server-pb', 'bind', '0.0.0.0')
        self.port = self._getint('smpp-server-pb', 'port', 14000)

        self.authentication = self._getbool('smpp-server-pb', 'authentication', True)
        self.admin_username = self._get('smpp-server-pb', 'admin_username', 'smppsadmin')
        self.admin_password = self._get(
            'smpp-server-pb', 'admin_password', "e97ab122faa16beea8682d84f3d2eea4").decode('hex')

        # Logging
        self.log_level = logging.getLevelName(self._get('smpp-server-pb', 'log_level', 'INFO'))
        self.log_rotate = self._get('smpp-server-pb', 'log_rotate', 'W6')
        self.log_file = self._get('smpp-server-pb', 'log_file', '%s/var/log/jasmin/smpp-server-pb.log' % ROOT_PATH)
        self.log_format = self._get(
            'smpp-server-pb', 'log_format', '%(asctime)s %(levelname)-8s %(process)d %(message)s')
        self.log_date_format = self._get('smpp-server-pb', 'log_date_format', '%Y-%m-%d %H:%M:%S')


class SMPPServerPBClientConfig(ConfigFile):
    """Config handler for 'smpp-server-pb-client' section"""

    def __init__(self, config_file=None):
        ConfigFile.__init__(self, config_file)

        self.host = self._get('smpp-server-pb-client', 'host', '127.0.0.1')
        self.port = self._getint('smpp-server-pb-client', 'port', 14000)

        self.username = self._get('smpp-server-pb-client', 'username', 'smppsadmin')
        self.password = self._get('smpp-server-pb-client', 'password', 'smppspwd')
