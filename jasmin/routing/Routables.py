"""
More info: http://docs.jasminsms.com/en/latest/routing/index.html
"""

#pylint: disable-msg=W0401,W0611
from datetime import datetime
from jasmin.vendor.smpp.pdu.pdu_types import PDURequest
from jasmin.routing.jasminApi import *

class InvalidRoutableParameterError(Exception):
    """Raised when a parameter is not an instance of a desired class (used for
    validating inputs
    """

class Routable:
    """Generick Routable:
    
    Routable represents a PDU and optionnaly its owner and source Connector, it can be
    a submit_sm or deliver_sm for example
    """
    pass

class SimpleRoutablePDU(Routable):
    def __init__(self, connector, PDU, user, date_time = None):
        if not isinstance(PDU, PDURequest):
            raise InvalidRoutableParameterError("PDU is not an instance of PDURequest")
        if not isinstance(connector, Connector):
            raise InvalidRoutableParameterError("connector is not an instance of jasmin.routing.jasminApi.Connector")
        if not isinstance(user, User):
            raise InvalidRoutableParameterError("user is not an instance of jasmin.routing.jasminApi.User")
        
        self.pdu = PDU
        self.connector = connector
        self.user = user
        
        # Assume now() if datetime is not provided
        self.datetime = date_time
        if self.datetime is None:
            self.datetime = datetime.now()
            
class RoutableSubmitSm(Routable):
    def __init__(self, PDU, user, date_time = None):
        if not isinstance(PDU, PDURequest):
            raise InvalidRoutableParameterError("PDU is not an instance of PDURequest")
        if not isinstance(user, User):
            raise InvalidRoutableParameterError("user is not an instance of jasmin.routing.jasminApi.User")
        
        self.pdu = PDU
        self.user = user
        
        # Assume now() if datetime is not provided
        self.datetime = date_time
        if self.datetime is None:
            self.datetime = datetime.now()
            
class RoutableDeliverSm(Routable):
    def __init__(self, PDU, connector, date_time = None):
        if not isinstance(PDU, PDURequest):
            raise InvalidRoutableParameterError("PDU is not an instance of PDURequest")
        if not isinstance(connector, Connector):
            raise InvalidRoutableParameterError("connector is not an instance of jasmin.routing.jasminApi.Connector")
        
        self.pdu = PDU
        self.connector = connector
        
        # Assume now() if datetime is not provided
        self.datetime = date_time
        if self.datetime is None:
            self.datetime = datetime.now()
