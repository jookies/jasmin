import sys
import logging
from logging.handlers import TimedRotatingFileHandler
import txredisapi as redis
from twisted.internet import reactor
from twisted.internet import defer
from jasmin.redis.configs import RedisForJasminConfig

LOG_CATEGORY = "jasmin-redis-client"


# Overloading https://github.com/fiorix/txredisapi

class RedisForJasminProtocol(redis.RedisProtocol):
    def connectionMade(self):
        redis.RedisProtocol.connectionMade(self)
        self.factory.log.info("Connection made")

    def execute_command(self, *args, **kwargs):
        self.factory.log.debug('Executing redis command: %s', args)

        return redis.RedisProtocol.execute_command(self, *args, **kwargs)


class RedisForJasminFactory(redis.RedisFactory):
    protocol = RedisForJasminProtocol

    def startedConnecting(self, connector):
        redis.RedisFactory.startedConnecting(self, connector)
        self.log.info('Connecting ...')

    def clientConnectionLost(self, connector, reason):
        redis.RedisFactory.clientConnectionLost(self, connector, reason)
        self.log.info('Lost connection.  Reason: %s', reason)

    def clientConnectionFailed(self, connector, reason):
        redis.RedisFactory.clientConnectionFailed(self, connector, reason)
        self.log.info('Connection failed. Reason: %s', reason)

    def __init__(self, uuid, dbid, poolsize, isLazy=True,
                 handler=redis.ConnectionHandler, config=None):
        redis.RedisFactory.__init__(self, uuid, dbid, poolsize, isLazy, handler)

        # Set up a dedicated logger
        self.log = logging.getLogger(LOG_CATEGORY)
        if config is not None:
            self.log.setLevel(config.log_level)
            if 'stdout' in config.log_file:
                handler = logging.StreamHandler(sys.stdout)
            else:
                handler = TimedRotatingFileHandler(filename=config.log_file,
                                                   when=config.log_rotate)
            formatter = logging.Formatter(config.log_format, config.log_date_format)
            handler.setFormatter(formatter)
        else:
            handler = logging.NullHandler()

        if len(self.log.handlers) != 1:
            self.log.addHandler(handler)
            self.log.propagate = False


def makeConnection(host, port, dbid, poolsize, reconnect, isLazy, _RedisForJasminConfig=None):
    uuid = "%s:%s" % (host, port)
    factory = RedisForJasminFactory(uuid, None, poolsize, isLazy, redis.ConnectionHandler, _RedisForJasminConfig)
    factory.continueTrying = reconnect
    for _ in range(poolsize):
        reactor.connectTCP(host, port, factory)

    if isLazy:
        return factory.handler
    else:
        return factory.deferred


def SimpleConnection(host="127.0.0.1", port=6379, dbid=None, reconnect=True):
    return makeConnection(host, port, dbid, 1, reconnect, False)


def ConnectionWithConfiguration(_RedisForJasminConfig):
    if _RedisForJasminConfig.password is not None:
        # If password is set, don't select dbid at connection, this will be done after authentication
        dbid = None
    else:
        dbid = _RedisForJasminConfig.dbid

    return makeConnection(_RedisForJasminConfig.host, _RedisForJasminConfig.port, dbid,
                          _RedisForJasminConfig.poolsize, True, False, _RedisForJasminConfig)


