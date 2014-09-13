class HttpApiError(Exception):
    def __init__(self, code, message = None):
        Exception.__init__(self)
        self.message = message
        self.code = code
    
    def __str__(self):
        return '%s: %s (%s)' % (self.code, self.__class__.__name__, self.message)
    
class ValidationError(HttpApiError):
    """
    An exception that is raised when url validation fails 
    """
    def __init__(self, message):
        HttpApiError.__init__(self, 400, message)

class ServerError(HttpApiError):
    def __init__(self, message = None):
        HttpApiError.__init__(self, 500, message)

class AuthenticationError(HttpApiError):
    def __init__(self, message = None):
        HttpApiError.__init__(self, 403, message)
        
class RouteNotFoundError(HttpApiError):
    def __init__(self, message = None):
        HttpApiError.__init__(self, 412, message)