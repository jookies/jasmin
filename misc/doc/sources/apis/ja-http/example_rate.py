# Python example
# http://jasminsms.com
import urllib2
import urllib
import json

# Check message rate price
params = {'username':'foo', 'password':'bar', 'to': '06222172'}
response = urllib2.urlopen("http://127.0.0.1:1401/rate?%s" % urllib.urlencode(params)).read()
response = json.loads(response)

print 'Unit rate price:', response['unit_rate']
print 'Units:', response['submit_sm_count']

#Unit rate price: 2.8
#Units: 1
