"This script will call HLR lookup api to get the MCC/MNC of the destination number"

import requests, json

hlr_lookup_url = "https://api.some-provider.com/hlr/lookup"
data = json.dumps({'number': routable.pdu.params['destination_addr']})
r = requests.post(hlr_lookup_url, data, auth=('user', '*****'))

if r.json['mnc'] == '214':
    # Spain
    if r.json['mcc'] == '01':
        # Vodaphone
        routable.addTag(21401)
    elif r.json['mcc'] == '03':
        # Orange
        routable.addTag(21403)
    elif r.json['mcc'] == '25':
        # Lyca mobile
        routable.addTag(21425)
