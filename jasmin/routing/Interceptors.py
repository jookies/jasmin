#pylint: disable=W0401,W0611,W0231
"""
More info: http://docs.jasminsms.com/en/latest/interception/index.html
"""

from jasmin.routing.Filters import Filter
from jasmin.routing.Routables import Routable
from jasmin.routing.jasminApi import *


class InvalidInterceptorParameterError(Exception):
    """Raised when a parameter is not an instance of a desired class.

    Used for validating inputs
    """
class InvalidInterceptorFilterError(Exception):
    "Raised when an interceptor is instanciated with a non-compatible type"

class Interceptor(object):
    """Generic Interceptor:

    Interceptor contain a couple of [Filter(s), InterceptorScript]
    When more than one Filter is given, matching these filters will use the AND operator
    """
    type = 'generic'
    _str = 'generic'
    filters = []
    script = None

    def __init__(self, filters, script):
        if not isinstance(script, InterceptorScript):
            raise InvalidInterceptorParameterError("script is not an instance of InterceptorScript")
        if not isinstance(filters, list):
            raise InvalidInterceptorParameterError("filters must be a list")
        for _filter in filters:
            if not isinstance(_filter, Filter):
                raise InvalidInterceptorParameterError(
                    "filter must be an instance of Filter, %s found" % type(_filter)
                    )
            if not self.type in _filter.usedFor:
                raise InvalidInterceptorFilterError(
                    "filter types (%s) is not compatible with this interceptor type (%s)" % (
                        _filter.usedFor, self.type
                    ))

        self.filters = filters
        self.script = script
        self._str = '%s/%s' % (self.__class__.__name__, repr(script))

    def __str__(self):
        return self._str

    def getScript(self):
        return self.script

    def matchFilters(self, routable):
        """If filters match routable, the script will be returned, if not, None will be returned
        """

        if not isinstance(routable, Routable):
            raise InvalidInterceptorParameterError("routable is not an instance of Routable")

        for _filter in self.filters:
            if not _filter.match(routable):
                return None
        return self.getScript()

class DefaultInterceptor(Interceptor):
    """This is a default interceptor which can contain one script
    """
    type = 'default'

    def __init__(self, script):
        "DefaultInterceptor can be for MO or MT messages"

        if not isinstance(script, InterceptorScript):
            raise InvalidInterceptorParameterError("script is not an instance of InterceptorScript")

        self.script = script
        self._str = '%s/%s' % (self.__class__.__name__, repr(script))

    def matchFilters(self, routable):
        return self.getScript()

class MTInterceptor(Interceptor):
    """Generic MT Interceptor
    """
    type = 'mt'

class MOInterceptor(Interceptor):
    """Generic MO Interceptor
    """
    type = 'mo'

class StaticMOInterceptor(MOInterceptor):
    """Return one unique interceptor
    """

class StaticMTInterceptor(MTInterceptor):
    """Return one unique interceptor
    """
