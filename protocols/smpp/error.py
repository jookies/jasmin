from jasmin.vendor.smpp.pdu.error import *

class ShortMessageCodingError(SMPPClientError):
    """Raised for incorrect coding of short_message
    """