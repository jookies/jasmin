import re

class jasminApiObject():
    pass

class Group(jasminApiObject):
    def __init__(self, gid):
        self.gid = gid

class User(jasminApiObject):
    def __init__(self, uid, group, username = '', password = ''):
        self.uid = uid
        self.group = group
        self.username = username
        self.password = password

class Connector(jasminApiObject):
    type = 'generic'
    
    def __init__(self, cid):
        self.cid = cid
        
class HttpConnector(Connector):
    def __init__(self, cid, baseurl, method = 'GET'):
        Connector.__init__(self, cid)
        self.type = 'http'
        self.baseurl = baseurl
        self.method = method
        
class SmppConnector(Connector):
    def __init__(self, cid):
        Connector.__init__(self, cid)
        self.type = 'smpp'