from twisted.web import server
from twisted.web.resource import Resource
from twisted.internet import reactor
import time

class LeafServer(Resource):
    isLeaf = True
    def render_GET(self, request):
        return 'render_GET'
    def render_POST(self, request):
        return 'render_POST'

class AckServer(LeafServer):
    last_request = None
    
    def render_GET(self, request):
        self.last_request = request
        return 'ACK/Jasmin'
    def render_POST(self, request):
        self.last_request = request
        return 'ACK/Jasmin'

class NoAckServer(LeafServer):
    def render_GET(self, request):
        return 'Anything'
    def render_POST(self, request):
        return 'Anything'

class TimeoutLeafServer(Resource):
    isLeaf = True
    hangTime = 60
    def render_GET(self, request):
        time.sleep(self.hangTime)
        return 'render_GET'
    def render_POST(self, request):
        time.sleep(self.hangTime)
        return 'render_POST'

class Error404Server(Resource):
    isLeaf = True
    def render_GET(self, request):
        request.setResponseCode(404)
        request.finish()
        return server.NOT_DONE_YET
    def render_POST(self, request):
        request.setResponseCode(404)
        request.finish()
        return server.NOT_DONE_YET

if __name__ == '__main__':
    s = LeafServer()
    site = server.Site(s)
    reactor.listenTCP(8080, site)
    reactor.run()