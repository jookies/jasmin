# Python example
# http://jasminsms.com
import urllib.request, urllib.error, urllib.parse
import urllib.request, urllib.parse, urllib.error

baseParams = {'username':'foo', 'password':'bar', 'to':'+336222172', 'content':'Hello'}

# Sending long content (more than 160 chars):
baseParams['content'] = 'Very long message ....................................................................................................................................................................................'
urllib.request.urlopen("http://127.0.0.1:1401/send?%s" % urllib.parse.urlencode(baseParams)).read()

# Sending UCS2 (UTF-16) arabic content
baseParams['content'] = '\x06\x23\x06\x31\x06\x46\x06\x28'
baseParams['coding'] = 8
urllib.request.urlopen("http://127.0.0.1:1401/send?%s" % urllib.parse.urlencode(baseParams)).read()

# Sending UCS2 (UTF-16) arabic binary content
baseParams['hex-content'] = '0623063106460628'
baseParams['coding'] = 8
urllib.request.urlopen("http://127.0.0.1:1401/send?%s" % urllib.parse.urlencode(baseParams)).read()
