from zope.interface import implements
from twisted.spread import pb
from twisted.cred import portal

class JasminPBRealm:
    implements(portal.IRealm)

    def __init__(self, PBFactory):
        self.PBFactory = PBFactory
        
    def requestAvatar(self, avatarId, mind, *interfaces):
        if pb.IPerspective not in interfaces:
            raise NotImplementedError
        
        self.PBFactory.setAvatar(avatarId)
        return pb.IPerspective, self.PBFactory, lambda:None