# Python example
# http://jasminsms.com
import urllib2
import urllib

baseParams = {'username':'fourat', 'password':'secret', 'to':'+21698700177', 'content':'Hello'}

# Sending long content (more than 160 chars):
baseParams['content'] = 'Very long message ....................................................................................................................................................................................'
urllib2.urlopen("http://127.0.0.1:1401/send?%s" % urllib.urlencode(baseParams)).read()

# Sending UCS2 (UTF-16) arabic content
baseParams['content'] = '\x0623\x0631\x0646\x0628'
baseParams['coding'] = 8
urllib2.urlopen("http://127.0.0.1:1401/send?%s" % urllib.urlencode(baseParams)).read()