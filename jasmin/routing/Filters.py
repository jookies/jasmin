"""
More info: http://docs.jasminsms.com/en/latest/routing/index.html
"""

#pylint: disable-msg=W0401,W0611
import datetime
from jasmin.routing.Routables import Routable
from jasmin.routing.jasminApi import *
from jasmin.tools.singleton import Singleton

class InvalidFilterParameterError(Exception):
    """Raised when a parameter is not an instance of a desired class (used for
    validating inputs
    """

class Filter:
    """
    Generick Filter:
    
    Filter will .match() a Routable against a filter and return true/false.
    
    Filters are written for specific Route types, that's why Filter.forRoutes should be set for each
    implemented Filter, here's a compatibility matrix of Filter vs Route types:
    
    Filter / Route type    | mo | mt | comment
    TransparentFilter      | x  | x  |
    ConnectorFilter        | x  |    | MT messages are identified by user instead of source connector
    UserFilter             |    | x  | MO messages are not authenticated
    GroupFilter            |    | x  | MO messages are not authenticated
    SourceAddrFilter       | x  |    | Only MO messages can have 'network-reliable' src addresses, MT messages
                                       can have user-defined source address
    DestinationAddrFilter  | x  | x  |
    ShortMessageFilter     | x  | x  |
    DateIntervalFilter     | x  | x  |
    TimeIntervalFilter     | x  | x  |
    EvalPyFilter           | x  | x  |
    """
    
    forRoutes = ['mt', 'mo']
    _str = 'Generick Filter'
    _repr = '<Generick Filter>'

    def __init__(self, connector = None, user = None, group = None, source_addr = None, 
                 destination_addr = None, short_message = None, 
                 dateInterval = None, timeInterval = None):
        if connector is not None and not isinstance(connector, Connector):
            raise InvalidFilterParameterError("connector is not an instance of Connector")
        if user is not None and not isinstance(user, User):
            raise InvalidFilterParameterError("user is not an instance of User")
        if group is not None and not isinstance(group, Group):
            raise InvalidFilterParameterError("group is not an instance of Group")
        if dateInterval is not None:
            if not isinstance(dateInterval, list):
                raise InvalidFilterParameterError("dateInterval must be a list")
            if len(dateInterval) != 2:
                raise InvalidFilterParameterError("dateInterval must be a list of 2 dates")
            if not isinstance(dateInterval[0], datetime.date):
                raise InvalidFilterParameterError("First date must be a datetime.date")
            if not isinstance(dateInterval[1], datetime.date):
                raise InvalidFilterParameterError("Second date must be a datetime.date")
        if timeInterval is not None:
            if not isinstance(timeInterval, list):
                raise InvalidFilterParameterError("timeInterval must be a list")
            if len(timeInterval) != 2:
                raise InvalidFilterParameterError("timeInterval must be a list of 2 times")
            if not isinstance(timeInterval[0], datetime.time):
                raise InvalidFilterParameterError("First time must be a datetime.time")
            if not isinstance(timeInterval[1], datetime.time):
                raise InvalidFilterParameterError("Second time must be a datetime.time")
        
        self.connector = connector
        self.user = user
        self.group = group
        self.source_addr = re.compile(source_addr) if source_addr is not None else source_addr
        self.destination_addr = re.compile(destination_addr) if destination_addr is not None else destination_addr
        self.short_message = re.compile(short_message) if short_message is not None else short_message
        self.dateInterval = dateInterval
        self.timeInterval = timeInterval
        
    def match(self, routable):
        if not isinstance(routable, Routable):
            raise InvalidFilterParameterError("routable is not an instance of Routable")
        
    def __repr__(self):
        return self._repr
    def __str__(self):
        return self._str
        
class TransparentFilter(Filter):
    """This filter will match any routable
    """
    def __init__(self):
        self._repr = '<T>'
        self._str = '%s' % (self.__class__.__name__)
        
    def match(self, routable):
        Filter.match(self, routable)

        return True

class ConnectorFilter(Filter):
    forRoutes = ['mo']
    
    def __init__(self, connector):
        Filter.__init__(self, connector = connector)
        
        self._repr = '<C (cid=%s)>' % (connector.cid)
        self._str = '%s:\ncid = %s' % (self.__class__.__name__, connector.cid)

    def match(self, routable):
        Filter.match(self, routable)

        if routable.connector.cid == self.connector.cid:
            return True
        else:
            return False
        
class UserFilter(Filter):
    forRoutes = ['mt']

    def __init__(self, user):
        Filter.__init__(self, user = user)
        
        self._repr = '<U (uid=%s)>' % (user.uid)
        self._str = '%s:\nuid = %s' % (self.__class__.__name__, user.uid)

    def match(self, routable):
        Filter.match(self, routable)

        if routable.user.uid == self.user.uid:
            return True
        else:
            return False
        
class GroupFilter(Filter):
    forRoutes = ['mt']

    def __init__(self, group):
        Filter.__init__(self, group = group)
        
        self._repr = '<G (gid=%s)>' % (group.gid)
        self._str = '%s:\ngid = %s' % (self.__class__.__name__, group.gid)

    def match(self, routable):
        Filter.match(self, routable)

        if routable.user.group.gid == self.group.gid:
            return True
        else:
            return False
        
class SourceAddrFilter(Filter):
    forRoutes = ['mo']

    def __init__(self, source_addr):
        Filter.__init__(self, source_addr = source_addr)
        
        self._repr = '<SA (src_addr=%s)>' % (source_addr)
        self._str = '%s:\nsource_addr = %s' % (self.__class__.__name__, source_addr)

    def match(self, routable):
        Filter.match(self, routable)

        return False if self.source_addr.match(routable.pdu.params['source_addr']) is None else True
        
class DestinationAddrFilter(Filter):
    def __init__(self, destination_addr):
        Filter.__init__(self, destination_addr = destination_addr)
        
        self._repr = '<DA (dst_addr=%s)>' % (destination_addr)
        self._str = '%s:\ndestination_addr = %s' % (self.__class__.__name__, destination_addr)

    def match(self, routable):
        Filter.match(self, routable)

        return False if self.destination_addr.match(routable.pdu.params['destination_addr']) is None else True

class ShortMessageFilter(Filter):
    def __init__(self, short_message):
        Filter.__init__(self, short_message = short_message)
        
        self._repr = '<SM (msg=%s)>' % (short_message)
        self._str = '%s:\nshort_message = %s' % (self.__class__.__name__, short_message)

    def match(self, routable):
        Filter.match(self, routable)

        return False if self.short_message.match(routable.pdu.params['short_message']) is None else True
            
class DateIntervalFilter(Filter):
    def __init__(self, dateInterval):
        Filter.__init__(self, dateInterval = dateInterval)
        
        self._repr = '<DI (%s,%s)>' % (dateInterval[0], dateInterval[1])
        self._str = '%s:\nLeft border = %s\nRight border = %s' % (self.__class__.__name__, dateInterval[0], dateInterval[1])

    def match(self, routable):
        Filter.match(self, routable)

        return True if self.dateInterval[0] <= routable.datetime.date() <= self.dateInterval[1] else False
            
class TimeIntervalFilter(Filter):
    def __init__(self, timeInterval):
        Filter.__init__(self, timeInterval = timeInterval)
        
        self._repr = '<TI (%s,%s)>' % (timeInterval[0], timeInterval[1])
        self._str = '%s:\nLeft border = %s\nRight border = %s' % (self.__class__.__name__, timeInterval[0], timeInterval[1])

    def match(self, routable):
        Filter.match(self, routable)

        return True if self.timeInterval[0] <= routable.datetime.time() <= self.timeInterval[1] else False

class CompiledNode:
    "A compiled code holder singleton to be used by EvalPyFilter"
    __metaclass__ = Singleton
    nodes = {}

    def get(self, pyCode):
        "Return a compiled pyCode object or instanciate a new one"
        pyCodeHash = pyCode.encode('hex')
        if pyCodeHash not in self.nodes:
            self.nodes[pyCodeHash] = compile(pyCode, '', 'exec')
        
        return self.nodes[pyCodeHash]

class EvalPyFilter(Filter):
    def __init__(self, pyCode):
        self.pyCode = pyCode
        
        self._repr = '<Ev (pyCode=%s ..)>' % (pyCode[:10].replace('\n', ''))
        self._str = '%s:\n%s' % (self.__class__.__name__, pyCode)

    def match(self, routable):
        Filter.match(self, routable)
        
        node = CompiledNode().get(self.pyCode)

        glo = {'routable': routable, 'result': False}
        eval(node, {}, glo)
        
        if not isinstance(glo['result'], bool):
            return False
        else:
            return glo['result']