class AbstractCredentialValidator:
    """An abstract CredentialValidator, when inherited it must validate self.user credentials
    agains self.action"""

    encoding_map = {
        b'0': 'gsm0338',
        b'1': 'ascii',
        b'2': '', #OCTET_UNSPECIFIED
        b'3': 'iso-8859-1',
        b'4': '', # OCTET_UNSPECIFIED_COMMON
        b'5': 'shift_jis',
        b'6': 'iso-8859-5',
        b'7': 'iso-8859-8',
        b'8': 'utf_16_be',
        b'9': 'cp932',
        b'10': 'iso2022_jp',
        b'13': 'cp932',
        b'14': 'ksc5601'
    }

    def __init__(self, action, user):
        self.action = action
        self.user = user

    def updatePDUWithUserDefaults(self, PDU):
        """Must update PDU.params from User credential defaults whenever a
        PDU.params item is None"""

        raise NotImplementedError()

    def validate(self):
        "Must validate requests through Authorizations and ValueFilters credential check"

        raise NotImplementedError()
