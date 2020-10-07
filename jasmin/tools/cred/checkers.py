from zope.interface import implementer
from twisted.cred import checkers, credentials, error as credError
from twisted.internet import defer


@implementer(checkers.ICredentialsChecker)
class RouterAuthChecker:
    """Will authenticate users with router_factory.authenticateUser()"""

    credentialInterfaces = (credentials.IUsernamePassword,)

    def __init__(self, router_factory):
        self.router_factory = router_factory

    def requestAvatarId(self, creds):
        user = self.router_factory.authenticateUser(
            creds.username,
            creds.password)

        # Username / Password correct ?
        if user is not None:
            return defer.succeed(creds.username)
        else:
            return defer.fail(
                credError.UnauthorizedLogin("Authentication credentials invalid"))
