class AbstractCredentialValidator:
    """An abstract CredentialValidator, when inherited it must validate self.user credentials
    agains self.action"""

    encoding_map = {
        b'0': 'gsm0338',
        b'1': 'ascii',
        b'2': None, #OCTET_UNSPECIFIED
        b'3': 'iso-8859-1',
        b'4': None, # OCTET_UNSPECIFIED_COMMON
        b'5': 'shift_jis',
        b'6': 'iso-8859-5',
        b'7': 'iso-8859-8',
        b'8': 'utf_16_be',
        b'9': 'shift_jis', #https://en.wikipedia.org/wiki/Short_Message_Peer-to-Peer#Unclear_support_for_Shift-JIS_encoding
        b'10': 'iso2022_jp',
        # jisx0212 doesnt exist in python codecs but it looks like its decodable/encodable in iso2022_jp assuming the escape patterns are included
        # https://github.com/python/cpython/blob/master/Modules/cjkcodecs/_codecs_iso2022.c#L51
        b'13': 'iso2022_jp', 
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
