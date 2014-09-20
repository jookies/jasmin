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
    
    def setAuthorization(self, key, value):
        if key in self.authorizations:
            self.authorizations[key] = value
        else:
            raise jasminApiCredentialError('%s is not a valid Authorization' % key)

    def getAuthorization(self, key):
        if key in self.authorizations:
            return self.authorizations[key]
        else:
            raise jasminApiCredentialError('%s is not a valid Authorization' % key)

    def setValueFilter(self, key, value):
        if key in self.value_filters:
            self.value_filters[key] = value
        else:
            raise jasminApiCredentialError('%s is not a valid Filter' % key)

    def getValueFilter(self, key):
        if key in self.value_filters:
            return self.value_filters[key]
        else:
            raise jasminApiCredentialError('%s is not a valid Filter' % key)

    def setDefaultValue(self, key, value):
        if key in self.defaults:
            self.defaults[key] = value
        else:
            raise jasminApiCredentialError('%s is not a valid Default value' % key)

    def getDefaultValue(self, key):
        if key in self.defaults:
            return self.defaults[key]
        else:
            raise jasminApiCredentialError('%s is not a valid Default value' % key)

    def setQuota(self, key, value):
        if key in self.quotas:
            self.quotas[key] = value
        else:
            raise jasminApiCredentialError('%s is not a valid Quata key' % key)

    def getQuota(self, key):
        if key in self.quotas:
            return self.quotas[key]
        else:
            raise jasminApiCredentialError('%s is not a valid Quota key' % key)

class MoMessagingCredential(CredentialGenerick):
    """Credential set for receiving MO messages"""
    
    authorizations = {}
        
    quotas = {}

    def __init__(self):
        self.authorizations = {'receive': True,}
            
        self.quotas = {'deliver_sm_count': None}

class MtMessagingCredential(CredentialGenerick):
    """Credential set for sending MT Messages"""
    
    authorizations = {}
    value_filters = {}
    defaults = {}
    quotas = {}
    
    def __init__(self):
        self.authorizations = {'send': True,
                          'long_content': True,
                          'set_dlr_level': True,
                          'set_dlr_method': True,
                          'set_source_address': True,
                          'set_priority': True,
                         }
        
        self.value_filters = {'destination_address': r'.*',
                         'source_address': r'.*',
                         'priority': r'^[0-3]$',
                         'content': r'.*'
                         }
        
        self.defaults = {'source_address': None,}
        
        self.quotas = {'balance': None, 'early_decrement_balance_percent': None, 'submit_sm_count': None}

class Group(jasminApiGenerick):
    """Every user must have a group"""
    
    def __init__(self, gid, mo_credential = None, mt_credential = None):
        self.gid = gid
        
        self.mo_credential = mo_credential
        self.mt_credential = mt_credential
        
        # When not set, mo and mt credentials are set to defaults
        if self.mo_credential is None:
            self.mo_credential = MoMessagingCredential()
        if self.mt_credential is None:
            self.mt_credential = MtMessagingCredential()

    def __str__(self):
        return str(self.gid)

class User(jasminApiGenerick):
    """Jasmin user"""
    
    def __init__(self, uid, group, username, password, mo_credential = None, mt_credential = None):
        self.uid = uid
        self.group = group
        self.username = username
        if type(password) == str:
            self.password = md5(password).digest()
        else:
            self.password = password

        self.mo_credential = mo_credential
        self.mt_credential = mt_credential
    
    def __str__(self):
        return self.username

    def getMOAuthorization(self, key):
        """Will return Authorization boolean for key
        """
        value = None
        if self.mo_credential is not None:
            value = self.mo_credential.getAuthorization(key)
        
        # If value is None (not set), we check if it is set in group wide context
        if value is None:
            return self.group.mo_credential.getAuthorization(key)
        else:
            return value

    def getMTAuthorization(self, key):
        """Will return Authorization boolean for key
        """
        value = None
        if self.mt_credential is not None:
            value = self.mt_credential.getAuthorization(key)
        
        # If value is None (not set), we check if it is set in group wide context
        if value is None:
            return self.group.mt_credential.getAuthorization(key)
        else:
            return value

    def getMOValueFilter(self, key):
        """Will return Value filter for key
        """
        value = None
        if self.mo_credential is not None:
            value = self.mo_credential.getValueFilter(key)
        
        # If value is None (not set), we check if it is set in group wide context
        if value is None:
            return self.group.mo_credential.getValueFilter(key)
        else:
            return value

    def getMTValueFilter(self, key):
        """Will return Value filter for key
        """
        value = None
        if self.mt_credential is not None:
            value = self.mt_credential.getValueFilter(key)
        
        # If value is None (not set), we check if it is set in group wide context
        if value is None:
            return self.group.mt_credential.getValueFilter(key)
        else:
            return value

    def getMODefault(self, key):
        """Will return Default value for key
        """
        value = None
        if self.mo_credential is not None:
            value = self.mo_credential.getDefaultValue(key)
        
        # If value is None (not set), we check if it is set in group wide context
        if value is None:
            return self.group.mo_credential.getDefaultValue(key)
        else:
            return value

    def getMTDefault(self, key):
        """Will return Default value for key
        """
        value = None
        if self.mt_credential is not None:
            value = self.mt_credential.getDefaultValue(key)
        
        # If value is None (not set), we check if it is set in group wide context
        if value is None:
            return self.group.mt_credential.getDefaultValue(key)
        else:
            return value

    def getMOQuota(self, key):
        """Will return Quota for key
        """
        value = None
        if self.mo_credential is not None:
            value = self.mo_credential.getQuota(key)
        
        # If value is None (not set), we check if it is set in group wide context
        if value is None:
            return self.group.mo_credential.getQuota(key)
        else:
            return value

    def getMTQuota(self, key):
        """Will return Quota for key
        """
        value = None
        if self.mt_credential is not None:
            value = self.mt_credential.getQuota(key)
        
        # If value is None (not set), we check if it is set in group wide context
        if value is None:
            return self.group.mt_credential.getQuota(key)
        else:
            return value

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