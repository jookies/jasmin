class HttpApiError(Exception):
    def __init__(self, code, message = None):
        Exception.__init__(self)
        self.message = message
        self.code = code
    
    def __str__(self):
        return '%s: %s (%s)' % (self.code, self.__class__.__name__, self.message)
    
class UrlArgsValidationError(HttpApiError):
    """
    Raised when url validation fails  (jasmin.protocols.http.validation.UrlArgsValidator)
    """
    def __init__(self, message):
        HttpApiError.__init__(self, 400, message)

class CredentialValidationError(HttpApiError):
    """
    Raised when user credential validation fails  (jasmin.protocols.http.validation.HttpAPICredentialValidator)
    """
    def __init__(self, message):
        HttpApiError.__init__(self, 400, message)

class ServerError(HttpApiError):
    """
    Raised on any occuring error inside HTTP Server
    """
    def __init__(self, message = None):
        HttpApiError.__init__(self, 500, message)

class AuthenticationError(HttpApiError):
    """
    Raised on authentication error
    """
    def __init__(self, message = None):
        HttpApiError.__init__(self, 403, message)
        
class RouteNotFoundError(HttpApiError):
    """
    Raised when no routes found for a given Routable
    """
    def __init__(self, message = None):
        HttpApiError.__init__(self, 412, message)

class ChargingError(HttpApiError):
    """
    Raised on any occuring error while charging user
    """
    def __init__(self, message = None):
        HttpApiError.__init__(self, 403, message)

class ThroughputExceededError(HttpApiError):
    """
    Raised when throughput is exceeded
    """
    def __init__(self, message = None):
        HttpApiError.__init__(self, 403, message)