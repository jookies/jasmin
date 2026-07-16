import sys
import os
import json
import datetime
from unittest.mock import MagicMock

# Add project root to sys.path
sys.path.append('/Users/minibot/Projects/bots-agents/jasmin-go')

from jasmin.routing.Filters import EvalPyFilter
from jasmin.routing.Routables import SimpleRoutablePDU
from jasmin.routing.jasminApi import Connector, User, Group
from smpp.pdu.pdu_types import PDURequest

def run_test(pyCode, routable):
    f = EvalPyFilter(pyCode)
    try:
        result = f.match(routable)
        return {"code": pyCode, "result": result, "error": None}
    except Exception as e:
        return {"code": pyCode, "result": None, "error": str(e)}

# Setup objects
group = Group('group1')
user = User('uid1', group, 'user1', 'pass1')
connector = Connector('abc')
pdu = PDURequest('submit_sm')
pdu.params['source_addr'] = b'123'
pdu.params['short_message'] = b'hello'

routable = SimpleRoutablePDU(connector, pdu, user)

fixtures = []

# Basic tests
fixtures.append(run_test("result = True", routable))
fixtures.append(run_test("result = False", routable))
fixtures.append(run_test("result = (1 + 1 == 2)", routable))

# Routable access tests
fixtures.append(run_test("result = routable.connector.cid == 'abc'", routable))
fixtures.append(run_test("result = routable.user.uid == 'uid1'", routable))
fixtures.append(run_test("result = routable.user.group.gid == 'group1'", routable))
fixtures.append(run_test("result = routable.pdu.params['source_addr'] == b'123'", routable))
fixtures.append(run_test("result = routable.pdu.params['short_message'] == b'hello'", routable))

# Tag tests
fixtures.append(run_test("routable.addTag('captured'); result = routable.hasTag('captured')", routable))

# Datetime tests
fixtures.append(run_test("result = routable.datetime.year > 2000", routable))

# Complex logic
fixtures.append(run_test("""
if routable.user.uid == 'uid1' and routable.connector.cid == 'abc':
    result = True
else:
    result = False
""", routable))

# Error tests
fixtures.append(run_test("result = 1 / 0", routable))
fixtures.append(run_test("invalid syntax", routable))

# Isolation tests (to see what's available)
fixtures.append(run_test("import os; result = os.name != ''", routable))
fixtures.append(run_test("result = __builtins__['len']([1,2]) == 2", routable))

print(json.dumps(fixtures, indent=2))
