"""
A set of objects used by Jasmin to manage users, groups and connectors in memory (no database storage)
"""

import re
from hashlib import md5

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
            
        self.quotas[key] += difference
        self.quotas_updated = True

    def getQuota(self, key):
        if key not in self.quotas:
            raise jasminApiCredentialError('%s is not a valid Quota key' % key)
        
        return self.quotas[key]

class MtMessagingCredential(CredentialGenerick):
    """Credential set for sending MT Messages"""
    
    def __init__(self, default_authorizations = True):
        if type(default_authorizations) != bool:
            default_authorizations = False
        
        self.authorizations = {'http_send': default_authorizations,
                          'long_content': default_authorizations,
                          'set_dlr_level': default_authorizations,
                          'set_dlr_method': default_authorizations,
                          'set_source_address': default_authorizations,
                          'set_priority': default_authorizations,
                         }
        
        self.value_filters = {'destination_address': re.compile(r'.*'),
                         'source_address': re.compile(r'.*'),
                         'priority': re.compile(r'^[0-3]$'),
                         'content': re.compile(r'.*'),
                         }
        
        self.defaults = {'source_address': None,}
        
        self.quotas = {'balance': None, 'early_decrement_balance_percent': None, 'submit_sm_count': None}
    
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
            
        CredentialGenerick.setQuota(self, key, value)

class Group(jasminApiGenerick):
    """Every user must have a group"""
    
    def __init__(self, gid):
        self.gid = gid

    def __str__(self):
        return str(self.gid)

class CnxStatus(jasminApiGenerick):
    """Connection status information holder"""
    class smpps:
        # This is a counter of all done binds
        smpps_binds_count = 0
        # This is a place holder to get datetimed bind types,
        # this will include:
        # - datetime
        # - smpps_id
        # - type of bind
        smpps_binds_types = {}
        # How many bound connection actually
        smpps_bound = 0
        # Types of actually bound connections,
        # this will include:
        # - datetime
        # - smpps_id
        # - type of bind
        smpps_bound_types = {}
        # Last smpp activity datetime over all bound connections
        smpps_last_activity_at = None

    class httpapi:
        # This is a counter of all connections
        http_connects_count = 0
        # Last http activity datetime (not including throwing activities)
        http_last_activity_at = None

class User(CnxStatus):
    """Jasmin user"""
    
    def __init__(self, uid, group, username, password, mt_credential = None):
        self.uid = uid
        self.group = group
        self.username = username
        if type(password) == str:
            self.password = md5(password).digest()
        else:
            self.password = password

        self.mt_credential = mt_credential
        if self.mt_credential is None:
            self.mt_credential = MtMessagingCredential()

    def __str__(self):
        return self.username

class Connector(jasminApiGenerick):
    """
    This is a generick connector, it's used through its implementations
    """
    
    type = 'generic'
    _str = '%s Connector' % type
    _repr = '%s Connector>' % type
    
    def __init__(self, cid):
        self.cid = cid
        
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
        if regex.match(cid) == None:
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