import re
from hashlib import md5

class jasminApiInvalidParamError(Exception):
    """Raised when trying to instanciate a jasminApi object with invalid parameter
    """

class jasminApiObject():
    pass

class Group(jasminApiObject):
    def __init__(self, gid):
        self.gid = gid

class User(jasminApiObject):
    def __init__(self, uid, group, username, password):
        self.uid = uid
        self.group = group
        self.username = username
        if type(password) == str:
            self.password = md5(password).digest()
        else:
            self.password = password

class Connector(jasminApiObject):
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
                                                               self.cid, self.baseurl, 
                                                               self.method)
        self._str = '%s:\ncid = %s\nbaseurl = %s\nmethod = %s' % (self.__class__.__name__, 
                                                                  self.cid, 
                                                                  self.baseurl, 
                                                                  self.method)
        
class SmppClientConnector(Connector):
    type = 'smppc'
    
    def __init__(self, cid):
        Connector.__init__(self, cid)