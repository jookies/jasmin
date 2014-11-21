from zope.interface import implements
from twisted.cred import checkers, credentials, error as credError
from twisted.internet import defer

class RouterAuthChecker:
    "Will authenticate users with router_factory.authenticateUser()"

    implements(checkers.ICredentialsChecker)
    credentialInterfaces = (credentials.IUsernamePassword,)

    def __init__(self, router_factory):
        self.router_factory = router_factory

    def requestAvatarId(self, credentials):
        username = credentials.username
        user = self.router_factory.authenticateUser(credentials.username,
                                             credentials.password)
        
        # Username / Password correct ?
        if user is not None:
            # Can bind ?
            if not user.smpps_credential.getAuthorization('bind'):
                return defer.fail(
                    credError.UnauthorizedLogin("Binding not authorized"))
            # Still didnt reach max_bindings ?
            elif (user.smpps_credential.getQuota('max_bindings') is not None and 
                user.smpps.smpps_bound >= user.smpps_credential.getQuota('max_bindings')):
                return defer.fail(
                    credError.UnauthorizedLogin("Max bindings limit reached"))

            # Everything is fine:
            return defer.succeed(credentials.username)
        else:
            return defer.fail(
                credError.UnauthorizedLogin("Authentication credentials invalid"))