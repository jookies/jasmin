"""
Bills are objects containing amounts to be charged on users
"""

class InvalidBillKeyError(Exception):
    """Raised when a bill key is not valid
    """

class Bill:
    """This is a generick Bill class, it must be inherited to defined billables
    A billable can be:
    - An amount to be deducted from user's balance
    - An action to be done (ex: decrement submit_sm_count on user balance)
    """
    amounts = None
    actions = None
    
    def __init__(self):
        self.amounts = {}
        self.actions = {}
    
    def getAmount(self, key):
        "Will return a billable amount"
        if key not in self.amounts:
            raise InvalidBillKeyError('%s is not a valid amount key.' % key)
        return self.amounts[key]
    
    def getTotalAmounts(self):
        "Will return a Sum of all amounts"
        t = 0.0
        for key in self.amounts:
            t+= self.amounts[key]
        
        return t

    def setAmount(self, key, amount):
        "Will set a billable amount"
        if key not in self.amounts:
            raise InvalidBillKeyError('%s is not a valid amount key.' % key)
        self.amounts[key] = amount
    
    def getAction(self, key):
        "Will return a billable action"
        if key not in self.actions:
            raise InvalidBillKeyError('%s is not a valid action key.' % key)
        return self.actions[key]
    
    def setAction(self, key, value):
        "Will set a billable action"
        if key not in self.actions:
            raise InvalidBillKeyError('%s is not a valid action key.' % key)
        self.actions[key] = value

class MTSMSBill(Bill):
    "This is the bill for user to pay when sending a MT SMS"
    
    def __init__(self):
        "Defining billables"
        Bill.__init__(self)

        self.amounts['submit_sm'] = 0.0
        self.amounts['submit_sm_resp'] = 0.0
        self.actions['decrement_submit_sm_count'] = 0