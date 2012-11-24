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
    def __init__(self, cid):
        self.cid = cid