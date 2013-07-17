from twisted.web import server
from twisted.web.resource import Resource
from twisted.internet import reactor
import time

class LeafServer(Resource):
    isLeaf = True
    def render_GET(self, request):
        return 'render_GET'

class AckServer(LeafServer):
    def render_GET(self, request):
        return 'ACK/Jasmin'

class NoAckServer(LeafServer):
    def render_GET(self, request):
        return 'Anything'

class TimeoutLeafServer(Resource):
    isLeaf = True
    hangTime = 60
    def render_GET(self, request):
        time.sleep(self.hangTime)
        return 'render_GET'

class Error404Server(Resource):
    isLeaf = True
    def render_GET(self, request):
        request.setResponseCode(404)
        request.finish()
        return server.NOT_DONE_YET

if __name__ == '__main__':
    s = LeafServer()
    site = server.Site(s)
    reactor.listenTCP(8080, site)
    reactor.run()