from hashlib import md5
from zope.interface import implements
from twisted.spread.pb import challenge, IJellyable, AsReferenceable, IPerspective
from twisted.cred.credentials import IUsernameHashedPassword, Anonymous
from twisted.spread.flavors import IPBRoot, Referenceable
from twisted.cred.error import UnhandledCredentials, UnauthorizedLogin

class _JellyableAvatarMixin(object):
    """
    Helper class for code which deals with avatars which PB must be capable of
    sending to a peer.
    """
    def _cb_login(self, (interface, avatar, logout)):
        """
        Ensure that the avatar to be returned to the client is jellyable and
        set up disconnection notification to call the realm's logout object.
        """
        if not IJellyable.providedBy(avatar):
            avatar = AsReferenceable(avatar, "perspective")

        puid = avatar.processUniqueID()

        # only call logout once, whether the connection is dropped (disconnect)
        # or a logout occurs (cleanup), and be careful to drop the reference to
        # it in either case
        logout = [logout]
        def maybeLogout():
            if not logout:
                return
            fn = logout[0]
            del logout[0]
            fn()
        self.broker._localCleanup[puid] = maybeLogout
        self.broker.notifyOnDisconnect(maybeLogout)

        return avatar

    def _login_error(self, err, username='Anonymous'):
        if err.type == UnhandledCredentials:
            if str(err.value) == 'No checker for twisted.cred.credentials.IAnonymous':
                self.log.info('Anonymous connection is not authorized !')
                return False, 'Anonymous connection is not authorized !'
            else:
                self.log.info('Authentication error: %s', username)
                return False, 'Authentication error: %s' % username
        elif err.type == UnauthorizedLogin:
            self.log.info('Authentication error %s', username)
            return False, 'Authentication error %s' % username
        else:
            # Fallback solution when err is not known
            self.log.error('Unknown authentication error: %s', err)
            return False, 'Unknown authentication error: %s' % err

class _PortalAuthVerifier(Referenceable, _JellyableAvatarMixin):
    """
    Called with response to verify received password (self.response) with
    the saved md5 digested password.

    This is slightly different from twisted.spread.pb._PortalAuthChallenger in a way
    where the checker is holding md5 digest passwords (no plaintext passwords on server
    side).
    """
    implements(IUsernameHashedPassword)

    def __init__(self, portal, broker, username, _challenge):
        self.portal = portal
        self.broker = broker
        self.username = username
        self.challenge = _challenge

        # Will use the PBFactory's logger
        self.log = self.portal.realm.PBFactory.log

    def remote_respond(self, response, mind):
        self.response = response
        d = self.portal.login(self, mind, IPerspective)
        d.addCallback(self._cb_login)
        d.addErrback(self._login_error, self.username)
        return d

    def checkPassword(self, md5password):
        md = md5()
        md.update(md5password)
        md.update(self.challenge)
        correct = md.digest()
        return self.response == correct

class _PortalWrapper(Referenceable, _JellyableAvatarMixin):
    """
    Root Referenceable object, used to login to portal.
    """

    def __init__(self, portal, broker):
        self.portal = portal
        self.broker = broker

        # Will use the PBFactory's logger
        self.log = self.portal.realm.PBFactory.log


    def remote_login(self, username):
        """
        Start of username/password login.
        """
        c = challenge()
        return c, _PortalAuthVerifier(self.portal, self.broker, username, c)

    def remote_loginAnonymous(self, mind):
        """
        Attempt an anonymous login.

        @param mind: An object to use as the mind parameter to the portal login
            call (possibly None).

        @rtype: L{Deferred}
        @return: A Deferred which will be called back with an avatar when login
            succeeds or which will be errbacked if login fails somehow.
        """
        d = self.portal.login(Anonymous(), mind, IPerspective)
        d.addCallback(self._cb_login)
        d.addErrback(self._login_error)
        return d

class JasminPBPortalRoot(object):
    implements(IPBRoot)

    def __init__(self, portal):
        self.portal = portal

    def rootObject(self, broker):
        return _PortalWrapper(self.portal, broker)
