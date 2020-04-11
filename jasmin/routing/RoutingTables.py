"""
More info: http://docs.jasminsms.com/en/latest/routing/index.html
"""

from jasmin.routing.Routables import Routable
from jasmin.routing.Routes import Route


class InvalidRoutingTableParameterError(Exception):
    """Raised when a parameter is not an instance of a desired class (used for
    validating inputs
    """


class RoutingTable:
    """Generic Routing table
    """
    _type = 'generic'

    def __init__(self):
        self.table = []

    def add(self, route, order):
        if not isinstance(route, Route):
            raise InvalidRoutingTableParameterError("route is not an instance of Route")
        if not isinstance(order, int):
            raise InvalidRoutingTableParameterError("order is not an integer")

        # Ensure connector type is correct for given route
        if self._type == 'mo':
            if not isinstance(route.connector, list):
                if route.connector._type not in ['http', 'smpps']:
                    raise InvalidRoutingTableParameterError("connector '%s' type '%s' is not valid for MO Route" % (
                    route.connector.cid, route.connector._type))
            else:
                for connector in route.connector:
                    if connector._type not in ['http', 'smpps']:
                        raise InvalidRoutingTableParameterError(
                            "connector '%s' type '%s' is not valid for MO Route" % (connector.cid, connector._type))
        elif self._type == 'mt':
            if not isinstance(route.connector, list):
                if route.connector._type not in ['smppc']:
                    raise InvalidRoutingTableParameterError("connector '%s' type '%s' is not valid for MT Route" % (
                    route.connector.cid, route.connector._type))
            else:
                for connector in route.connector:
                    if connector._type not in ['smppc']:
                        raise InvalidRoutingTableParameterError(
                            "connector '%s' type '%s' is not valid for MT Route" % (connector.cid, connector._type))

        if order < 0:
            raise InvalidRoutingTableParameterError("order must be 0 (default route) or greater")
        if order != 0 and route._type != self._type:
            raise InvalidRoutingTableParameterError(
                "route must be of type '%s', '%s' was given" % (self._type, route._type))
        if order == 0 and route._type != 'default':
            raise InvalidRoutingTableParameterError("Route with order=0 must be a DefaultRoute")

        # Replace older routes with the same given order
        self.remove(order)

        self.table.append({order: route})
        self.table = sorted(self.table, key=lambda x: sorted(x.keys()), reverse=True)

    def remove(self, order):
        for r in self.table:
            if list(r)[0] == order:
                self.table.remove(r)
                return True

        return False

    def getAll(self):
        return self.table

    def flush(self):
        self.table = []

    def getRouteFor(self, routable):
        """This will return the right route to send the routable to, None returned otherwise
        """

        if not isinstance(routable, Routable):
            raise InvalidRoutingTableParameterError("routable is not an instance of Routable")

        for r in self.table:
            route = list(r.values())[0]
            if route.matchFilters(routable):
                return route

        return None


class MTRoutingTable(RoutingTable):
    """MT Routing table"""
    _type = 'mt'


class MORoutingTable(RoutingTable):
    """MO Routing table"""
    _type = 'mo'
