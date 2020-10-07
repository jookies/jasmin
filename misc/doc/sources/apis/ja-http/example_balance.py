# Python example
# http://jasminsms.com
import urllib.request, urllib.error, urllib.parse
import urllib.request, urllib.parse, urllib.error
import json

# Check user balance
params = {'username':'foo', 'password':'bar'}
response = urllib.request.urlopen("http://127.0.0.1:1401/balance?%s" % urllib.parse.urlencode(params)).read()
response = json.loads(response)

print('Balance:', response['balance'])
print('SMS Count:', response['sms_count'])

#Balance: 100.0
#SMS Count: ND
