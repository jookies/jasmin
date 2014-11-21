from datetime import datetime
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

class SmppsRealm(object):
    implements(portal.IRealm)

    def __init__(self, smpps_id, router_factory):
        self.smpps_id = smpps_id
        self.router_factory = router_factory

    def on_connection_hook(self, user):
        # Update CnxStatus
        user.smpps.smpps_binds_count+= 1
        user.smpps.smpps_bound+= 1
        user.smpps.smpps_last_activity_at = datetime.now()
        # Inform router to persist User objects
        self.router_factory.persistenceState['users'] = False

    def on_disconnection_hook(self, user):
        # Update CnxStatus
        user.smpps.smpps_binds_count-= 1
        user.smpps.smpps_bound-= 1
        user.smpps.smpps_last_activity_at = datetime.now()
        # Inform router to persist User objects
        self.router_factory.persistenceState['users'] = False
    
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
            self.on_connection_hook(user)
            return ('SMPPs', user, lambda u=user:self.on_disconnection_hook(u))