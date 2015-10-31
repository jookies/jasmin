"""This script will receive Mobile-Originated messages and
ask CGRateS for authorization through ApierV2.GetMaxUsage call.
"""
import json, socket
from datetime import datetime

CGR_HOST="172.20.20.140"
CGR_PORT=3422

def call(sck, name, params):
    # Build the request
    request = dict(id=1,
                    params=list(params),
                    method=name)
    sck.sendall(json.dumps(request).encode())
    # This must loop if resp is bigger than 4K
    buffer = ''
    data = True
    while data:
        data = sck.recv(4096)
        buffer += data
        if len(data) < 4096:
            break
    response = json.loads(buffer.decode())
    if response.get('id') != request.get('id'):
        raise Exception("expected id=%s, received id=%s: %s"
                        %(request.get('id'), response.get('id'),
                                response.get('error')))

    if response.get('error') is not None:
        raise Exception(response.get('error'))

    return response.get('result')

sck = None
globals()['sck'] = sck
globals()['json'] = json
try:
    sck = socket.create_connection((CGR_HOST, CGR_PORT))

    # Prepare for RPC call
    name = "ApierV2.GetMaxUsage"
    params = [{
        "Category": "sms-mt",
        "Usage": "1",
        "Direction": "*outbound",
        "ReqType": "*subscribers",
        "TOR": "*sms-mt",
        "ExtraFields": {"Cli": routable.pdu.params['source_addr']},
        "Destination": routable.pdu.params['destination_addr'],
        "Account": "*subscribers",
        "Tenant": "*subscribers",
        "SetupTime": datetime.utcnow().isoformat() + 'Z'}]

    result = call(sck, name, params)
except Exception, e:
    # We got an error when calling for charging
    # Return ESME_RDELIVERYFAILURE
    smpp_status = 254
else:
    # CGRateS has returned a value

    if type(result) == int and result >= 1:
        # Return ESME_ROK
        smpp_status = 0
    else:
        # Return ESME_RDELIVERYFAILURE
        smpp_status = 254
finally:
    if sck is not None:
        sck.close()
