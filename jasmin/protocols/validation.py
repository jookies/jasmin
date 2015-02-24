class AbstractCredentialValidator:
    """An abstract CredentialValidator, when inherited it must validate self.user credentials
    agains self.action"""

    def __init__(self, action, user, submit_sm):
        self.action = action
        self.submit_sm = submit_sm
        self.user = user
        
    def updatePDUWithUserDefaults(self, PDU):
        """Must update PDU.params from User credential defaults whenever a 
        PDU.params item is None"""
        
        raise NotImplementedError()
    
    def validate(self, ):
        "Must validate requests through Authorizations and ValueFilters credential check"
        
        raise NotImplementedError()