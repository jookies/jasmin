from txamqp.protocol import AMQClient

class AmqpProtocol(AMQClient):
    def connectionMade(self):
        """Called when a connection has been made."""
        self.factory.log.info("Connection made to %s:%s" % (self.factory.config.host, self.factory.config.port))
        AMQClient.connectionMade(self)

        self.factory.connectDeferred.callback(self)