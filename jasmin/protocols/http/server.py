import logging
from datetime import datetime
from logging.handlers import TimedRotatingFileHandler

from twisted.internet import reactor
from twisted.web.resource import Resource

import jasmin
from jasmin.protocols.http.endpoints.send import Send
from jasmin.protocols.http.endpoints.rate import Rate
from jasmin.protocols.http.endpoints.ping import Ping
from jasmin.protocols.http.endpoints.balance import Balance
from jasmin.protocols.http.stats import HttpAPIStatsCollector

LOG_CATEGORY = "jasmin-http-api"

# @TODO make it configurable
reactor.suggestThreadPoolSize(30)


class HTTPApi(Resource):

    def __init__(self, RouterPB, SMPPClientManagerPB, config, interceptor=None):
        Resource.__init__(self)

        # Setup stats collector
        stats = HttpAPIStatsCollector().get()
        stats.set('created_at', datetime.now())

        # Set up a dedicated logger
        log = logging.getLogger(LOG_CATEGORY)
        if len(log.handlers) != 1:
            log.setLevel(config.log_level)
            handler = TimedRotatingFileHandler(filename=config.log_file, when=config.log_rotate)
            formatter = logging.Formatter(config.log_format, config.log_date_format)
            handler.setFormatter(formatter)
            log.addHandler(handler)
            log.propagate = False

        self.log = log
        # Set http url routings
        log.debug("Setting http url routing for /send")
        self.putChild(b'send', Send(config, RouterPB, SMPPClientManagerPB, stats, log, interceptor))
        log.debug("Setting http url routing for /rate")
        self.putChild(b'rate', Rate(config, RouterPB, stats, log, interceptor))
        log.debug("Setting http url routing for /balance")
        self.putChild(b'balance', Balance(RouterPB, stats, log))
        log.debug("Setting http url routing for /ping")
        self.putChild(b'ping', Ping(log))

    def getChild(self, name, request):
        self.log.debug("Getting child with name %s", name)
        if name == b'':
            return self
        return Resource.getChild(self, name, request)

    def render_GET(self, request):
        self.log.debug("Rendering / response with args: %s from %s",
                       request.args, request.getClientIP())
        request.setResponseCode(200)
        return jasmin.get_release().encode()
