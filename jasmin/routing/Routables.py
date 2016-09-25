#pylint: disable=W0401,W0611
"""
More info: http://docs.jasminsms.com/en/latest/routing/index.html
"""

from datetime import datetime

from jasmin.routing.jasminApi import *
from jasmin.vendor.smpp.pdu.pdu_types import PDURequest


class InvalidRoutableParameterError(Exception):
    """Raised when a parameter is not an instance of a desired class (used for
    validating inputs
    """

class InvalidTagError(Exception):
    """Raised when a tag type is not correct
    """

class TagNotFoundError(Exception):
    """Raised when trying to access a non existent tag
    """

class InvalidLockError(Exception):
    """Raised when an error is catched while trying to lock a pdu param
    """

class Routable(object):
    """Generic Routable:

    Routable represents a PDU and optionnaly its owner and source Connector, it can be
    a submit_sm or deliver_sm for example
    """

    def __init__(self):
        "Init routable"
        self.flushTags()

    def addTag(self, tag):
        "Add tag to this routable"
        if not isinstance(tag, int) and not isinstance(tag, str):
            raise InvalidTagError('Non int or str tag given: (%s):%s' % (type(tag), tag))

        self._tags.append(tag)

    def hasTag(self, tag):
        "Check if this routable has 'tag'"
        if not isinstance(tag, int) and not isinstance(tag, str):
            raise InvalidTagError('Non int or str tag given: (%s):%s' % (type(tag), tag))

        return tag in self._tags

    def getTags(self):
        "Return all routable tags"
        return self._tags

    def removeTag(self, tag):
        "Remove tag from routable"
        if not isinstance(tag, int) and not isinstance(tag, str):
            raise InvalidTagError('Non int or str tag given: (%s):%s' % (type(tag), tag))
        if tag not in self._tags:
            raise TagNotFoundError('Trying to remove non existent tag: %s' % tag)

        return self._tags.remove(tag)

    def flushTags(self):
        "Remove all tags from routable"
        self._tags = []

class SimpleRoutablePDU(Routable):
    "Used for Jasmin unit testing"

    def __init__(self, connector, PDU, user, date_time=None):
        Routable.__init__(self)

        if not isinstance(PDU, PDURequest):
            raise InvalidRoutableParameterError("PDU is not an instance of PDURequest")
        if not isinstance(connector, Connector):
            raise InvalidRoutableParameterError(
                "connector is not an instance of jasmin.routing.jasminApi.Connector")
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
    def __init__(self, PDU, user, date_time=None):
        Routable.__init__(self)

        if not isinstance(PDU, PDURequest):
            raise InvalidRoutableParameterError("PDU is not an instance of PDURequest")
        if not isinstance(user, User):
            raise InvalidRoutableParameterError("user is not an instance of jasmin.routing.jasminApi.User")

        self.pdu = PDU
        self.user = user

        self.flushPduLocks()

        # Assume now() if datetime is not provided
        self.datetime = date_time
        if self.datetime is None:
            self.datetime = datetime.now()

    def lockPduParam(self, param):
        "Protect 'param' from being updated by Jasmin"
        if param not in self.pdu.params:
            raise InvalidLockError('Invalid locking param: %s' % param)

        self._pdu_locks.append(param)

    def pduParamIsLocked(self, param):
        "Check if this pdu 'param' is locked"
        if param not in self.pdu.params:
            raise InvalidLockError('Invalid locking param: %s' % param)

        return param in self._pdu_locks

    def flushPduLocks(self):
        "Remove all pdu locks from routable"
        self._pdu_locks = []

class RoutableDeliverSm(Routable):
    def __init__(self, PDU, connector, date_time=None):
        Routable.__init__(self)

        if not isinstance(PDU, PDURequest):
            raise InvalidRoutableParameterError("PDU is not an instance of PDURequest")
        if not isinstance(connector, Connector):
            raise InvalidRoutableParameterError(
                "connector is not an instance of jasmin.routing.jasminApi.Connector")

        self.pdu = PDU
        self.connector = connector

        # Assume now() if datetime is not provided
        self.datetime = date_time
        if self.datetime is None:
            self.datetime = datetime.now()
