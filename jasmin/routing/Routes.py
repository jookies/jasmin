#pylint: disable=W0401,W0611,W0231
"""
More info: http://docs.jasminsms.com/en/latest/routing/index.html
"""

import random

from jasmin.routing.Bills import SubmitSmBill
from jasmin.routing.Filters import Filter
from jasmin.routing.Routables import Routable
from jasmin.routing.jasminApi import *


class InvalidRouteParameterError(Exception):
    """Raised when a parameter is not an instance of a desired class (used for
    validating inputs
    """
class InvalidRouteFilterError(Exception):
    """Raised when a route is instanciated with a non-compatible type, e.g. an MORoute
    can not have UserFilter (MO messages are not authentified).
    """

class Route(object):
    """Generic Route:

    Route contain a triplet of [Filter(s), Connector, Rate]
    When more than one Filter is given, matching these filters will use the AND operator
    """
    type = 'generic'
    _str = 'generic'
    _repr = 'Route'
    filters = []
    connector = None
    rate = 0.0

    def __init__(self, filters, connector, rate):
        if not isinstance(connector, Connector):
            raise InvalidRouteParameterError("connector is not an instance of Connector")
        if not isinstance(rate, float):
            raise InvalidRouteParameterError("rate is not float")
        if rate < 0:
            raise InvalidRouteParameterError("rate can not be a negative value")
        if not isinstance(filters, list):
            raise InvalidRouteParameterError("filters must be a list")
        for _filter in filters:
            if not isinstance(_filter, Filter):
                raise InvalidRouteParameterError(
                    "filter must be an instance of Filter, %s found" % type(_filter)
                    )
            if not self.type in _filter.usedFor:
                raise InvalidRouteFilterError(
                    "filter types (%s) is not compatible with this route type (%s)" % (
                        _filter.usedFor, self.type
                    ))

        self.filters = filters
        self.connector = connector
        self.rate = rate

        if self.rate > 0:
            rate_str = 'rated %.2f' % self.rate
        else:
            rate_str = 'NOT RATED'
        self._str = '%s to %s(%s) %s' % (self.__class__.__name__, connector.type, connector.cid, rate_str)

    def __str__(self):
        return self._str

    def __repr__(self):
        return self.__class__.__name__

    def getConnector(self):
        return self.connector

    def getRate(self):
        return self.rate

    def getBillFor(self, user):
        """This will return the exact bill for user depending on his defined quotas
        """

        if not isinstance(user, User):
            raise InvalidRouteParameterError("user is not an instance of User")

        # Init
        bill = SubmitSmBill(user)

        # Route billing processing
        # [RULE 1] If route is rated and user's balance is not unlimited (balance != None) then
        # user will be billed for the selected route rate.
        if self.getRate() > 0 and user.mt_credential.getQuota('balance') is not None:
            early_decrement_balance_percent = user.mt_credential.getQuota('early_decrement_balance_percent')
            route_rate = self.getRate()
            # if early_decrement_balance_percent is defined then user will be:
            # - First: billed early_decrement_balance_percent % of the route rate on submit_sm
            # - Second: billed for the rest of the route rate on submit_sm_resp reception
            # If early_decrement_balance_percent is None (undefined) then the route rate will be
            # billed on submit_sm with no care about submit_sm_resp
            if early_decrement_balance_percent is not None:
                bill.setAmount('submit_sm', route_rate * early_decrement_balance_percent / 100)
                bill.setAmount('submit_sm_resp', route_rate - bill.getAmount('submit_sm'))
            else:
                bill.setAmount('submit_sm', route_rate)
                bill.setAmount('submit_sm_resp', 0)

        # [RULE 2] if user's submit_sm_count is not unlimited (!=None) then decrement it when sending
        # submit_sm
        if user.mt_credential.getQuota('submit_sm_count') is not None:
            bill.setAction('decrement_submit_sm_count', 1)

        return bill

    def matchFilters(self, routable):
        """If filters match routable, True will be returned, if not, False will be returned
        """

        if not isinstance(routable, Routable):
            raise InvalidRouteParameterError("routable is not an instance of Routable")

        for _filter in self.filters:
            if not _filter.match(routable):
                return False
        return True

class DefaultRoute(Route):
    """This is a default route which can contain one connector
    """
    type = 'default'

    def __init__(self, connector, rate=0.0):
        """
        Default rate is set to 0.0 since DefaultRoute can be for MO or MT routes, rate must be set only for
        MT routes, otherwise it will be ignored
        """
        if not isinstance(connector, Connector):
            raise InvalidRouteParameterError("connector is not an instance of Connector")
        if not isinstance(rate, float):
            raise InvalidRouteParameterError("rate is not float")
        if rate < 0:
            raise InvalidRouteParameterError("rate can not be a negative value")

        self.connector = connector
        self.rate = rate

        if self.rate > 0:
            rate_str = 'rated %.2f' % self.rate
        else:
            rate_str = 'NOT RATED'
        self._str = '%s to %s(%s) %s' % (self.__class__.__name__, connector.type, connector.cid, rate_str)

    def matchFilters(self, routable):
        return self.getConnector()

class MTRoute(Route):
    """Generic MT Route
    """
    type = 'mt'

class MORoute(Route):
    """Generic MO Route
    """
    type = 'mo'

    def __init__(self, filters, connector, rate=0.0):
        "Overriding Route's __init__ to remove rate parameter, MORoutes are not rated"

        Route.__init__(self, filters, connector, 0.0)

class StaticMORoute(MORoute):
    """Return one unique route
    """

class StaticMTRoute(MTRoute):
    """Return one unique route
    """

class RoundrobinRoute(object):
    """Generic RoundrobinRoute
    """
    def __init__(self, filters, connectors):
        if not isinstance(connectors, list):
            raise InvalidRouteParameterError("connectors must be a list")
        if len(connectors) == 0:
            raise InvalidRouteParameterError("Route cannot have zero connectors")
        for _connector in connectors:
            if not isinstance(_connector, Connector):
                raise InvalidRouteParameterError("connector is not an instance of Connector")
        if not isinstance(filters, list):
            raise InvalidRouteParameterError("filters must be a list")
        for _filter in filters:
            if not isinstance(_filter, Filter):
                raise InvalidRouteParameterError(
                    "filter must be an instance of Filter, %s found" % type(_filter))
            if self.type not in _filter.usedFor:
                raise InvalidRouteFilterError(
                    "filter types (%s) is not compatible with this route type (%s)" % (
                        _filter.usedFor, self.type))

        self.filters = filters
        self.connector = connectors

        connector_list_str = ''
        for c in connectors:
            if connector_list_str != '':
                connector_list_str += '\n'
            connector_list_str += '\t- %s(%s)' % (c.type, c.cid)
        self._str = '%s to %s connectors:\n%s' % (self.__class__.__name__,
                                                  len(connectors),
                                                  connector_list_str)

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
    def __init__(self, filters, connectors, rate):
        "Overriding RoundrobinRoute's __init__ to add rate parameter as it is only used for MT Routes"

        if not isinstance(rate, float):
            raise InvalidRouteParameterError("rate is not float")
        if rate < 0:
            raise InvalidRouteParameterError("rate can not be a negative value")

        self.rate = rate

        RoundrobinRoute.__init__(self, filters, connectors)

        if self.rate > 0:
            rate_str = '\nrated %.2f' % self.rate
        else:
            rate_str = '\nNOT RATED'
        self._str = "%s %s" % (self._str, rate_str)

class FailoverRoute(object):
    """Generic FailoverRoute"""
    def __init__(self, filters, connectors):
        if not isinstance(connectors, list):
            raise InvalidRouteParameterError("connectors must be a list")
        if len(connectors) == 0:
            raise InvalidRouteParameterError("Route cannot have zero connectors")
        for _connector in connectors:
            if not isinstance(_connector, Connector):
                raise InvalidRouteParameterError("connector is not an instance of Connector")
        if not isinstance(filters, list):
            raise InvalidRouteParameterError("filters must be a list")
        for _filter in filters:
            if not isinstance(_filter, Filter):
                raise InvalidRouteParameterError(
                    "filter must be an instance of Filter, %s found" % type(_filter))
            if self.type not in _filter.usedFor:
                raise InvalidRouteFilterError(
                    "filter types (%s) is not compatible with this route type (%s)" % (
                        _filter.usedFor, self.type))

        self.filters = filters
        self.seq = -1
        self.connector = connectors

        connector_list_str = ''
        for c in connectors:
            if connector_list_str != '':
                connector_list_str += '\n'
            connector_list_str += '\t- %s(%s)' % (c.type, c.cid)
        self._str = '%s to %s connectors:\n%s' % (self.__class__.__name__,
                                                  len(connectors),
                                                  connector_list_str)

    def __str__(self):
        return self._str

    def getConnector(self):
        try:
            self.seq += 1
            return self.connector[self.seq]
        except IndexError:
            return None


class FailoverMORoute(FailoverRoute, MORoute):
    """Returned connector from getConnector method is iterated through the list of available connectors
    """

    def __init__(self, filters, connectors):
        FailoverRoute.__init__(self, filters, connectors)

        # Don't accept mixed connector types for FailoverMORoute
        #  This is a design limitation: the deliver_sm throwers are isolated by type of connector,
        #  they cannot execute a failover algorithm on top of two different connector types.
        _types = None
        for c in connectors:
            if _types is not None and c.type != _types:
                raise InvalidRouteParameterError('FailoverMORoute cannot have mixed connector types')
            _types = c.type

        if not isinstance(connectors, list):
            raise InvalidRouteParameterError("connectors must be a list")

    def getConnectors(self):
        return self.connector

    def matchFilters(self, routable):
        # Initialize self.seq to return first connector
        self.seq = -1

        return MORoute.matchFilters(self, routable)


class FailoverMTRoute(FailoverRoute, MTRoute):
    """Returned connector from getConnector method is iterated through the list of available connectors
    """

    def __init__(self, filters, connectors, rate):
        """Overriding FailoverRoute's __init__ to add rate parameter as it is only used for MT Routes"""

        if not isinstance(rate, float):
            raise InvalidRouteParameterError("rate is not float")
        if rate < 0:
            raise InvalidRouteParameterError("rate can not be a negative value")

        self.rate = rate

        FailoverRoute.__init__(self, filters, connectors)

        if self.rate > 0:
            rate_str = '\nrated %.2f' % self.rate
        else:
            rate_str = '\nNOT RATED'
        self._str = "%s %s" % (self._str, rate_str)

    def matchFilters(self, routable):
        # Initialize self.seq to return first connector
        self.seq = -1

        return MTRoute.matchFilters(self, routable)


class BestQualityMTRoute(MTRoute):
    # @todo: Work in progress
    """Take the best route based on:
        * (submit_sm / submit_sm_resp) ratio
        * (delivered submits / underlivered submits)
    """
    def __init__(self, filters, connector, rate):
        MTRoute.__init__(self, filters, connector, rate)
        raise NotImplementedError
