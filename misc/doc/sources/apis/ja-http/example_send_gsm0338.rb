# Sending simple message using Ruby
# http://jasminsms.com

require 'net/http'

uri = URI('http://127.0.0.1:1401/send')
params = { :username => 'fourat', :password => 'secred',
           :to => '+21698700177', :content => 'Hello world' }
uri.query = URI.encode_www_form(params)

response = Net::HTTP.get_response(uri)
