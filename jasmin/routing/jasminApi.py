"""
A set of objects used by Jasmin to manage users, groups and connectors in memory (no database storage)
"""

import re
from hashlib import md5
from jasmin.tools.singleton import Singleton

class jasminApiInvalidParamError(Exception):
    """Raised when trying to instanciate a jasminApi object with invalid parameter
    """

class jasminApiCredentialError(Exception):
    """Raised for ant Credential-related error
    """

class jasminApiGenerick():
    pass

class CredentialGenerick(jasminApiGenerick):
    """A generick credential object"""
    authorizations = {}
    value_filters = {}
    defaults = {}
    quotas = {}
    quotas_updated = False
    
    def setAuthorization(self, key, value):
        if key not in self.authorizations:
            raise jasminApiCredentialError('%s is not a valid Authorization' % key)
        if type(value) != bool:
            raise jasminApiCredentialError('%s is not a boolean value: %s' % (key, value))
        
        self.authorizations[key] = value

    def getAuthorization(self, key):
        if key not in self.authorizations:
            raise jasminApiCredentialError('%s is not a valid Authorization' % key)
        
        return self.authorizations[key]

    def setValueFilter(self, key, value):
        if key not in self.value_filters:
            raise jasminApiCredentialError('%s is not a valid Filter' % key)

        try:
            self.value_filters[key] = re.compile(value)
        except TypeError:
            raise jasminApiCredentialError('%s is not a regex pattern: %s' % (key, value))

    def getValueFilter(self, key):
        if key not in self.value_filters:
            raise jasminApiCredentialError('%s is not a valid Filter' % key)
        
        return self.value_filters[key]

    def setDefaultValue(self, key, value):
        if key not in self.defaults:
            raise jasminApiCredentialError('%s is not a valid Default value' % key)
        
        self.defaults[key] = value

    def getDefaultValue(self, key):
        if key not in self.defaults:
            raise jasminApiCredentialError('%s is not a valid Default value' % key)
        
        return self.defaults[key]

    def setQuota(self, key, value):
        if key not in self.quotas:
            raise jasminApiCredentialError('%s is not a valid Quata key' % key)
        
        self.quotas[key] = value
    
    def updateQuota(self, key, difference):
        if key not in self.quotas:
            raise jasminApiCredentialError('%s is not a valid Quota key' % key)
        if self.quotas[key] is None:
            raise jasminApiCredentialError('Cannot update a None Quota value for key %s' % key)
        if type(difference) not in [float, int]:
            raise jasminApiCredentialError('Incorrect type for value (%s), must be int or float' % difference)
        if type(self.quotas[key]) == int and type(difference) == float:
            raise jasminApiCredentialError('Type mismatch, cannot update an int with a float value')
            
        self.quotas[key] += difference
        self.quotas_updated = True

    def getQuota(self, key):
        if key not in self.quotas:
            raise jasminApiCredentialError('%s is not a valid Quota key' % key)
        
        return self.quotas[key]

class MtMessagingCredential(CredentialGenerick):
    """Credential set for sending MT Messages through"""
    
    def __init__(self, default_authorizations = True):
        if type(default_authorizations) != bool:
            default_authorizations = False
        
        self.authorizations = {'http_send': default_authorizations,
                          'http_bulk': False,
                          'http_balance': default_authorizations,
                          'http_rate': default_authorizations,
                          'smpps_send': default_authorizations,
                          'http_long_content': default_authorizations,
                          'set_dlr_level': default_authorizations,
                          'http_set_dlr_method': default_authorizations,
                          'set_source_address': default_authorizations,
                          'set_priority': default_authorizations,
                          'set_validity_period': default_authorizations,
                         }
        
        self.value_filters = {'destination_address': re.compile(r'.*'),
                         'source_address': re.compile(r'.*'),
                         'priority': re.compile(r'^[0-3]$'),
                         'validity_period': re.compile(r'^\d+$'),
                         'content': re.compile(r'.*'),
                         }
        
        self.defaults = {'source_address': None,}
        
        self.quotas = {'balance': None, 
                       'early_decrement_balance_percent': None, 
                       'submit_sm_count': None,
                       'http_throughput': None,
                       'smpps_throughput': None,
                       }
    
    def setQuota(self, key, value):
        "Additional validation steps"
        if key == 'balance' and value is not None and ( value < 0 ):
            raise jasminApiCredentialError('%s is not a valid value (%s), it must be None or a positive number' % ( key, value ))
        elif (key == 'early_decrement_balance_percent' and value is not None and 
              ( value < 1 or value > 100 )):
            raise jasminApiCredentialError('%s is not a valid value (%s), it must be None or a number in 1..100' % ( key, value ))
        elif (key == 'submit_sm_count' and value is not None and 
              ( value < 0 or type(value) != int )):
            raise jasminApiCredentialError('%s is not a valid value (%s), it must be a positive int' % ( key, value ))
        elif key in ['http_throughput', 'smpps_throughput'] and value is not None and ( value < 0 ):
            raise jasminApiCredentialError('%s is not a valid value (%s), it must be None or a positive number' % ( key, value ))
            
        CredentialGenerick.setQuota(self, key, value)

class SmppsCredential(CredentialGenerick):
    """Credential set for SMPP Server connection"""
    
    def __init__(self, default_authorizations = True):
        if type(default_authorizations) != bool:
            default_authorizations = False
        
        self.authorizations = {'bind': default_authorizations,}
                
        self.quotas = {'max_bindings': None}
    
    def setQuota(self, key, value):
        "Additional validation steps"
        if key == 'max_bindings' and value is not None and ( value < 0 or type(value) != int ):
            raise jasminApiCredentialError('%s is not a valid value (%s), it must be a positive int' % ( key, value ))
            
        CredentialGenerick.setQuota(self, key, value)

class Group(jasminApiGenerick):
    """Every user must have a group"""
    
    def __init__(self, gid):
        # Validate gid
        regex = re.compile(r'^[A-Za-z0-9_-]{1,16}$')
        if regex.match(str(gid)) == None:
            raise jasminApiInvalidParamError('Group gid syntax is invalid')
        self.gid = gid

    def __str__(self):
        return str(self.gid)

class CnxStatus(jasminApiGenerick):
    """Connection status information holder"""

    def __init__(self):
        self.smpps = {'bind_count': 0,
                      'unbind_count': 0,
                      'bound_connections_count': {
                        'bind_receiver': 0,
                        'bind_transceiver': 0,
                        'bind_transmitter': 0,
                       },
                      'submit_sm_request_count': 0,
                      'last_activity_at': 0,
                      'qos_last_submit_sm_at': 0,
                      'submit_sm_count': 0,
                      'deliver_sm_count': 0,
                      'data_sm_count': 0,
                      'elink_count': 0,
                      'throttling_error_count': 0,
                      'other_submit_error_count': 0,
                      }

        self.httpapi = {'connects_count': 0,
                        'last_activity_at': 0,
                        'submit_sm_request_count': 0,
                        'balance_request_count': 0,
                        'rate_request_count': 0,
                        'qos_last_submit_sm_at': 0,
                        }

class UserStats:
    "User statistics singleton holder"
    __metaclass__ = Singleton
    users = {}

    def get(self, uid):
        "Return a user stats dic or create a new one"
        if uid not in self.users:
            self.users[uid] = {'cnx': CnxStatus()}
        
        return self.users[uid]

    def set(self, uid, stats):
        self.users[uid] = stats

class User(jasminApiGenerick):
    """Jasmin user"""
    
    def __init__(self, uid, group, username, password, mt_credential = None, smpps_credential = None):
        # Validate uid
        regex = re.compile(r'^[A-Za-z0-9_-]{1,16}$')
        if regex.match(str(uid)) == None:
            raise jasminApiInvalidParamError('User uid syntax is invalid')

        self.uid = uid
        self.group = group

        # Validate username, if needed because User object
        # can be called with a None username for some purposes
        regex = re.compile(r'^[A-Za-z0-9_-]{1,15}$')
        if username is not None and regex.match(username) == None:
            raise jasminApiInvalidParamError('User username syntax is invalid')
        self.username = username
        
        if type(password) == str:
            if len(password) == 0 or len(password) > 8:
                raise jasminApiInvalidParamError('Invalid password length !')
            self.password = md5(password).digest()
        else:
            # Password is already encrypted:
            self.password = password

        # Credentials
        self.mt_credential = mt_credential
        if self.mt_credential is None:
            self.mt_credential = MtMessagingCredential()
        self.smpps_credential = smpps_credential
        if self.smpps_credential is None:
            self.smpps_credential = SmppsCredential()

    def getCnxStatus(self):
        """CnxStatus is a singleton which is not persisted to disk,
        this will resolve the reported issue #207."""
        return UserStats().get(self.uid)['cnx']

    def setCnxStatus(self, status):
        UserStats().set(self.uid, {'cnx': status})

    def __str__(self):
        return self.username

class Connector(jasminApiGenerick):
    """
    This is a generick connector, it's used through its implementations
    """
    
    type = 'generic'
    _str = 'Generick Connector'
    _repr = '<Generick Connector>'
    
    def __init__(self, cid):
        self.cid = cid
        self._str = '%s Connector' % self.type
        self._repr = '<%s Connector>' % self.type
        
    def __repr__(self):
        return self._repr
    def __str__(self):
        return self._str
        
class HttpConnector(Connector):
    """This is a HTTP Client connector used to throw router SMS MOs"""
    
    type = 'http'

    def __init__(self, cid, baseurl, method = 'GET'):
        # Validate cid
        regex = re.compile(r'^[A-Za-z0-9_-]{3,25}$')
        if regex.match(str(cid)) == None:
            raise jasminApiInvalidParamError('HttpConnector cid syntax is invalid')
        # Validate method
        if method.lower() not in ['get', 'post']:
            raise jasminApiInvalidParamError('HttpConnector method syntax is invalid, must be GET or POST')
        # Validate baseurl
        regex = re.compile(
                           #r'^(?:http|ftp)s?://' # http:// or https://
                           r'^(?:http)s?://' # http:// or https://
                           r'(?:(?:[A-Z0-9](?:[A-Z0-9-]{0,61}[A-Z0-9])?\.)+(?:[A-Z]{2,6}\.?|[A-Z0-9-]{2,}\.?)|' # domain...
                           r'localhost|' # localhost...
                           r'\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}|' # ...or ipv4
                           r'\[?[A-F0-9]*:[A-F0-9:]+\]?)' # ...or ipv6
                           r'(?::\d+)?' # optional port
                           r'(?:/?|[/?]\S+)$', re.IGNORECASE)
        if regex.match(baseurl) == None:
            raise jasminApiInvalidParamError('HttpConnector url syntax is invalid')

        Connector.__init__(self, cid)
        self.baseurl = baseurl
        self.method = method
        
        self._repr = '<%s (cid=%s, baseurl=%s, method=%s)>' % (self.__class__.__name__, 
                                                               self.cid, 
                                                               self.baseurl, 
                                                               self.method)
        self._str = '%s:\ncid = %s\nbaseurl = %s\nmethod = %s' % (self.__class__.__name__, 
                                                                  self.cid, 
                                                                  self.baseurl, 
                                                                  self.method)
        
class SmppClientConnector(Connector):
    """This is a SMPP Client connector"""
    
    type = 'smppc'
    
    def __init__(self, cid):
        Connector.__init__(self, cid)

class SmppServerSystemIdConnector(Connector):
    """This is a SMPP Server connector mapped to a system_id, it is used to deliver Messages
    through the SMPP server to a bound system_id (receiver or transceiver)"""
    
    type = 'smpps'
    
    def __init__(self, system_id):
        Connector.__init__(self, system_id)

        self.system_id = system_id