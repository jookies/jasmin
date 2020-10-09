# Python example
# http://jasminsms.com
import urllib.request, urllib.error, urllib.parse
import urllib.request, urllib.parse, urllib.error

baseParams = {'username':'foo', 'password':'bar', 'to':'+336222172', 'content':'Hello'}

# Send an SMS-MT with minimal parameters
urllib.request.urlopen("http://127.0.0.1:1401/send?%s" % urllib.parse.urlencode(baseParams)).read()

# Send an SMS-MT with defined originating address
baseParams['from'] = 'Jasmin GW'
urllib.request.urlopen("http://127.0.0.1:1401/send?%s" % urllib.parse.urlencode(baseParams)).read()
