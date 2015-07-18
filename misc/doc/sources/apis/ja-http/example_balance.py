# Python example
# http://jasminsms.com
import urllib2
import urllib
import json

# Check user balance
params = {'username':'fourat', 'password':'secret'}
response = urllib2.urlopen("http://127.0.0.1:1401/balance?%s" % urllib.urlencode(params)).read()
response = json.loads(response)

print 'Balance:', response['balance']
print 'SMS Count:', response['sms_count']

#Balance: 100.0
#SMS Count: ND