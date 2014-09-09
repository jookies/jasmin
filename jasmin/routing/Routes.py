#pylint: disable-msg=W0401,W0611
import random
from jasmin.routing.jasminApi import *
from jasmin.routing.Filters import Filter
from jasmin.routing.Routables import Routable

class InvalidRouteParameterError(Exception):
    """Raised when a parameter is not an instance of a desired class (used for
    validating inputs
    """
class InvalidRouteFilterError(Exception):
    """Raised when a route is instanciated with a non-compatible type, e.g. an MORoute
    can not have UserFilter (MO messages are not authentified).
    """

class Route:
    """Generick Route:
    
    Route contain a couple of [Filter(s), Connector]
    When more than one Filter is given, matching these filters will use the AND operator
    """
    type = 'generick'
    _str = 'generick'
    filters = []
    connector = None

    def __init__(self, filters, connector):
        if not isinstance(connector, Connector):
            raise InvalidRouteParameterError("connector is not an instance of Connector")
        if type(filters) is not list:
            raise InvalidRouteParameterError("filters must be a list")
        for _filter in filters:
            if not isinstance(_filter, Filter):
                raise InvalidRouteParameterError("filter must be an instance of Filter, %s found" % type(_filter))
            if not self.type in _filter.forRoutes:
                raise InvalidRouteFilterError("filter types (%s) is not compatible with this route type (%s)" % (_filter.forRoutes, self.type))
        
        self.connector = connector
        self.filters = filters
        self._str = '%s to cid:%s' % (self.__class__.__name__, connector.cid)
        
    def __str__(self):
        return self._str
        
    def getConnector(self):
        return self.connector
    
    def matchFilters(self, routable):
        """If filters matche routable, the connector will be returned, if not, None will be returned
        """
        
        if not isinstance(routable, Routable):
            raise InvalidRouteParameterError("routable is not an instance of Routable")
        
        for _filter in self.filters:
            if not _filter.match(routable):
                return None
        return self.getConnector()
    
class DefaultRoute(Route):
    """This is a default route which can contain one connector
    """
    type = 'default'
    
    def __init__(self, connector):
        if not isinstance(connector, Connector):
            raise InvalidRouteParameterError("connector is not an instance of Connector")

        self.connector = connector
        self._str = '%s to cid:%s' % (self.__class__.__name__, connector.cid)

    def matchFilters(self, routable):
        return self.getConnector()

class MTRoute(Route):
    """Generick MT Route
    """
    type = 'mt'

class MORoute(Route):
    """Generick MO Route
    """
    type = 'mo'

class StaticMORoute(MORoute):
    """Return one unique route
    """
        
class StaticMTRoute(MTRoute):
    """Return one unique route
    """
        
class RoundrobinRoute():
    """Generick RoundrobinRoute
    """
    def __init__(self, filters, connectors):
        if type(connectors) is not list:
            raise InvalidRouteParameterError("connectors must be a list")
        for _connector in connectors:
            if not isinstance(_connector, Connector):
                raise InvalidRouteParameterError("connector is not an instance of Connector")
        if type(filters) is not list:
            raise InvalidRouteParameterError("filters must be a list")
        for _filter in filters:
            if not isinstance(_filter, Filter):
                raise InvalidRouteParameterError("filter must be an instance of Filter, %s found" % type(_filter))
            if not self.type in _filter.forRoutes:
                raise InvalidRouteFilterError("filter types (%s) is not compatible with this route type (%s)" % (_filter.forRoutes, self.type))
         
        self.connector = connectors
        self.filters = filters
        
        connectorList_str = ''
        for c in connectors:
            if connectorList_str != '':
                connectorList_str += '\n'
            connectorList_str += '\t- %s' % c.cid
        self._str = '%s to %s connectors:\n%s' % (self.__class__.__name__, len(connectors), connectorList_str)
        
    def __str__(self):
        return self._str

    def getConnector(self):
        return random.choice(self.connector)

class RandomRoundrobinMORoute(RoundrobinRoute, MORoute):
    """Return one route taken randomly from a pool of
    routes
    """
    
class RandomRoundrobinMTRoute(RoundrobinRoute, MTRoute):
    """Return one route taken randomly from a pool of
    routes
    """
    
class FailoverMORoute(MORoute):
    # @todo: Work in progress
    """Return a principal route with multiple failover
    routes to try if the principal route failed
    """
    def __init__(self, filters, connector):
        MORoute.__init__(self, filters, connector)
        raise NotImplementedError
    
class FailoverMTRoute(MTRoute):
    # @todo: Work in progress
    """Return a principal route with multiple failover
    routes to try if the principal route failed
    """
    def __init__(self, filters, connector):
        MORoute.__init__(self, filters, connector)
        raise NotImplementedError
    
class LeastCostMTRoute(MTRoute):
    # @todo: Work in progress
    """Take the least cost route
    """
    def __init__(self, filters, connector):
        MORoute.__init__(self, filters, connector)
        raise NotImplementedError

class BestQualityMTRoute(MTRoute):
    # @todo: Work in progress
    """Take the best route based on:
        * (submit_sm / submit_sm_resp) ratio
        * (delivered submits / underlivered submits)
    """
    def __init__(self, filters, connector):
        MORoute.__init__(self, filters, connector)
        raise NotImplementedError