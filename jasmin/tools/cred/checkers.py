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
            return defer.succeed(credentials.username)
        else:
            return defer.fail(
                credError.UnauthorizedLogin("Authentication credentials invalid"))