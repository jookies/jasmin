# Python example
# http://jasminsms.com
import urllib.request, urllib.error, urllib.parse
import urllib.request, urllib.parse, urllib.error

# Send an SMS-MT and request terminal level acknowledgement callback to http://myserver/acknowledgement
params = {'username':'foo', 'password':'bar', 'to':'+336222172', 'content':'Hello',
          'dlr-url':'http://myserver/acknowledgement', 'dlr-level':2}
urllib.request.urlopen("http://127.0.0.1:1401/send?%s" % urllib.parse.urlencode(params)).read()
