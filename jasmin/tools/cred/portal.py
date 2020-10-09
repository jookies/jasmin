from zope.interface import implementer
from twisted.spread import pb
from twisted.cred import portal


@implementer(portal.IRealm)
class JasminPBRealm:

    def __init__(self, PBFactory):
        self.PBFactory = PBFactory

    def requestAvatar(self, avatarId, mind, *interfaces):
        if pb.IPerspective not in interfaces:
            raise NotImplementedError

        self.PBFactory.setAvatar(avatarId)
        return pb.IPerspective, self.PBFactory, lambda: None


@implementer(portal.IRealm)
class SmppsRealm:

    def __init__(self, smpps_id, router_factory):
        self.smpps_id = smpps_id
        self.router_factory = router_factory

    def requestAvatar(self, avatarId, mind, *interfaces):
        user = None
        # Lookout for user from router
        for _user in self.router_factory.users:
            if avatarId == _user.username:
                user = _user
                break

        if user is None:
            return ('SMPPs', None, lambda: None)
        else:
            return ('SMPPs', user, lambda: None)
