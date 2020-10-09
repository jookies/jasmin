from twisted.web.resource import Resource


class Ping(Resource):
    isleaf = True

    def __init__(self, log):
        Resource.__init__(self)
        self.log = log

    def render_GET(self, request):
        """
        /ping request processing

        Note: Ping is used to check Jasmin's http api
        """

        self.log.debug("Rendering /ping response with args: %s from %s",
                       request.args, request.getClientIP())
        self.log.info("Received ping from %s", request.getClientIP())
        request.setResponseCode(200)
        return b'Jasmin/PONG'
