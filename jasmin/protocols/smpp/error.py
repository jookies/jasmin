from jasmin.vendor.smpp.pdu import constants
from jasmin.vendor.smpp.pdu.error import *
from jasmin.vendor.smpp.pdu import pdu_types


class LongSubmitSmTransactionError(SMPPError):
    """Raised inside a long message transaction
    """
    def __init__(self, message):
        SMPPError.__init__(self, message)
        
        self.message = message


class SubmitSmEventHandlerErrorNoShutdown(SMPPProtocolError):
    """Errors raised from jasmin.protocols.smpp.factory.SMPPServerFactory.submit_sm_event()
    Any error raising this exception will not cause connection shutdown
    """
    def __init__(self, message=None):
        if message is None:
            SMPPProtocolError.__init__(self, "%s" % self.getStatusDescription(), self.status)
        else:
            SMPPProtocolError.__init__(self, message, self.status)


class SubmitSmEventHandlerErrorShutdown(SMPPError):
    """Errors raised from jasmin.protocols.smpp.factory.SMPPServerFactory.submit_sm_event()
    Any error raising this exception will cause connection shutdown
    """


class SubmitSmInvalidArgsError(SubmitSmEventHandlerErrorNoShutdown):
    """Raised when args validation fail
    """
    def __init__(self):
        self.status = pdu_types.CommandStatus.ESME_RSYSERR
        SubmitSmEventHandlerErrorNoShutdown.__init__(self)


class SubmitSmWithoutDestinationAddrError(SubmitSmEventHandlerErrorNoShutdown):
    """Raised when args validation fail
    """
    def __init__(self):
        self.status = pdu_types.CommandStatus.ESME_RINVDSTADR
        SubmitSmEventHandlerErrorNoShutdown.__init__(self)


class SubmitSmRouteNotFoundError(SubmitSmEventHandlerErrorNoShutdown):
    """Raised when no route is found for a given submit_sm
    """
    def __init__(self):
        self.status = pdu_types.CommandStatus.ESME_RINVDSTADR
        SubmitSmEventHandlerErrorNoShutdown.__init__(self)


class SubmitSmRoutingError(SubmitSmEventHandlerErrorNoShutdown):
    """Raised when failing to send SubmitSm to routedConnector
    """
    def __init__(self):
        self.status = pdu_types.CommandStatus.ESME_RSUBMITFAIL
        SubmitSmEventHandlerErrorNoShutdown.__init__(self)


class SubmitSmChargingError(SubmitSmEventHandlerErrorNoShutdown):
    """Raised when charging user for sending submit_sm failed
    """
    def __init__(self):
        self.status = pdu_types.CommandStatus.ESME_RSYSERR
        SubmitSmEventHandlerErrorNoShutdown.__init__(self)


class SubmitSmThroughputExceededError(SubmitSmEventHandlerErrorNoShutdown):
    """Raised when throughput is exceeded
    """
    def __init__(self):
        self.status = pdu_types.CommandStatus.ESME_RTHROTTLED
        SubmitSmEventHandlerErrorNoShutdown.__init__(self)


class CredentialValidationError(SubmitSmEventHandlerErrorShutdown):
    """
    Raised when user credential validation fails
    (jasmin.protocols.smpp.validation.SmppsCredentialValidator)
    """


class AuthorizationError(SubmitSmEventHandlerErrorNoShutdown):
    """
    Raised when user credential validation fails in _checkSendAuthorizations()
    (jasmin.protocols.smpp.validation.SmppsCredentialValidator)
    """
    def __init__(self, message):
        self.status = pdu_types.CommandStatus.ESME_RINVSYSID
        SubmitSmEventHandlerErrorNoShutdown.__init__(self, message)


class FilterError(SubmitSmEventHandlerErrorNoShutdown):
    """
    Raised when user credential validation fails in _checkSendFilters()
    (jasmin.protocols.smpp.validation.SmppsCredentialValidator)
    """
    def __init__(self, message, filter_key=None):
        if filter_key == 'destination_address':
            self.status = pdu_types.CommandStatus.ESME_RINVDSTADR
        elif filter_key == 'source_address':
            self.status = pdu_types.CommandStatus.ESME_RINVSRCADR
        elif filter_key == 'priority':
            self.status = pdu_types.CommandStatus.ESME_RINVPRTFLG
        else:
            self.status = pdu_types.CommandStatus.ESME_RSYSERR

        SubmitSmEventHandlerErrorNoShutdown.__init__(self, message)


class InterceptorError(SMPPProtocolError):
    """Errors raised when intercepting a submit_sm or deliver_sm pdu
    Any error raising this exception will not cause connection shutdown
    """
    def __init__(self, code, message=None):
        if isinstance(code, int) and code > 0 and code in constants.command_status_value_map:
            self.status = getattr(pdu_types.CommandStatus, constants.command_status_value_map[code]['name'])
        else:
            # This is a fallback status
            # Error code must be numeric, not zero and must be a known one (in command_status_value_map)
            # Otherwise:
            self.status = pdu_types.CommandStatus.ESME_RSYSERR

        if message is None:
            SMPPProtocolError.__init__(self, "%s" % self.getStatusDescription(), self.status)
        else:
            SMPPProtocolError.__init__(self, message, self.status)


class DeliverSmInterceptionError(InterceptorError):
    pass


class SubmitSmInterceptionError(InterceptorError):
    pass


class SubmitSmInterceptionSuccess(InterceptorError):
    """This is used to return a ESME_ROK status after interception
    As described in #589"""
    def __init__(self, message=None):
        self.status = pdu_types.CommandStatus.ESME_ROK
        SMPPProtocolError.__init__(self, "%s" % self.getStatusDescription(), self.status)


class InterceptorNotSetError(InterceptorError):
    def __init__(self, message=None):
        InterceptorError.__init__(self, code=8, message=message)


class InterceptorNotConnectedError(InterceptorError):
    def __init__(self, message=None):
        InterceptorError.__init__(self, code=8, message=message)


class InterceptorRunError(InterceptorError):
    def __init__(self, message=None):
        InterceptorError.__init__(self, code=8, message=message)
