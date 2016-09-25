"""
Bills are objects containing amounts to be charged on users
"""
import uuid

class InvalidBillKeyError(Exception):
    """Raised when a bill key is not valid
    """

class InvalidBillValueError(Exception):
    """Raised when a bill value is not valid
    """

def randomUniqueId():
    "Returns a UUID4 unique message id"
    msgid = str(uuid.uuid4())

    return msgid

class Bill(object):
    """This is a generic Bill class, it must be inherited to defined billables
    A billable can be:
    - An amount to be deducted from user's balance
    - An action to be done (ex: decrement submit_sm_count on user balance)
    """
    amounts = None
    actions = None
    user = None
    bid = None

    def __init__(self, user):
        self.bid = randomUniqueId()
        self.user = user
        self.amounts = {}
        self.actions = {}

    def getAmount(self, key):
        "Will return a billable amount"
        if key not in self.amounts:
            raise InvalidBillKeyError('%s is not a valid amount key.' % key)
        return self.amounts[key]

    def getTotalAmounts(self):
        "Will return a Sum of all amounts"
        total_amount = 0.0
        for key in self.amounts:
            total_amount += self.amounts[key]

        return total_amount

    def setAmount(self, key, amount):
        "Will set a billable amount"
        if key not in self.amounts:
            raise InvalidBillKeyError('%s is not a valid amount key.' % key)
        if not isinstance(amount, int) and not isinstance(amount, float):
            raise InvalidBillValueError('%s is not a valid amount value for key %s.' % (amount, key))
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
        if not isinstance(value, int):
            raise InvalidBillValueError('%s is not a valid value for key %s.' % (value, key))
        self.actions[key] = value

class SubmitSmBill(Bill):
    "This is the bill for user to pay when sending a MT SMS"

    def __init__(self, user):
        "Defining billables"
        Bill.__init__(self, user)

        self.amounts['submit_sm'] = 0.0
        self.amounts['submit_sm_resp'] = 0.0
        self.actions['decrement_submit_sm_count'] = 0

    def getSubmitSmRespBill(self):
        """
        Will return a separate Bill for submit_sm_resp
        """

        bill = SubmitSmRespBill(self.user)
        bill.setAmount('submit_sm_resp', self.getAmount('submit_sm_resp'))

        return bill

class SubmitSmRespBill(Bill):
    "This is the bill for user to pay when sending a MT SMS"

    def __init__(self, user):
        "Defining billables"
        Bill.__init__(self, user)

        self.amounts['submit_sm_resp'] = 0.0
