# Python example
# http://jasminsms.com
import urllib2
import urllib

# Send an SMS-MT and request terminal level acknowledgement callback to http://myserver/acknowledgement
params = {'username':'fourat', 'password':'secret', 'to':'+21698700177', 'content':'Hello', 
          'dlr-level':'http://myserver/acknowledgement', 'dlr-level':2}
urllib2.urlopen("http://127.0.0.1:1401/send?%s" % urllib.urlencode(params)).read()