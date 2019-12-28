# -*- coding: utf-8 -*-
"External URL controlled routing and custom message_id"
import sys
reload(sys)
sys.setdefaultencoding('utf8')

import logging
import requests
import urllib
import json

# Set logger
logger = logging.getLogger('logging-example')
if len(logger.handlers) != 1:
    hdlr = logging.FileHandler('/var/log/jasmin/interception-mt.log')
    formatter = logging.Formatter('%(asctime)s %(levelname)s %(message)s')
    hdlr.setFormatter(formatter)
    logger.addHandler(hdlr)
    logger.setLevel(logging.DEBUG)
#logger.info('v4 PDU: %s' % routable.pdu)

destination_addr = str( routable.pdu.params['destination_addr'] )
source_addr = str( routable.pdu.params['source_addr'] )
short_message = routable.pdu.params['short_message'].decode('utf-16-be').encode('utf-8')

if 'message_payload' in routable.pdu.params:
    short_message = routable.pdu.params['message_payload'].decode('utf-16-be').encode('utf-8')
    routable.pdu.params['short_message'] = routable.pdu.params['message_payload']
    if 'button_text' in routable.pdu.params:
        routable.pdu.params['button_text'] = routable.pdu.params['button_text'].encode('cp1251')
    del routable.pdu.params['message_payload']

# Register request and get message id
headers = {'User-Agent': 'Interceptor-MT'}
link = ("http://api.greensms.ru/?user="+routable.user.uid+"&to="+destination_addr+"&from="+urllib.quote_plus(source_addr)+"&txt="+urllib.quote_plus(short_message) )
response = requests.get(link, headers=headers)

result_json = json.loads(response.content)
uuid = str(result_json['request_id'])
smsc = str(result_json['smsc'])

# Setting message_id for later DLR identifying
routable.pdu.params['message_id'] = uuid

# Add routing tag base on external service
routable.addTag(smsc)