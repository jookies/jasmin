"""
More info: http://docs.jasminsms.com/en/latest/interception/index.html
"""

from jasmin.routing.Interceptors import Interceptor
from jasmin.routing.Routables import Routable

class InvalidInterceptionTableParameterError(Exception):
    """Raised when a parameter is not an instance of a desired class (used for
    validating inputs
    """

class InterceptionTable(object):
    """Generic Interception table
    """
    type = 'generic'

    def __init__(self):
        self.table = []

    def add(self, interceptor, order):
        if not isinstance(interceptor, Interceptor):
            raise InvalidInterceptionTableParameterError("interceptor is not an instance of Interceptor")
        if not isinstance(order, int):
            raise InvalidInterceptionTableParameterError("order is not an integer")

        # Ensure script type is correct for given interceptor
        if self.type == 'mo' and interceptor.script.type != 'moi':
            raise InvalidInterceptionTableParameterError("Script type '%s' is not valid for MO Interceptor",
                                                         interceptor.script.type)
        elif self.type == 'mt' and interceptor.script.type != 'mti':
            raise InvalidInterceptionTableParameterError("Script type '%s' is not valid for MT Interceptor",
                                                         interceptor.script.type)

        if order < 0:
            raise InvalidInterceptionTableParameterError("order must be 0 (default interceptor) or greater")
        if order != 0 and interceptor.type != self.type:
            raise InvalidInterceptionTableParameterError("interceptor must be of type '%s', '%s' was given",
                                                         self.type, interceptor.type)
        if order == 0 and interceptor.type != 'default':
            raise InvalidInterceptionTableParameterError(
                "interceptor with order=0 must be a DefaultInterceptor")

        # Replace older interceptors with the same given order
        self.remove(order)

        self.table.append({order: interceptor})
        self.table.sort(reverse=True)

    def remove(self, order):
        for r in self.table:
            if r.keys()[0] == order:
                self.table.remove(r)
                return True

        return False

    def getAll(self):
        return self.table

    def flush(self):
        self.table = []

    def getInterceptorFor(self, routable):
        """This will return the right interceptor to pass the routable to, None returned otherwise
        """

        if not isinstance(routable, Routable):
            raise InvalidInterceptionTableParameterError("routable is not an instance of Routable")

        for r in self.table:
            interceptor = r.values()[0]
            if interceptor.matchFilters(routable):
                return interceptor

        return None

class MTInterceptionTable(InterceptionTable):
    "MT Interception table"
    type = 'mt'

class MOInterceptionTable(InterceptionTable):
    "MO Interception table"
    type = 'mo'
